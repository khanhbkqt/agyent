package ports

import (
	"context"
	"time"

	"agyent/internal/core/domain"
)

// TemporalContextPort formats human-readable temporal gap markers for conversation turns.
type TemporalContextPort interface {
	FormatTemporalTag(lastTime, currTime time.Time, loc *time.Location) string
}

// TaskStateGuardPort determines whether a conversation turn slice is complete or in-progress.
type TaskStateGuardPort interface {
	EvaluateTaskState(snapshot domain.ConversationSnapshot) domain.TaskProgressState
}

// HeuristicFilterPort provides fast Zero-LLM pre-filtering of conversation snapshots.
type HeuristicFilterPort interface {
	ShouldReflect(ctx context.Context, snapshot domain.ConversationSnapshot) (bool, float64, string)
}

// SecurityGuardrailPort handles secret redaction and prompt injection defense.
type SecurityGuardrailPort interface {
	RedactSecrets(text string) string
	ValidateInjection(text string) error
}

// ReflectionEnginePort performs root-cause analysis and extracts actionable constraints.
type ReflectionEnginePort interface {
	Reflect(ctx context.Context, snapshot domain.ConversationSnapshot, abortSig <-chan struct{}) ([]domain.MemoryCandidate, error)
}

// ConflictResolverPort aligns new memory candidates with existing rules in 4D MEMORY.md and USER.md.
type ConflictResolverPort interface {
	ResolveAndMerge4D(existingMarkdown string, candidates []domain.MemoryCandidate) (string, []domain.MemoryCandidate, error)
}

// MemoryStorePort manages two-tier atomic persistence, OS file locking, and memory compaction.
type MemoryStorePort interface {
	AppendDailyLog(ctx context.Context, workspaceDir string, candidate domain.MemoryCandidate) error
	PromoteToDurable4D(ctx context.Context, workspaceDir string, candidates []domain.MemoryCandidate) error
	CompactDurableMemory(ctx context.Context, workspaceDir string, lineLimit int) error
	UpdateReflectedStep(ctx context.Context, conversationID string, step int) error
}

// EvolutionOrchestratorPort controls the asynchronous background self-learning lifecycle.
type EvolutionOrchestratorPort interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	TriggerConversationEvolution(ctx context.Context, convID string, trigger domain.EvolutionTrigger) error
	NotifyUserActivity(sessionKey string)
}
