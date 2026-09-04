package subagent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"agyent/internal/adapters/harness/agy"
	"agyent/internal/config"
	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

// SubagentDispatcher coordinates the background subagent worker pool, task queue, and lifecycle.
type SubagentDispatcher struct {
	storage    ports.SubagentRepository
	eventBus   ports.EventBusPort
	config     config.SubagentConfig
	binaryPath string
	registry   *taskRegistry
	executor   *taskExecutor
	taskQueue  chan domain.SubagentTask
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	started    bool
	mu         sync.Mutex
}

// NewDispatcher constructs a new SubagentDispatcher instance.
func NewDispatcher(
	cfg config.SubagentConfig,
	binaryPath string,
	storage ports.SubagentRepository,
	eventBus ports.EventBusPort,
) *SubagentDispatcher {
	if cfg.MaxConcurrentWorkers <= 0 {
		cfg.MaxConcurrentWorkers = 3
	}
	if cfg.DefaultTimeoutSeconds <= 0 {
		cfg.DefaultTimeoutSeconds = 1800
	}

	queueCap := cfg.MaxConcurrentWorkers * 10
	if queueCap < 50 {
		queueCap = 50
	}

	return &SubagentDispatcher{
		storage:    storage,
		eventBus:   eventBus,
		config:     cfg,
		binaryPath: binaryPath,
		registry:   newTaskRegistry(),
		executor:   newTaskExecutor(binaryPath, time.Duration(cfg.DefaultTimeoutSeconds)*time.Second),
		taskQueue:  make(chan domain.SubagentTask, queueCap),
	}
}

// Start launches the background worker pool consumers.
func (d *SubagentDispatcher) Start(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.started {
		return nil
	}

	d.ctx, d.cancel = context.WithCancel(ctx)
	d.started = true

	for i := 0; i < d.config.MaxConcurrentWorkers; i++ {
		d.wg.Add(1)
		go d.workerLoop(i + 1)
	}

	d.wg.Add(1)
	go d.pollerLoop()

	slog.Info("subagent dispatcher started",
		"workers", d.config.MaxConcurrentWorkers,
		"timeout_seconds", d.config.DefaultTimeoutSeconds,
	)

	return nil
}

func (d *SubagentDispatcher) pollerLoop() {
	defer d.wg.Done()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			d.enqueuePendingTasks()
		}
	}
}

func (d *SubagentDispatcher) enqueuePendingTasks() {
	if d.storage == nil {
		return
	}
	pendingTasks, err := d.storage.ListPendingSubagentTasks(d.ctx, 10)
	if err != nil || len(pendingTasks) == 0 {
		return
	}

	for _, task := range pendingTasks {
		if !d.registry.Has(task.ID) {
			tCtx := &taskRuntimeContext{
				task:      task,
				startedAt: time.Now(),
			}
			d.registry.Register(tCtx)

			if d.eventBus != nil {
				d.eventBus.AsyncEmit(d.ctx, domain.NewEvent(domain.EventSubagentDispatched, domain.SubagentEventPayload{Task: task}))
			}

			select {
			case d.taskQueue <- task:
			default:
				// Queue is full, will retry next tick
			}
		}
	}
}

// Stop gracefully shuts down the worker pool and cancels running tasks.
func (d *SubagentDispatcher) Stop(ctx context.Context) error {
	d.mu.Lock()
	if !d.started {
		d.mu.Unlock()
		return nil
	}
	d.started = false
	d.cancel()
	close(d.taskQueue)
	d.mu.Unlock()

	// Wait for workers to drain or context timeout
	doneChan := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(doneChan)
	}()

	select {
	case <-doneChan:
		slog.Info("subagent dispatcher stopped cleanly")
	case <-ctx.Done():
		slog.Warn("subagent dispatcher stop timed out, cancelling remaining tasks")
	}

	return nil
}

