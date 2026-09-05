package subagent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"agyent/internal/adapters/storage/sqlite"
	"agyent/internal/config"
	"agyent/internal/core/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubagentDispatcher_LifecycleAndQueue(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_dispatcher.db")

	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer store.Close()

	sessionKey := "telegram:112233"
	_, err = store.GetOrCreateSession(ctx, sessionKey, "agyent")
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	cfg := config.SubagentConfig{
		MaxConcurrentWorkers:  2,
		DefaultTimeoutSeconds: 5,
		DefaultModel:          "flash",
		DefaultEffort:         "low",
	}

	dispatcher := NewDispatcher(cfg, "non_existent_binary_for_mock", store, nil)
	if err := dispatcher.Start(ctx); err != nil {
		t.Fatalf("failed to start dispatcher: %v", err)
	}
	defer func() { _ = dispatcher.Stop(ctx) }()

	// 1. Test Non-blocking Dispatch (< 5ms)
	start := time.Now()
	task1 := domain.SubagentTask{
		ParentSessionKey:     sessionKey,
		ParentConversationID: "conv-parent-01",
		AgentName:            "coder",
		Title:                "Test Task Dispatch",
		Prompt:               "Do something",
	}

	taskID, err := dispatcher.DispatchTask(ctx, task1)
	latency := time.Since(start)
	if err != nil {
		t.Fatalf("failed to dispatch task: %v", err)
	}
	if taskID == "" {
		t.Errorf("expected non-empty taskID")
	}
	if latency > 50*time.Millisecond {
		t.Errorf("dispatch latency took too long: %v (expected < 50ms)", latency)
	}

	// 2. Test GetTask immediately (In-memory or SQLite)
	retrieved, err := dispatcher.GetTask(ctx, taskID)
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}
	if retrieved.ID != taskID {
		t.Errorf("expected task ID %s, got %s", taskID, retrieved.ID)
	}
	if retrieved.ParentSessionKey != sessionKey {
		t.Errorf("expected session key %s, got %s", sessionKey, retrieved.ParentSessionKey)
	}

	// 3. Test ListActiveTasks
	activeTasks, err := dispatcher.ListActiveTasks(ctx, sessionKey)
	if err != nil {
		t.Fatalf("failed to list active tasks: %v", err)
	}
	if len(activeTasks) == 0 {
		t.Logf("task was already picked up and executed by mock executor")
	}

	// 4. Test CancelTask
	cancelTask := domain.SubagentTask{
		ID:                   "task-cancel-me",
		ParentSessionKey:     sessionKey,
		ParentConversationID: "conv-parent-01",
		AgentName:            "coder",
		Title:                "To be cancelled",
		Prompt:               "Cancel prompt",
	}
	if err := store.SaveSubagentTask(ctx, &cancelTask); err != nil {
		t.Fatalf("failed to save task for cancel: %v", err)
	}

	if err := dispatcher.CancelTask(ctx, "task-cancel-me"); err != nil {
		t.Fatalf("failed to cancel task: %v", err)
	}

	cancelledTask, err := store.GetSubagentTask(ctx, "task-cancel-me")
	if err != nil {
		t.Fatalf("failed to get cancelled task: %v", err)
	}
	if cancelledTask.Status != domain.TaskStatusCancelled {
		t.Errorf("expected status CANCELLED, got %s", cancelledTask.Status)
	}
}

func TestSubagentDispatcher_SendTaskInput_StateCheck(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_input.db")

	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer store.Close()

	sessionKey := "telegram:556677"
	_, _ = store.GetOrCreateSession(ctx, sessionKey, "agyent")

	cfg := config.SubagentConfig{
		MaxConcurrentWorkers:  2,
		DefaultTimeoutSeconds: 5,
	}

	dispatcher := NewDispatcher(cfg, "non_existent_binary", store, nil)
	_ = dispatcher.Start(ctx)
	defer func() { _ = dispatcher.Stop(ctx) }()

	// 1. Task not in WAITING_FOR_INPUT -> SendTaskInput returns error
	taskRunning := domain.SubagentTask{
		ID:                   "task-running-01",
		ParentSessionKey:     sessionKey,
		ParentConversationID: "conv-01",
		Prompt:               "running task",
		Status:               domain.TaskStatusRunning,
	}
	_ = store.SaveSubagentTask(ctx, &taskRunning)

	err = dispatcher.SendTaskInput(ctx, "task-running-01", "my answer")
	if err == nil {
		t.Errorf("expected error when calling SendTaskInput on RUNNING task, got nil")
	}

	// 2. Task in WAITING_FOR_INPUT -> SendTaskInput succeeds
	taskWaiting := domain.SubagentTask{
		ID:                   "task-waiting-01",
		ParentSessionKey:     sessionKey,
		ParentConversationID: "conv-01",
		SubConversationID:    "sub-conv-brain-01",
		Prompt:               "waiting task",
		Status:               domain.TaskStatusWaitingInput,
		PendingQuestion:      "Which option?",
	}
	_ = store.SaveSubagentTask(ctx, &taskWaiting)

	err = dispatcher.SendTaskInput(ctx, "task-waiting-01", "Option A")
	if err != nil {
		t.Errorf("unexpected error on SendTaskInput for WAITING_FOR_INPUT task: %v", err)
	}
}

func TestTaskExecutorWorkspaceIsolationAndFailClosedPolicy(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "subagent_workspace.db"))
	require.NoError(t, err)
	defer store.Close()

	workspace := t.TempDir()
	require.NoError(t, store.SaveAgent(ctx, &domain.Agent{
		Name:           "researcher",
		Status:         domain.StatusInitialized,
		WorkspacePath:  workspace,
		SecurityPreset: domain.PresetStrict,
	}))

	executor := newTaskExecutor("must-not-run", time.Second)
	executor.storage = store
	task := domain.SubagentTask{
		ID:               "task-scratch-isolation",
		ParentSessionKey: "telegram:123",
		AgentName:        "researcher",
		WorkspaceMode:    "scratch",
		Prompt:           "test",
	}

	scratch, _, cleanup, err := executor.resolveWorkspace(ctx, task)
	require.NoError(t, err)
	assert.NotEqual(t, workspace, scratch)
	info, err := os.Stat(scratch)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
	cleanup()
	_, err = os.Stat(scratch)
	assert.True(t, os.IsNotExist(err))

	tCtx := &taskRuntimeContext{task: task, startedAt: time.Now()}
	res, err := executor.executeTurn(ctx, tCtx, task.Prompt, "", nil)
	require.Error(t, err)
	require.NotNil(t, res)
	assert.Contains(t, err.Error(), "policy engine is not initialized")
}
