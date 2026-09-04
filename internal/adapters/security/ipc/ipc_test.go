package ipc

import (
	"context"
	"testing"
	"time"

	"agyent/internal/core/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockSecurityManager struct {
	evaluateToolCallFn   func(ctx context.Context, req domain.ToolEvaluationRequest) (domain.SecurityDecision, error)
	sanitizeToolOutputFn func(ctx context.Context, toolName string, output string) (string, error)
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
	respDeny, err := client.SendHookRequest(domain.HookRequest{
		HookType: "pre",
		ToolCall: domain.HookToolCall{
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
	respAllow, err := client.SendHookRequest(domain.HookRequest{
		HookType: "pre",
		ToolCall: domain.HookToolCall{
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

	resp, err := client.SendHookRequest(domain.HookRequest{
		HookType: "pre",
		ToolCall: domain.HookToolCall{
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

func TestIPC_ScheduleAndHeartbeatActions(t *testing.T) {
	addr := "127.0.0.1:49989"
	mockMgr := &mockSecurityManager{}
	mockSched := &mockScheduler{}

	server := NewServer(mockMgr, addr, nil)
	server.SetScheduler(mockSched)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := server.Start(ctx)
	require.NoError(t, err)
	defer server.Stop()

	time.Sleep(20 * time.Millisecond)

	client := NewClient(addr)

	// 1. Test schedule_task
	schedResp, err := client.SendAction("schedule_task", map[string]interface{}{
		"title":           "Daily Backup",
		"prompt":          "Perform automated backup",
		"time_expression": "0 2 * * *",
		"schedule_type":   "cron",
		"agent_name":      "coder",
	}, 2*time.Second)
	require.NoError(t, err)
	assert.True(t, schedResp.Success)
	assert.Contains(t, string(schedResp.Data), "Daily Backup")

	// 2. Test list_schedules
	listResp, err := client.SendAction("list_schedules", map[string]interface{}{
		"agent_name": "coder",
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
		"agent_name": "coder",
		"enabled":    true,
		"interval":   "45m",
		"prompt":     "Review unread alerts",
	}, 2*time.Second)
	require.NoError(t, err)
	assert.True(t, hbResp.Success)
	assert.Contains(t, string(hbResp.Data), "2700")

	// 5. Test trigger_heartbeat
	trigResp, err := client.SendAction("trigger_heartbeat", map[string]interface{}{
		"agent_name": "coder",
	}, 2*time.Second)
	require.NoError(t, err)
	assert.True(t, trigResp.Success)
	assert.Contains(t, string(trigResp.Data), "TRIGGERED")
}
