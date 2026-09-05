package domain

// PrincipalKind defines the categorization of a security principal.
type PrincipalKind string

const (
	PrincipalUser   PrincipalKind = "user"
	PrincipalSystem PrincipalKind = "system"
	PrincipalPlugin PrincipalKind = "plugin"
)

// Principal represents the authenticating entity interacting with Agyent.
type Principal struct {
	Kind      PrincipalKind `json:"kind"`
	Provider  string        `json:"provider"`   // telegram, discord, cli, internal
	AccountID string        `json:"account_id"` // bot/application identity (e.g. Telegram Bot ID)
	SubjectID string        `json:"subject_id"` // Platform user ID or system service identity
}

// Key returns a canonical string representation of the principal identity.
func (p Principal) Key() string {
	return p.Provider + ":" + p.SubjectID
}

// SystemRole defines global daemon-level administrative privileges.
type SystemRole string

const (
	SystemRoleSuperAdmin SystemRole = "superadmin"
	SystemRoleUser       SystemRole = "user"
	SystemRoleAnonymous  SystemRole = "anonymous"
)

// AgentRole defines granular role-based permissions scoped to a specific agent.
type AgentRole string

const (
	AgentRoleOwner    AgentRole = "owner"
	AgentRoleAdmin    AgentRole = "admin"
	AgentRoleOperator AgentRole = "operator"
	AgentRoleViewer   AgentRole = "viewer"
	AgentRolePublic   AgentRole = "public"
	AgentRoleNone     AgentRole = "none"
)

// ResourceKind defines the target entity type being accessed or manipulated.
type ResourceKind string

const (
	ResourceKindSystem       ResourceKind = "system"
	ResourceKindAgent        ResourceKind = "agent"
	ResourceKindProject      ResourceKind = "project"
	ResourceKindSession      ResourceKind = "session"
	ResourceKindConversation ResourceKind = "conversation"
	ResourceKindSubagentTask ResourceKind = "subagent_task"
	ResourceKindSchedule     ResourceKind = "schedule"
	ResourceKindPlugin       ResourceKind = "plugin"
)

// Resource captures the context and identity of the object being accessed.
type Resource struct {
	Kind        ResourceKind `json:"kind"`
	ID          string       `json:"id,omitempty"`
	SessionKey  string       `json:"session_key,omitempty"`
	AgentName   string       `json:"agent_name,omitempty"`
	ProjectName string       `json:"project_name,omitempty"`
	OwnerID     string       `json:"owner_id,omitempty"`
	IsPublic    bool         `json:"is_public,omitempty"`
}

// Action represents a discrete, granular operation protected by the policy engine.
type Action string

const (
	// Turn & Execution
	ActionTurnExecute     Action = "turn.execute"
	ActionTurnInterrupt   Action = "turn.interrupt"
	ActionTurnForceUnlock Action = "turn.force_unlock"

	// Session Lifecycle
	ActionSessionReset   Action = "session.reset"
	ActionSessionCompact Action = "session.compact"

	// Project Management
	ActionProjectInspect    Action = "project.inspect"
	ActionProjectCreate     Action = "project.create"
	ActionProjectCreatePath Action = "project.create_custom_path"
	ActionProjectDelete     Action = "project.delete"
	ActionProjectReset      Action = "project.reset"

	// Conversation Management
	ActionConvoInspect Action = "conversation.inspect"
	ActionConvoSwitch  Action = "conversation.switch"
	ActionConvoDelete  Action = "conversation.delete"
	ActionConvoPin     Action = "conversation.pin"
	ActionConvoArchive Action = "conversation.archive"
	ActionConvoRename  Action = "conversation.rename"

	// Agent Management
	ActionAgentInspect Action = "agent.inspect"
	ActionAgentCreate  Action = "agent.create"
	ActionAgentEdit    Action = "agent.edit"
	ActionAgentDelete  Action = "agent.delete"
	ActionAgentClaim   Action = "agent.claim"
	ActionAgentShare   Action = "agent.share"
	ActionAgentRevoke  Action = "agent.revoke"

	// Subagent Tasks
	ActionTaskDispatch Action = "task.dispatch"
	ActionTaskInspect  Action = "task.inspect"
	ActionTaskReply    Action = "task.reply"
	ActionTaskCancel   Action = "task.cancel"
	ActionTaskPurge    Action = "task.purge"

	// Schedule & Heartbeat
	ActionScheduleCreate   Action = "schedule.create"
	ActionScheduleList     Action = "schedule.list"
	ActionScheduleCancel   Action = "schedule.cancel"
	ActionHeartbeatConfig  Action = "heartbeat.config"
	ActionHeartbeatTrigger Action = "heartbeat.trigger"

	// System Administration (SuperAdmin only)
	ActionPluginManage     Action = "plugin.manage"
	ActionSecurityConfig   Action = "security.config"
	ActionWhitelistManage  Action = "whitelist.manage"
	ActionModeChange       Action = "mode.change"
	ActionStreamModeChange Action = "stream_mode.change"
	ActionModelDefaultSet  Action = "model.default_set"
)
