package engine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"agyent/internal/core/domain"
)

var (
	userRequestRegex       = regexp.MustCompile(`(?s)<\s*USER_REQUEST\s*>\s*(.*?)\s*(?:<\s*/USER_REQUEST\s*>|$)`)
	systemEnvelopeTagRegex = regexp.MustCompile(`(?s)<\s*(?:CONTEXT_SUMMARY|ADDITIONAL_METADATA|USER_SETTINGS_CHANGE|SYSTEM_DIRECTIVES|ENVIRONMENT_CONTEXT|METADATA)\b[^>]*>.*?(?:<\s*/(?:CONTEXT_SUMMARY|ADDITIONAL_METADATA|USER_SETTINGS_CHANGE|SYSTEM_DIRECTIVES|ENVIRONMENT_CONTEXT|METADATA)\s*>|$)`)
	temporalTagStripRegex  = regexp.MustCompile(`(?s)\[TEMPORAL (?:CONTEXT|GAP)[^\]]*\]\s*`)
	validConvIDRegex       = regexp.MustCompile(`^[a-zA-Z0-9_\-]+$`)

	// Sensitive secrets and tokens to scrub from retained dialogue (SEC-03)
	openAISecretRegex     = regexp.MustCompile(`(?i)\b(sk-[a-zA-Z0-9_\-]{20,})\b`)
	anthropicSecretRegex  = regexp.MustCompile(`(?i)\b(sk-ant-[a-zA-Z0-9_\-]{20,})\b`)
	githubPATSecretRegex  = regexp.MustCompile(`\b(gh[pousr]_[a-zA-Z0-9]{20,})\b`)
	geminiSecretRegex     = regexp.MustCompile(`\b(AIzaSy[a-zA-Z0-9_\-]{33})\b`)
	bearerAuthSecretRegex = regexp.MustCompile(`(?i)\b(Bearer\s+[a-zA-Z0-9_\-\.]{20,})\b`)
	genericSecretKVRegex  = regexp.MustCompile(`(?i)\b([a-zA-Z0-9_\-]*(?:password|secret|api_key|token|access_key)[a-zA-Z0-9_\-]*)\s*([:=])\s*(["']?)([^\s"'\r\n]{6,})(["']?)`)
	privateKeyBlockRegex  = regexp.MustCompile(`-----BEGIN [A-Z0-9_-]+ PRIVATE KEY-----[\s\S]*?-----END [A-Z0-9_-]+ PRIVATE KEY-----`)
)

// RetainedMessage represents a dialogue turn captured between user and agent.
type RetainedMessage struct {
	Role    string `json:"role"`    // "user" or "agent"
	Content string `json:"content"` // cleaned message text
}

// CompactionResult contains the outcome metrics of a conversation compaction operation.
type CompactionResult struct {
	SessionKey        string            `json:"session_key"`
	OldConversationID string            `json:"old_conversation_id"`
	NewConversationID string            `json:"new_conversation_id"`
	Digest            string            `json:"digest"`
	RetainedMessages  []RetainedMessage `json:"retained_messages,omitempty"`
	OriginalTokens    int               `json:"original_tokens"`
	CompactedTokens   int               `json:"compacted_tokens"`
	ReductionPercent  float64           `json:"reduction_percent"`
	Reason            string            `json:"reason"` // "manual" or "auto-threshold"
}