// DispatchTask enqueues a new background task and returns the assigned TaskID immediately (< 1ms).
func (d *SubagentDispatcher) DispatchTask(ctx context.Context, task domain.SubagentTask) (string, error) {
	d.mu.Lock()
	if !d.started {
		d.mu.Unlock()
		return "", errors.New("subagent dispatcher is not running or has been stopped")
	}
	d.mu.Unlock()

	if task.ID == "" {
		task.ID = generateTaskID()
	}
	if task.ParentSessionKey == "" {
		return "", errors.New("parent_session_key is required")
	}
	if task.Prompt == "" {
		return "", errors.New("prompt is required")
	}
	if task.Title == "" {
		task.Title = task.Prompt
		if len(task.Title) > 40 {
			task.Title = task.Title[:37] + "..."
		}
	}
	if task.AgentName == "" {
		task.AgentName = "agyent"
	}
	if task.Model == "" {
		task.Model = d.config.DefaultModel
		if task.Model == "" {
			task.Model = "flash"
		}
	}
	if task.Effort == "" {
		task.Effort = d.config.DefaultEffort
		if task.Effort == "" {
			task.Effort = "low"
		}
	}
	if task.WorkspaceMode == "" {
		task.WorkspaceMode = "share"
	}
	if task.CallbackMode == "" {
		task.CallbackMode = domain.CallbackNotifyUser
	}
	task.Status = domain.TaskStatusPending
	task.CreatedAt = time.Now()
	task.UpdatedAt = time.Now()

	// 1. Save to SQLite for persistence
	if err := d.storage.SaveSubagentTask(ctx, &task); err != nil {
		return "", fmt.Errorf("failed to persist subagent task: %w", err)
	}

	// 2. Pre-register in task registry to prevent pollerLoop double-enqueue race
	tCtx := &taskRuntimeContext{
		task:      task,
		startedAt: time.Now(),
	}
	d.registry.Register(tCtx)

	// 3. Emit Dispatched Event
	if d.eventBus != nil {
		d.eventBus.AsyncEmit(ctx, domain.NewEvent(domain.EventSubagentDispatched, domain.SubagentEventPayload{Task: task}))
	}

	// 4. Enqueue to Worker Pool
	select {
	case d.taskQueue <- task:
		slog.Info("subagent task enqueued",
			"task_id", task.ID,
			"session_key", task.ParentSessionKey,
			"agent", task.AgentName,
		)
	default:
		slog.Warn("subagent task queue full, spawning immediate worker goroutine", "task_id", task.ID)
		go d.runTask(task)
	}

	return task.ID, nil
}

// GetTask queries current task state and live progress metadata.
func (d *SubagentDispatcher) GetTask(ctx context.Context, taskID string) (*domain.SubagentTask, error) {
	if tCtx, ok := d.registry.Get(taskID); ok {
		snap := tCtx.snapshot()
		return &snap, nil
	}
	return d.storage.GetSubagentTask(ctx, taskID)
}

// ListActiveTasks returns all in-flight tasks for a given session.
func (d *SubagentDispatcher) ListActiveTasks(ctx context.Context, sessionKey string) ([]domain.SubagentTask, error) {
	active := d.registry.ListActive(sessionKey)
	if len(active) > 0 {
		return active, nil
	}
	return d.storage.ListActiveSubagentTasks(ctx, sessionKey)
}

// ListTasks returns paginated tasks for a given session.
func (d *SubagentDispatcher) ListTasks(ctx context.Context, sessionKey string, limit, offset int) ([]domain.SubagentTask, int, error) {
	return d.storage.ListSubagentTasks(ctx, sessionKey, limit, offset)
}

// SendTaskInput resumes a sub-agent waiting for clarification (WAITING_FOR_INPUT).
func (d *SubagentDispatcher) SendTaskInput(ctx context.Context, taskID string, input string) error {
	task, err := d.GetTask(ctx, taskID)
	if err != nil {
		return err
	}

	if task.Status != domain.TaskStatusWaitingInput {
		return fmt.Errorf("task %s is in state %s, not WAITING_FOR_INPUT", taskID, task.Status)
	}

	// Launch continuation turn in background
	go d.resumeTask(*task, input)
	return nil
}

// CancelTask forcefully halts a running task and tears down the associated process tree.
func (d *SubagentDispatcher) CancelTask(ctx context.Context, taskID string) error {
	if tCtx, ok := d.registry.Get(taskID); ok {
		tCtx.mu.Lock()
		if tCtx.cancel != nil {
			tCtx.cancel()
		}
		tCtx.mu.Unlock()
		tCtx.setCancelled()
	}

	if err := d.storage.UpdateSubagentTaskCancelled(ctx, taskID); err != nil {
		return err
	}

	if d.eventBus != nil {
		if task, err := d.storage.GetSubagentTask(ctx, taskID); err == nil {
			d.eventBus.AsyncEmit(ctx, domain.NewEvent(domain.EventSubagentCancelled, domain.SubagentEventPayload{Task: *task}))
		}
	}

	slog.Info("subagent task cancelled", "task_id", taskID)
	return nil
}

func (d *SubagentDispatcher) workerLoop(workerID int) {
	defer d.wg.Done()

	for {
		select {
		case <-d.ctx.Done():
			return
		case task, ok := <-d.taskQueue:
			if !ok {
				return
			}
			d.runTask(task)
		}
	}
}

