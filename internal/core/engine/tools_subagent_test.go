package engine

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agyent/internal/adapters/storage/sqlite"
	"agyent/internal/adapters/subagent"
	"agyent/internal/config"
	"agyent/internal/core/domain"
)

func TestSubagentTools_HandleSubagentToolCall(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_engine_subagent.db")

	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite store: %v", err)
	}
	defer store.Close()

	sessionKey := "telegram:123456"
	_, _ = store.GetOrCreateSession(ctx, sessionKey, "agyent")

	cfg := config.SubagentConfig{
		MaxConcurrentWorkers:  2,
		DefaultTimeoutSeconds: 5,
		DefaultModel:          "flash",
		DefaultEffort:         "low",
	}

	dispatcher := subagent.NewDispatcher(cfg, "non_existent_binary", store, nil)
	if err := dispatcher.Start(ctx); err != nil {
		t.Fatalf("failed to start dispatcher: %v", err)
	}
	defer func() { _ = dispatcher.Stop(ctx) }()

	// 1. Test SubagentToolDefinitions schema
	defs := SubagentToolDefinitions()
	if len(defs) != 5 {
		t.Fatalf("expected 5 tool definitions, got %d", len(defs))
	}

	// 2. Test dispatch_subagent tool call
	dispatchParams := map[string]any{
		"title":          "Refactor storage layer",
		"prompt":         "Analyze and refactor internal/adapters/storage",
		"agent_name":     "coder",
		"model":          "flash",
		"effort":         "low",
		"workspace_mode": "share",
		"callback_mode":  "notify_user",
	}

	resStr, err := HandleSubagentToolCall(ctx, dispatcher, sessionKey, "conv-001", "dispatch_subagent", dispatchParams)
	if err != nil {
		t.Fatalf("failed to handle dispatch_subagent: %v", err)
	}
	if !strings.Contains(resStr, "task-") || !strings.Contains(resStr, "PENDING") {
		t.Errorf("unexpected response from dispatch_subagent: %s", resStr)
	}

	// 3. Test list_subagents tool call
	listParams := map[string]any{
		"limit": float64(10),
	}
	resStr, err = HandleSubagentToolCall(ctx, dispatcher, sessionKey, "conv-001", "list_subagents", listParams)
	if err != nil {
		t.Fatalf("failed to handle list_subagents: %v", err)
	}
	if !strings.Contains(resStr, "Refactor storage layer") {
		t.Errorf("expected list_subagents to contain task title, got: %s", resStr)
	}

	// 4. Test check_subagent_progress on existing task
	activeTasks, err := dispatcher.ListActiveTasks(ctx, sessionKey)
	if err != nil || len(activeTasks) == 0 {
		t.Fatalf("expected at least 1 active task, got %d (err: %v)", len(activeTasks), err)
	}
	taskID := activeTasks[0].ID

	checkParams := map[string]any{
		"task_id": taskID,
	}
	resStr, err = HandleSubagentToolCall(ctx, dispatcher, sessionKey, "conv-001", "check_subagent_progress", checkParams)
	if err != nil {
		t.Fatalf("failed to check progress: %v", err)
	}
	if !strings.Contains(resStr, taskID) {
		t.Errorf("expected progress output to contain taskID %s, got: %s", taskID, resStr)
	}

	// 5. Test cancel_subagent_task tool call
	cancelParams := map[string]any{
		"task_id": taskID,
	}
	resStr, err = HandleSubagentToolCall(ctx, dispatcher, sessionKey, "conv-001", "cancel_subagent_task", cancelParams)
	if err != nil {
		t.Fatalf("failed to cancel task: %v", err)
	}
	if !strings.Contains(resStr, "CANCELLED") {
		t.Errorf("expected cancellation confirmation, got: %s", resStr)
	}
}

func TestSubagentCommands_Lifecycle(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_engine_cmds.db")

	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite store: %v", err)
	}
	defer store.Close()

	sessionKey := "telegram:998877"
	session, _ := store.GetOrCreateSession(ctx, sessionKey, "agyent")

	cfg := config.DefaultConfig()
	cfg.Storage.DBPath = dbPath

	dispatcher := subagent.NewDispatcher(cfg.Subagent, "mock_binary", store, nil)
	_ = dispatcher.Start(ctx)
	defer func() { _ = dispatcher.Stop(ctx) }()

	eng := NewEngine(cfg, store, nil, nil, nil, nil, nil, nil, nil, nil)
	eng.SetSubagentDispatcher(dispatcher)

	// 1. /tasks when empty
	text, kb := eng.handleTasksCommand(ctx, session, nil)
	if !strings.Contains(text, "No background sub-agent tasks found") {
		t.Errorf("expected empty tasks notice, got: %s", text)
	}
	if len(kb) != 0 {
		t.Errorf("expected empty keyboard, got %d rows", len(kb))
	}

	// 2. Dispatch a task manually to database
	task := domain.SubagentTask{
		ID:                   "task-cmd-01",
		ParentSessionKey:     sessionKey,
		ParentConversationID: "conv-01",
		AgentName:            "researcher",
		Title:                "Web research on Go 1.25",
		Prompt:               "Scrape docs",
		Status:               domain.TaskStatusWaitingInput,
		PendingQuestion:      "Which domain to focus on?",
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
	}
	_ = store.SaveSubagentTask(ctx, &task)

	// 3. /tasks when populated
	text, kb = eng.handleTasksCommand(ctx, session, nil)
	if !strings.Contains(text, "Web research on Go 1.25") || !strings.Contains(text, "WAITING_FOR_INPUT") {
		t.Errorf("expected tasks list to contain task details, got: %s", text)
	}
	if len(kb) == 0 {
		t.Errorf("expected inline keyboard with action buttons, got empty")
	}

	// 4. /task task-cmd-01 (inspect)
	text, _ = eng.handleTaskSubcommand(ctx, session, []string{"task-cmd-01"})
	if !strings.Contains(text, "Sub-Agent Task Details") || !strings.Contains(text, "Which domain to focus on?") {
		t.Errorf("expected detailed task view, got: %s", text)
	}

	// 5. /task reply task-cmd-01 <answer>
	text, _ = eng.handleTaskSubcommand(ctx, session, []string{"reply", "task-cmd-01", "Focus on memory model"})
	if !strings.Contains(text, "Reply injected into Task") {
		t.Errorf("expected reply confirmation, got: %s", text)
	}

	// 6. /task cancel task-cmd-01
	text, _ = eng.handleTaskSubcommand(ctx, session, []string{"cancel", "task-cmd-01"})
	if !strings.Contains(text, "cancelled") {
		t.Errorf("expected cancel confirmation, got: %s", text)
	}

	// 7. /task clean
	text, _ = eng.handleTaskSubcommand(ctx, session, []string{"clean"})
	if !strings.Contains(text, "Cleaned up") {
		t.Errorf("expected cleanup summary, got: %s", text)
	}
}
