package ports

import (
	"context"

	"agyent/internal/core/domain"
)

// SecurityPort defines authorization and inbound inspection contracts.
type SecurityPort interface {
	// AuthorizeUser checks if a given user is whitelisted or permitted to interact with the system.
	AuthorizeUser(ctx context.Context, user domain.User) (bool, error)

	// AuthorizeGroup checks if a given group/supergroup chat is permitted to interact with the system.
	AuthorizeGroup(ctx context.Context, group domain.Group) (bool, error)

	// InspectInput scans input text for dangerous command injection or unauthorized patterns.
	InspectInput(ctx context.Context, text string) error
}

// SecurityManagerPort coordinates multi-layer tool interception, path jailing, and policy decisions.
type SecurityManagerPort interface {
	// EvaluateToolCall evaluates any tool call synchronously intercepted by PreToolUse hook.
	EvaluateToolCall(ctx context.Context, req domain.ToolEvaluationRequest) (domain.SecurityDecision, error)

	// EvaluateCommand checks a shell command against active blacklist/whitelist/HITL rules.
	EvaluateCommand(ctx context.Context, sessionKey string, role string, cmd string) (domain.SecurityDecision, error)

	// EvaluatePath verifies if target file access is permitted within the active workspace jail.
	EvaluatePath(ctx context.Context, sessionKey string, workspaceDir string, targetPath string, isWrite bool) (domain.SecurityDecision, error)

	// EvaluateURL verifies that destination URL does not target private IPs or cloud metadata (with DNS Rebinding protection).
	EvaluateURL(ctx context.Context, urlStr string) (domain.SecurityDecision, error)

	// SanitizeToolOutput inspects external tool outputs (web, file, shell) for sensitive secrets and indirect injections.
	SanitizeToolOutput(ctx context.Context, toolName string, output string) (string, error)

	// GrantSessionPermission adds a temporary permission grant to the session cache.
	GrantSessionPermission(sessionKey string, pattern string)

	// SetPreset switches the active security preset.
	SetPreset(preset domain.SecurityPreset)

	// SetRedactionMode switches the active secret redaction mode.
	SetRedactionMode(mode domain.RedactionMode)

	// AddWhitelistEntry dynamically appends a custom command or path to the active whitelist.
	AddWhitelistEntry(entry string)

	// GetDashboardSummary returns statistics for /security slash command.
	GetDashboardSummary(sessionKey string) domain.SecurityDashboard

	// EnsureWorkspaceHooks guarantees that .agents/hooks.json is provisioned in the given workspace.
	EnsureWorkspaceHooks(workspaceDir string) error

	// RegisterActiveTurn registers the active sessionKey, preset, and workspace associated with a running turn.
	RegisterActiveTurn(turn domain.TurnSecurityContext)

	// UnregisterActiveTurn removes the active turn association when execution concludes.
	UnregisterActiveTurn(convID string, workspaceDir string)

	// ResolveSessionKey retrieves the active sessionKey for a given conversationID or workspace.
	ResolveSessionKey(convID string, workspaceDir string) string

	// ResolveTurnContext retrieves the full active TurnSecurityContext for a given conversationID or workspace.
	ResolveTurnContext(convID string, workspaceDir string) (domain.TurnSecurityContext, bool)

	// CancelSessionApprovals terminates all pending approval requests for a given session.
	CancelSessionApprovals(sessionKey string)
}

// HookIPCPort defines the IPC server interface communicating with agyent-hook binary.
type HookIPCPort interface {
	Start(ctx context.Context) error
	Stop() error
	HandleHookRequest(ctx context.Context, req domain.HookRequest) (domain.HookResponse, error)
}

// HITLApprovalPort coordinates interactive approval requests over communication channels.
type HITLApprovalPort interface {
	// RequestApproval sends an interactive card and suspends execution until user action or timeout.
	RequestApproval(ctx context.Context, req domain.ApprovalRequest) (domain.ApprovalDecision, error)

	// HandleCallback processes inline keyboard clicks from Telegram/Discord with strict RBAC verification.
	HandleCallback(ctx context.Context, callbackID string, userID int64, action string) error

	// CancelPendingRequest terminates a pending approval request when the turn is aborted.
	CancelPendingRequest(requestID string)

	// CancelPendingRequestsForSession terminates all pending approval requests for a given session.
	CancelPendingRequestsForSession(sessionKey string)
}
