package scheduler

import (
	"context"
	"fmt"
	"testing"
	"time"

	"agyent/internal/config"
	"agyent/internal/core/domain"
	"agyent/internal/core/eventbus"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskExecutor_ModelAndEffortResolution(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.AGY.DefaultModel = ""
	cfg.AGY.DefaultEffort = "high"
	cfg.Agents = map[string]config.AgentProfileConfig{
		"claude_agent": {
			DefaultModel:  "claude",
			DefaultEffort: "high",
		},
		"pro_agent": {
			DefaultModel:  "pro",
			DefaultEffort: "medium",
		},
	}

	exec := NewTaskExecutor(cfg, nil, nil, nil, nil)

	// 1. Default fallback ("flash") with global effort "high"
	// Should resolve to canonical gemini-3.8-flash with effort "high"
	model, effort := exec.resolveModelAndEffort("default_agent")
	assert.Equal(t, "gemini-3.8-flash", model)
	assert.Equal(t, "high", effort)

	// 2. Agent with Claude: effort should be stripped
	modelClaude, effortClaude := exec.resolveModelAndEffort("claude_agent")
	assert.Equal(t, "claude-sonnet-4-6", modelClaude)
	assert.Equal(t, "", effortClaude)

	// 3. Agent with Pro: medium effort should be clamped to high
	modelPro, effortPro := exec.resolveModelAndEffort("pro_agent")
	assert.Equal(t, "gemini-3.1-pro", modelPro)
	assert.Equal(t, "high", effortPro)
}

type effortRejectingRunner struct {
	attempts     int
	receivedReqs []domain.ExecutionRequest
}

func (r *effortRejectingRunner) Name() string { return "rejecting-runner" }
func (r *effortRejectingRunner) Execute(ctx context.Context, req domain.ExecutionRequest) (*domain.ExecutionResult, error) {
	r.attempts++
	r.receivedReqs = append(r.receivedReqs, req)

	if req.Effort != "" {
		return nil, fmt.Errorf("exit status 1: invalid model selection (--model %q --effort %q): --effort is not supported for model %q", req.Model, req.Effort, req.Model)
	}

	return &domain.ExecutionResult{
		Success:      true,
		ResponseText: "Executed successfully without effort flag.",
	}, nil
}
func (r *effortRejectingRunner) ExecuteStream(ctx context.Context, req domain.ExecutionRequest, sessionKey string) (*domain.ExecutionResult, error) {
	return r.Execute(ctx, req)
}
func (r *effortRejectingRunner) InterruptStream(ctx context.Context, sessionKey string) error {
	return nil
}
func (r *effortRejectingRunner) HealthCheck(ctx context.Context) error { return nil }
func (r *effortRejectingRunner) ListAvailableModels(ctx context.Context) ([]domain.ModelCapability, error) {
	return nil, nil
}

func TestTaskExecutor_EffortRejectionRecovery(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Storage.AgentsDir = t.TempDir()

	runner := &effortRejectingRunner{}
	bus := eventbus.NewEventBus(10, 1)
	defer bus.Close()

	exec := NewTaskExecutor(cfg, runner, nil, bus, nil)

	task := domain.ScheduleTask{
		ID:        "cron-test-retry",
		AgentName: "dev_agent",
		Title:     "Morning Greetings",
		Prompt:    "Say good morning",
	}

	res, err := exec.ExecuteSchedule(context.Background(), task)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.Success)
	assert.Equal(t, "Executed successfully without effort flag.", res.ResponseText)

	// Should have attempted twice: first with effort, then retried with empty effort
	assert.Equal(t, 2, runner.attempts)
	require.Len(t, runner.receivedReqs, 2)
	assert.NotEmpty(t, runner.receivedReqs[0].Effort)
	assert.Empty(t, runner.receivedReqs[1].Effort)
}

type artifactReturningRunner struct {
	convID    string
	artifacts []domain.Attachment
}

func (r *artifactReturningRunner) Name() string { return "artifact-runner" }
func (r *artifactReturningRunner) Execute(ctx context.Context, req domain.ExecutionRequest) (*domain.ExecutionResult, error) {
	return &domain.ExecutionResult{
		Success:        true,
		ConversationID: r.convID,
		ResponseText:   "Dạ em gửi anh ảnh nè: ![Bé Na](be_na.png)",
		Artifacts:      r.artifacts,
	}, nil
}
func (r *artifactReturningRunner) ExecuteStream(ctx context.Context, req domain.ExecutionRequest, sessionKey string) (*domain.ExecutionResult, error) {
	return r.Execute(ctx, req)
}
func (r *artifactReturningRunner) InterruptStream(ctx context.Context, sessionKey string) error {
	return nil
}
func (r *artifactReturningRunner) HealthCheck(ctx context.Context) error { return nil }
func (r *artifactReturningRunner) ListAvailableModels(ctx context.Context) ([]domain.ModelCapability, error) {
	return nil, nil
}

