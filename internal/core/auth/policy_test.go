package auth

import (
	"context"
	"testing"

	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockStorageReader struct {
	agents      map[string]*domain.Agent
	permissions map[string]map[string]string // agentName -> userID -> role
}

func (m *mockStorageReader) GetAgent(ctx context.Context, name string) (*domain.Agent, error) {
	if a, ok := m.agents[name]; ok {
		return a, nil
	}
	return nil, nil
}

func (m *mockStorageReader) CheckAgentAccess(ctx context.Context, agentName, userID string) (bool, string, error) {
	if perms, ok := m.permissions[agentName]; ok {
		if role, ok := perms[userID]; ok {
			return true, role, nil
		}
	}
	return false, "", nil
}

type mockConfigProvider struct {
	superAdmins   map[string]bool
	allowedGroups map[string]bool
}

func (m *mockConfigProvider) IsSuperAdmin(principal domain.Principal) bool {
	return m.superAdmins[principal.Key()]
}

func (m *mockConfigProvider) IsGroupAllowed(groupID string, provider string) bool {
	if m.allowedGroups == nil {
		return false
	}
	return m.allowedGroups[groupID] || m.allowedGroups[provider+":"+groupID]
}

func setupTestPolicyEngine() (*Engine, *mockStorageReader, *mockConfigProvider) {
	storage := &mockStorageReader{
		agents: map[string]*domain.Agent{
			"default_agent": {
				Name:     "default_agent",
				OwnerID:  "owner-123",
				IsPublic: false,
			},
			"public_agent": {
				Name:     "public_agent",
				OwnerID:  "owner-123",
				IsPublic: true,
			},
			"unowned_agent": {
				Name:     "unowned_agent",
				OwnerID:  "",
				IsPublic: false,
			},
		},
		permissions: map[string]map[string]string{
			"default_agent": {
				"admin-456":    "admin",
				"operator-789": "operator",
				"viewer-101":   "viewer",
			},
		},
	}

	cfg := &mockConfigProvider{
		superAdmins: map[string]bool{
			"telegram:999999": true,
			"cli:root":        true,
		},
	}

	return NewEngine(storage, cfg), storage, cfg
}

func TestPolicyEngine_SuperAdmin(t *testing.T) {
	engine, _, _ := setupTestPolicyEngine()
	ctx := context.Background()

	superAdmin := domain.Principal{
		Kind:      domain.PrincipalUser,
		Provider:  "telegram",
		SubjectID: "999999",
	}

	res := domain.Resource{
		Kind:      domain.ResourceKindAgent,
		AgentName: "default_agent",
	}

	// SuperAdmin can perform all system admin actions
	systemActions := []domain.Action{
		domain.ActionPluginManage,
		domain.ActionSecurityConfig,
		domain.ActionWhitelistManage,
		domain.ActionModeChange,
		domain.ActionStreamModeChange,
		domain.ActionModelDefaultSet,
		domain.ActionAgentClaim,
		domain.ActionProjectCreatePath,
		domain.ActionTaskPurge,
	}

	for _, act := range systemActions {
		err := engine.Authorize(ctx, superAdmin, act, res)
		assert.NoError(t, err, "SuperAdmin must be permitted for %s", act)
	}

	// SuperAdmin can also perform all agent actions
	agentActions := []domain.Action{
		domain.ActionTurnExecute,
		domain.ActionAgentDelete,
		domain.ActionProjectCreate,
		domain.ActionScheduleCreate,
	}
	for _, act := range agentActions {
		err := engine.Authorize(ctx, superAdmin, act, res)
		assert.NoError(t, err, "SuperAdmin must be permitted for %s", act)
	}
}

func TestPolicyEngine_Owner(t *testing.T) {
	engine, _, _ := setupTestPolicyEngine()
	ctx := context.Background()

	owner := domain.Principal{
		Kind:      domain.PrincipalUser,
		Provider:  "telegram",
		SubjectID: "owner-123",
	}

	res := domain.Resource{
		Kind:      domain.ResourceKindAgent,
		AgentName: "default_agent",
	}

	// Positive test: Owner can manage agent
	allowedActions := []domain.Action{
		domain.ActionTurnExecute,
		domain.ActionAgentEdit,
		domain.ActionAgentDelete,
		domain.ActionAgentShare,
		domain.ActionAgentRevoke,
		domain.ActionProjectCreate,
		domain.ActionProjectDelete,
		domain.ActionScheduleCreate,
		domain.ActionScheduleCancel,
		domain.ActionTaskCancel,
	}
	for _, act := range allowedActions {
		err := engine.Authorize(ctx, owner, act, res)
		assert.NoError(t, err, "Owner must be permitted for %s", act)
	}

	// Negative test: Owner is NOT SuperAdmin, cannot perform system-level actions
	deniedActions := []domain.Action{
		domain.ActionPluginManage,
		domain.ActionSecurityConfig,
		domain.ActionProjectCreatePath,
		domain.ActionTaskPurge,
	}
	for _, act := range deniedActions {
		err := engine.Authorize(ctx, owner, act, res)
		assert.ErrorIs(t, err, ports.ErrAccessDenied, "Owner without superadmin must be denied for %s", act)
	}
}

func TestPolicyEngine_AgentAdmin(t *testing.T) {
	engine, _, _ := setupTestPolicyEngine()
	ctx := context.Background()

	admin := domain.Principal{
		Kind:      domain.PrincipalUser,
		Provider:  "telegram",
		SubjectID: "admin-456",
	}

	res := domain.Resource{
		Kind:      domain.ResourceKindAgent,
		AgentName: "default_agent",
	}

	// Positive test: Agent Admin can manage projects, schedules, sharing
	allowedActions := []domain.Action{
		domain.ActionTurnExecute,
		domain.ActionProjectCreate,
		domain.ActionProjectDelete,
		domain.ActionScheduleCreate,
		domain.ActionScheduleCancel,
		domain.ActionAgentShare,
		domain.ActionAgentRevoke,
		domain.ActionSessionReset,
		domain.ActionConvoPin,
	}
	for _, act := range allowedActions {
		err := engine.Authorize(ctx, admin, act, res)
		assert.NoError(t, err, "Agent Admin must be permitted for %s", act)
	}

	// Negative test: Agent Admin CANNOT delete or edit agent definition
	deniedActions := []domain.Action{
		domain.ActionAgentDelete,
		domain.ActionAgentEdit,
		domain.ActionAgentClaim,
		domain.ActionPluginManage,
		domain.ActionProjectCreatePath,
	}
	for _, act := range deniedActions {
		err := engine.Authorize(ctx, admin, act, res)
		assert.ErrorIs(t, err, ports.ErrAccessDenied, "Agent Admin must be denied for %s", act)
	}
}

func TestPolicyEngine_Operator(t *testing.T) {
	engine, _, _ := setupTestPolicyEngine()
	ctx := context.Background()

	operator := domain.Principal{
		Kind:      domain.PrincipalUser,
		Provider:  "telegram",
		SubjectID: "operator-789",
	}

	res := domain.Resource{
		Kind:      domain.ResourceKindAgent,
		AgentName: "default_agent",
	}

	// Positive test: Operator can execute turns, reset sessions, manipulate conversations & tasks
	allowedActions := []domain.Action{
		domain.ActionTurnExecute,
		domain.ActionTurnInterrupt,
		domain.ActionSessionReset,
		domain.ActionSessionCompact,
		domain.ActionConvoInspect,
		domain.ActionConvoSwitch,
		domain.ActionConvoPin,
		domain.ActionConvoArchive,
		domain.ActionConvoRename,
		domain.ActionTaskDispatch,
		domain.ActionTaskReply,
		domain.ActionTaskCancel,
	}
	for _, act := range allowedActions {
		err := engine.Authorize(ctx, operator, act, res)
		assert.NoError(t, err, "Operator must be permitted for %s", act)
	}

	// Negative test: Operator CANNOT create/delete projects or schedules or share agent
	deniedActions := []domain.Action{
		domain.ActionProjectCreate,
		domain.ActionProjectDelete,
		domain.ActionScheduleCreate,
		domain.ActionScheduleCancel,
		domain.ActionAgentShare,
		domain.ActionAgentEdit,
		domain.ActionAgentDelete,
	}
	for _, act := range deniedActions {
		err := engine.Authorize(ctx, operator, act, res)
		assert.ErrorIs(t, err, ports.ErrAccessDenied, "Operator must be denied for %s", act)
	}
}

func TestPolicyEngine_Viewer(t *testing.T) {
	engine, _, _ := setupTestPolicyEngine()
	ctx := context.Background()

	viewer := domain.Principal{
		Kind:      domain.PrincipalUser,
		Provider:  "telegram",
		SubjectID: "viewer-101",
	}

	res := domain.Resource{
		Kind:      domain.ResourceKindAgent,
		AgentName: "default_agent",
	}

	// Positive test: Viewer can only execute model turns (read-only/plan) and inspect conversations
	allowedActions := []domain.Action{
		domain.ActionTurnExecute,
		domain.ActionConvoInspect,
		domain.ActionConvoSwitch,
	}
	for _, act := range allowedActions {
		err := engine.Authorize(ctx, viewer, act, res)
		assert.NoError(t, err, "Viewer must be permitted for %s", act)
	}

	// Negative test: Viewer CANNOT mutate sessions, tasks, projects, or schedules
	deniedActions := []domain.Action{
		domain.ActionSessionReset,
		domain.ActionConvoDelete,
		domain.ActionConvoPin,
		domain.ActionConvoArchive,
		domain.ActionTaskDispatch,
		domain.ActionTaskCancel,
		domain.ActionProjectCreate,
		domain.ActionScheduleCreate,
		domain.ActionAgentShare,
	}
	for _, act := range deniedActions {
		err := engine.Authorize(ctx, viewer, act, res)
		assert.ErrorIs(t, err, ports.ErrAccessDenied, "Viewer must be denied for %s", act)
	}
}

func TestPolicyEngine_PublicUser(t *testing.T) {
	engine, _, _ := setupTestPolicyEngine()
	ctx := context.Background()

	stranger := domain.Principal{
		Kind:      domain.PrincipalUser,
		Provider:  "telegram",
		SubjectID: "stranger-000",
	}

	// 1. On private agent: Denied everything
	privateRes := domain.Resource{
		Kind:      domain.ResourceKindAgent,
		AgentName: "default_agent",
		IsPublic:  false,
	}
	err := engine.Authorize(ctx, stranger, domain.ActionTurnExecute, privateRes)
	assert.ErrorIs(t, err, ports.ErrAccessDenied, "Stranger must be denied turn.execute on private agent")

	// 2. On public agent: Allowed turn.execute and convo.inspect only
	publicRes := domain.Resource{
		Kind:      domain.ResourceKindAgent,
		AgentName: "public_agent",
		IsPublic:  true,
	}
	err = engine.Authorize(ctx, stranger, domain.ActionTurnExecute, publicRes)
	assert.NoError(t, err, "Public user must be permitted turn.execute on public agent")

	err = engine.Authorize(ctx, stranger, domain.ActionConvoInspect, publicRes)
	assert.NoError(t, err, "Public user must be permitted convo.inspect on public agent")

	// Mutating actions are denied even on public agent
	err = engine.Authorize(ctx, stranger, domain.ActionSessionReset, publicRes)
	assert.ErrorIs(t, err, ports.ErrAccessDenied, "Public user must be denied mutations on public agent")
}

func TestPolicyEngine_UnownedAgent_RequiresSuperAdmin(t *testing.T) {
	engine, _, _ := setupTestPolicyEngine()
	ctx := context.Background()

	res := domain.Resource{
		Kind:      domain.ResourceKindAgent,
		AgentName: "unowned_agent",
		IsPublic:  false,
	}

	// Normal user denied
	normalUser := domain.Principal{
		Kind:      domain.PrincipalUser,
		Provider:  "telegram",
		SubjectID: "user-123",
	}
	err := engine.Authorize(ctx, normalUser, domain.ActionTurnExecute, res)
	assert.ErrorIs(t, err, ports.ErrAccessDenied, "Normal user must be denied access to unowned agent")

	err = engine.Authorize(ctx, normalUser, domain.ActionAgentClaim, res)
	assert.ErrorIs(t, err, ports.ErrAccessDenied, "Normal user cannot claim unowned agent")

	// SuperAdmin allowed to claim and execute
	superAdmin := domain.Principal{
		Kind:      domain.PrincipalUser,
		Provider:  "telegram",
		SubjectID: "999999",
	}
	err = engine.Authorize(ctx, superAdmin, domain.ActionAgentClaim, res)
	assert.NoError(t, err, "SuperAdmin must be permitted to claim unowned agent")
}

func TestPolicyEngine_SystemPrincipals(t *testing.T) {
	engine, _, _ := setupTestPolicyEngine()
	ctx := context.Background()

	res := domain.Resource{
		Kind:      domain.ResourceKindAgent,
		AgentName: "default_agent",
	}

	// Scheduler
	scheduler := domain.Principal{
		Kind:      domain.PrincipalSystem,
		Provider:  "internal",
		SubjectID: "system:scheduler",
	}
	require.NoError(t, engine.Authorize(ctx, scheduler, domain.ActionTurnExecute, res))
	require.ErrorIs(t, engine.Authorize(ctx, scheduler, domain.ActionAgentDelete, res), ports.ErrAccessDenied)

	// Heartbeat
	heartbeat := domain.Principal{
		Kind:      domain.PrincipalSystem,
		Provider:  "internal",
		SubjectID: "system:heartbeat",
	}
	require.NoError(t, engine.Authorize(ctx, heartbeat, domain.ActionTurnExecute, res))
	require.ErrorIs(t, engine.Authorize(ctx, heartbeat, domain.ActionPluginManage, res), ports.ErrAccessDenied)

	// Compactor
	compactor := domain.Principal{
		Kind:      domain.PrincipalSystem,
		Provider:  "internal",
		SubjectID: "system:compactor",
	}
	require.NoError(t, engine.Authorize(ctx, compactor, domain.ActionTurnExecute, res))
	require.NoError(t, engine.Authorize(ctx, compactor, domain.ActionSessionCompact, res))
	require.ErrorIs(t, engine.Authorize(ctx, compactor, domain.ActionProjectCreate, res), ports.ErrAccessDenied)

	// Reflection
	reflection := domain.Principal{
		Kind:      domain.PrincipalSystem,
		Provider:  "internal",
		SubjectID: "system:reflection",
	}
	require.NoError(t, engine.Authorize(ctx, reflection, domain.ActionTurnExecute, res))
	require.NoError(t, engine.Authorize(ctx, reflection, domain.ActionConvoInspect, res))
	require.ErrorIs(t, engine.Authorize(ctx, reflection, domain.ActionAgentEdit, res), ports.ErrAccessDenied)
}

func TestPolicyEngine_InvalidPrincipal(t *testing.T) {
	engine, _, _ := setupTestPolicyEngine()
	ctx := context.Background()

	res := domain.Resource{Kind: domain.ResourceKindAgent, AgentName: "default_agent"}

	// Empty SubjectID
	p1 := domain.Principal{Kind: domain.PrincipalUser, Provider: "telegram", SubjectID: ""}
	err := engine.Authorize(ctx, p1, domain.ActionTurnExecute, res)
	assert.ErrorIs(t, err, ports.ErrInvalidPrincipal)

	// Empty Provider
	p2 := domain.Principal{Kind: domain.PrincipalUser, Provider: "", SubjectID: "123"}
	err = engine.Authorize(ctx, p2, domain.ActionTurnExecute, res)
	assert.ErrorIs(t, err, ports.ErrInvalidPrincipal)
}

func TestPolicyEngine_WhitelistedGroupAccess(t *testing.T) {
	engine, _, cfg := setupTestPolicyEngine()
	ctx := context.Background()

	// Configure whitelisted group
	cfg.allowedGroups = map[string]bool{
		"-100123456789": true,
		"zalo:group-999": true,
	}

	// Normal user (not owner, not admin)
	normalUser := domain.Principal{
		Kind:      domain.PrincipalUser,
		Provider:  "telegram",
		SubjectID: "user-random-999",
	}

	// 1. Direct message (1-1) on a private agent ("default_agent") -> Denied!
	dmRes := domain.Resource{
		Kind:       domain.ResourceKindAgent,
		AgentName:  "default_agent",
		SessionKey: "telegram:user-random-999",
		IsPublic:   false,
	}
	err := engine.Authorize(ctx, normalUser, domain.ActionTurnExecute, dmRes)
	assert.ErrorIs(t, err, ports.ErrAccessDenied, "random user should be denied in DM on private agent")

	// 2. Message in whitelisted Telegram group on a private agent -> Allowed for Turn Execution!
	groupRes := domain.Resource{
		Kind:       domain.ResourceKindAgent,
		AgentName:  "default_agent",
		SessionKey: "telegram:-100123456789:0",
		IsPublic:   false,
	}
	err = engine.Authorize(ctx, normalUser, domain.ActionTurnExecute, groupRes)
	assert.NoError(t, err, "member of whitelisted group should be allowed to chat with private agent")

	// Convo inspect in whitelisted group is also allowed
	err = engine.Authorize(ctx, normalUser, domain.ActionConvoInspect, groupRes)
	assert.NoError(t, err, "member of whitelisted group should be allowed to inspect convo")

	// Admin actions (e.g. ActionAgentDelete, ActionSecurityConfig) are STILL denied!
	err = engine.Authorize(ctx, normalUser, domain.ActionAgentDelete, groupRes)
	assert.ErrorIs(t, err, ports.ErrAccessDenied, "group member must not be allowed to delete agent")

	// 3. Message in NON-whitelisted group on a private agent -> Denied!
	unlistedGroupRes := domain.Resource{
		Kind:       domain.ResourceKindAgent,
		AgentName:  "default_agent",
		SessionKey: "telegram:-100999999999:0",
		IsPublic:   false,
	}
	err = engine.Authorize(ctx, normalUser, domain.ActionTurnExecute, unlistedGroupRes)
	assert.ErrorIs(t, err, ports.ErrAccessDenied, "unlisted group should be denied")
}
