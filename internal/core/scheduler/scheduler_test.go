package scheduler

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"agyent/internal/adapters/storage/sqlite"
	"agyent/internal/adapters/workspace"
	"agyent/internal/config"
	"agyent/internal/core/domain"
	"agyent/internal/core/eventbus"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockRunner struct {
	executedReqs []domain.ExecutionRequest
}

func (m *mockRunner) Name() string { return "mock-runner" }
func (m *mockRunner) Execute(ctx context.Context, req domain.ExecutionRequest) (*domain.ExecutionResult, error) {
	m.executedReqs = append(m.executedReqs, req)
	return &domain.ExecutionResult{
		Success:      true,
		ResponseText: "Heartbeat execution completed successfully. All systems nominal.",
	}, nil
}
func (m *mockRunner) ExecuteStream(ctx context.Context, req domain.ExecutionRequest, sessionKey string) (*domain.ExecutionResult, error) {
	return m.Execute(ctx, req)
}
func (m *mockRunner) InterruptStream(ctx context.Context, sessionKey string) error { return nil }
func (m *mockRunner) HealthCheck(ctx context.Context) error                         { return nil }
func (m *mockRunner) ListAvailableModels(ctx context.Context) ([]domain.ModelCapability, error) {
	return nil, nil
}

func TestScheduler_LifecycleAndCRUD(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_sched.db")
	agentsDir := filepath.Join(tempDir, "agents")

	cfg := config.DefaultConfig()
	cfg.Storage.DBPath = dbPath
	cfg.Storage.AgentsDir = agentsDir

	store, err := sqlite.Open(dbPath)
	require.NoError(t, err)
	defer store.Close()

	wsMgr := workspace.NewManager(nil)
	runner := &mockRunner{}
	bus := eventbus.NewEventBus(100, 2)
	defer bus.Close()

	sched := NewScheduler(cfg, store, wsMgr, runner, bus, nil)

	ctx := context.Background()
	err = sched.Start(ctx)
	require.NoError(t, err)

	// 1. Create Schedule
	task := domain.ScheduleTask{
		AgentName:    "dev_bot",
		Title:        "Reminder test",
		ScheduleType: domain.ScheduleTypeOnce,
		ScheduleExpr: "in 1h",
		Prompt:       "Check server disk usage",
		ChatID:       "123456",
		CreatedBy:    "user-1",
	}

	created, err := sched.CreateSchedule(ctx, task)
	require.NoError(t, err)
	assert.NotEmpty(t, created.ID)
	assert.Equal(t, domain.ScheduleStatusActive, created.Status)

	// 2. List
	tasks, err := sched.ListSchedules(ctx, "dev_bot", "")
	require.NoError(t, err)
	assert.Len(t, tasks, 1)

	// 3. Heartbeat Configuration
	hbCfg := domain.HeartbeatConfig{
		AgentName:        "dev_bot",
		Enabled:          true,
		IntervalSeconds:  600,
		TargetSessionKey: "telegram:123456",
		ChatID:           "123456",
	}
	err = sched.ConfigureHeartbeat(ctx, hbCfg, "# Directives\nCheck queues.")
	require.NoError(t, err)

	fetchedHBCfg, prompt, err := sched.GetHeartbeat(ctx, "dev_bot")
	require.NoError(t, err)
	assert.True(t, fetchedHBCfg.Enabled)
	assert.Equal(t, 600, fetchedHBCfg.IntervalSeconds)
	assert.Contains(t, prompt, "Check queues.")

	// 4. Cancel Schedule
	err = sched.CancelSchedule(ctx, created.ID)
	require.NoError(t, err)

	tasksAfterCancel, err := sched.ListSchedules(ctx, "dev_bot", "")
	require.NoError(t, err)
	assert.Empty(t, tasksAfterCancel)

	// 5. Graceful Stop (< 2.8s)
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer stopCancel()

	stopStart := time.Now()
	err = sched.Stop(stopCtx)
	require.NoError(t, err)
	assert.Less(t, time.Since(stopStart), 1500*time.Millisecond)
}