func TestTaskExecutor_ExecuteSchedule_EmitsArtifactsAndContext(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Storage.AgentsDir = t.TempDir()

	expectedConvID := "conv-sched-123456"
	expectedArtifacts := []domain.Attachment{
		{
			FileName: "be_na.png",
			FilePath: "/tmp/be_na.png",
			Type:     "image",
		},
	}

	runner := &artifactReturningRunner{
		convID:    expectedConvID,
		artifacts: expectedArtifacts,
	}

	bus := eventbus.NewEventBus(10, 1)
	defer bus.Close()

	payloadChan := make(chan domain.ScheduleEventPayload, 1)
	bus.SubscribeAsync(domain.EventScheduleCompleted, func(ctx context.Context, evt domain.Event) {
		if p, ok := evt.Payload.(domain.ScheduleEventPayload); ok {
			payloadChan <- p
		}
	})

	exec := NewTaskExecutor(cfg, runner, nil, bus, nil)

	task := domain.ScheduleTask{
		ID:        "sched-test-media",
		AgentName: "agyent",
		Title:     "Send Photo Task",
		Prompt:    "Generate and send a photo",
	}

	res, err := exec.ExecuteSchedule(context.Background(), task)
	require.NoError(t, err)
	require.NotNil(t, res)

	select {
	case payload := <-payloadChan:
		assert.Equal(t, task.ID, payload.Task.ID)
		assert.Equal(t, expectedConvID, payload.ConversationID, "ConversationID must be propagated in payload")
		assert.NotEmpty(t, payload.WorkspaceDir, "WorkspaceDir must be populated in payload")
		require.Len(t, payload.Artifacts, 1, "Artifacts must be propagated in payload")
		assert.Equal(t, "be_na.png", payload.Artifacts[0].FileName)
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for EventScheduleCompleted")
	}
}

func TestTaskExecutor_ExecuteSchedule_DynamicImageTimeout(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Scheduler.DefaultTaskTimeoutSeconds = 30 // Set low timeout

	runner := &effortRejectingRunner{}
	bus := eventbus.NewEventBus(10, 1)
	defer bus.Close()

	exec := NewTaskExecutor(cfg, runner, nil, bus, nil)

	// Case 1: Image task should be dynamically boosted to >= 300s
	taskImage := domain.ScheduleTask{
		ID:        "cron-1a29f7e8",
		AgentName: "agyent",
		Title:     "Lịch sinh hoạt 05:30 sáng: Chào buổi sáng & gửi ảnh Bé Na thức dậy",
		Prompt:    "Gửi lời chào buổi sáng và tạo hình ảnh Bé Na thức dậy",
	}
	_, err := exec.ExecuteSchedule(context.Background(), taskImage)
	require.NoError(t, err)
	require.NotEmpty(t, runner.receivedReqs)
	lastReq := runner.receivedReqs[len(runner.receivedReqs)-1]
	assert.GreaterOrEqual(t, lastReq.Timeout, 300*time.Second, "Image tasks must have dynamically boosted timeout >= 300s")

	// Case 2: Non-image task keeps default timeout
	taskNormal := domain.ScheduleTask{
		ID:        "cron-git-status",
		AgentName: "agyent",
		Title:     "Daily Git Status Check",
		Prompt:    "Check git status of repository",
	}
	_, err = exec.ExecuteSchedule(context.Background(), taskNormal)
	require.NoError(t, err)
	lastReq = runner.receivedReqs[len(runner.receivedReqs)-1]
	assert.Equal(t, 30*time.Second, lastReq.Timeout, "Non-image task should keep configured timeout")
}

type mockExecutionService struct {
	lastPrincipal  domain.Principal
	lastReq        domain.ExecutionRequest
	lastSessionKey string
	resultToReturn *domain.ExecutionResult
	errToReturn    error
}

func (m *mockExecutionService) ExecuteTurn(ctx context.Context, principal domain.Principal, req domain.ExecutionRequest, sessionKey string, isStreaming bool) (*domain.ExecutionResult, error) {
	m.lastPrincipal = principal
	m.lastReq = req
	m.lastSessionKey = sessionKey
	return m.resultToReturn, m.errToReturn
}

func (m *mockExecutionService) InterruptTurn(ctx context.Context, sessionKey string) error {
	return nil
}

