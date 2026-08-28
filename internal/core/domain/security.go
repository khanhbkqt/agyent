package domain

import (
	"time"
)

// SecurityPreset defines predefined zero-config security postures.
type SecurityPreset string

const (
	PresetUnrestricted SecurityPreset = "unrestricted"
	PresetDeveloper    SecurityPreset = "developer"
	PresetBalanced     SecurityPreset = "balanced"
	PresetStrict       SecurityPreset = "strict"
	PresetReadOnly     SecurityPreset = "read_only"
)

// AllSecurityPresets lists all supported security presets ordered from least secure to most secure.
var AllSecurityPresets = []SecurityPreset{
	PresetUnrestricted,
	PresetDeveloper,
	PresetBalanced,
	PresetStrict,
	PresetReadOnly,
}

// PresetLevel returns the numeric security level (higher = more secure/restrictive).
func PresetLevel(preset SecurityPreset) int {
	switch preset {
	case PresetUnrestricted:
		return 0
	case PresetDeveloper:
		return 1
	case PresetBalanced, "":
		return 2
	case PresetStrict:
		return 3
	case PresetReadOnly:
		return 4
	default:
		return 2
	}
}

// CanSwitchPreset evaluates whether switching from fromPreset to toPreset is permitted (monotonic upgrade).
// An agent cannot transition to a more permissive/lower security level than its baseline.
func CanSwitchPreset(fromPreset, toPreset SecurityPreset) bool {
	return PresetLevel(toPreset) >= PresetLevel(fromPreset)
}

// GetAllowedPresets returns all security presets that are at or above the given baseline level.
func GetAllowedPresets(baseline SecurityPreset) []SecurityPreset {
	minLevel := PresetLevel(baseline)
	var allowed []SecurityPreset
	for _, p := range AllSecurityPresets {
		if PresetLevel(p) >= minLevel {
			allowed = append(allowed, p)
		}
	}
	return allowed
}

// SecurityDecisionType represents the policy evaluator outcome.
type SecurityDecisionType string

const (
	DecisionAllow SecurityDecisionType = "allow"
	DecisionDeny  SecurityDecisionType = "deny"
	DecisionAsk   SecurityDecisionType = "ask"
)

// RedactionMode controls how sensitive tokens and secrets are redacted.
type RedactionMode string

const (
	RedactStrict     RedactionMode = "strict"
	RedactPermissive RedactionMode = "permissive"
	RedactAuditOnly  RedactionMode = "audit_only"
)

// SecurityDecision encapsulates the decision and rationale for an intercepted tool call.
type SecurityDecision struct {
	Decision  SecurityDecisionType   `json:"decision"`
	Reason    string                 `json:"reason,omitempty"`
	Overwrite map[string]interface{} `json:"overwrite,omitempty"`
	LatencyMs float64                `json:"latency_ms,omitempty"`
}

// ToolEvaluationRequest represents an incoming tool execution intercept.
type ToolEvaluationRequest struct {
	SessionKey     string                 `json:"session_key,omitempty"`
	Role           string                 `json:"role,omitempty"`
	ToolName       string                 `json:"tool_name"`
	Args           map[string]interface{} `json:"args"`
	ConversationID string                 `json:"conversation_id,omitempty"`
	StepIdx        int                    `json:"step_idx,omitempty"`
	WorkspaceDir   string                 `json:"workspace_dir,omitempty"`
	IsSubagent     bool                   `json:"is_subagent,omitempty"`
	CascadeDepth   int                    `json:"cascade_depth,omitempty"`
}

// TurnSecurityContext captures the security identity and preset bound to an active turn.
type TurnSecurityContext struct {
	ConversationID string         `json:"conversation_id"`
	SessionKey     string         `json:"session_key"`
	WorkspaceDir   string         `json:"workspace_dir"`
	Preset         SecurityPreset `json:"preset"`
	AgentName      string         `json:"agent_name"`
	CreatedAt      time.Time      `json:"created_at"`
}