// CompactSessionContext performs hybrid context compression on a conversation session.
// It synthesizes an executive continuity digest, archives the old conversation, resets
// the session active conversation ID, and stages the digest for Level 4 seed injection on the next turn.
func (e *Engine) CompactSessionContext(
	ctx context.Context,
	session *domain.Session,
	agent *domain.Agent,
	reason string,
	customNotes ...string,
) (*CompactionResult, error) {
	if session == nil {
		return nil, fmt.Errorf("session cannot be nil")
	}

	activeConvID := session.GetActiveConversationID()
	if activeConvID == "" {
		return nil, fmt.Errorf("no active conversation in session %q to compact", session.SessionKey)
	}

	slog.InfoContext(ctx, "Starting session context compaction",
		slog.String("session_key", session.SessionKey),
		slog.String("conversation_id", activeConvID),
		slog.String("agent", session.ActiveAgent),
		slog.String("reason", reason),
	)

	// 1. Query original token usage stats
	stats, _ := e.storage.GetTokenStats(ctx, session.SessionKey, activeConvID)
	originalTokens := 0
	if stats != nil {
		originalTokens = stats.TotalTokens
	}

	// Fetch recent audit logs for context synthesis
	recentLogs, _ := e.storage.ListAuditLogs(ctx, session.SessionKey, 5)
	var convLogs []domain.AuditLog
	for _, l := range recentLogs {
		if l.ConversationID == activeConvID {
			convLogs = append(convLogs, l)
		}
	}

	// Extract the last 2 messages (User + Agent) to retain immediate conversational grounding
	retainedMsgs := e.getRecentDialogueForCompaction(session.SessionKey, activeConvID)

	// 2. Synthesize Continuity Digest (Hybrid: Semantic LLM with Heuristic Fallback)
	digest := e.synthesizeContinuityDigest(ctx, session, agent, activeConvID, convLogs, retainedMsgs, customNotes...)

	// Estimate token size of compacted digest (~1 token ~= 4 chars)
	compactedTokens := len(digest) / 4
	if compactedTokens == 0 {
		compactedTokens = 1
	}

	var reductionPercent float64
	if originalTokens > 0 {
		reductionPercent = float64(originalTokens-compactedTokens) / float64(originalTokens) * 100.0
		if reductionPercent < 0 {
			reductionPercent = 0
		}
	}

	// 3. Archive Old Conversation and Update Title (guarded with uncancelled context)
	scope := domain.ConversationScope{
		SessionKey:  session.SessionKey,
		AgentName:   session.ActiveAgent,
		ProjectName: session.ActiveProject,
	}
	dbCtx, dbCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer dbCancel()

	_ = e.storage.SetConversationArchivedScoped(dbCtx, scope, activeConvID, true)

	oldConv, err := e.storage.GetConversationScoped(dbCtx, scope, activeConvID)
	if err == nil && oldConv != nil {
		oldTitle := oldConv.Title
		if !strings.HasPrefix(oldTitle, "[Compacted]") {
			_ = e.storage.SetConversationTitleScoped(dbCtx, scope, activeConvID, "[Compacted] "+oldTitle)
		}
	}

	// 4. Reset Active Conversation ID on Session
	session.ResetActiveConversationID()
	session.UpdatedAt = time.Now()
	if err := e.storage.SaveSession(dbCtx, session); err != nil {
		slog.ErrorContext(dbCtx, "Failed to save session after compaction", slog.String("error", err.Error()))
	}

	// 5. Store Continuity Digest for Level 4 Injection on Next Turn
	e.SetPendingCompactionDigest(session.SessionKey, digest)

	result := &CompactionResult{
		SessionKey:        session.SessionKey,
		OldConversationID: activeConvID,
		Digest:            digest,
		RetainedMessages:  retainedMsgs,
		OriginalTokens:    originalTokens,
		CompactedTokens:   compactedTokens,
		ReductionPercent:  reductionPercent,
		Reason:            reason,
	}

	slog.InfoContext(ctx, "Session context compacted successfully",
		slog.String("session_key", session.SessionKey),
		slog.String("old_conversation_id", activeConvID),
		slog.Int("original_tokens", originalTokens),
		slog.Int("compacted_tokens", compactedTokens),
		slog.Float64("reduction_percent", reductionPercent),
		slog.Int("retained_messages_count", len(retainedMsgs)),
	)

	return result, nil
}

