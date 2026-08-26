package domain

import (
	"encoding/json"
	"time"
)

// SubagentTaskStatus defines the operational lifecycle state of an asynchronous sub-agent task.
type SubagentTaskStatus string

const (
	TaskStatusPending      SubagentTaskStatus = "PENDING"
	TaskStatusRunning      SubagentTaskStatus = "RUNNING"
	TaskStatusWaitingInput SubagentTaskStatus = "WAITING_FOR_INPUT"
	TaskStatusCompleted    SubagentTaskStatus = "COMPLETED"
	TaskStatusFailed       SubagentTaskStatus = "FAILED"
	TaskStatusCancelled    SubagentTaskStatus = "CANCELLED"
)

// SubagentCallbackMode defines how the sub-agent task reports back upon completion or clarification.
type SubagentCallbackMode string

const (
	CallbackNotifyUser   SubagentCallbackMode = "notify_user"
	CallbackInvokeMain   SubagentCallbackMode = "callback_main"
	CallbackSilentMemory SubagentCallbackMode = "silent"
)

// SubagentTask represents an asynchronous background work unit executed by a sub-agent.
type SubagentTask struct {
	ID                   string               `json:"id"`
	ParentSessionKey     string               `json:"parent_session_key"`
	ParentConversationID string               `json:"parent_conversation_id"`
	SubConversationID    string               `json:"sub_conversation_id"`
	AgentName            string               `json:"agent_name"`
	ProjectName          string               `json:"project_name"`
	Title                string               `json:"title"`
	Prompt               string               `json:"prompt"`
	Model                string               `json:"model"`
	Effort               string               `json:"effort"`
	WorkspaceMode        string               `json:"workspace_mode"` // "share" | "scratch" | "persona"
	CallbackMode         SubagentCallbackMode `json:"callback_mode"`
	Status               SubagentTaskStatus   `json:"status"`
	CurrentStep          int                  `json:"current_step"`
	CurrentTool          string               `json:"current_tool"`
	ProgressMessage      string               `json:"progress_message"`
	PendingQuestion      string               `json:"pending_question,omitempty"`
	ResultSummary        string               `json:"result_summary"`
	Artifacts            []Attachment         `json:"artifacts,omitempty"`
	ErrorMessage         string               `json:"error_message,omitempty"`
	Usage                TokenUsage           `json:"usage"`
	DurationSeconds      float64              `json:"duration_seconds"`
	CreatedAt            time.Time            `json:"created_at"`
	UpdatedAt            time.Time            `json:"updated_at"`
}

// IsTerminal returns true if the task has reached an immutable end state.
func (t *SubagentTask) IsTerminal() bool {
	return t.Status == TaskStatusCompleted || t.Status == TaskStatusFailed || t.Status == TaskStatusCancelled
}

// IsActive returns true if the task is queued, running, or waiting for input.
func (t *SubagentTask) IsActive() bool {
	return t.Status == TaskStatusPending || t.Status == TaskStatusRunning || t.Status == TaskStatusWaitingInput
}

// ArtifactsJSON marshals the artifacts slice to a JSON string for SQLite storage.
func (t *SubagentTask) ArtifactsJSON() string {
	if len(t.Artifacts) == 0 {
		return "[]"
	}
	data, err := json.Marshal(t.Artifacts)
	if err != nil {
		return "[]"
	}
	return string(data)
}

// ParseArtifactsJSON unmarshals a JSON string from SQLite into the Artifacts slice.
func (t *SubagentTask) ParseArtifactsJSON(raw string) {
	if len(raw) == 0 || raw == "[]" {
		t.Artifacts = nil
		return
	}
	var atts []Attachment
	if err := json.Unmarshal([]byte(raw), &atts); err == nil {
		t.Artifacts = atts
	}
}
