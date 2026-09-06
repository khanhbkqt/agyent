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
	storage      ports.SubagentRepository
	eventBus     ports.EventBusPort
	config       config.SubagentConfig
	binaryPath   string
	registry     *taskRegistry
	executor     *taskExecutor
	taskQueue    chan domain.SubagentTask
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	started      bool
	mu           sync.RWMutex
	policyEngine ports.PolicyEngine
	storagePort  ports.StoragePort
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

// SetSecurityManager injects the security manager port for subagent turn registration.
func (d *SubagentDispatcher) SetSecurityManager(sec ports.SecurityManagerPort) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.executor != nil {
		d.executor.securityManager = sec
	}
}

// SetPolicyEngine injects the policy engine port for subagent execution authorization.
func (d *SubagentDispatcher) SetPolicyEngine(p ports.PolicyEngine) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.policyEngine = p
	if d.executor != nil {
		d.executor.policy = p
	}
}

// SetStoragePort injects the full storage port for project and agent resolution.
func (d *SubagentDispatcher) SetStoragePort(s ports.StoragePort) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.storagePort = s
	if d.executor != nil {
		d.executor.storage = s
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

	if d.storage != nil {
		if recCount, err := d.storage.ReconcileStaleCancellingTasks(ctx); err == nil && recCount > 0 {
			slog.Info("reconciled stale subagent tasks on startup", "count", recCount)
		}
		if runCount, err := d.storage.ReconcileStaleRunningTasks(ctx); err == nil && runCount > 0 {
			slog.Info("reconciled stale running subagent tasks on startup", "count", runCount)
		}
	}

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
	d.mu.RLock()
	if !d.started || d.ctx == nil {
		d.mu.RUnlock()
		return
	}
	runCtx := d.ctx
	d.mu.RUnlock()

	if d.storage == nil {
		return
	}
	pendingTasks, err := d.storage.ListPendingSubagentTasks(runCtx, 10)
	if err != nil || len(pendingTasks) == 0 {
		return
	}

	for _, task := range pendingTasks {
		tCtx := &taskRuntimeContext{
			task:      task,
			startedAt: time.Now(),
		}
		if d.registry.TryRegister(tCtx) {

			if d.eventBus != nil {
				d.eventBus.AsyncEmit(runCtx, domain.NewEvent(domain.EventSubagentDispatched, domain.SubagentEventPayload{Task: task}))
			}

			select {
			case d.taskQueue <- task:
			default:
				// Queue is full, rollback registration so next tick can re-evaluate
				d.registry.Delete(task.ID)
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
	d.mu.RLock()
	defer d.mu.RUnlock()
	if !d.started {
		return "", errors.New("subagent dispatcher is not running or has been stopped")
	}

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
	if task.WorkspaceMode != "share" && task.WorkspaceMode != "scratch" && task.WorkspaceMode != "persona" {
		return "", fmt.Errorf("unsupported workspace_mode %q", task.WorkspaceMode)
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
	if !d.registry.TryRegister(tCtx) {
		return "", fmt.Errorf("task %s is already queued", task.ID)
	}

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
		// Queue full: rollback reservation, task remains PENDING in SQLite for poller pickup
		d.registry.Delete(task.ID)
		slog.Warn("subagent task queue full, task retained as PENDING in database for poller", "task_id", task.ID)
	}

	return task.ID, nil
}

// GetTask queries current task state and live progress metadata.
func (d *SubagentDispatcher) GetTask(ctx context.Context, taskID string) (*domain.SubagentTask, error) {
	return d.GetTaskScoped(ctx, "", taskID)
}

// GetTaskScoped queries task state ensuring it matches sessionKey.
func (d *SubagentDispatcher) GetTaskScoped(ctx context.Context, sessionKey, taskID string) (*domain.SubagentTask, error) {
	if tCtx, ok := d.registry.Get(taskID); ok {
		snap := tCtx.snapshot()
		if sessionKey != "" && snap.ParentSessionKey != sessionKey {
			return nil, fmt.Errorf("%w: task %s does not belong to session %s", ports.ErrNotFound, taskID, sessionKey)
		}
		return &snap, nil
	}
	if sessionKey != "" {
		return d.storage.GetSubagentTaskScoped(ctx, sessionKey, taskID)
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
	return d.SendTaskInputScoped(ctx, "", taskID, input)
}

// SendTaskInputScoped resumes a sub-agent waiting for clarification, scoped by sessionKey.
func (d *SubagentDispatcher) SendTaskInputScoped(ctx context.Context, sessionKey, taskID, input string) error {
	task, err := d.GetTaskScoped(ctx, sessionKey, taskID)
	if err != nil {
		return err
	}

	if task.Status != domain.TaskStatusWaitingInput {
		return fmt.Errorf("task %s is in state %s, not WAITING_FOR_INPUT", taskID, task.Status)
	}

	// 1. CAS transition in SQLite to PENDING
	transitioned, err := d.storage.TransitionTaskStatus(ctx, task.ID, domain.TaskStatusWaitingInput, domain.TaskStatusPending)
	if err != nil {
		return err
	}
	if !transitioned {
		return fmt.Errorf("task %s is not in WAITING_FOR_INPUT state", taskID)
	}

	// 2. Update task prompt with input
	task.Prompt = input
	task.Status = domain.TaskStatusPending
	task.UpdatedAt = time.Now()
	if err := d.storage.SaveSubagentTask(ctx, task); err != nil {
		return fmt.Errorf("failed to save resumed subagent task: %w", err)
	}

	d.mu.RLock()
	defer d.mu.RUnlock()
	if !d.started {
		return errors.New("subagent dispatcher is not running or has been stopped")
	}

	// 3. Pre-register in registry with continuation turn
	tCtx := &taskRuntimeContext{
		task:           *task,
		startedAt:      time.Now(),
		conversationID: task.SubConversationID,
	}
	if !d.registry.TryRegister(tCtx) {
		return fmt.Errorf("task %s is already queued", task.ID)
	}

	// 4. Enqueue into worker pool
	select {
	case d.taskQueue <- *task:
		slog.Info("subagent continuation task enqueued", "task_id", task.ID, "session_key", task.ParentSessionKey)
	default:
		d.registry.Delete(task.ID)
		slog.Warn("subagent task queue full on resume, task retained as PENDING for poller", "task_id", task.ID)
	}
	return nil
}

// CancelTask forcefully halts a running task and tears down the associated process tree using CAS state transition.
func (d *SubagentDispatcher) CancelTask(ctx context.Context, taskID string) error {
	return d.CancelTaskScoped(ctx, "", taskID)
}

// CancelTaskScoped halts a running task scoped by sessionKey.
func (d *SubagentDispatcher) CancelTaskScoped(ctx context.Context, sessionKey, taskID string) error {
	// 1. CAS transition in SQLite to CANCELLING
	var transitioned bool
	var err error
	if sessionKey != "" {
		transitioned, err = d.storage.TransitionTaskToCancellingScoped(ctx, sessionKey, taskID)
	} else {
		transitioned, err = d.storage.TransitionTaskToCancelling(ctx, taskID)
	}
	if err != nil {
		return err
	}
	if !transitioned {
		// Task is already terminal or cancelled; idempotent success
		return nil
	}

	// 2. Interrupt in-memory task context and kill running process tree
	if tCtx, ok := d.registry.Get(taskID); ok {
		tCtx.mu.Lock()
		if tCtx.cancel != nil {
			tCtx.cancel()
		}
		tCtx.mu.Unlock()
		tCtx.setCancelled()
	}

	// 3. Atomically transition in SQLite from CANCELLING to CANCELLED
	if err := d.storage.TransitionTaskToCancelled(ctx, taskID); err != nil {
		return err
	}

	if d.eventBus != nil {
		if task, err := d.storage.GetSubagentTask(ctx, taskID); err == nil {
			d.eventBus.AsyncEmit(ctx, domain.NewEvent(domain.EventSubagentCancelled, domain.SubagentEventPayload{Task: *task}))
		}
	}

	slog.Info("subagent task cancelled", "task_id", taskID, "session_key", sessionKey)
	return nil
}

func (d *SubagentDispatcher) workerLoop(workerID int) {
	defer d.wg.Done()

	for {
		select {
		case <-d.ctx.Done():
			return
		case task := <-d.taskQueue:
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

	// Transition status to RUNNING via CAS
	if d.storage != nil {
		transitioned, err := d.storage.TransitionTaskStatus(d.ctx, task.ID, domain.TaskStatusPending, domain.TaskStatusRunning)
		if err != nil || !transitioned {
			slog.Info("subagent task status transition to RUNNING skipped or cancelled", "task_id", task.ID, "err", err)
			return
		}
	}

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

func (d *SubagentDispatcher) handleTurnResult(tCtx *taskRuntimeContext, res *TurnResult, err error) {
	taskID := tCtx.task.ID
	// Cancellation wins every race with process completion. The SQL updates are
	// guarded too, but avoiding an in-memory/event transition prevents users
	// from receiving a false "completed" notification for a cancelled task.
	if tCtx.snapshot().Status == domain.TaskStatusCancelled {
		return
	}

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
		_ = d.storage.UpdateSubagentTaskCompleted(d.ctx, taskID, res.Response, snap.Artifacts, res.Usage, res.DurationSeconds)
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
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("task-%x", time.Now().UnixNano())
	}
	return fmt.Sprintf("task-%s", hex.EncodeToString(b))
}
