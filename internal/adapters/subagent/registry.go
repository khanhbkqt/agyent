package subagent

import (
	"context"
	"sync"
	"time"

	"agyent/internal/core/domain"
)

// taskRuntimeContext represents an in-flight background task.
type taskRuntimeContext struct {
	mu              sync.RWMutex
	task            domain.SubagentTask
	cancel          context.CancelFunc
	startedAt       time.Time
	conversationID  string
	pendingQuestion string
}

func (c *taskRuntimeContext) snapshot() domain.SubagentTask {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.task
}

func (c *taskRuntimeContext) updateProgress(step int, tool, progressMsg string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.task.CurrentStep = step
	c.task.CurrentTool = tool
	c.task.ProgressMessage = progressMsg
	c.task.Status = domain.TaskStatusRunning
	c.task.UpdatedAt = time.Now()
}

func (c *taskRuntimeContext) setWaitingInput(question, convID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.task.Status = domain.TaskStatusWaitingInput
	c.task.PendingQuestion = question
	c.pendingQuestion = question
	if convID != "" {
		c.task.SubConversationID = convID
		c.conversationID = convID
	}
	c.task.UpdatedAt = time.Now()
}

func (c *taskRuntimeContext) setCompleted(resultSummary string, artifacts []domain.Attachment, usage domain.TokenUsage, durationSec float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.task.Status = domain.TaskStatusCompleted
	c.task.ResultSummary = resultSummary
	c.task.Artifacts = artifacts
	c.task.Usage = usage
	c.task.DurationSeconds = durationSec
	c.task.PendingQuestion = ""
	c.task.UpdatedAt = time.Now()
}

func (c *taskRuntimeContext) setFailed(errMsg string, durationSec float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.task.Status = domain.TaskStatusFailed
	c.task.ErrorMessage = errMsg
	c.task.DurationSeconds = durationSec
	c.task.UpdatedAt = time.Now()
}

func (c *taskRuntimeContext) setCancelled() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.task.Status = domain.TaskStatusCancelled
	c.task.UpdatedAt = time.Now()
}

// taskRegistry provides thread-safe in-memory caching of active background tasks.
type taskRegistry struct {
	tasks sync.Map // taskID (string) -> *taskRuntimeContext
}

func newTaskRegistry() *taskRegistry {
	return &taskRegistry{}
}

func (r *taskRegistry) Register(tCtx *taskRuntimeContext) {
	if tCtx != nil && tCtx.task.ID != "" {
		r.tasks.Store(tCtx.task.ID, tCtx)
	}
}

// TryRegister atomically reserves a task ID for one producer. Polling and
// direct dispatch run concurrently, so a Load followed by Store is not enough
// to prevent duplicate queueing of the same persisted task.
func (r *taskRegistry) TryRegister(tCtx *taskRuntimeContext) bool {
	if tCtx == nil || tCtx.task.ID == "" {
		return false
	}
	_, loaded := r.tasks.LoadOrStore(tCtx.task.ID, tCtx)
	return !loaded
}

func (r *taskRegistry) Get(taskID string) (*taskRuntimeContext, bool) {
	val, ok := r.tasks.Load(taskID)
	if !ok {
		return nil, false
	}
	return val.(*taskRuntimeContext), true
}

func (r *taskRegistry) Has(taskID string) bool {
	_, ok := r.tasks.Load(taskID)
	return ok
}

func (r *taskRegistry) Delete(taskID string) {
	r.tasks.Delete(taskID)
}

func (r *taskRegistry) ListActive(parentSessionKey string) []domain.SubagentTask {
	var list []domain.SubagentTask
	r.tasks.Range(func(key, value any) bool {
		tCtx := value.(*taskRuntimeContext)
		task := tCtx.snapshot()
		if parentSessionKey == "" || task.ParentSessionKey == parentSessionKey {
			if task.IsActive() {
				list = append(list, task)
			}
		}
		return true
	})
	return list
}