// ToolEvaluationResponse represents the output sent back to the hook bridge.
type ToolEvaluationResponse struct {
	Decision  SecurityDecisionType   `json:"decision"`
	Reason    string                 `json:"reason,omitempty"`
	Overwrite map[string]interface{} `json:"overwrite,omitempty"`
	LatencyMs float64                `json:"latency_ms,omitempty"`
}

// ApprovalRequest represents an interactive Human-In-The-Loop (HITL) approval card.
type ApprovalRequest struct {
	RequestID    string                `json:"request_id"`
	SessionKey   string                `json:"session_key"`
	ToolName     string                `json:"tool_name"`
	CommandLine  string                `json:"command_line,omitempty"`
	TargetFile   string                `json:"target_file,omitempty"`
	Directory    string                `json:"directory,omitempty"`
	AgentName    string                `json:"agent_name,omitempty"`
	TaskID       string                `json:"task_id,omitempty"`
	RiskLevel    string                `json:"risk_level,omitempty"` // "Low", "Medium", "High", "Critical"
	DiffPreview  string                `json:"diff_preview,omitempty"`
	IsConfigEdit bool                  `json:"is_config_edit,omitempty"`
	CreatedAt    time.Time             `json:"created_at"`
	ExpiresAt    time.Time             `json:"expires_at"`
	ResponseChan chan ApprovalDecision `json:"-"`
}

// ApprovalDecision represents the user's action on an interactive HITL approval card.
type ApprovalDecision struct {
	RequestID string    `json:"request_id"`
	UserID    int64     `json:"user_id"`
	Action    string    `json:"action"` // "allow_once", "allow_session", "deny", "force_kill"
	Approved  bool      `json:"approved"`
	Pattern   string    `json:"pattern,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// SecurityDashboard provides live metrics for the /security slash command.
type SecurityDashboard struct {
	Preset           SecurityPreset `json:"preset"`
	ActiveJail       string         `json:"active_jail"`
	AllowedPaths     []string       `json:"allowed_paths"`
	AllowedCommands  []string       `json:"allowed_commands"`
	TotalEvaluations int64          `json:"total_evaluations"`
	BlockedToday     int64          `json:"blocked_today"`
	ApprovedToday    int64          `json:"approved_today"`
	PendingApprovals int            `json:"pending_approvals"`
	RedactionMode    RedactionMode  `json:"redaction_mode"`
	ConfigDelegated  bool           `json:"config_delegated"`
}

// AuditSecurityEvent models security-related audit log records.
type AuditSecurityEvent struct {
	EventID        string                 `json:"event_id"`
	Timestamp      time.Time              `json:"timestamp"`
	SessionKey     string                 `json:"session_key"`
	UserID         int64                  `json:"user_id,omitempty"`
	AgentName      string                 `json:"agent_name,omitempty"`
	Checkpoint     string                 `json:"checkpoint"` // "InboundRBAC", "PathJail", "PreToolUse", "SubagentGate", "SSRF", "PostToolSanitizer", "DLP"
	ToolName       string                 `json:"tool_name,omitempty"`
	TargetResource string                 `json:"target_resource,omitempty"`
	Decision       SecurityDecisionType   `json:"decision"`
	Reason         string                 `json:"reason,omitempty"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
}

// HookRequest is the wire format passed between agyent-hook and the IPC server.
type HookRequest struct {
	HookType       string       `json:"hook_type"` // "pre", "post"
	ToolCall       HookToolCall `json:"toolCall"`
	StepIdx        int          `json:"stepIdx,omitempty"`
	ConversationID string       `json:"conversationId,omitempty"`
	WorkspacePaths []string     `json:"workspacePaths,omitempty"`
	TranscriptPath string       `json:"transcriptPath,omitempty"`
	Error          string       `json:"error,omitempty"`
}

// HookToolCall represents the tool execution descriptor from Antigravity.
type HookToolCall struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args"`
}

// HookResponse is the wire response returned to agyent-hook.
type HookResponse struct {
	Decision            string                 `json:"decision,omitempty"` // "allow", "deny", "ask"
	Reason              string                 `json:"reason,omitempty"`
	Overwrite           map[string]interface{} `json:"overwrite,omitempty"`
	PermissionOverrides []string               `json:"permissionOverrides,omitempty"`
}
