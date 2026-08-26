package domain

import "time"

// FeedbackType indicates whether feedback was directly stated or inferred from behavior.
type FeedbackType string

const (
	FeedbackExplicit FeedbackType = "explicit"
	FeedbackImplicit FeedbackType = "implicit"
)

// SentimentCategory classifies the sentiment of the user feedback.
type SentimentCategory string

const (
	SentimentPositive SentimentCategory = "positive"
	SentimentNegative SentimentCategory = "negative"
	SentimentNeutral  SentimentCategory = "neutral"
)

// MemoryCategory classifies target destination and retention of learned knowledge.
type MemoryCategory string

const (
	CategoryPreference MemoryCategory = "preference" // Targets USER.md
	CategoryLesson     MemoryCategory = "lesson"     // Targets memory/YYYY-MM-DD.md & MEMORY.md
	CategoryADR        MemoryCategory = "adr"        // Targets MEMORY.md (Decisions)
	CategoryDailyNote  MemoryCategory = "daily_note" // Targets memory/YYYY-MM-DD.md
)

// TaskProgressState represents whether a conversation task is completed or paused mid-work.
type TaskProgressState string

const (
	TaskStateCompleted        TaskProgressState = "completed"
	TaskStateInProgressPaused TaskProgressState = "in_progress_paused"
	TaskStateEphemeral        TaskProgressState = "ephemeral"
)

// EvolutionTrigger identifies why the self-learning reflection loop was initiated.
type EvolutionTrigger string

const (
	TriggerExplicitSwitch EvolutionTrigger = "explicit_switch" // /new, /c, /use, /p, /reset
	TriggerIdleTimeout    EvolutionTrigger = "idle_timeout"    // Scanner detected inactive session
	TriggerTaskDone       EvolutionTrigger = "task_done"       // User explicitly stated completion
)

// TemporalMarker holds human-readable temporal gap context between turns.
type TemporalMarker struct {
	ElapsedMinutes  int       `json:"elapsed_minutes"`
	HumanLabel      string    `json:"human_label"`
	FormattedTag    string    `json:"formatted_tag"`
	LastTurnTime    time.Time `json:"last_turn_time"`
	CurrentTurnTime time.Time `json:"current_turn_time"`
}

// ConversationSnapshot captures an incremental slice of a conversation for reflection.
type ConversationSnapshot struct {
	ConversationID    string             `json:"conversation_id"`
	SessionKey        string             `json:"session_key"`
	AgentName         string             `json:"agent_name"`
	ProjectName       string             `json:"project_name"`
	WorkspaceDir      string             `json:"workspace_dir"`
	GenerationID      int64              `json:"generation_id"`
	LastTurnStatus    string             `json:"last_turn_status"`
	HasOpenQuestion   bool               `json:"has_open_question"`
	Turns             []CanonicalMessage `json:"turns"`
	AuditEntries      []AuditLog         `json:"audit_entries"`
	LastReflectedStep int                `json:"last_reflected_step"`
	CurrentStep       int                `json:"current_step"`
	Trigger           EvolutionTrigger   `json:"trigger"`
	Timestamp         time.Time          `json:"timestamp"`
}

// FeedbackSignal represents evaluated user feedback or behavioral correction.
type FeedbackSignal struct {
	Type         FeedbackType      `json:"type"`
	Sentiment    SentimentCategory `json:"sentiment"`
	RawFeedback  string            `json:"raw_feedback"`
	PriorContext string            `json:"prior_context"`
	Confidence   float64           `json:"confidence"`
	Timestamp    time.Time         `json:"timestamp"`
}

// MemoryCandidate represents an extracted lesson, preference, or ADR ready for persistence.
type MemoryCandidate struct {
	Category    MemoryCategory `json:"category"`
	Title       string         `json:"title"`
	Constraint  string         `json:"constraint"`
	Rationale   string         `json:"rationale,omitempty"`
	TargetFile  string         `json:"target_file"`
	ConflictKey string         `json:"conflict_key,omitempty"`
	IsDurable   bool           `json:"is_durable"`
	CreatedAt   time.Time      `json:"created_at"`
}

// RuleConstraint represents a parsed rule in MEMORY.md or USER.md for conflict resolution.
type RuleConstraint struct {
	Key          string         `json:"key"`
	Category     MemoryCategory `json:"category"`
	Section      string         `json:"section"`
	OriginalText string         `json:"original_text"`
	Content      string         `json:"content"`
	Timestamp    time.Time      `json:"timestamp"`
}
