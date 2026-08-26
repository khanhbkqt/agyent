package ports

import (
	"context"

	"agyent/internal/core/domain"
)

// SubagentRepository defines persistence operations for asynchronous subagent tasks in SQLite.
type SubagentRepository interface {
	GetSubagentTask(ctx context.Context, id string) (*domain.SubagentTask, error)
	ListSubagentTasks(ctx context.Context, parentSessionKey string, limit, offset int) ([]domain.SubagentTask, int, error)
	ListActiveSubagentTasks(ctx context.Context, parentSessionKey string) ([]domain.SubagentTask, error)
	ListPendingSubagentTasks(ctx context.Context, limit int) ([]domain.SubagentTask, error)
	SaveSubagentTask(ctx context.Context, task *domain.SubagentTask) error
	UpdateSubagentTaskProgress(ctx context.Context, id string, step int, tool, progressMsg string) error
	UpdateSubagentTaskWaitingInput(ctx context.Context, id string, question, subConvID string) error
	UpdateSubagentTaskCompleted(ctx context.Context, id string, resultSummary, artifactsJSON string, usage domain.TokenUsage, durationSec float64) error
	UpdateSubagentTaskFailed(ctx context.Context, id string, errMsg string, durationSec float64) error
	UpdateSubagentTaskCancelled(ctx context.Context, id string) error
	PurgeSubagentTasks(ctx context.Context, olderThanDays int) (int64, error)
}

// SubagentDispatcherPort coordinates async worker pool management, task lifecycle, and progress inspection.
type SubagentDispatcherPort interface {
	// DispatchTask enqueues a new background task and returns the assigned TaskID immediately (<1ms).
	DispatchTask(ctx context.Context, task domain.SubagentTask) (string, error)

	// GetTask queries current task state and live progress metadata.
	GetTask(ctx context.Context, taskID string) (*domain.SubagentTask, error)

	// ListActiveTasks returns all in-flight tasks for a given session.
	ListActiveTasks(ctx context.Context, sessionKey string) ([]domain.SubagentTask, error)

	// ListTasks returns paginated tasks for a given session.
	ListTasks(ctx context.Context, sessionKey string, limit, offset int) ([]domain.SubagentTask, int, error)

	// SendTaskInput resumes a sub-agent waiting for clarification (WAITING_FOR_INPUT).
	SendTaskInput(ctx context.Context, taskID string, input string) error

	// CancelTask forcefully halts a running task and tears down the associated process tree.
	CancelTask(ctx context.Context, taskID string) error

	// Start initializes background worker pool consumers.
	Start(ctx context.Context) error

	// Stop gracefully shuts down the dispatcher and terminates active subprocesses.
	Stop(ctx context.Context) error
}
