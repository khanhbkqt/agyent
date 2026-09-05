package execution

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

type mockRunner struct {
	mu           sync.Mutex
	executeCalls []domain.ExecutionRequest
	streamCalls  []domain.ExecutionRequest
	executeFunc  func(ctx context.Context, req domain.ExecutionRequest) (*domain.ExecutionResult, error)
	streamFunc   func(ctx context.Context, req domain.ExecutionRequest, sessionKey string) (*domain.ExecutionResult, error)
	interruptFn  func(ctx context.Context, sessionKey string) error
}

func (m *mockRunner) Name() string { return "mock-runner" }
func (m *mockRunner) Execute(ctx context.Context, req domain.ExecutionRequest) (*domain.ExecutionResult, error) {
	m.mu.Lock()
	m.executeCalls = append(m.executeCalls, req)
	fn := m.executeFunc
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, req)
	}
	return &domain.ExecutionResult{Success: true, ResponseText: "mock response"}, nil
}
func (m *mockRunner) ExecuteStream(ctx context.Context, req domain.ExecutionRequest, sessionKey string) (*domain.ExecutionResult, error) {
	m.mu.Lock()
	m.streamCalls = append(m.streamCalls, req)
	fn := m.streamFunc
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, req, sessionKey)
	}
	return &domain.ExecutionResult{Success: true, ResponseText: "mock stream response"}, nil
}
func (m *mockRunner) InterruptStream(ctx context.Context, sessionKey string) error {
	if m.interruptFn != nil {
		return m.interruptFn(ctx, sessionKey)
	}
	return nil
}
func (m *mockRunner) HealthCheck(ctx context.Context) error { return nil }
func (m *mockRunner) ListAvailableModels(ctx context.Context) ([]domain.ModelCapability, error) {
	return nil, nil
}

type mockPolicy struct {
	authFn func(ctx context.Context, p domain.Principal, a domain.Action, r domain.Resource) error
}

func (m *mockPolicy) Authorize(ctx context.Context, p domain.Principal, a domain.Action, r domain.Resource) error {
	if m.authFn != nil {
		return m.authFn(ctx, p, a, r)
	}
	return nil
}

func (m *mockPolicy) ResolveAgentRole(ctx context.Context, principal domain.Principal, agentName string) (domain.AgentRole, error) {
	return domain.AgentRoleOwner, nil
}

type mockSecurityManager struct {
	mu         sync.Mutex
	registered map[string]domain.TurnSecurityContext
	unregTurns []string
}

func newMockSecurityManager() *mockSecurityManager {
	return &mockSecurityManager{
		registered: make(map[string]domain.TurnSecurityContext),
	}
}

func (m *mockSecurityManager) RegisterActiveTurn(turn domain.TurnSecurityContext) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.registered[turn.TurnID] = turn
}
func (m *mockSecurityManager) UnregisterTurnByID(turnID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.unregTurns = append(m.unregTurns, turnID)
	delete(m.registered, turnID)
}
func (m *mockSecurityManager) UnregisterActiveTurn(convID string, workspaceDir string) {}
func (m *mockSecurityManager) ResolveSessionKey(convID string, workspaceDir string) string {
	return ""
}
func (m *mockSecurityManager) ResolveTurnContext(convID string, workspaceDir string) (domain.TurnSecurityContext, bool) {
	return domain.TurnSecurityContext{}, false
}
func (m *mockSecurityManager) ResolveTurnByID(turnID string) (domain.TurnSecurityContext, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.registered[turnID]
	return t, ok
}
func (m *mockSecurityManager) EvaluateToolCall(ctx context.Context, req domain.ToolEvaluationRequest) (domain.SecurityDecision, error) {
	return domain.SecurityDecision{Decision: domain.DecisionAllow}, nil
}
func (m *mockSecurityManager) EvaluateCommand(ctx context.Context, sessionKey string, role string, cmd string) (domain.SecurityDecision, error) {
	return domain.SecurityDecision{Decision: domain.DecisionAllow}, nil
}
func (m *mockSecurityManager) EvaluatePath(ctx context.Context, sessionKey string, workspaceDir string, targetPath string, isWrite bool) (domain.SecurityDecision, error) {
	return domain.SecurityDecision{Decision: domain.DecisionAllow}, nil
}
func (m *mockSecurityManager) EvaluateURL(ctx context.Context, urlStr string) (domain.SecurityDecision, error) {
	return domain.SecurityDecision{Decision: domain.DecisionAllow}, nil
}
func (m *mockSecurityManager) SanitizeToolOutput(ctx context.Context, toolName string, output string) (string, error) {
	return output, nil
}
func (m *mockSecurityManager) GrantSessionPermission(sessionKey string, pattern string) {}
func (m *mockSecurityManager) SetPreset(preset domain.SecurityPreset)                   {}
func (m *mockSecurityManager) SetRedactionMode(mode domain.RedactionMode)               {}
func (m *mockSecurityManager) AddWhitelistEntry(entry string)                           {}
func (m *mockSecurityManager) GetDashboardSummary(sessionKey string) domain.SecurityDashboard {
	return domain.SecurityDashboard{}
}
func (m *mockSecurityManager) EnsureWorkspaceHooks(workspaceDir string) error { return nil }
func (m *mockSecurityManager) CancelSessionApprovals(sessionKey string)        {}

