package domain

import (
	"errors"
	"strings"
	"time"
)

// EffortNone indicates that reasoning effort flag must not be passed to CLI.
const EffortNone = "none"

// ExecutionRequest specifies arguments needed to run an AGY session/command.
type ExecutionRequest struct {
	Prompt                     string              `json:"prompt"`
	ConversationID             string              `json:"conversation_id,omitempty"`
	TurnID                     string              `json:"turn_id,omitempty"`
	WorkspaceDir               string              `json:"workspace_dir"`
	Files                      []string            `json:"files,omitempty"`
	Attachments                []Attachment        `json:"attachments,omitempty"`
	Model                      string              `json:"model,omitempty"`
	Mode                       string              `json:"mode,omitempty"`   // e.g. "accept-edits", "plan"
	Effort                     string              `json:"effort,omitempty"` // e.g. "low", "medium", "high"
	DisableEffort              bool                `json:"disable_effort,omitempty"`
	Timeout                    time.Duration       `json:"timeout"`
	DangerouslySkipPermissions bool                `json:"dangerously_skip_permissions"`
	AgentName                  string              `json:"agent_name,omitempty"`
	ProjectName                string              `json:"project_name,omitempty"`
	SessionKey                 string              `json:"session_key,omitempty"`
	UserID                     string              `json:"user_id,omitempty"`
	Env                        map[string]string   `json:"env,omitempty"`
	Admission                  *ExecutionAdmission `json:"admission,omitempty"`
}

// ExecutionAdmission represents an immutable authorization admission ticket
// issued by internal/core/execution.Service to admit an AGY CLI execution.
type ExecutionAdmission struct {
	AdmissionID          string    `json:"admission_id"`
	TenantID             string    `json:"tenant_id"`
	AgentName            string    `json:"agent_name"`
	AgentGeneration      int       `json:"agent_generation"`
	ExecutionHostID      string    `json:"execution_host_id"`
	AGYConfigNamespaceID string    `json:"agy_config_namespace_id"`
	AGYProjectID         string    `json:"agy_project_id"`
	WorkspaceDir         string    `json:"workspace_dir"`
	TurnID               string    `json:"turn_id"`
	SessionKey           string    `json:"session_key"`
	Principal            Principal `json:"principal"`
	Mode                 string    `json:"mode,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
}

// AGYProjectStatus represents the operational lifecycle state of an AGY project mapping.
type AGYProjectStatus string

const (
	AGYProjectStatusActive      AGYProjectStatus = "ACTIVE"
	AGYProjectStatusQuarantined AGYProjectStatus = "QUARANTINED"
	AGYProjectStatusRevoked     AGYProjectStatus = "REVOKED"
)

// AGYProjectMapping represents the persistent mapping between an agent and its dedicated AGY project scope.
type AGYProjectMapping struct {
	TenantID             string           `json:"tenant_id"`
	AgentName            string           `json:"agent_name"`
	AgentGeneration      int              `json:"agent_generation"`
	ExecutionHostID      string           `json:"execution_host_id"`
	AGYConfigNamespaceID string           `json:"agy_config_namespace_id"`
	AGYProjectID         string           `json:"agy_project_id"`
	WorkspaceDir         string           `json:"workspace_dir"`
	Status               AGYProjectStatus `json:"status"`
	CreatedAt            time.Time        `json:"created_at"`
	UpdatedAt            time.Time        `json:"updated_at"`
}

// AGYCapabilities represents detected host CLI capabilities.
type AGYCapabilities struct {
	Version                     string `json:"version"`
	SupportsSandbox             bool   `json:"supports_sandbox"`
	SupportsProjectScopedGrants bool   `json:"supports_project_scoped_grants"`
	SupportsStreamJSON          bool   `json:"supports_stream_json"`
	Platform                    string `json:"platform"`
}

// ExecutionOutcome classifies terminal execution results independently from
// adapter-specific error strings.
type ExecutionOutcome string

const (
	StatusNativePermissionDenied ExecutionOutcome = "NATIVE_PERMISSION_DENIED"
	StatusPolicyDenied           ExecutionOutcome = "POLICY_DENIED"
	StatusExecutionFailed        ExecutionOutcome = "EXECUTION_FAILED"
)

// ExecutionOutcomeError preserves a machine-readable outcome while retaining
// an optional wrapped cause for errors.Is/errors.As callers.
type ExecutionOutcomeError struct {
	Outcome ExecutionOutcome
	Message string
	Cause   error
}

func (e *ExecutionOutcomeError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return string(e.Outcome)
}

func (e *ExecutionOutcomeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// NewExecutionOutcomeError constructs a normalized typed execution error. It
// removes repeated classification prefixes produced by nested adapters.
func NewExecutionOutcomeError(outcome ExecutionOutcome, message string, cause error) error {
	message = strings.TrimSpace(message)
	prefix := executionOutcomePrefix(outcome)
	if prefix != "" {
		for strings.HasPrefix(strings.ToLower(message), strings.ToLower(prefix+":")) {
			message = strings.TrimSpace(message[len(prefix)+1:])
		}
		if message == "" {
			message = prefix
		} else {
			message = prefix + ": " + message
		}
	}
	return &ExecutionOutcomeError{Outcome: outcome, Message: message, Cause: cause}
}

// ExecutionOutcomeFromError extracts the outcome from a typed execution error.
func ExecutionOutcomeFromError(err error) ExecutionOutcome {
	var outcomeErr *ExecutionOutcomeError
	if errors.As(err, &outcomeErr) {
		return outcomeErr.Outcome
	}
	return ""
}

func executionOutcomePrefix(outcome ExecutionOutcome) string {
	switch outcome {
	case StatusNativePermissionDenied:
		return "native permission denial"
	case StatusPolicyDenied:
		return "policy denial"
	case StatusExecutionFailed:
		return "execution failed"
	default:
		return ""
	}
}

// ExecutionResult contains the outcome of an AGY execution run.
type ExecutionResult struct {
	Success        bool             `json:"success"`
	Outcome        ExecutionOutcome `json:"outcome,omitempty"`
	ConversationID string           `json:"conversation_id"`
	ResponseText   string           `json:"response_text"`
	DurationSec    float64          `json:"duration_sec"`
	NumTurns       int              `json:"num_turns,omitempty"`
	Usage          TokenUsage       `json:"usage"`
	Artifacts      []Attachment     `json:"artifacts,omitempty"`
	Error          string           `json:"error,omitempty"`
}
