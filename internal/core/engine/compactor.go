package engine

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"agyent/internal/core/domain"
)

// CompactionResult contains the outcome metrics of a conversation compaction operation.
type CompactionResult struct {
	SessionKey        string  `json:"session_key"`
	OldConversationID string  `json:"old_conversation_id"`
	NewConversationID string  `json:"new_conversation_id"`
	Digest            string  `json:"digest"`
	OriginalTokens    int     `json:"original_tokens"`
	CompactedTokens   int     `json:"compacted_tokens"`
	ReductionPercent  float64 `json:"reduction_percent"`
	Reason            string  `json:"reason"` // "manual" or "auto-threshold"
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

	// 2. Synthesize Continuity Digest (Hybrid: Semantic LLM with Heuristic Fallback)
	digest := e.synthesizeContinuityDigest(ctx, session, agent, activeConvID, convLogs, customNotes...)

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

	// 3. Archive Old Conversation and Update Title
	_ = e.storage.SetConversationArchived(ctx, activeConvID, true)

	oldConv, err := e.storage.GetConversation(ctx, activeConvID)
	if err == nil && oldConv != nil {
		oldTitle := oldConv.Title
		if !strings.HasPrefix(oldTitle, "[Compacted]") {
			_ = e.storage.SetConversationTitle(ctx, activeConvID, "[Compacted] "+oldTitle)
		}
	}

	// 4. Reset Active Conversation ID on Session
	session.ResetActiveConversationID()
	session.UpdatedAt = time.Now()
	if err := e.storage.SaveSession(ctx, session); err != nil {
		slog.ErrorContext(ctx, "Failed to save session after compaction", slog.String("error", err.Error()))
	}

	// 5. Store Continuity Digest for Level 4 Injection on Next Turn
	e.SetPendingCompactionDigest(session.SessionKey, digest)

	result := &CompactionResult{
		SessionKey:        session.SessionKey,
		OldConversationID: activeConvID,
		Digest:            digest,
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
	customNotes ...string,
) string {
	workspaceDir := ""
	if agent != nil {
		workspaceDir = agent.WorkspacePath
	}

	// Note added by user or reason
	noteStr := ""
	if len(customNotes) > 0 && strings.TrimSpace(customNotes[0]) != "" {
		noteStr = fmt.Sprintf("\n- User Note / Reason: %s", strings.TrimSpace(customNotes[0]))
	}

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
		sb.WriteString("1. 🎯 **Task Goals & Context:** Continuation of previous conversational thread.\n")
		sb.WriteString("2. 💡 **Key Constraints & Preferences:** Adhere strictly to user preferences and directives in AGENTS.md.\n")
		sb.WriteString("3. 📁 **Workspace State:** Workspace files and repository structure are preserved on disk.\n")
		sb.WriteString("4. ⏳ **Next Steps:** Proceed seamlessly with current objectives based on subsequent user prompts.\n")
		return sb.String()
	}

	// If no runner, return fallback
	if e.runner == nil {
		return fallbackDigest()
	}

	// Fast Semantic Synthesis Prompt
	synthesisPrompt := fmt.Sprintf(`You are an expert conversational context compressor for the AI assistant system '%s'.
Summarize the current working state of this conversation into an ultra-dense, structured Continuity Digest for the next session.
Do NOT include conversational filler. Produce ONLY the following 4 structured sections:

1. 🎯 **Original Goals & Intent**: Core objectives requested by the user.
2. 💡 **Key Decisions & Technical Constraints**: Decisions made, user preferences, and architectural rules established.
3. 📁 **Workspace State & Modified Files**: Files created, edited, verified, or investigated.
4. ⏳ **Pending Tasks & Next Steps**: Immediate next actions required.

Scope: %s | Agent: %s%s`, session.ActiveAgent, session.ActiveProject, session.ActiveAgent, noteStr)

	synthCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	resolvedModel, _, _ := e.ResolveExecutionParams("", "", session, agent)

	req := domain.ExecutionRequest{
		Prompt:                     synthesisPrompt,
		ConversationID:             convID,
		WorkspaceDir:               workspaceDir,
		Timeout:                    15 * time.Second,
		Model:                      resolvedModel,
		Mode:                       "plan",
		Effort:                     "low",
		DangerouslySkipPermissions: true,
	}

	execRes, err := e.runner.Execute(synthCtx, req)
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

	return sb.String()
}
