package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"agyent/internal/core/domain"
	"agyent/internal/core/ports"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScheduleRepo_Lifecycle(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_schedule.db")

	store, err := Open(dbPath)
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	now := time.Now().Truncate(time.Millisecond)
	task := &domain.ScheduleTask{
		ID:               "sched-001",
		AgentName:        "dev_architect",
		Title:            "Daily Standup Summary",
		ScheduleType:     domain.ScheduleTypeCron,
		ScheduleExpr:     "0 9 * * *",
		Prompt:           "Review github commits and report status",
		TargetSessionKey: "sched:dev_architect:sched-001",
		ChatID:           "123456",
		ThreadID:         "42",
		Channel:          "telegram",
		Status:           domain.ScheduleStatusActive,
		OverlapPolicy:    domain.OverlapPolicySkip,
		MisfirePolicy:    domain.MisfirePolicySkipToLatest,
		NextRunAt:        now.Add(-10 * time.Minute), // Due in the past
		RunCount:         0,
		MaxRuns:          0,
		CreatedBy:        "user-999",
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	// 1. Save
	err = store.SaveSchedule(ctx, task)
	require.NoError(t, err)

	// 2. Get
	fetched, err := store.GetSchedule(ctx, "sched-001")
	require.NoError(t, err)
	assert.Equal(t, task.ID, fetched.ID)
	assert.Equal(t, task.AgentName, fetched.AgentName)
	assert.Equal(t, domain.ScheduleStatusActive, fetched.Status)

	// 3. List
	tasks, total, err := store.ListSchedules(ctx, "dev_architect", "", 10, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	assert.Len(t, tasks, 1)

	// 4. Acquire Due Schedules (Atomically sets to RUNNING)
	due, err := store.AcquireDueSchedules(ctx, now.UnixMilli(), 10)
	require.NoError(t, err)
	require.Len(t, due, 1)
	assert.Equal(t, "sched-001", due[0].ID)
	assert.Equal(t, domain.ScheduleStatusRunning, due[0].Status)

	// Verify status in DB is RUNNING
	fetchedRunning, err := store.GetSchedule(ctx, "sched-001")
	require.NoError(t, err)
	assert.Equal(t, domain.ScheduleStatusRunning, fetchedRunning.Status)

	// 5. Update Schedule Run
	nextRun := now.Add(24 * time.Hour).UnixMilli()
	err = store.UpdateScheduleRun(ctx, "sched-001", nextRun, "", domain.ScheduleStatusActive)
	require.NoError(t, err)

	fetchedUpdated, err := store.GetSchedule(ctx, "sched-001")
	require.NoError(t, err)
	assert.Equal(t, domain.ScheduleStatusActive, fetchedUpdated.Status)
	assert.Equal(t, 1, fetchedUpdated.RunCount)

	// 6. Test Sanitize Interrupted Schedules on Crash Recovery
	// Set task back to RUNNING manually to simulate mid-turn crash
	task.Status = domain.ScheduleStatusRunning
	_ = store.SaveSchedule(ctx, task)

	err = store.SanitizeInterruptedSchedules(ctx)
	require.NoError(t, err)

	sanitized, err := store.GetSchedule(ctx, "sched-001")
	require.NoError(t, err)
	assert.Equal(t, domain.ScheduleStatusActive, sanitized.Status)
	assert.Contains(t, sanitized.LastError, "daemon restart")

	// 7. Delete
	err = store.DeleteSchedule(ctx, "sched-001")
	require.NoError(t, err)

	_, err = store.GetSchedule(ctx, "sched-001")
	assert.ErrorIs(t, err, ports.ErrNotFound)
}

func TestHeartbeatRepo_Lifecycle(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_heartbeat.db")

	store, err := Open(dbPath)
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond)

	hb := &domain.HeartbeatConfig{
		AgentName:        "researcher",
		Enabled:          true,
		IntervalSeconds:  1800,
		TargetSessionKey: "telegram:12345:67",
		ChatID:           "12345",
		ThreadID:         "67",
		Channel:          "telegram",
		Status:           domain.HeartbeatStatusIdle,
		NextRunAt:        now.Add(-5 * time.Minute), // Due
		UpdatedAt:        now,
	}

	// 1. Save
	err = store.SaveHeartbeat(ctx, hb)
	require.NoError(t, err)

	// 2. Get
	fetched, err := store.GetHeartbeat(ctx, "researcher")
	require.NoError(t, err)
	assert.Equal(t, "researcher", fetched.AgentName)
	assert.True(t, fetched.Enabled)
	assert.Equal(t, 1800, fetched.IntervalSeconds)

	// 3. Acquire Due Heartbeats
	dueHBs, err := store.AcquireDueHeartbeats(ctx, now.UnixMilli(), 10)
	require.NoError(t, err)
	require.Len(t, dueHBs, 1)
	assert.Equal(t, "researcher", dueHBs[0].AgentName)
	assert.Equal(t, domain.HeartbeatStatusRunning, dueHBs[0].Status)

	// 4. Update Heartbeat Run
	nextRun := now.Add(30 * time.Minute).UnixMilli()
	err = store.UpdateHeartbeatRun(ctx, "researcher", nextRun, "", domain.HeartbeatStatusIdle)
	require.NoError(t, err)

	fetchedAfterRun, err := store.GetHeartbeat(ctx, "researcher")
	require.NoError(t, err)
	assert.Equal(t, domain.HeartbeatStatusIdle, fetchedAfterRun.Status)

	// 5. Sanitize Interrupted Heartbeats
	hb.Status = domain.HeartbeatStatusRunning
	_ = store.SaveHeartbeat(ctx, hb)

	err = store.SanitizeInterruptedHeartbeats(ctx)
	require.NoError(t, err)

	sanitizedHB, err := store.GetHeartbeat(ctx, "researcher")
	require.NoError(t, err)
	assert.Equal(t, domain.HeartbeatStatusIdle, sanitizedHB.Status)
}