func (d *SubagentDispatcher) runTask(task domain.SubagentTask) {
	tCtx, ok := d.registry.Get(task.ID)
	if !ok {
		tCtx = &taskRuntimeContext{
			task:      task,
			startedAt: time.Now(),
		}
		d.registry.Register(tCtx)
	}
	defer d.registry.Delete(task.ID)

	// Update status to RUNNING
	_ = d.storage.UpdateSubagentTaskProgress(d.ctx, task.ID, 0, "", "Starting subagent worker...")

	onEvent := func(evt agy.StreamEvent) {
		if evt.Event == "step_update" && evt.StepUpdate != nil {
			step := evt.StepUpdate
			name := step.ToolName
			if name == "" && step.ToolInfo != nil {
				name = step.ToolInfo.Name
			}
			if step.StepType == "tool" && step.State == "ACTIVE" {
				_ = d.storage.UpdateSubagentTaskProgress(d.ctx, task.ID, step.StepIndex, name, fmt.Sprintf("Executing tool %s...", name))
				if d.eventBus != nil {
					d.eventBus.AsyncEmit(d.ctx, domain.NewEvent(domain.EventSubagentProgress, domain.SubagentEventPayload{Task: tCtx.snapshot()}))
				}
			}
		}
	}

	res, err := d.executor.executeTurn(d.ctx, tCtx, task.Prompt, task.SubConversationID, onEvent)
	d.handleTurnResult(tCtx, res, err)
}

func (d *SubagentDispatcher) resumeTask(task domain.SubagentTask, input string) {
	tCtx := &taskRuntimeContext{
		task:           task,
		startedAt:      time.Now(),
		conversationID: task.SubConversationID,
	}
	d.registry.Register(tCtx)
	defer d.registry.Delete(task.ID)

	_ = d.storage.UpdateSubagentTaskProgress(d.ctx, task.ID, task.CurrentStep+1, "", "Resuming with input...")

	onEvent := func(evt agy.StreamEvent) {
		if evt.Event == "step_update" && evt.StepUpdate != nil {
			step := evt.StepUpdate
			name := step.ToolName
			if name == "" && step.ToolInfo != nil {
				name = step.ToolInfo.Name
			}
			if step.StepType == "tool" && step.State == "ACTIVE" {
				_ = d.storage.UpdateSubagentTaskProgress(d.ctx, task.ID, step.StepIndex, name, fmt.Sprintf("Executing tool %s...", name))
				if d.eventBus != nil {
					d.eventBus.AsyncEmit(d.ctx, domain.NewEvent(domain.EventSubagentProgress, domain.SubagentEventPayload{Task: tCtx.snapshot()}))
				}
			}
		}
	}

	res, err := d.executor.executeTurn(d.ctx, tCtx, input, task.SubConversationID, onEvent)
	d.handleTurnResult(tCtx, res, err)
}

func (d *SubagentDispatcher) handleTurnResult(tCtx *taskRuntimeContext, res *TurnResult, err error) {
	taskID := tCtx.task.ID

	if res == nil {
		errMsg := "unknown execution error"
		if err != nil {
			errMsg = err.Error()
		}
		tCtx.setFailed(errMsg, time.Since(tCtx.startedAt).Seconds())
		_ = d.storage.UpdateSubagentTaskFailed(d.ctx, taskID, errMsg, time.Since(tCtx.startedAt).Seconds())
		if d.eventBus != nil {
			d.eventBus.AsyncEmit(d.ctx, domain.NewEvent(domain.EventSubagentFailed, domain.SubagentEventPayload{Task: tCtx.snapshot()}))
		}
		return
	}

	switch res.Status {
	case domain.TaskStatusWaitingInput:
		tCtx.setWaitingInput(res.Question, res.ConversationID)
		_ = d.storage.UpdateSubagentTaskWaitingInput(d.ctx, taskID, res.Question, res.ConversationID)
		if d.eventBus != nil {
			d.eventBus.AsyncEmit(d.ctx, domain.NewEvent(domain.EventSubagentWaitingInput, domain.SubagentEventPayload{Task: tCtx.snapshot()}))
		}
		slog.Info("subagent task waiting for input", "task_id", taskID, "question", res.Question)

	case domain.TaskStatusCompleted:
		tCtx.setCompleted(res.Response, res.Artifacts, res.Usage, res.DurationSeconds)
		snap := tCtx.snapshot()
		_ = d.storage.UpdateSubagentTaskCompleted(d.ctx, taskID, res.Response, snap.ArtifactsJSON(), res.Usage, res.DurationSeconds)
		if d.eventBus != nil {
			d.eventBus.AsyncEmit(d.ctx, domain.NewEvent(domain.EventSubagentCompleted, domain.SubagentEventPayload{Task: snap}))
		}
		slog.Info("subagent task completed",
			"task_id", taskID,
			"duration_seconds", res.DurationSeconds,
			"tokens", res.Usage.TotalTokens,
		)

	case domain.TaskStatusFailed:
		tCtx.setFailed(res.ErrorMessage, res.DurationSeconds)
		_ = d.storage.UpdateSubagentTaskFailed(d.ctx, taskID, res.ErrorMessage, res.DurationSeconds)
		if d.eventBus != nil {
			d.eventBus.AsyncEmit(d.ctx, domain.NewEvent(domain.EventSubagentFailed, domain.SubagentEventPayload{Task: tCtx.snapshot()}))
		}
		slog.Warn("subagent task failed", "task_id", taskID, "error", res.ErrorMessage)
	}
}

func generateTaskID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("task-%s", hex.EncodeToString(b))
}
