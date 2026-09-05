package auth

import (
	"context"
	"fmt"

	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

// StorageReader defines the minimal persistence read queries required for policy decisions.
type StorageReader interface {
	GetAgent(ctx context.Context, name string) (*domain.Agent, error)
	CheckAgentAccess(ctx context.Context, agentName, userID string) (bool, string, error)
}

// ConfigProvider encapsulates administrative privilege resolution.
type ConfigProvider interface {
	IsSuperAdmin(principal domain.Principal) bool
}

// Engine implements the centralized ports.PolicyEngine.
type Engine struct {
	storage StorageReader
	config  ConfigProvider
}

// NewEngine constructs a new PolicyEngine instance.
func NewEngine(storage StorageReader, cfg ConfigProvider) *Engine {
	return &Engine{
		storage: storage,
		config:  cfg,
	}
}

// Authorize evaluates whether the given principal is permitted to perform the specified action on the target resource.
// Enforces strict Default-Deny semantics: any unmapped action, missing role, or unverified resource is rejected.
func (e *Engine) Authorize(ctx context.Context, principal domain.Principal, action domain.Action, resource domain.Resource) error {
	// 1. Principal Validation
	if principal.SubjectID == "" || principal.Provider == "" {
		return ports.ErrInvalidPrincipal
	}

	// 2. System Principals (Background Daemons / Cron / Maintenance)
	if principal.Kind == domain.PrincipalSystem {
		return e.authorizeSystemPrincipal(principal, action, resource)
	}

	// 3. SuperAdmin Evaluation
	if e.config != nil && e.config.IsSuperAdmin(principal) {
		return nil // SuperAdmin possesses unrestricted privileges across all actions and resources
	}

	// 4. Deny System Admin Actions for Non-SuperAdmins
	if isSystemAdminAction(action) {
		return fmt.Errorf("%w: action %s requires system superadmin privileges", ports.ErrAccessDenied, action)
	}

	// 5. Resolve Effective Agent Role
	var role domain.AgentRole = domain.AgentRoleNone
	if resource.AgentName != "" && e.storage != nil {
		r, err := e.ResolveAgentRole(ctx, principal, resource.AgentName)
		if err != nil {
			return fmt.Errorf("%w: failed to resolve agent role: %v", ports.ErrAccessDenied, err)
		}
		role = r
	}

	// 6. Evaluate Role against Requested Action
	return e.evaluateRoleAction(role, action, resource, principal)
}

// ResolveAgentRole determines the effective granular role of a principal on a named agent profile.
func (e *Engine) ResolveAgentRole(ctx context.Context, principal domain.Principal, agentName string) (domain.AgentRole, error) {
	if agentName == "" {
		return domain.AgentRoleNone, nil
	}

	// SuperAdmin possesses implicit Owner status
	if e.config != nil && e.config.IsSuperAdmin(principal) {
		return domain.AgentRoleOwner, nil
	}

	if e.storage == nil {
		return domain.AgentRoleNone, nil
	}

	agent, err := e.storage.GetAgent(ctx, agentName)
	if err != nil {
		return domain.AgentRoleNone, nil
	}
	if agent == nil {
		return domain.AgentRoleNone, nil
	}

	// 1. Direct Verified Owner
	if agent.OwnerID != "" && agent.OwnerID == principal.SubjectID {
		return domain.AgentRoleOwner, nil
	}

	// 2. Collaborator Access via ACL table
	allowed, roleStr, err := e.storage.CheckAgentAccess(ctx, agentName, principal.SubjectID)
	if err == nil && allowed {
		switch roleStr {
		case "admin":
			return domain.AgentRoleAdmin, nil
		case "operator":
			return domain.AgentRoleOperator, nil
		case "viewer":
			return domain.AgentRoleViewer, nil
		}
	}

	// 3. Public Agent Visibility
	if agent.IsPublic {
		return domain.AgentRolePublic, nil
	}

	// Unowned and unshared agent is inaccessible to non-superadmins
	return domain.AgentRoleNone, nil
}

func (e *Engine) authorizeSystemPrincipal(principal domain.Principal, action domain.Action, resource domain.Resource) error {
	switch principal.SubjectID {
	case "system:scheduler":
		if action == domain.ActionTurnExecute {
			return nil
		}
	case "system:heartbeat":
		if action == domain.ActionTurnExecute {
			return nil
		}
	case "system:compactor":
		if action == domain.ActionTurnExecute || action == domain.ActionSessionCompact {
			return nil
		}
	case "system:reflection":
		if action == domain.ActionTurnExecute || action == domain.ActionConvoInspect {
			return nil
		}
	case "system:subagent":
		if action == domain.ActionTurnExecute || action == domain.ActionTaskDispatch {
			return nil
		}
	}
	return fmt.Errorf("%w: system principal %s not permitted for action %s", ports.ErrAccessDenied, principal.SubjectID, action)
}

func (e *Engine) evaluateRoleAction(role domain.AgentRole, action domain.Action, resource domain.Resource, principal domain.Principal) error {
	switch role {
	case domain.AgentRoleOwner:
		// Owner possesses all agent-scoped permissions
		return nil

	case domain.AgentRoleAdmin:
		// Agent Admin has operator permissions + project, schedule, and sharing management
		// Excludes deleting or changing owner of the agent
		switch action {
		case domain.ActionAgentDelete, domain.ActionAgentEdit, domain.ActionAgentClaim:
			return fmt.Errorf("%w: action %s requires agent owner or superadmin", ports.ErrAccessDenied, action)
		default:
			return nil
		}

	case domain.AgentRoleOperator:
		// Operator has viewer permissions + session reset, conversation mutations, and task mutations within scope
		switch action {
		case domain.ActionTurnExecute, domain.ActionTurnInterrupt, domain.ActionTurnForceUnlock,
			domain.ActionSessionReset, domain.ActionSessionCompact,
			domain.ActionConvoInspect, domain.ActionConvoSwitch, domain.ActionConvoDelete,
			domain.ActionConvoPin, domain.ActionConvoArchive, domain.ActionConvoRename,
			domain.ActionTaskDispatch, domain.ActionTaskInspect, domain.ActionTaskReply, domain.ActionTaskCancel:
			return nil
		default:
			return fmt.Errorf("%w: operator role cannot perform %s", ports.ErrAccessDenied, action)
		}

	case domain.AgentRoleViewer:
		// Viewer can only execute model turns (read-only/plan) and inspect conversations
		switch action {
		case domain.ActionTurnExecute, domain.ActionConvoInspect, domain.ActionConvoSwitch:
			return nil
		default:
			return fmt.Errorf("%w: viewer role cannot perform %s", ports.ErrAccessDenied, action)
		}

	case domain.AgentRolePublic:
		// Public user can only execute model turns on explicitly public agents
		if action == domain.ActionTurnExecute && resource.IsPublic {
			return nil
		}
		if action == domain.ActionConvoInspect && resource.IsPublic {
			return nil
		}
		return fmt.Errorf("%w: public user cannot perform %s", ports.ErrAccessDenied, action)

	case domain.AgentRoleNone, "":
		return fmt.Errorf("%w: unassigned role cannot perform %s on agent %s", ports.ErrAccessDenied, action, resource.AgentName)

	default:
		return fmt.Errorf("%w: unrecognized role %s", ports.ErrAccessDenied, role)
	}
}

func isSystemAdminAction(action domain.Action) bool {
	switch action {
	case domain.ActionPluginManage,
		domain.ActionSecurityConfig,
		domain.ActionWhitelistManage,
		domain.ActionModeChange,
		domain.ActionStreamModeChange,
		domain.ActionModelDefaultSet,
		domain.ActionAgentClaim,
		domain.ActionProjectCreatePath,
		domain.ActionTaskPurge:
		return true
	default:
		return false
	}
}