type mockConfigProvider struct {
	admins map[string]bool
}

func (m *mockConfigProvider) IsAdminForProvider(senderID, provider string) bool {
	return m.admins[senderID]
}

func (m *mockConfigProvider) IsSuperAdmin(principal domain.Principal) bool {
	return m.admins[principal.SubjectID]
}

func TestExecuteTurn_AuthorizationDenied(t *testing.T) {
	runner := &mockRunner{}
	policy := &mockPolicy{
		authFn: func(ctx context.Context, p domain.Principal, a domain.Action, r domain.Resource) error {
			return ports.ErrAccessDenied
		},
	}
	secMgr := newMockSecurityManager()
	svc := NewService(runner, policy, secMgr, nil, nil, "test-secret", nil)

	principal := domain.Principal{
		SubjectID: "user-unauthorized",
		Provider:  "telegram",
		Kind:      domain.PrincipalUser,
	}
	req := domain.ExecutionRequest{
		Prompt:    "whoami",
		AgentName: "private-agent",
	}

	res, err := svc.ExecuteTurn(context.Background(), principal, req, "tg:chat1", false)
	require.Error(t, err)
	assert.Nil(t, res)
	assert.True(t, errors.Is(err, ports.ErrAccessDenied) || errors.Unwrap(err) != nil)
	assert.Empty(t, runner.executeCalls, "runner should not be invoked on denied authorization")
}

func TestExecuteTurn_PrivilegedFlagsStrippedForNonAdmin(t *testing.T) {
	runner := &mockRunner{}
	policy := &mockPolicy{}
	secMgr := newMockSecurityManager()
	cfgProvider := &mockConfigProvider{admins: map[string]bool{"admin-user": true}}
	svc := NewService(runner, policy, secMgr, nil, cfgProvider, "test-secret", nil)

	// Non-admin user tries dangerously skip permissions
	principal := domain.Principal{
		SubjectID: "regular-user",
		Provider:  "telegram",
		Kind:      domain.PrincipalUser,
	}
	req := domain.ExecutionRequest{
		Prompt:                     "run something",
		AgentName:                  "default",
		DangerouslySkipPermissions: true,
	}

	res, err := svc.ExecuteTurn(context.Background(), principal, req, "tg:chat1", false)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Len(t, runner.executeCalls, 1)
	assert.False(t, runner.executeCalls[0].DangerouslySkipPermissions, "dangerously_skip_permissions must be stripped for non-admin")
	assert.NotEmpty(t, runner.executeCalls[0].TurnID)
	assert.Equal(t, "test-secret", runner.executeCalls[0].Env["AGYENT_IPC_TOKEN"])
	assert.Equal(t, runner.executeCalls[0].TurnID, runner.executeCalls[0].Env["AGYENT_TURN_ID"])
	assert.NotEmpty(t, runner.executeCalls[0].Env["AGYENT_MCP_DIR"])
}

