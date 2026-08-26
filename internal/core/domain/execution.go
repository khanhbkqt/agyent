package domain

import "time"

// ExecutionRequest specifies arguments needed to run an AGY session/command.
type ExecutionRequest struct {
	Prompt                     string        `json:"prompt"`
	ConversationID             string        `json:"conversation_id,omitempty"`
	WorkspaceDir               string        `json:"workspace_dir"`
	Files                      []string      `json:"files,omitempty"`
	Attachments                []Attachment  `json:"attachments,omitempty"`
	Model                      string        `json:"model,omitempty"`
	Mode                       string        `json:"mode,omitempty"`   // e.g. "accept-edits", "plan"
	Effort                     string        `json:"effort,omitempty"` // e.g. "low", "medium", "high"
	Timeout                    time.Duration `json:"timeout"`
	DangerouslySkipPermissions bool          `json:"dangerously_skip_permissions"`
}

// ExecutionResult contains the outcome of an AGY execution run.
type ExecutionResult struct {
	Success        bool         `json:"success"`
	ConversationID string       `json:"conversation_id"`
	ResponseText   string       `json:"response_text"`
	DurationSec    float64      `json:"duration_sec"`
	Usage          TokenUsage   `json:"usage"`
	Artifacts      []Attachment `json:"artifacts,omitempty"`
	Error          string       `json:"error,omitempty"`
}
