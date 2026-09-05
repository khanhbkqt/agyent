package engine

import (
	"context"
	"fmt"

	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

// authorizeAgentAction enforces the policy matrix for a command operating in
// an agent/session/project scope. The production bootstrap wires a policy
// engine; retaining the nil guard keeps isolated unit tests backward compatible.
func (e *Engine) authorizeAgentAction(
	ctx context.Context,
	sender domain.SenderUser,
	action domain.Action,
	kind domain.ResourceKind,
	id string,
	session *domain.Session,
) error {
	if e.policyEngine == nil {
		return nil
	}
	if session == nil || session.ActiveAgent == "" {
		return fmt.Errorf("%w: missing active agent context", ports.ErrAccessDenied)
	}

	resource := domain.Resource{
		Kind:        kind,
		ID:          id,
		SessionKey:  session.SessionKey,
		AgentName:   session.ActiveAgent,
		ProjectName: session.ActiveProject,
	}
	if agent, err := e.storage.GetAgent(ctx, session.ActiveAgent); err == nil && agent != nil {
		resource.OwnerID = agent.OwnerID
		resource.IsPublic = agent.IsPublic
	}

	principal := domain.Principal{
		Kind:      domain.PrincipalUser,
		Provider:  sender.Provider,
		SubjectID: sender.ID,
	}
	if principal.Provider == "" {
		principal.Provider = "telegram"
	}
	return e.policyEngine.Authorize(ctx, principal, action, resource)
}

func (e *Engine) authorizeTargetAgentAction(ctx context.Context, sender domain.SenderUser, action domain.Action, agentName string) error {
	if e.policyEngine == nil {
		return fmt.Errorf("%w: policy engine is not initialized", ports.ErrAccessDenied)
	}
	resource := domain.Resource{Kind: domain.ResourceKindAgent, ID: agentName, AgentName: agentName}
	if agent, err := e.storage.GetAgent(ctx, agentName); err == nil && agent != nil {
		resource.OwnerID = agent.OwnerID
		resource.IsPublic = agent.IsPublic
	}
	provider := sender.Provider
	if provider == "" {
		provider = "telegram"
	}
	return e.policyEngine.Authorize(ctx, domain.Principal{
		Kind:      domain.PrincipalUser,
		Provider:  provider,
		SubjectID: sender.ID,
	}, action, resource)
}

func commandAction(cmd string, args []string) (domain.Action, domain.ResourceKind, bool) {
	subcmd := ""
	if len(args) > 0 {
		subcmd = args[0]
	}

	switch cmd {
	case "/task":
		switch subcmd {
		case "cancel":
			return domain.ActionTaskCancel, domain.ResourceKindSubagentTask, true
		case "reply":
			return domain.ActionTaskReply, domain.ResourceKindSubagentTask, true
		case "clean", "purge":
			return domain.ActionTaskPurge, domain.ResourceKindSubagentTask, true
		default:
			return domain.ActionTaskInspect, domain.ResourceKindSubagentTask, true
		}
	case "/tasks", "/subagents":
		return domain.ActionTaskInspect, domain.ResourceKindSubagentTask, true
	case "/schedule", "/schedules", "/cron":
		if subcmd == "cancel" || subcmd == "delete" || subcmd == "del" {
			return domain.ActionScheduleCancel, domain.ResourceKindSchedule, true
		}
		return domain.ActionScheduleList, domain.ResourceKindSchedule, true
	case "/heartbeat", "/hb":
		switch subcmd {
		case "on", "enable", "off", "disable", "interval":
			return domain.ActionHeartbeatConfig, domain.ResourceKindAgent, true
		case "trigger", "run", "now":
			return domain.ActionHeartbeatTrigger, domain.ResourceKindAgent, true
		}
	case "/compact", "/compress":
		return domain.ActionSessionCompact, domain.ResourceKindSession, true
	case "/new":
		return domain.ActionConvoSwitch, domain.ResourceKindConversation, true
	case "/pin", "/unpin":
		return domain.ActionConvoPin, domain.ResourceKindConversation, true
	case "/projects", "/project", "/p":
		switch subcmd {
		case "", "list", "info":
			return domain.ActionProjectInspect, domain.ResourceKindProject, true
		case "new", "create":
			return domain.ActionProjectCreate, domain.ResourceKindProject, true
		case "reset":
			return domain.ActionProjectReset, domain.ResourceKindProject, true
		case "exit", "~", "use":
			return domain.ActionConvoSwitch, domain.ResourceKindProject, true
		default:
			return domain.ActionConvoSwitch, domain.ResourceKindProject, true
		}
	case "/conversations", "/c":
		switch subcmd {
		case "new", "create", "switch", "use":
			return domain.ActionConvoSwitch, domain.ResourceKindConversation, true
		case "pin", "unpin":
			return domain.ActionConvoPin, domain.ResourceKindConversation, true
		case "rename", "title":
			return domain.ActionConvoRename, domain.ResourceKindConversation, true
		case "archive", "close":
			return domain.ActionConvoArchive, domain.ResourceKindConversation, true
		case "clean", "purge":
			return domain.ActionConvoDelete, domain.ResourceKindConversation, true
		default:
			return domain.ActionConvoInspect, domain.ResourceKindConversation, true
		}
	}
	return "", "", false
}
