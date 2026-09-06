package evolution

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

var _ ports.ReflectionEnginePort = (*ReflectionEngine)(nil)

const staticReflectionPrefix = `[SYSTEM REFLECTION FOUNDATION - ROOT CAUSE & BEHAVIORAL EVOLUTION]
You are the Self-Reflection & Knowledge Evolution Engine for "agyent".
Your mission is to perform root-cause analysis on the provided dialogue slice and extract actionable behavioral constraints, user preferences, or architecture decisions (ADRs).

[EXTRACTION PRINCIPLES]
1. Root-Cause Focus: Identify why expectations and actual outcomes mismatched.
2. Actionable Constraints: Produce strict positive/negative rules (e.g. "When scanning SQLite timestamps, always use FlexTime").
3. Deduplication Key (conflict_key): Assign a semantic snake_case key to enable in-place replacement (e.g. "sqlite_timestamp_scanner", "response_verbosity").
4. Categories:
   - "preference": Updates USER.md (e.g. communication style, tone, habits).
   - "lesson": Actionable constraint for MEMORY.md & memory/YYYY-MM-DD.md.
   - "adr": Significant architecture/tech stack decision for MEMORY.md.
   - "daily_note": Short contextual note for memory/YYYY-MM-DD.md.

[OUTPUT FORMAT]
Return ONLY a valid JSON object matching this schema without markdown fences:
{
  "candidates": [
    {
      "category": "lesson",
      "title": "Short descriptive title",
      "constraint": "Actionable rule description",
      "rationale": "Root cause rationale",
      "conflict_key": "semantic_conflict_key",
      "is_durable": true
    }
  ]
}`

// ReflectionEngine handles root cause extraction with static prefix prompt caching.
type ReflectionEngine struct {
	runner           ports.RunnerPort
	executionService ports.ExecutionServicePort
	security         ports.SecurityGuardrailPort
	extractor        *DeltaExtractor
	timeout          time.Duration
}

// NewReflectionEngine constructs a new reflection engine.
func NewReflectionEngine(runner ports.RunnerPort, security ports.SecurityGuardrailPort, timeout time.Duration) *ReflectionEngine {
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	if security == nil {
		security = NewSecurityGuardrail()
	}
	return &ReflectionEngine{
		runner:    runner,
		security:  security,
		extractor: NewDeltaExtractor(),
		timeout:   timeout,
	}
}

// SetExecutionService injects the execution service chokepoint into ReflectionEngine.
func (r *ReflectionEngine) SetExecutionService(svc ports.ExecutionServicePort) {
	r.executionService = svc
}

type reflectionOutput struct {
	Candidates []domain.MemoryCandidate `json:"candidates"`
}