// synthesizeContinuityDigest creates a structured Markdown Continuity Digest.
// Uses fast semantic LLM synthesis if available, falling back to a deterministic heuristic template.
func (e *Engine) synthesizeContinuityDigest(
	ctx context.Context,
	session *domain.Session,
	agent *domain.Agent,
	convID string,
	logs []domain.AuditLog,
	retainedMessages []RetainedMessage,
	customNotes ...string,
) string {
	workspaceDir := ""
	if agent != nil {
		workspaceDir = agent.WorkspacePath
	}

	// Note added by user or reason
	noteStr := ""
	if len(customNotes) > 0 && strings.TrimSpace(customNotes[0]) != "" {
		noteStr = fmt.Sprintf("\n- User Directive Note: %s", strings.TrimSpace(customNotes[0]))
	}

	retainedBlock := formatRetainedDialogueBlock(retainedMessages)

	// Heuristic fallback generator
	fallbackDigest := func() string {
		var sb strings.Builder
		sb.WriteString("[CONVERSATION CONTINUITY & CONTEXT SNAPSHOT]\n")
		sb.WriteString(fmt.Sprintf("• Prior Session ID: `%s`\n", convID))
		sb.WriteString(fmt.Sprintf("• Active Agent: `%s`\n", session.ActiveAgent))
		if session.ActiveProject != "" {
			sb.WriteString(fmt.Sprintf("• Active Project: `%s`\n", session.ActiveProject))
		}
		if noteStr != "" {
			sb.WriteString(noteStr + "\n")
		}
		sb.WriteString("\n### Executive Continuity Digest\n")
		if noteStr != "" {
			sb.WriteString(fmt.Sprintf("1. 🎯 **Context & Task Trajectory:** User instruction: %s\n", strings.TrimSpace(customNotes[0])))
		} else {
			sb.WriteString("1. 🎯 **Context & Task Trajectory:** Continuation of previous conversational thread.\n")
		}
		sb.WriteString("2. 💡 **Key Decisions, Invariants & Trade-offs:** Adhere strictly to user preferences, constraints, and directives in AGENTS.md.\n")
		sb.WriteString("3. 📁 **Modified Files & Working Tree:** Workspace files and repository structure are preserved on disk.\n")
		sb.WriteString("4. ⚠️ **Errors Encountered & Solutions:** No unhandled fatal errors recorded.\n")
		sb.WriteString("5. ⏳ **Immediate Next Action & Open Items:** Proceed seamlessly with current objectives based on subsequent user prompts.\n")
		if retainedBlock != "" {
			sb.WriteString(retainedBlock)
		}
		return sb.String()
	}

	// If no runner, return fallback
	if e.runner == nil {
		return fallbackDigest()
	}

	// High-Resolution Technical Continuity Checkpoint Prompt (inspired by Claude Code)
	synthesisPrompt := fmt.Sprintf(`You are an expert software engineering assistant performing a Context Compaction Checkpoint for '%s'.
Your task is to create a detailed, high-resolution Continuity Digest to hand off to the next turn without losing critical context.

CRITICAL INSTRUCTIONS:
- Do NOT over-summarize into vague generalizations. Preserve exact technical facts: specific file paths, function/struct names, CLI commands, test outcomes, and error messages.
- Language: Write in the primary language of the conversation (e.g., Vietnamese if user speaks Vietnamese, English if English). Keep code identifiers, flags, and technical terms exact.
- Rejected Alternatives & Decisions: Explicitly document WHY certain technical choices were made and what was rejected, so the next turn does not regress or repeat mistakes.
- User Context Note: If provided below, treat it as the highest-priority directive for the next steps.

Produce ONLY the following structured sections in Markdown:

1. 🎯 **Context & Task Trajectory (Bối cảnh & Tiến độ công việc)**:
   - What was the user's primary objective, and how has the task evolved?
   - What sub-tasks have been fully completed vs what is currently active?

2. 💡 **Key Decisions, Invariants & Trade-offs (Quyết định kiến trúc & Lý do)**:
   - Specific architectural choices made, user preferences, and constraints.
   - What approaches were tried and discarded, and why?

3. 📁 **Modified Files & Working Tree (Tệp đã sửa & Hiện trạng code)**:
   - Exact paths of files created, modified, or inspected (with summary of changes per file).
   - Test commands executed and their exact pass/fail status.

4. ⚠️ **Errors Encountered & Solutions (Các lỗi đã xử lý)**:
   - Key bugs/errors discovered and how they were solved (to prevent repeating them).

5. ⏳ **Immediate Next Action & Open Items (Bước tiếp theo & Câu hỏi tồn đọng)**:
   - The exact next step to execute immediately.
   - Any blockers or questions waiting for user feedback.

Scope: Project=%s | Agent=%s%s`, session.ActiveAgent, session.ActiveProject, session.ActiveAgent, noteStr)

	synthCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()

	resolvedModel, _, _ := e.ResolveExecutionParams("", "", session, agent)

	req := domain.ExecutionRequest{
		Prompt:                     synthesisPrompt,
		ConversationID:             convID,
		WorkspaceDir:               workspaceDir,
		Timeout:                    35 * time.Second,
		Model:                      resolvedModel,
		Mode:                       "plan",
		Effort:                     "low",
		DangerouslySkipPermissions: false,
		AgentName:                  session.ActiveAgent,
		ProjectName:                session.ActiveProject,
		SessionKey:                 session.SessionKey,
	}

	var (
		execRes *domain.ExecutionResult
		err     error
	)
	if e.executionService != nil {
		principal := domain.Principal{
			Kind:      domain.PrincipalSystem,
			Provider:  "internal",
			SubjectID: "system:compactor",
		}
		execRes, err = e.executionService.ExecuteTurn(synthCtx, principal, req, session.SessionKey, false)
	} else {
		execRes, err = e.runner.Execute(synthCtx, req)
	}
	if err != nil || execRes == nil || !execRes.Success || strings.TrimSpace(execRes.ResponseText) == "" {
		slog.DebugContext(ctx, "Semantic synthesis timed out or failed, using heuristic digest",
			slog.Any("error", err),
		)
		return fallbackDigest()
	}

	cleanResponse := strings.TrimSpace(execRes.ResponseText)
	var sb strings.Builder
	sb.WriteString("[CONVERSATION CONTINUITY & CONTEXT SNAPSHOT]\n")
	sb.WriteString(fmt.Sprintf("• Prior Session ID: `%s`\n", convID))
	sb.WriteString(fmt.Sprintf("• Active Agent: `%s`\n", session.ActiveAgent))
	if session.ActiveProject != "" {
		sb.WriteString(fmt.Sprintf("• Active Project: `%s`\n", session.ActiveProject))
	}
	if noteStr != "" {
		sb.WriteString(noteStr + "\n")
	}
	sb.WriteString("\n")
	sb.WriteString(cleanResponse)
	sb.WriteString("\n")

	if retainedBlock != "" {
		sb.WriteString(retainedBlock)
	}

	return sb.String()
}

