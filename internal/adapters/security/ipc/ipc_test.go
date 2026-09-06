package ipc

import (
	"context"
	"testing"
	"time"

	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockSecurityManager struct {
	evaluateToolCallFn   func(ctx context.Context, req domain.ToolEvaluationRequest) (domain.SecurityDecision, error)
	sanitizeToolOutputFn func(ctx context.Context, toolName string, output string) (string, error)
	turn                 *domain.TurnSecurityContext
}

type allowPolicy struct{}

func (allowPolicy) Authorize(context.Context, domain.Principal, domain.Action, domain.Resource) error {
	return nil
}

func (allowPolicy) ResolveAgentRole(context.Context, domain.Principal, string) (domain.AgentRole, error) {
	return domain.AgentRoleOwner, nil
}

func (m *mockSecurityManager) EvaluateToolCall(ctx context.Context, req domain.ToolEvaluationRequest) (domain.SecurityDecision, error) {
	if m.evaluateToolCallFn != nil {
		return m.evaluateToolCallFn(ctx, req)
	}
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
	if m.sanitizeToolOutputFn != nil {
		return m.sanitizeToolOutputFn(ctx, toolName, output)
	}
	return output, nil
}

func (m *mockSecurityManager) GrantSessionPermission(sessionKey string, pattern string) {}
func (m *mockSecurityManager) ClearSessionGrants(sessionKey string)                     {}
func (m *mockSecurityManager) ClearAllSessionGrants()                                   {}
func (m *mockSecurityManager) SetPreset(preset domain.SecurityPreset)                   {}
func (m *mockSecurityManager) SetRedactionMode(mode domain.RedactionMode)               {}
func (m *mockSecurityManager) AddWhitelistEntry(entry string)                           {}
func (m *mockSecurityManager) GetDashboardSummary(sessionKey string) domain.SecurityDashboard {
	return domain.SecurityDashboard{}
}
func (m *mockSecurityManager) EnsureWorkspaceHooks(workspaceDir string) error {
	return nil
}
func (m *mockSecurityManager) RegisterActiveTurn(turn domain.TurnSecurityContext)          {}
func (m *mockSecurityManager) UnregisterActiveTurn(convID string, workspaceDir string)     {}
func (m *mockSecurityManager) ResolveSessionKey(convID string, workspaceDir string) string { return "" }
func (m *mockSecurityManager) ResolveTurnContext(convID string, workspaceDir string) (domain.TurnSecurityContext, bool) {
	return domain.TurnSecurityContext{}, false
}
func (m *mockSecurityManager) ResolveTurnByID(turnID string) (domain.TurnSecurityContext, bool) {
	if m.turn != nil && turnID == m.turn.TurnID {
		return *m.turn, true
	}
	if turnID != "" {
		return domain.TurnSecurityContext{
			TurnID:     turnID,
			SessionKey: "telegram:12345",
			AgentName:  "agyent",
			Principal: domain.Principal{
				Kind:      domain.PrincipalUser,
				Provider:  "telegram",
				SubjectID: "12345",
			},
			Resource: domain.Resource{AgentName: "agyent"},
		}, true
	}
	return domain.TurnSecurityContext{}, false
}

func TestIPC_HookUsesRegisteredWorkspaceAndConversation(t *testing.T) {
	addr := "127.0.0.1:49974"
	trustedTurn := domain.TurnSecurityContext{
		TurnID:         "turn-trusted-scope",
		ConversationID: "conversation-trusted",
		SessionKey:     "telegram:12345",
		AgentName:      "agyent",
		WorkspaceDir:   "/trusted/workspace",
	}
	mockMgr := &mockSecurityManager{turn: &trustedTurn}
	mockMgr.evaluateToolCallFn = func(_ context.Context, req domain.ToolEvaluationRequest) (domain.SecurityDecision, error) {
		assert.Equal(t, trustedTurn.WorkspaceDir, req.WorkspaceDir)
		assert.Equal(t, trustedTurn.ConversationID, req.ConversationID)
		return domain.SecurityDecision{Decision: domain.DecisionAllow}, nil
	}
	server := NewServer(mockMgr, addr, nil)
	require.NoError(t, server.Start(context.Background()))
	defer server.Stop()

	resp, err := NewClient(addr).SendHookRequest(HookRequest{
		TurnID:         trustedTurn.TurnID,
		HookType:       "pre",
		ConversationID: "attacker-conversation",
		WorkspacePaths: []string{"/"},
		ToolCall:       HookToolCall{Name: "view_file", Args: map[string]interface{}{}},
	}, 2*time.Second)
	require.NoError(t, err)
	assert.Equal(t, string(domain.DecisionAllow), resp.Decision)
}
func (m *mockSecurityManager) UnregisterTurnByID(turnID string)         {}
func (m *mockSecurityManager) CancelSessionApprovals(sessionKey string) {}

func TestIPCServerAndClient_PreToolUse(t *testing.T) {
	addr := "127.0.0.1:49988"
	mockMgr := &mockSecurityManager{
		evaluateToolCallFn: func(ctx context.Context, req domain.ToolEvaluationRequest) (domain.SecurityDecision, error) {
			if req.ToolName == "run_command" {
				if cmd, ok := req.Args["CommandLine"].(string); ok && cmd == "rm -rf /" {
					return domain.SecurityDecision{
						Decision: domain.DecisionDeny,
						Reason:   "Destructive command blocked",
					}, nil
				}
			}
			return domain.SecurityDecision{Decision: domain.DecisionAllow}, nil
		},
	}

	server := NewServer(mockMgr, addr, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := server.Start(ctx)
	require.NoError(t, err)
	defer server.Stop()

	// Wait for server to start
	time.Sleep(20 * time.Millisecond)

	client := NewClient(addr)

	// Test 1: Denied command
	respDeny, err := client.SendHookRequest(HookRequest{
		TurnID:   "turn-test-123",
		HookType: "pre",
		ToolCall: HookToolCall{
			Name: "run_command",
			Args: map[string]interface{}{"CommandLine": "rm -rf /"},
		},
		ConversationID: "conv-123",
		StepIdx:        1,
	}, 2*time.Second)

	require.NoError(t, err)
	assert.Equal(t, "deny", respDeny.Decision)
	assert.Contains(t, respDeny.Reason, "Destructive command blocked")

	// Test 2: Allowed command
	respAllow, err := client.SendHookRequest(HookRequest{
		TurnID:   "turn-test-123",
		HookType: "pre",
		ToolCall: HookToolCall{
			Name: "run_command",
			Args: map[string]interface{}{"CommandLine": "go test ./..."},
		},
		ConversationID: "conv-123",
		StepIdx:        2,
	}, 2*time.Second)

	require.NoError(t, err)
	assert.Equal(t, "allow", respAllow.Decision)
}

func TestIPCClient_OfflineDefaultDeny(t *testing.T) {
	client := NewClient("127.0.0.1:49999") // Offline port

	resp, err := client.SendHookRequest(HookRequest{
		HookType: "pre",
		ToolCall: HookToolCall{
			Name: "run_command",
			Args: map[string]interface{}{"CommandLine": "rm -rf /"},
		},
	}, 500*time.Millisecond)

	assert.Error(t, err)
	assert.Equal(t, "deny", resp.Decision)
	assert.Contains(t, resp.Reason, "Fail-safe Default-Deny")
}

type mockScheduler struct {
	createScheduleFn     func(ctx context.Context, task domain.ScheduleTask) (*domain.ScheduleTask, error)
	listSchedulesFn      func(ctx context.Context, agentName string, status domain.ScheduleStatus) ([]domain.ScheduleTask, error)
	cancelScheduleFn     func(ctx context.Context, taskID string) error
	configureHeartbeatFn func(ctx context.Context, cfg domain.HeartbeatConfig, prompt string) error
	getHeartbeatFn       func(ctx context.Context, agentName string) (*domain.HeartbeatConfig, string, error)
	triggerHeartbeatFn   func(ctx context.Context, agentName string) error
}

func (m *mockScheduler) CreateSchedule(ctx context.Context, task domain.ScheduleTask) (*domain.ScheduleTask, error) {
	if m.createScheduleFn != nil {
		return m.createScheduleFn(ctx, task)
	}
	task.ID = "sched-test-1"
	return &task, nil
}
func (m *mockScheduler) ListSchedules(ctx context.Context, agentName string, status domain.ScheduleStatus) ([]domain.ScheduleTask, error) {
	if m.listSchedulesFn != nil {
		return m.listSchedulesFn(ctx, agentName, status)
	}
	return []domain.ScheduleTask{{ID: "sched-test-1", Title: "Mock Task"}}, nil
}
func (m *mockScheduler) CancelSchedule(ctx context.Context, taskID string) error {
	if m.cancelScheduleFn != nil {
		return m.cancelScheduleFn(ctx, taskID)
	}
	return nil
}
func (m *mockScheduler) ConfigureHeartbeat(ctx context.Context, cfg domain.HeartbeatConfig, prompt string) error {
	if m.configureHeartbeatFn != nil {
		return m.configureHeartbeatFn(ctx, cfg, prompt)
	}
	return nil
}
func (m *mockScheduler) GetHeartbeat(ctx context.Context, agentName string) (*domain.HeartbeatConfig, string, error) {
	if m.getHeartbeatFn != nil {
		return m.getHeartbeatFn(ctx, agentName)
	}
	return &domain.HeartbeatConfig{AgentName: agentName, Enabled: true, IntervalSeconds: 1800}, "Heartbeat prompt", nil
}
func (m *mockScheduler) TriggerHeartbeatNow(ctx context.Context, agentName string) error {
	if m.triggerHeartbeatFn != nil {
		return m.triggerHeartbeatFn(ctx, agentName)
	}
	return nil
}
func (m *mockScheduler) Start(ctx context.Context) error { return nil }
func (m *mockScheduler) Stop(ctx context.Context) error  { return nil }

// mockScheduleRepository is deliberately separate from mockScheduler because
// the persistence and orchestration ports intentionally expose different list
// signatures. It lets cancellation tests prove that IPC checks stored scope
// before invoking the scheduler.
type mockScheduleRepository struct {
	task *domain.ScheduleTask
}

var _ ports.ScheduleRepository = (*mockScheduleRepository)(nil)

func (m *mockScheduleRepository) SaveSchedule(context.Context, *domain.ScheduleTask) error {
	return nil
}
func (m *mockScheduleRepository) GetSchedule(_ context.Context, id string) (*domain.ScheduleTask, error) {
	if m.task == nil || m.task.ID != id {
		return nil, ports.ErrNotFound
	}
	copy := *m.task
	return &copy, nil
}
func (m *mockScheduleRepository) DeleteSchedule(context.Context, string) error { return nil }
func (m *mockScheduleRepository) ListSchedules(context.Context, string, domain.ScheduleStatus, int, int) ([]domain.ScheduleTask, int, error) {
	return nil, 0, nil
}
func (m *mockScheduleRepository) AcquireDueSchedules(context.Context, int64, int) ([]domain.ScheduleTask, error) {
	return nil, nil
}
func (m *mockScheduleRepository) UpdateScheduleRun(context.Context, string, int64, string, domain.ScheduleStatus) error {
	return nil
}
func (m *mockScheduleRepository) AdvanceScheduleNextRun(context.Context, string, int64) error {
	return nil
}
func (m *mockScheduleRepository) SanitizeInterruptedSchedules(context.Context) error { return nil }
func (m *mockScheduleRepository) GetHeartbeat(context.Context, string) (*domain.HeartbeatConfig, error) {
	return nil, ports.ErrNotFound
}
func (m *mockScheduleRepository) SaveHeartbeat(context.Context, *domain.HeartbeatConfig) error {
	return nil
}
func (m *mockScheduleRepository) AcquireDueHeartbeats(context.Context, int64, int) ([]domain.HeartbeatConfig, error) {
	return nil, nil
}
func (m *mockScheduleRepository) UpdateHeartbeatRun(context.Context, string, int64, string, domain.HeartbeatStatus) error {
	return nil
}
func (m *mockScheduleRepository) SanitizeInterruptedHeartbeats(context.Context) error { return nil }

func TestIPC_ScheduleAndHeartbeatActions(t *testing.T) {
	addr := "127.0.0.1:49989"
	mockMgr := &mockSecurityManager{}
	mockSched := &mockScheduler{}

	server := NewServer(mockMgr, addr, nil)
	server.SetPolicyEngine(allowPolicy{})
	server.SetScheduler(mockSched)
	server.SetScheduleStore(&mockScheduleRepository{task: &domain.ScheduleTask{
		ID:               "sched-test-1",
		AgentName:        "agyent",
		TargetSessionKey: "telegram:12345",
	}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := server.Start(ctx)
	require.NoError(t, err)
	defer server.Stop()

	time.Sleep(20 * time.Millisecond)

	client := NewClient(addr)
	client.SetTurnID("turn-schedule-test")

	// 1. Test schedule_task
	schedResp, err := client.SendAction("schedule_task", map[string]interface{}{
		"title":           "Daily Backup",
		"prompt":          "Perform automated backup",
		"time_expression": "0 2 * * *",
		"schedule_type":   "cron",
		"agent_name":      "agyent",
	}, 2*time.Second)
	require.NoError(t, err)
	assert.True(t, schedResp.Success)
	assert.Contains(t, string(schedResp.Data), "Daily Backup")

	// 2. Test list_schedules
	listResp, err := client.SendAction("list_schedules", map[string]interface{}{
		"agent_name": "agyent",
	}, 2*time.Second)
	require.NoError(t, err)
	assert.True(t, listResp.Success)
	assert.Contains(t, string(listResp.Data), "Mock Task")

	// 3. Test cancel_schedule
	cancelResp, err := client.SendAction("cancel_schedule", map[string]interface{}{
		"task_id": "sched-test-1",
	}, 2*time.Second)
	require.NoError(t, err)
	assert.True(t, cancelResp.Success)
	assert.Contains(t, string(cancelResp.Data), "CANCELLED")

	// 4. Test configure_heartbeat
	hbResp, err := client.SendAction("configure_heartbeat", map[string]interface{}{
		"agent_name": "agyent",
		"enabled":    true,
		"interval":   "45m",
		"prompt":     "Review unread alerts",
	}, 2*time.Second)
	require.NoError(t, err)
	assert.True(t, hbResp.Success)
	assert.Contains(t, string(hbResp.Data), "2700")

	// 5. Test trigger_heartbeat
	trigResp, err := client.SendAction("trigger_heartbeat", map[string]interface{}{
		"agent_name": "agyent",
	}, 2*time.Second)
	require.NoError(t, err)
	assert.True(t, trigResp.Success)
	assert.Contains(t, string(trigResp.Data), "TRIGGERED")
}

type mockSubagentDispatcher struct {
	dispatchTaskFn func(ctx context.Context, task domain.SubagentTask) (string, error)
	getTaskFn      func(ctx context.Context, taskID string) (*domain.SubagentTask, error)
	listTasksFn    func(ctx context.Context, sessionKey string, limit, offset int) ([]domain.SubagentTask, int, error)
	cancelTaskFn   func(ctx context.Context, taskID string) error
}

func (m *mockSubagentDispatcher) DispatchTask(ctx context.Context, task domain.SubagentTask) (string, error) {
	if m.dispatchTaskFn != nil {
		return m.dispatchTaskFn(ctx, task)
	}
	return "task-mock-123", nil
}

func (m *mockSubagentDispatcher) GetTask(ctx context.Context, taskID string) (*domain.SubagentTask, error) {
	if m.getTaskFn != nil {
		return m.getTaskFn(ctx, taskID)
	}
	return &domain.SubagentTask{ID: taskID, AgentName: "agyent", Status: domain.TaskStatusRunning, Title: "Mock Running Task"}, nil
}

func (m *mockSubagentDispatcher) GetTaskScoped(ctx context.Context, sessionKey, taskID string) (*domain.SubagentTask, error) {
	return m.GetTask(ctx, taskID)
}

func (m *mockSubagentDispatcher) ListActiveTasks(ctx context.Context, sessionKey string) ([]domain.SubagentTask, error) {
	return nil, nil
}

func (m *mockSubagentDispatcher) ListTasks(ctx context.Context, sessionKey string, limit, offset int) ([]domain.SubagentTask, int, error) {
	if m.listTasksFn != nil {
		return m.listTasksFn(ctx, sessionKey, limit, offset)
	}
	return []domain.SubagentTask{{ID: "task-1", AgentName: "agyent", Title: "Task 1"}}, 1, nil
}

func (m *mockSubagentDispatcher) SendTaskInput(ctx context.Context, taskID string, input string) error {
	return nil
}

func (m *mockSubagentDispatcher) SendTaskInputScoped(ctx context.Context, sessionKey, taskID, input string) error {
	return m.SendTaskInput(ctx, taskID, input)
}

func (m *mockSubagentDispatcher) CancelTask(ctx context.Context, taskID string) error {
	if m.cancelTaskFn != nil {
		return m.cancelTaskFn(ctx, taskID)
	}
	return nil
}

func (m *mockSubagentDispatcher) CancelTaskScoped(ctx context.Context, sessionKey, taskID string) error {
	return m.CancelTask(ctx, taskID)
}

func (m *mockSubagentDispatcher) Start(ctx context.Context) error { return nil }
func (m *mockSubagentDispatcher) Stop(ctx context.Context) error  { return nil }

func TestIPCServerAndClient_SubagentActions(t *testing.T) {
	addr := "127.0.0.1:49977"
	mockMgr := &mockSecurityManager{}
	mockSub := &mockSubagentDispatcher{
		dispatchTaskFn: func(ctx context.Context, task domain.SubagentTask) (string, error) {
			assert.Equal(t, "Deep Research", task.Title)
			return "task-sub-test-99", nil
		},
		getTaskFn: func(ctx context.Context, taskID string) (*domain.SubagentTask, error) {
			return &domain.SubagentTask{
				ID:        taskID,
				AgentName: "agyent",
				Status:    domain.TaskStatusRunning,
				Title:     "Deep Research",
			}, nil
		},
		cancelTaskFn: func(ctx context.Context, taskID string) error {
			assert.Equal(t, "task-sub-test-99", taskID)
			return nil
		},
	}

	server := NewServer(mockMgr, addr, nil)
	server.SetPolicyEngine(allowPolicy{})
	server.SetSubagents(mockSub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := server.Start(ctx)
	require.NoError(t, err)
	defer server.Stop()

	time.Sleep(20 * time.Millisecond)
	client := NewClient(addr)
	client.SetTurnID("turn-subagent-test")

	// 1. Test dispatch_subagent
	dispResp, err := client.SendAction("dispatch_subagent", map[string]interface{}{
		"title":              "Deep Research",
		"prompt":             "Research Go concurrency models",
		"agent_name":         "agyent",
		"parent_session_key": "telegram:12345",
	}, 2*time.Second)
	require.NoError(t, err)
	assert.True(t, dispResp.Success)
	assert.Contains(t, string(dispResp.Data), "task-sub-test-99")

	// 2. Test check_subagent_progress
	progResp, err := client.SendAction("check_subagent_progress", map[string]interface{}{
		"task_id": "task-sub-test-99",
	}, 2*time.Second)
	require.NoError(t, err)
	assert.True(t, progResp.Success)
	assert.Contains(t, string(progResp.Data), "Deep Research")

	// 3. Test cancel_subagent_task
	cancResp, err := client.SendAction("cancel_subagent_task", map[string]interface{}{
		"task_id": "task-sub-test-99",
	}, 2*time.Second)
	require.NoError(t, err)
	assert.True(t, cancResp.Success)
	assert.Contains(t, string(cancResp.Data), "CANCELLED")

	// 4. Test list_subagents
	listResp, err := client.SendAction("list_subagents", map[string]interface{}{
		"session_key": "telegram:12345",
		"limit":       5,
	}, 2*time.Second)
	require.NoError(t, err)
	assert.True(t, listResp.Success)
	assert.Contains(t, string(listResp.Data), "task-1")
}

func TestIPC_ActionTurnCapabilityAndHookValidation(t *testing.T) {
	addr := "127.0.0.1:49976"
	mockMgr := &mockSecurityManager{}
	server := NewServer(mockMgr, addr, nil)
	server.SetPolicyEngine(allowPolicy{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := server.Start(ctx)
	require.NoError(t, err)
	defer server.Stop()

	time.Sleep(20 * time.Millisecond)
	client := NewClient(addr)

	// 1. Actions without an active turn capability are rejected. A process-wide
	// token is intentionally not accepted as an alternative credential.
	unauthResp, err := client.SendAction("list_subagents", map[string]interface{}{}, 2*time.Second)
	require.NoError(t, err)
	assert.False(t, unauthResp.Success)
	assert.Contains(t, unauthResp.Error, "unauthorized")

	// 2. A valid active turn reaches the handler, which then reports its own
	// configuration error because no subagent dispatcher is installed.
	client.SetTurnID("turn-capability-test")
	authResp, err := client.SendAction("list_subagents", map[string]interface{}{}, 2*time.Second)
	require.NoError(t, err)
	assert.False(t, authResp.Success) // fails at subagent nil check, not auth!
	assert.Contains(t, authResp.Error, "subagent dispatcher is not initialized")

	// 3. Unknown hook type should be rejected
	hookResp, err := client.SendHookRequest(HookRequest{
		HookType: "invalid_hook_type",
	}, 2*time.Second)
	require.NoError(t, err)
	assert.Equal(t, string(domain.DecisionDeny), hookResp.Decision)
	assert.Contains(t, hookResp.Reason, "Unsupported or unauthorized hook type")
}