// Reflect extracts memory candidates from a conversation snapshot with fail-open timeout and abort handling.
func (r *ReflectionEngine) Reflect(ctx context.Context, snapshot domain.ConversationSnapshot, abortSig <-chan struct{}) ([]domain.MemoryCandidate, error) {
	// 1. Check for immediate abort
	select {
	case <-abortSig:
		return nil, nil
	default:
	}

	// 2. Setup Fail-Open Context Timeout
	reflectCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	// 3. Format Snapshot Dialogue Slice
	dialogueText := r.extractor.FormatSnapshotTurns(snapshot)
	if len(snapshot.AuditEntries) > 0 {
		var auditSb strings.Builder
		auditSb.WriteString("\n[AUDIT LOGS OF TURNS]\n")
		for _, a := range snapshot.AuditEntries {
			if a.Status != "SUCCESS" || a.ErrorMessage != "" {
				auditSb.WriteString(fmt.Sprintf("- Status: %s | Error: %s\n", a.Status, a.ErrorMessage))
			}
		}
		dialogueText += auditSb.String()
	}

	// 4. Sanitize and redact secrets from input
	sanitizedInput := r.security.RedactSecrets(dialogueText)
	if err := r.security.ValidateInjection(sanitizedInput); err != nil {
		return nil, fmt.Errorf("security check failed during reflection: %w", err)
	}

	// 5. Assemble Prompt with Static Prefix (Index 0 for KV-Cache hits)
	fullPrompt := fmt.Sprintf("%s\n\n[INPUT DIALOGUE SLICE FOR REFLECTION]\n%s", staticReflectionPrefix, sanitizedInput)

	// If no runner is provided (e.g. unit testing or fallback heuristic mode), generate deterministic fallback
	if r.runner == nil {
		return r.heuristicFallbackReflection(snapshot), nil
	}

	agentName := snapshot.AgentName
	if agentName == "" {
		agentName = "agyent"
	}

	req := domain.ExecutionRequest{
		Prompt:                     fullPrompt,
		AgentName:                  agentName,
		ProjectName:                snapshot.ProjectName,
		SessionKey:                 snapshot.SessionKey,
		WorkspaceDir:               snapshot.WorkspaceDir,
		Timeout:                    r.timeout,
		Effort:                     "medium",
		Mode:                       "plan",
		DangerouslySkipPermissions: false,
	}

	// Channel for runner completion
	type runnerResult struct {
		res *domain.ExecutionResult
		err error
	}
	resChan := make(chan runnerResult, 1)

	go func() {
		var (
			res *domain.ExecutionResult
			err error
		)
		if r.executionService != nil {
			principal := domain.Principal{
				Kind:      domain.PrincipalSystem,
				Provider:  "internal",
				SubjectID: "system:reflection",
			}
			res, err = r.executionService.ExecuteTurn(reflectCtx, principal, req, snapshot.SessionKey, false)
		} else {
			res, err = r.runner.Execute(reflectCtx, req)
		}
		resChan <- runnerResult{res: res, err: err}
	}()

	select {
	case <-abortSig:
		cancel()
		return nil, nil
	case <-reflectCtx.Done():
		// Fail-open: Return empty slice instead of crashing
		return nil, nil
	case out := <-resChan:
		if out.err != nil || out.res == nil || !out.res.Success {
			// Fail-open
			return nil, nil
		}

		candidates := r.parseCandidates(out.res.ResponseText)
		now := time.Now()
		for i := range candidates {
			candidates[i].CreatedAt = now
			candidates[i].Constraint = r.security.RedactSecrets(candidates[i].Constraint)
			candidates[i].Rationale = r.security.RedactSecrets(candidates[i].Rationale)
		}
		return candidates, nil
	}
}

func (r *ReflectionEngine) parseCandidates(response string) []domain.MemoryCandidate {
	text := strings.TrimSpace(response)
	// Strip markdown json fences if present
	if strings.HasPrefix(text, "```json") {
		text = strings.TrimPrefix(text, "```json")
		text = strings.TrimSuffix(text, "```")
	} else if strings.HasPrefix(text, "```") {
		text = strings.TrimPrefix(text, "```")
		text = strings.TrimSuffix(text, "```")
	}
	text = strings.TrimSpace(text)

	var out reflectionOutput
	if err := json.Unmarshal([]byte(text), &out); err == nil && len(out.Candidates) > 0 {
		return out.Candidates
	}

	// Fallback heuristic extraction if JSON is slightly unaligned
	return nil
}

func (r *ReflectionEngine) heuristicFallbackReflection(snapshot domain.ConversationSnapshot) []domain.MemoryCandidate {
	var candidates []domain.MemoryCandidate
	now := time.Now()

	for _, a := range snapshot.AuditEntries {
		if a.Status == "ERROR" && a.ErrorMessage != "" {
			candidates = append(candidates, domain.MemoryCandidate{
				Category:    domain.CategoryLesson,
				Title:       "Error Recovery Rule",
				Constraint:  fmt.Sprintf("Prevent failure: %s", r.security.RedactSecrets(a.ErrorMessage)),
				Rationale:   "Auto-extracted from audit log error entry",
				ConflictKey: "audit_error_rule",
				IsDurable:   true,
				CreatedAt:   now,
			})
		}
	}

	for _, turn := range snapshot.Turns {
		text := strings.ToLower(turn.Text)
		if strings.Contains(text, "sai rồi") || strings.Contains(text, "phải là") {
			candidates = append(candidates, domain.MemoryCandidate{
				Category:    domain.CategoryLesson,
				Title:       "User Correction Guideline",
				Constraint:  r.security.RedactSecrets(turn.Text),
				Rationale:   "User explicitly corrected behavior during turn",
				ConflictKey: "user_correction_rule",
				IsDurable:   true,
				CreatedAt:   now,
			})
		}
		if strings.Contains(text, "ngắn gọn") || strings.Contains(text, "súc tích") {
			candidates = append(candidates, domain.MemoryCandidate{
				Category:    domain.CategoryPreference,
				Title:       "Concise Communication Preference",
				Constraint:  "Keep responses concise, clear, and focused without unnecessary fluff.",
				ConflictKey: "response_verbosity",
				IsDurable:   true,
				CreatedAt:   now,
			})
		}
	}

	return candidates
}