// getRecentDialogueForCompaction retrieves recent dialogue turns, attempting transcript extraction first then in-memory cache.
func (e *Engine) getRecentDialogueForCompaction(sessionKey, convID string) []RetainedMessage {
	// 1. Try reading from transcript.jsonl on disk
	if transcriptPath := FindBrainTranscript(convID); transcriptPath != "" {
		if msgs, err := ExtractLastDialogueFromTranscript(transcriptPath, 1000); err == nil && len(msgs) > 0 {
			return msgs
		}
	}

	// 2. Fall back to in-memory recorded recent turns
	if memMsgs := e.GetRecentDialogue(sessionKey, convID); len(memMsgs) > 0 {
		return memMsgs
	}

	return nil
}

// CleanUserPromptText removes system wrappers, XML prompts, and metadata tags from user transcript content.
func CleanUserPromptText(raw string) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return ""
	}

	// 1. Extract content inside <USER_REQUEST>...</USER_REQUEST> if present
	if match := userRequestRegex.FindStringSubmatch(text); len(match) > 1 {
		text = strings.TrimSpace(match[1])
	}

	// 2. Strip known system envelope metadata tags without wiping legitimate user XML/HTML tags (CORR-02)
	text = systemEnvelopeTagRegex.ReplaceAllString(text, "")

	// 3. Strip temporal context markers (SEC-04)
	text = temporalTagStripRegex.ReplaceAllString(text, "")

	// 4. Extract actual user message after [USER MESSAGE] marker (from bootstrap / prompt templates)
	if idx := strings.LastIndex(text, "[USER MESSAGE]"); idx != -1 {
		text = strings.TrimSpace(text[idx+len("[USER MESSAGE]"):])
	} else if idx := strings.LastIndex(text, "User Prompt: "); idx != -1 {
		text = strings.TrimSpace(text[idx+len("User Prompt: "):])
	}

	return strings.TrimSpace(text)
}

