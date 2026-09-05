package ports

import (
	"context"
	"errors"
	"fmt"

	"agyent/internal/core/domain"
)

// Domain sentinel errors for storage operations.
var (
	ErrNotFound               = errors.New("storage: entity not found")
	ErrSessionNotFound        = fmt.Errorf("%w: session not found", ErrNotFound)
	ErrAgentNotFound          = fmt.Errorf("%w: agent not found", ErrNotFound)
	ErrProjectNotFound        = fmt.Errorf("%w: project not found", ErrNotFound)
	ErrUserNotFound           = fmt.Errorf("%w: user not found", ErrNotFound)
	ErrGroupNotFound          = fmt.Errorf("%w: group not found", ErrNotFound)
	ErrAlreadyExists          = errors.New("storage: entity already exists")
	ErrInvalidStateTransition = errors.New("storage: invalid state transition")
)

// SessionRepository defines persistence operations for interactive user/group sessions.
type SessionRepository interface {
	GetSession(ctx context.Context, key string) (*domain.Session, error)
	GetOrCreateSession(ctx context.Context, key string, defaultAgent string) (*domain.Session, error)
	SaveSession(ctx context.Context, session *domain.Session) error
	DeleteSession(ctx context.Context, key string) error

	// Project-specific conversation context isolation
	GetProjectConversationID(ctx context.Context, sessionKey, projectID string) (string, error)
	SetProjectConversationID(ctx context.Context, sessionKey, projectID, convID string) error
	ClearProjectConversationID(ctx context.Context, sessionKey, projectID string) error
}

// AgentRepository defines persistence operations for agent profiles.
type AgentRepository interface {
	GetAgent(ctx context.Context, name string) (*domain.Agent, error)
	ListAgents(ctx context.Context) ([]domain.Agent, error)
	SaveAgent(ctx context.Context, agent *domain.Agent) error
	DeleteAgent(ctx context.Context, name string) error

	// Granular RBAC & Collaborator Sharing
	ShareAgent(ctx context.Context, perm *domain.AgentPermission) error
	RevokeAgentAccess(ctx context.Context, agentName, userID string) error
	ListAgentPermissions(ctx context.Context, agentName string) ([]domain.AgentPermission, error)
	CheckAgentAccess(ctx context.Context, agentName, userID string) (bool, string, error)
	ListAgentsForUser(ctx context.Context, userID string) ([]domain.Agent, error)
	ClaimAgent(ctx context.Context, name string, newOwnerID string) (bool, error)
	ClaimAgentWithAudit(ctx context.Context, name string, newOwnerID string, audit domain.AuditLog) (bool, error)
}

// ProjectRepository defines persistence operations for managed projects.
type ProjectRepository interface {
	GetProject(ctx context.Context, id string) (*domain.Project, error)
	ListProjects(ctx context.Context, agentName string) ([]domain.Project, error)
	SaveProject(ctx context.Context, project *domain.Project) error
	DeleteProject(ctx context.Context, id string) error
}

// UserRepository defines persistence and authorization check operations for users.
type UserRepository interface {
	GetUser(ctx context.Context, id string) (*domain.User, error)
	ListUsers(ctx context.Context) ([]domain.User, error)
	SaveUser(ctx context.Context, user *domain.User) error
	DeleteUser(ctx context.Context, id string) error
	IsUserAllowed(ctx context.Context, id string) (bool, error)
}

// GroupRepository defines persistence and authorization check operations for allowed groups.
type GroupRepository interface {
	GetGroup(ctx context.Context, groupID string) (*domain.Group, error)
	ListGroups(ctx context.Context) ([]domain.Group, error)
	SaveGroup(ctx context.Context, group *domain.Group) error
	DeleteGroup(ctx context.Context, groupID string) error
	IsGroupAllowed(ctx context.Context, groupID string) (bool, error)
}

// AuditRepository defines logging operations for execution audits and token usage.
type AuditRepository interface {
	LogAudit(ctx context.Context, log *domain.AuditLog) error
	ListAuditLogs(ctx context.Context, sessionKey string, limit int) ([]domain.AuditLog, error)
	GetTokenStats(ctx context.Context, sessionKey string, convID string) (*domain.TokenUsage, error)
	GetTokenEfficiencyReport(ctx context.Context, sessionKey string, agentName string) (*domain.TokenEfficiencyReport, error)

	// Security audit events (security_audit_events table)
	LogSecurityEvent(ctx context.Context, evt *domain.AuditSecurityEvent) error
	ListSecurityEvents(ctx context.Context, limit int) ([]domain.AuditSecurityEvent, error)
}

// ISP (Interface Segregation Principle) Store Aliases:
type SessionStore = SessionRepository
type AgentStore = AgentRepository
type ProjectStore = ProjectRepository
type UserStore = UserRepository
type GroupStore = GroupRepository
type AuditStore = AuditRepository
type ConversationStore = ConversationRepository
type SubagentStore = SubagentRepository
type ScheduleStore = ScheduleRepository

// ConversationRepository defines persistence and lifecycle operations for multi-conversation management.
type ConversationRepository interface {
	GetConversation(ctx context.Context, id string) (*domain.Conversation, error)
	GetConversationScoped(ctx context.Context, scope domain.ConversationScope, id string) (*domain.Conversation, error)
	GetConversationByAlias(ctx context.Context, sessionKey, agentName, projectName string, aliasIndex int) (*domain.Conversation, error)
	ListRecentConversations(ctx context.Context, sessionKey, agentName, projectName string, limit int, offset int) ([]domain.Conversation, int, error)
	SaveConversation(ctx context.Context, conv *domain.Conversation) error
	TouchConversation(ctx context.Context, sessionKey, agentName, projectName, convID, promptSnippet string) error
	SetConversationPinned(ctx context.Context, id string, isPinned bool) error
	SetConversationPinnedScoped(ctx context.Context, scope domain.ConversationScope, id string, isPinned bool) error
	SetConversationArchived(ctx context.Context, id string, isArchived bool) error
	SetConversationArchivedScoped(ctx context.Context, scope domain.ConversationScope, id string, isArchived bool) error
	SetConversationTitle(ctx context.Context, id string, title string) error
	SetConversationTitleScoped(ctx context.Context, scope domain.ConversationScope, id string, title string) error
	DeleteConversationScoped(ctx context.Context, scope domain.ConversationScope, id string) error
	GetExpiredArchivedConversationIDs(ctx context.Context, olderThanDays int) ([]string, error)
	PurgeConversations(ctx context.Context, ids []string) error
	UpdateConversationReflectedStep(ctx context.Context, id string, step int) error
}

// StoragePort is the unified interface combining all repositories and lifecycle management.
type StoragePort interface {
	SessionRepository
	AgentRepository
	ProjectRepository
	UserRepository
	GroupRepository
	AuditRepository
	ConversationRepository
	SubagentRepository
	ScheduleRepository

	// Close gracefully closes any open database connections.
	Close() error
}
