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