type mockSecurityManager struct {
	clearedSessions []string
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
func (m *mockSecurityManager) ClearSessionGrants(sessionKey string) {
	m.clearedSessions = append(m.clearedSessions, sessionKey)
}
func (m *mockSecurityManager) ClearAllSessionGrants()                                   {}
func (m *mockSecurityManager) SetPreset(preset domain.SecurityPreset)                   {}
func (m *mockSecurityManager) SetRedactionMode(mode domain.RedactionMode)               {}
func (m *mockSecurityManager) AddWhitelistEntry(entry string)                           {}
func (m *mockSecurityManager) GetDashboardSummary(sessionKey string) domain.SecurityDashboard {
	return domain.SecurityDashboard{}
}
func (m *mockSecurityManager) EnsureWorkspaceHooks(workspaceDir string) error          { return nil }
func (m *mockSecurityManager) RegisterActiveTurn(turn domain.TurnSecurityContext)      {}
func (m *mockSecurityManager) UnregisterActiveTurn(convID string, workspaceDir string) {}
func (m *mockSecurityManager) UnregisterTurnByID(turnID string)                        {}
func (m *mockSecurityManager) ResolveSessionKey(convID string, workspaceDir string) string {
	return ""
}
func (m *mockSecurityManager) ResolveTurnContext(convID string, workspaceDir string) (domain.TurnSecurityContext, bool) {
	return domain.TurnSecurityContext{}, false
}
func (m *mockSecurityManager) ResolveTurnByID(turnID string) (domain.TurnSecurityContext, bool) {
	return domain.TurnSecurityContext{}, false
}
func (m *mockSecurityManager) CancelSessionApprovals(sessionKey string) {}

func TestTaskExecutor_DelegatedPrincipal_AndSessionRouting(t *testing.T) {
	cfg := config.DefaultConfig()
	bus := eventbus.NewEventBus(10, 1)
	defer bus.Close()

	mockExec := &mockExecutionService{
		resultToReturn: &domain.ExecutionResult{
			Success:      true,
			ResponseText: "Top trending Etsy POD shirts: 1. Vintage German Eagle",
		},
	}
	mockSec := &mockSecurityManager{}

	exec := NewTaskExecutor(cfg, nil, nil, bus, nil)
	exec.SetExecutionService(mockExec)
	exec.SetSecurityManager(mockSec)

	task := domain.ScheduleTask{
		ID:        "cron-93103b51",
		AgentName: "wife_assistant",
		Title:     "Radar POD DE",
		Prompt:    "Cào dữ liệu bestseller Etsy DE",
		Channel:   "telegram",
		ChatID:    "8220274185",
		ThreadID:  "0",
		CreatedBy: "8220274185",
	}

	res, err := exec.ExecuteSchedule(context.Background(), task)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.Success)

	// 1. Verify Delegated Principal
	assert.Equal(t, domain.PrincipalUser, mockExec.lastPrincipal.Kind)
	assert.Equal(t, "telegram", mockExec.lastPrincipal.Provider)
	assert.Equal(t, "8220274185", mockExec.lastPrincipal.SubjectID)
	assert.Equal(t, "wife_assistant", mockExec.lastPrincipal.AccountID)

	// 2. Verify Channel-Routable SessionKey
	expectedSessionKey := "telegram:8220274185"
	assert.Equal(t, expectedSessionKey, mockExec.lastSessionKey)

	// 3. Verify Session Grant Invalidation on Completion
	require.Len(t, mockSec.clearedSessions, 1)
	assert.Equal(t, expectedSessionKey, mockSec.clearedSessions[0])
}

func TestTaskExecutor_QualityAssertion_FalsePositiveProtection(t *testing.T) {
	cfg := config.DefaultConfig()
	bus := eventbus.NewEventBus(10, 1)
	defer bus.Close()

	mockExec := &mockExecutionService{}
	exec := NewTaskExecutor(cfg, nil, nil, bus, nil)
	exec.SetExecutionService(mockExec)

	task := domain.ScheduleTask{
		ID:        "cron-quality-check",
		AgentName: "wife_assistant",
		Title:     "Radar Etsy",
		Prompt:    "Cào dữ liệu",
		Channel:   "telegram",
		ChatID:    "8220274185",
		CreatedBy: "8220274185",
	}

	// Case 1: Empty output despite exit code 0 -> Must fail assertion
	mockExec.resultToReturn = &domain.ExecutionResult{
		Success:      true,
		ResponseText: "   ",
	}
	res, _ := exec.ExecuteSchedule(context.Background(), task)
	assert.False(t, res.Success, "Empty output must be marked as failed")
	assert.Contains(t, res.Error, "empty output")

	// Case 2: Soft-deny response text -> Must fail assertion
	mockExec.resultToReturn = &domain.ExecutionResult{
		Success:      true,
		ResponseText: "🛡️ [Security Gate]: Action rejected by user or approval timed out",
	}
	res, _ = exec.ExecuteSchedule(context.Background(), task)
	assert.False(t, res.Success, "Soft-deny response must be marked as failed")
	assert.Contains(t, res.Error, "blocked by security gate")
}

