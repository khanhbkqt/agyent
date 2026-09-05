package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"agyent/internal/core/domain"
)

func TestSQLiteStore_SubagentRepository(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_subagent.db")

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer store.Close()

	// 1. Prepare agent and session
	agent := &domain.Agent{
		Name:          "dev_agent",
		Description:   "Developer Agent",
		Status:        domain.StatusInitialized,
		WorkspacePath: tempDir,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	if err := store.SaveAgent(ctx, agent); err != nil {
		t.Fatalf("failed to save agent: %v", err)
	}

	sessionKey := "telegram:998877"
	_, err = store.GetOrCreateSession(ctx, sessionKey, agent.Name)
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	// 2. Test SaveSubagentTask (Insert)
	task1 := &domain.SubagentTask{
		ID:                   "task-sub-001",
		ParentSessionKey:     sessionKey,
		ParentConversationID: "conv-parent-01",
		AgentName:            "coder",
		Title:                "Refactor SQLite Adapter",
		Prompt:               "Refactor DB repository to pure-go WAL",
		Model:                "flash",
		Effort:               "low",
		WorkspaceMode:        "share",
		CallbackMode:         domain.CallbackInvokeMain,
		Status:               domain.TaskStatusPending,
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
	}

	if err := store.SaveSubagentTask(ctx, task1); err != nil {
		t.Fatalf("failed to save subagent task 1: %v", err)
	}

	// 3. Test GetSubagentTask
	retrieved, err := store.GetSubagentTask(ctx, "task-sub-001")
	if err != nil {
		t.Fatalf("failed to retrieve task 1: %v", err)
	}
	if retrieved.ID != "task-sub-001" {
		t.Errorf("expected ID task-sub-001, got %s", retrieved.ID)
	}
	if retrieved.Title != "Refactor SQLite Adapter" {
		t.Errorf("expected title 'Refactor SQLite Adapter', got %s", retrieved.Title)
	}
	if retrieved.Status != domain.TaskStatusPending {
		t.Errorf("expected status PENDING, got %s", retrieved.Status)
	}
	if retrieved.CallbackMode != domain.CallbackInvokeMain {
		t.Errorf("expected callback_mode callback_main, got %s", retrieved.CallbackMode)
	}

	// 4. Test UpdateSubagentTaskProgress
	if err := store.UpdateSubagentTaskProgress(ctx, "task-sub-001", 2, "find_by_name", "Scanning directories..."); err != nil {
		t.Fatalf("failed to update progress: %v", err)
	}
	retrieved, _ = store.GetSubagentTask(ctx, "task-sub-001")
	if retrieved.Status != domain.TaskStatusRunning {
		t.Errorf("expected status RUNNING, got %s", retrieved.Status)
	}
	if retrieved.CurrentStep != 2 {
		t.Errorf("expected step 2, got %d", retrieved.CurrentStep)
	}
	if retrieved.CurrentTool != "find_by_name" {
		t.Errorf("expected tool find_by_name, got %s", retrieved.CurrentTool)
	}

	// 5. Test UpdateSubagentTaskWaitingInput
	if err := store.UpdateSubagentTaskWaitingInput(ctx, "task-sub-001", "Which schema to use?", "conv-sub-brain-01"); err != nil {
		t.Fatalf("failed to update waiting input: %v", err)
	}
	retrieved, _ = store.GetSubagentTask(ctx, "task-sub-001")
	if retrieved.Status != domain.TaskStatusWaitingInput {
		t.Errorf("expected status WAITING_FOR_INPUT, got %s", retrieved.Status)
	}
	if retrieved.PendingQuestion != "Which schema to use?" {
		t.Errorf("expected pending question 'Which schema to use?', got %s", retrieved.PendingQuestion)
	}
	if retrieved.SubConversationID != "conv-sub-brain-01" {
		t.Errorf("expected sub_conversation_id 'conv-sub-brain-01', got %s", retrieved.SubConversationID)
	}

	// 6. Test UpdateSubagentTaskCompleted
	usage := domain.TokenUsage{
		InputTokens:  1500,
		OutputTokens: 250,
		TotalTokens:  1750,
	}
	atts := []domain.Attachment{
		{FileName: "result.go", FilePath: "/tmp/result.go"},
	}
	if err := store.UpdateSubagentTaskCompleted(ctx, "task-sub-001", "Refactoring complete!", atts, usage, 14.5); err != nil {
		t.Fatalf("failed to update completed: %v", err)
	}
	retrieved, _ = store.GetSubagentTask(ctx, "task-sub-001")
	if retrieved.Status != domain.TaskStatusCompleted {
		t.Errorf("expected status COMPLETED, got %s", retrieved.Status)
	}
	if retrieved.ResultSummary != "Refactoring complete!" {
		t.Errorf("expected result summary 'Refactoring complete!', got %s", retrieved.ResultSummary)
	}
	if retrieved.Usage.TotalTokens != 1750 {
		t.Errorf("expected total tokens 1750, got %d", retrieved.Usage.TotalTokens)
	}
	if len(retrieved.Artifacts) != 1 || retrieved.Artifacts[0].FileName != "result.go" {
		t.Errorf("expected 1 artifact named result.go, got %+v", retrieved.Artifacts)
	}

	// 7. Test ListActiveSubagentTasks and ListSubagentTasks
	task2 := &domain.SubagentTask{
		ID:                   "task-sub-002",
		ParentSessionKey:     sessionKey,
		ParentConversationID: "conv-parent-01",
		AgentName:            "researcher",
		Title:                "Crawl Documentation",
		Prompt:               "Scrape docs",
		Model:                "flash_lite",
		Effort:               "low",
		WorkspaceMode:        "scratch",
		CallbackMode:         domain.CallbackNotifyUser,
		Status:               domain.TaskStatusRunning,
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
	}
	if err := store.SaveSubagentTask(ctx, task2); err != nil {
		t.Fatalf("failed to save subagent task 2: %v", err)
	}

	activeTasks, err := store.ListActiveSubagentTasks(ctx, sessionKey)
	if err != nil {
		t.Fatalf("failed to list active tasks: %v", err)
	}
	if len(activeTasks) != 1 || activeTasks[0].ID != "task-sub-002" {
		t.Errorf("expected 1 active task (task-sub-002), got %d tasks", len(activeTasks))
	}

	allTasks, total, err := store.ListSubagentTasks(ctx, sessionKey, 10, 0)
	if err != nil {
		t.Fatalf("failed to list all tasks: %v", err)
	}
	if total != 2 || len(allTasks) != 2 {
		t.Errorf("expected total 2 tasks, got total %d, returned %d", total, len(allTasks))
	}

	// 8. Test PurgeSubagentTasks
	purged, err := store.PurgeSubagentTasks(ctx, 0)
	if err != nil {
		t.Fatalf("failed to purge subagent tasks: %v", err)
	}
	if purged != 1 { // Only task-sub-001 (COMPLETED) should be purged; task-sub-002 (RUNNING) is preserved
		t.Errorf("expected 1 purged task, got %d", purged)
	}
}

func TestSQLiteStore_SubagentStateMachineAndRace(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test_sm_race.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer store.Close()

	agent := &domain.Agent{Name: "worker_agent"}
	_ = store.SaveAgent(ctx, agent)
	sessionKey := "telegram:sm_test"
	_, _ = store.GetOrCreateSession(ctx, sessionKey, agent.Name)

	t.Run("Valid CAS State Machine Transitions", func(t *testing.T) {
		taskID := "task-sm-01"
		task := &domain.SubagentTask{
			ID:               taskID,
			ParentSessionKey: sessionKey,
			AgentName:        agent.Name,
			Status:           domain.TaskStatusRunning,
		}
		if err := store.SaveSubagentTask(ctx, task); err != nil {
			t.Fatalf("failed to save task: %v", err)
		}

		// Transition to CANCELLING
		ok, err := store.TransitionTaskToCancelling(ctx, taskID)
		if err != nil || !ok {
			t.Fatalf("expected transition to CANCELLING, got ok=%v, err=%v", ok, err)
		}

		// Check status is CANCELLING
		retrieved, _ := store.GetSubagentTask(ctx, taskID)
		if retrieved.Status != domain.TaskStatusCancelling {
			t.Fatalf("expected CANCELLING, got %s", retrieved.Status)
		}

		// Late worker completion attempt must NOT overwrite CANCELLING
		_ = store.UpdateSubagentTaskCompleted(ctx, taskID, "Late result", nil, domain.TokenUsage{TotalTokens: 100}, 2.5)
		retrieved, _ = store.GetSubagentTask(ctx, taskID)
		if retrieved.Status != domain.TaskStatusCancelling {
			t.Fatalf("late worker completion overwrote CANCELLING: %s", retrieved.Status)
		}

		// Transition to CANCELLED
		if err := store.TransitionTaskToCancelled(ctx, taskID); err != nil {
			t.Fatalf("failed to transition to CANCELLED: %v", err)
		}
		retrieved, _ = store.GetSubagentTask(ctx, taskID)
		if retrieved.Status != domain.TaskStatusCancelled {
			t.Fatalf("expected CANCELLED, got %s", retrieved.Status)
		}

		// Late worker completion attempt must NOT overwrite CANCELLED
		_ = store.UpdateSubagentTaskCompleted(ctx, taskID, "Late result 2", nil, domain.TokenUsage{TotalTokens: 100}, 2.5)
		retrieved, _ = store.GetSubagentTask(ctx, taskID)
		if retrieved.Status != domain.TaskStatusCancelled {
			t.Fatalf("late worker completion overwrote CANCELLED: %s", retrieved.Status)
		}
	})

	t.Run("Startup Reconciler Cleans Up Orphaned Cancelling Tasks", func(t *testing.T) {
		orphanID := "task-orphan-cancelling"
		orphanTask := &domain.SubagentTask{
			ID:               orphanID,
			ParentSessionKey: sessionKey,
			AgentName:        agent.Name,
			Status:           domain.TaskStatusCancelling,
		}
		_ = store.SaveSubagentTask(ctx, orphanTask)

		reconciled, err := store.ReconcileStaleCancellingTasks(ctx)
		if err != nil {
			t.Fatalf("reconciliation failed: %v", err)
		}
		if reconciled < 1 {
			t.Fatalf("expected at least 1 reconciled task, got %d", reconciled)
		}

		retrieved, _ := store.GetSubagentTask(ctx, orphanID)
		if retrieved.Status != domain.TaskStatusCancelled {
			t.Fatalf("expected reconciled status CANCELLED, got %s", retrieved.Status)
		}
	})

	t.Run("High Concurrency Race Test (Cancel vs Complete)", func(t *testing.T) {
		const rounds = 200 // 200 independent racing tasks
		for i := 0; i < rounds; i++ {
			raceID := filepath.Join(t.Name(), string(rune(i)))
			raceID = "task-race-" + string(rune('a'+(i%26))) + "-" + string(rune('0'+(i%10))) + "-" + string(rune('A'+(i%26)))
			raceTask := &domain.SubagentTask{
				ID:               raceID,
				ParentSessionKey: sessionKey,
				AgentName:        agent.Name,
				Status:           domain.TaskStatusRunning,
			}
			_ = store.SaveSubagentTask(ctx, raceTask)

			start := make(chan struct{})
			done := make(chan struct{}, 2)

			// Worker 1: Attempts Cancel
			go func(tid string) {
				<-start
				ok, _ := store.TransitionTaskToCancelling(ctx, tid)
				if ok {
					_ = store.TransitionTaskToCancelled(ctx, tid)
				}
				done <- struct{}{}
			}(raceID)

			// Worker 2: Attempts Complete
			go func(tid string) {
				<-start
				_ = store.UpdateSubagentTaskCompleted(ctx, tid, "Finished successfully", nil, domain.TokenUsage{TotalTokens: 50}, 1.0)
				done <- struct{}{}
			}(raceID)

			close(start)
			<-done
			<-done

			// Inspect final state: Must be strictly CANCELLED or COMPLETED, never corrupted or in an invalid state
			task, err := store.GetSubagentTask(ctx, raceID)
			if err != nil {
				t.Fatalf("round %d: failed to get task %s: %v", i, raceID, err)
			}
			if task.Status != domain.TaskStatusCancelled && task.Status != domain.TaskStatusCompleted {
				t.Fatalf("round %d: illegal task status reached: %s", i, task.Status)
			}
		}
	})
}