// ScrubSensitiveDialogue redacts known API keys, tokens, and private keys from retained text (SEC-03).
func ScrubSensitiveDialogue(text string) string {
	if text == "" {
		return text
	}
	text = openAISecretRegex.ReplaceAllString(text, "[REDACTED_API_KEY]")
	text = anthropicSecretRegex.ReplaceAllString(text, "[REDACTED_API_KEY]")
	text = githubPATSecretRegex.ReplaceAllString(text, "[REDACTED_TOKEN]")
	text = geminiSecretRegex.ReplaceAllString(text, "[REDACTED_API_KEY]")
	text = bearerAuthSecretRegex.ReplaceAllString(text, "Bearer [REDACTED_TOKEN]")
	text = privateKeyBlockRegex.ReplaceAllString(text, "[REDACTED_PRIVATE_KEY]")
	text = genericSecretKVRegex.ReplaceAllString(text, "$1$2$3[REDACTED]$5")
	return text
}

// PruneMessageContent bounds message length to prevent bloat during compaction (~250 tokens per message).
// Uses rune slicing to safely handle multi-byte UTF-8 characters.
func PruneMessageContent(text string, maxChars int) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if maxChars <= 0 || len(runes) <= maxChars {
		return text
	}

	half := (maxChars - 60) / 2
	if half <= 0 {
		half = maxChars / 2
	}
	head := strings.TrimSpace(string(runes[:half]))
	tail := strings.TrimSpace(string(runes[len(runes)-half:]))
	truncatedChars := len(runes) - (len([]rune(head)) + len([]rune(tail)))

	return fmt.Sprintf("%s\n\n... [TRUNCATED %d CHARACTERS FOR COMPACT CONTEXT] ...\n\n%s", head, truncatedChars, tail)
}

