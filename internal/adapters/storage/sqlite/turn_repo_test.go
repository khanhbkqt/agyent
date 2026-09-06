package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"agyent/internal/core/domain"
)

func TestSQLiteStore_InFlightTurnRepository(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_turns.db")

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer store.Close()

	// 1. Save InFlightTurn
	turn1 := &domain.InFlightTurn{
		TurnID:           "turn-test-001",
		SessionKey:       "telegram:12345",
		ConversationID:   "conv-abc-123",
		AgentName:        "agyent",
		ProjectName:      "web-app",
		Channel:          "telegram",
		ChatID:           "12345",
		ThreadID:         "10",
		InboundMessageID: 1001,
		BotID:            999,
		UserID:           "user-42",
		UserName:         "alice",
		Prompt:           "Refactor tests",
		IsEphemeral:      false,
		Status:           domain.TurnStatusExecuting,
		RetryCount:       0,
		MaxRetries:       1,
		RecoveryMode:     "auto",
		CreatedAt:        time.Now().Add(-5 * time.Minute),
		UpdatedAt:        time.Now().Add(-5 * time.Minute),
	}

	if err := store.SaveInFlightTurn(ctx, turn1); err != nil {
		t.Fatalf("failed to save in-flight turn: %v", err)
	}

	// 2. Get InFlightTurn
	retrieved, err := store.GetInFlightTurn(ctx, "turn-test-001")
	if err != nil {
		t.Fatalf("failed to get in-flight turn: %v", err)
	}
	if retrieved.TurnID != "turn-test-001" || retrieved.InboundMessageID != 1001 || retrieved.Status != domain.TurnStatusExecuting {
		t.Errorf("unexpected retrieved turn: %+v", retrieved)
	}

	// 3. Update Status
	if err := store.UpdateInFlightTurnStatus(ctx, "turn-test-001", domain.TurnStatusRecovering, "recovering turn"); err != nil {
		t.Fatalf("failed to update turn status: %v", err)
	}
	retrieved2, _ := store.GetInFlightTurn(ctx, "turn-test-001")
	if retrieved2.Status != domain.TurnStatusRecovering || retrieved2.ErrorMessage != "recovering turn" {
		t.Errorf("status not updated: %+v", retrieved2)
	}

	// 4. Save terminal turn
	turn2 := &domain.InFlightTurn{
		TurnID:           "turn-test-002",
		SessionKey:       "telegram:12345",
		AgentName:        "agyent",
		Channel:          "telegram",
		ChatID:           "12345",
		UserID:           "user-42",
		Prompt:           "Done task",
		Status:           domain.TurnStatusCompleted,
		CreatedAt:        time.Now().Add(-10 * time.Minute),
		UpdatedAt:        time.Now().Add(-10 * time.Minute),
	}
	if err := store.SaveInFlightTurn(ctx, turn2); err != nil {
		t.Fatalf("failed to save turn2: %v", err)
	}

	// 5. List Interrupted Turns
	interrupted, err := store.ListInterruptedTurns(ctx)
	if err != nil {
		t.Fatalf("failed to list interrupted turns: %v", err)
	}
	if len(interrupted) != 1 || interrupted[0].TurnID != "turn-test-001" {
		t.Errorf("expected 1 interrupted turn (turn-test-001), got: %d", len(interrupted))
	}

	// 6. Purge
	purged, err := store.PurgeInFlightTurns(ctx, 0)
	if err != nil {
		t.Fatalf("failed to purge turns: %v", err)
	}
	if purged != 1 {
		t.Errorf("expected 1 purged turn (turn-test-002), got %d", purged)
	}
}

func TestSQLiteStore_SubagentReconcileStaleRunningTasks(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_subagent_reconcile.db")

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer store.Close()

	sessionKey := "telegram:554433"
	_, _ = store.GetOrCreateSession(ctx, sessionKey, "agyent")

	// Task 1: RUNNING with RetryCount=0, MaxRetries=1 -> should be re-queued to PENDING with RetryCount=1
	t1 := &domain.SubagentTask{
		ID:                   "task-reconcile-1",
		ParentSessionKey:     sessionKey,
		ParentConversationID: "conv-p-1",
		AgentName:            "coder",
		Title:                "Worker 1",
		Prompt:               "Do work",
		Status:               domain.TaskStatusRunning,
		RetryCount:           0,
		MaxRetries:           1,
	}
	if err := store.SaveSubagentTask(ctx, t1); err != nil {
		t.Fatalf("failed to save t1: %v", err)
	}

	// Task 2: RUNNING with RetryCount=1, MaxRetries=1 -> should be failed
	t2 := &domain.SubagentTask{
		ID:                   "task-reconcile-2",
		ParentSessionKey:     sessionKey,
		ParentConversationID: "conv-p-1",
		AgentName:            "coder",
		Title:                "Worker 2",
		Prompt:               "Do work 2",
		Status:               domain.TaskStatusRunning,
		RetryCount:           1,
		MaxRetries:           1,
	}
	if err := store.SaveSubagentTask(ctx, t2); err != nil {
		t.Fatalf("failed to save t2: %v", err)
	}

	// Reconcile
	requeued, err := store.ReconcileStaleRunningTasks(ctx)
	if err != nil {
		t.Fatalf("failed to reconcile stale running tasks: %v", err)
	}
	if requeued != 1 {
		t.Errorf("expected 1 requeued task, got %d", requeued)
	}

	// Verify t1 is now PENDING with RetryCount=1
	resT1, err := store.GetSubagentTask(ctx, "task-reconcile-1")
	if err != nil || resT1.Status != domain.TaskStatusPending || resT1.RetryCount != 1 {
		t.Errorf("t1 unexpected state: %+v, err: %v", resT1, err)
	}

	// Verify t2 is now FAILED
	resT2, err := store.GetSubagentTask(ctx, "task-reconcile-2")
	if err != nil || resT2.Status != domain.TaskStatusFailed {
		t.Errorf("t2 unexpected state: %+v, err: %v", resT2, err)
	}
}
