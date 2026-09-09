package domain

import (
	"path/filepath"
	"strings"
	"time"
)

// ExtractBaseCommand extracts the primary binary/executable name from a command line string.
func ExtractBaseCommand(rawCmd string) string {
	trimmed := strings.TrimSpace(rawCmd)
	if trimmed == "" {
		return ""
	}
	fields := strings.Fields(trimmed)
	for _, f := range fields {
		// Skip leading environment variable assignments (e.g. FOO=bar python3 ...)
		if strings.Contains(f, "=") && !strings.HasPrefix(f, "./") && !strings.HasPrefix(f, "/") && !strings.HasPrefix(f, `\`) {
			continue
		}
		base := filepath.Base(f)
		base = strings.TrimSuffix(base, ".exe")
		return base
	}
	if len(fields) > 0 {
		return filepath.Base(fields[0])
	}
	return ""
}

// SecurityPreset defines predefined zero-config security postures.
type SecurityPreset string

const (
	PresetUnrestricted   SecurityPreset = "unrestricted"
	PresetDeveloper      SecurityPreset = "developer"
	PresetWorkspaceOnly  SecurityPreset = "workspace_only"
	PresetBalanced       SecurityPreset = "balanced"
	PresetStrict         SecurityPreset = "strict"
	PresetReadOnly       SecurityPreset = "read_only"
)

// AllSecurityPresets lists all supported security presets ordered from least secure to most secure.
var AllSecurityPresets = []SecurityPreset{
	PresetUnrestricted,
	PresetDeveloper,
	PresetWorkspaceOnly,
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
	case PresetWorkspaceOnly:
		return 2
	case PresetBalanced, "":
		return 3
	case PresetStrict:
		return 4
	case PresetReadOnly:
		return 5
	default:
		return 3
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

// TurnSecurityContext captures the security identity, authorization principal, and preset bound to an active turn.
type TurnSecurityContext struct {
	TurnID         string         `json:"turn_id"`
	Principal      Principal      `json:"principal"`
	Action         Action         `json:"action"`
	Resource       Resource       `json:"resource"`
	ConversationID string         `json:"conversation_id"`
	SessionKey     string         `json:"session_key"`
	AgentName      string         `json:"agent_name"`
	ProjectName    string         `json:"project_name,omitempty"`
	WorkspaceDir   string         `json:"workspace_dir"`
	Preset         SecurityPreset `json:"preset"`
	AllowedPaths   []string       `json:"allowed_paths,omitempty"`
	RuntimeNonce   string         `json:"runtime_nonce,omitempty"`
	ProcessID      int            `json:"process_id,omitempty"`
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
	ShortCode    string                `json:"short_code,omitempty"`
	SessionKey   string                `json:"session_key"`
	ToolName     string                `json:"tool_name"`
	CommandLine  string                `json:"command_line,omitempty"`
	TargetFile   string                `json:"target_file,omitempty"`
	Directory    string                `json:"directory,omitempty"`
	AgentName    string                `json:"agent_name,omitempty"`
	TaskID       string                `json:"task_id,omitempty"`
	RiskLevel    string                `json:"risk_level,omitempty"` // "Low", "Medium", "High", "Critical"
	Reason       string                `json:"reason,omitempty"`
	DiffPreview  string                `json:"diff_preview,omitempty"`
	IsConfigEdit bool                  `json:"is_config_edit,omitempty"`
	CreatedAt    time.Time             `json:"created_at"`
	ExpiresAt    time.Time             `json:"expires_at"`
	ResponseChan chan ApprovalDecision `json:"-"`
}

// ApprovalAction represents canonical user action types for interactive approvals.
type ApprovalAction string

// Approval action constants.
const (
	ActionAllowOnce       ApprovalAction = "allow_once"
	ActionAllowSession    ApprovalAction = "allow_session"
	ActionAllowAllSession ApprovalAction = "allow_all_session"
	ActionDeny            ApprovalAction = "deny"
	ActionForceKill       ApprovalAction = "force_kill"
	ActionTimeout         ApprovalAction = "timeout"
	ActionCancelled       ApprovalAction = "cancelled"
)

// ParseApprovalAction converts a raw string (including quick aliases, numbers, and Vietnamese keywords)
// into a canonical ApprovalAction.
func ParseApprovalAction(raw string) (ApprovalAction, bool) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	switch normalized {
	case "allow_all_session", "all_session", "all", "allow_all", "3", "tat_ca", "tat ca", "tatca", "tất cả", "tất_cả", "tấtcả":
		return ActionAllowAllSession, true
	case "allow_session", "session", "always", "2", "phien", "ca_phien", "ca phien", "phiên", "cả phiên", "cả_phiên", "cảphiên":
		return ActionAllowSession, true
	case "allow_once", "allow", "once", "approve", "ok", "yes", "y", "1", "duyet", "dong_y", "dong y", "dongy", "duyệt", "đồng ý", "đồng_ý", "đồngý", "true":
		return ActionAllowOnce, true
	case "deny", "reject", "no", "n", "4", "tu_choi", "tu choi", "tuchoi", "từ chối", "từ_chối", "từchối", "ko", "khong", "không", "false":
		return ActionDeny, true
	case "force_kill", "kill", "terminate", "stop", "5", "dung", "dừng":
		return ActionForceKill, true
	case "timeout":
		return ActionTimeout, true
	case "cancelled", "cancel", "huy", "hủy":
		return ActionCancelled, true
	default:
		return ApprovalAction(normalized), false
	}
}

// SessionGrant models an in-memory session permission grant.
type SessionGrant struct {
	Pattern   string    `json:"pattern"`
	Scope     string    `json:"scope"` // "wildcard" | "command"
	GrantedBy int64     `json:"granted_by,omitempty"`
	GrantedAt time.Time `json:"granted_at"`
}

// ApprovalDecision represents the user's action on an interactive HITL approval card.
type ApprovalDecision struct {
	RequestID string         `json:"request_id"`
	UserID    int64          `json:"user_id"`
	Action    ApprovalAction `json:"action"` // "allow_once", "allow_session", "allow_all_session", "deny", "force_kill"
	Approved  bool           `json:"approved"`
	Pattern   string         `json:"pattern,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
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