func TestExecuteTurn_PrivilegedFlagsPreservedForSuperAdmin(t *testing.T) {
	runner := &mockRunner{}
	policy := &mockPolicy{}
	secMgr := newMockSecurityManager()
	cfgProvider := &mockConfigProvider{admins: map[string]bool{"admin-user": true}}
	svc := NewService(runner, policy, secMgr, nil, cfgProvider, "test-secret", nil)

	principal := domain.Principal{
		SubjectID: "admin-user",
		Provider:  "telegram",
		Kind:      domain.PrincipalUser,
	}
	req := domain.ExecutionRequest{
		Prompt:                     "admin command",
		AgentName:                  "default",
		DangerouslySkipPermissions: true,
	}

	res, err := svc.ExecuteTurn(context.Background(), principal, req, "tg:chat1", false)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Len(t, runner.executeCalls, 1)
	assert.True(t, runner.executeCalls[0].DangerouslySkipPermissions, "dangerously_skip_permissions should be preserved for admin")
}

func TestExecuteTurn_EffortErrorFallback(t *testing.T) {
	var attempts int
	runner := &mockRunner{
		executeFunc: func(ctx context.Context, req domain.ExecutionRequest) (*domain.ExecutionResult, error) {
			attempts++
			if attempts == 1 {
				return &domain.ExecutionResult{
					Success: false,
					Error:   "invalid argument: reasoning_effort is not supported for model gemini-1.5-flash",
				}, errors.New("exit status 1")
			}
			return &domain.ExecutionResult{
				Success:      true,
				ResponseText: "retry succeeded",
			}, nil
		},
	}
	policy := &mockPolicy{}
	svc := NewService(runner, policy, nil, nil, nil, "", nil)

	principal := domain.Principal{
		SubjectID: "user1",
		Provider:  "telegram",
		Kind:      domain.PrincipalUser,
	}
	req := domain.ExecutionRequest{
		Prompt: "explain this",
		Model:  "gemini-1.5-flash",
		Effort: "high",
	}

	res, err := svc.ExecuteTurn(context.Background(), principal, req, "tg:chat1", false)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.Success)
	assert.Equal(t, 2, attempts)
	assert.Equal(t, "high", runner.executeCalls[0].Effort)
	assert.Equal(t, domain.EffortNone, runner.executeCalls[1].Effort, "Effort flag should be set to EffortNone on retry")
	assert.True(t, runner.executeCalls[1].DisableEffort, "DisableEffort must be true on retry")
}

func TestExecuteTurn_StreamingAndCleanup(t *testing.T) {
	runner := &mockRunner{}
	policy := &mockPolicy{}
	secMgr := newMockSecurityManager()
	svc := NewService(runner, policy, secMgr, nil, nil, "token123", nil)

	principal := domain.Principal{
		SubjectID: "user1",
		Provider:  "telegram",
		Kind:      domain.PrincipalUser,
	}
	req := domain.ExecutionRequest{
		Prompt: "stream me",
	}

	res, err := svc.ExecuteTurn(context.Background(), principal, req, "tg:chat1", true)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Len(t, runner.streamCalls, 1)

	// Verify that turn was registered and then unregistered upon completion
	turnID := runner.streamCalls[0].TurnID
	assert.NotEmpty(t, turnID)
	assert.Contains(t, secMgr.unregTurns, turnID)
}

func TestInterruptTurn(t *testing.T) {
	var interruptedKey string
	runner := &mockRunner{
		interruptFn: func(ctx context.Context, sessionKey string) error {
			interruptedKey = sessionKey
			return nil
		},
	}
	svc := NewService(runner, nil, nil, nil, nil, "", nil)

	err := svc.InterruptTurn(context.Background(), "tg:session123")
	require.NoError(t, err)
	assert.Equal(t, "tg:session123", interruptedKey)
}