// transcriptStep represents a step entry in transcript.jsonl.
type transcriptStep struct {
	StepIndex int    `json:"step_index"`
	Source    string `json:"source"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	Content   string `json:"content"`
}

// FindBrainTranscript searches potential brain directory locations for convID with strict path traversal defense (SEC-01, CORR-03).
func FindBrainTranscript(convID string) string {
	convID = strings.TrimSpace(convID)
	if convID == "" {
		return ""
	}
	// Defense-in-depth: Reject any path traversal sequences or invalid identifier formats
	if filepath.Base(convID) != convID || strings.Contains(convID, "..") || strings.ContainsAny(convID, "/\\") {
		return ""
	}
	if !validConvIDRegex.MatchString(convID) {
		return ""
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	cleanHome := filepath.Clean(home)

	candidates := []string{
		filepath.Join(cleanHome, ".gemini", "antigravity", "brain", convID, ".system_generated", "logs", "transcript.jsonl"),
		filepath.Join(cleanHome, ".gemini", "antigravity-cli", "brain", convID, ".system_generated", "logs", "transcript.jsonl"),
	}
	for _, p := range candidates {
		cleanP := filepath.Clean(p)
		if !strings.HasPrefix(cleanP, cleanHome+string(filepath.Separator)) {
			continue
		}
		if info, err := os.Stat(cleanP); err == nil && !info.IsDir() {
			return cleanP
		}
	}
	return ""
}

// ExtractLastDialogueFromTranscript scans transcript.jsonl backwards to find the last user message and agent response.
// Uses a bounded circular buffer (max 50 steps) to prevent unbounded memory allocation on large transcripts (SEC-05).
func ExtractLastDialogueFromTranscript(transcriptPath string, maxCharsPerMsg int) ([]RetainedMessage, error) {
	file, err := os.Open(transcriptPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 2*1024*1024)

	const maxStepsBuffer = 50
	var steps = make([]transcriptStep, 0, maxStepsBuffer)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var step transcriptStep
		if err := json.Unmarshal(line, &step); err == nil {
			if len(steps) >= maxStepsBuffer {
				steps = append(steps[1:], step)
			} else {
				steps = append(steps, step)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	var (
		lastUserMsg  string
		lastAgentMsg string
		foundUser    bool
		foundAgent   bool
	)

	// Scan backwards
	for i := len(steps) - 1; i >= 0; i-- {
		s := steps[i]
		cleanContent := strings.TrimSpace(s.Content)

		// CORR-01: Disallow matching PLANNER_RESPONSE once foundUser is true.
		// If foundUser was encountered before any PLANNER_RESPONSE, the latest turn had no textual agent response!
		if !foundAgent && !foundUser && s.Type == "PLANNER_RESPONSE" && cleanContent != "" {
			scrubbed := ScrubSensitiveDialogue(cleanContent)
			lastAgentMsg = PruneMessageContent(scrubbed, maxCharsPerMsg)
			foundAgent = true
		}

		// Find last user input
		if !foundUser && s.Type == "USER_INPUT" && cleanContent != "" {
			cleaned := CleanUserPromptText(cleanContent)
			if cleaned != "" {
				scrubbed := ScrubSensitiveDialogue(cleaned)
				lastUserMsg = PruneMessageContent(scrubbed, maxCharsPerMsg)
				foundUser = true
			}
		}

		if foundUser && foundAgent {
			break
		}
	}

	var results []RetainedMessage
	if foundUser && lastUserMsg != "" {
		results = append(results, RetainedMessage{Role: "user", Content: lastUserMsg})
	}
	if foundAgent && lastAgentMsg != "" {
		results = append(results, RetainedMessage{Role: "agent", Content: lastAgentMsg})
	}

	return results, nil
}

func capitalizeRole(role string) string {
	role = strings.TrimSpace(role)
	if role == "" {
		return ""
	}
	return strings.ToUpper(role[:1]) + strings.ToLower(role[1:])
}

// formatRetainedDialogueBlock renders the retained messages into Markdown for the Continuity Digest.
func formatRetainedDialogueBlock(messages []RetainedMessage) string {
	if len(messages) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n### Recent Interaction (Last Messages Retained for Context)\n")
	for _, m := range messages {
		scrubbedContent := ScrubSensitiveDialogue(m.Content)
		switch m.Role {
		case "user":
			sb.WriteString(fmt.Sprintf("• 👤 **User:** %s\n", scrubbedContent))
		case "agent":
			sb.WriteString(fmt.Sprintf("• 🤖 **Agent:** %s\n", scrubbedContent))
		default:
			sb.WriteString(fmt.Sprintf("• 💬 **%s:** %s\n", capitalizeRole(m.Role), scrubbedContent))
		}
	}
	return sb.String()
}
