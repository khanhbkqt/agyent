package scheduler

import (
	"context"
	"fmt"
	"testing"

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
	// Should resolve to canonical gemini-3.7-flash with effort "high"
	model, effort := exec.resolveModelAndEffort("default_agent")
	assert.Equal(t, "gemini-3.7-flash", model)
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
