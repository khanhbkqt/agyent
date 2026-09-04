package ports

import (
	"context"

	"agyent/internal/core/domain"
)

// ScheduleRepository defines persistence operations for agent schedules and heartbeats in SQLite.
type ScheduleRepository interface {
	// Schedule tasks
	SaveSchedule(ctx context.Context, task *domain.ScheduleTask) error
	GetSchedule(ctx context.Context, id string) (*domain.ScheduleTask, error)
	DeleteSchedule(ctx context.Context, id string) error
	ListSchedules(ctx context.Context, agentName string, status domain.ScheduleStatus, limit, offset int) ([]domain.ScheduleTask, int, error)
	AcquireDueSchedules(ctx context.Context, nowUnixMs int64, limit int) ([]domain.ScheduleTask, error)
	UpdateScheduleRun(ctx context.Context, id string, nextRunAt int64, lastError string, status domain.ScheduleStatus) error
	SanitizeInterruptedSchedules(ctx context.Context) error

	// Agent heartbeats
	GetHeartbeat(ctx context.Context, agentName string) (*domain.HeartbeatConfig, error)
	SaveHeartbeat(ctx context.Context, hb *domain.HeartbeatConfig) error
	AcquireDueHeartbeats(ctx context.Context, nowUnixMs int64, limit int) ([]domain.HeartbeatConfig, error)
	UpdateHeartbeatRun(ctx context.Context, agentName string, nextRunAt int64, lastError string, status domain.HeartbeatStatus) error
	SanitizeInterruptedHeartbeats(ctx context.Context) error
}

// SchedulerPort coordinates daemon-level scheduling, cron evaluation, and heartbeat lifecycle.
type SchedulerPort interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error

	CreateSchedule(ctx context.Context, task domain.ScheduleTask) (*domain.ScheduleTask, error)
	CancelSchedule(ctx context.Context, id string) error
	ListSchedules(ctx context.Context, agentName string, status domain.ScheduleStatus) ([]domain.ScheduleTask, error)

	ConfigureHeartbeat(ctx context.Context, cfg domain.HeartbeatConfig, prompt string) error
	GetHeartbeat(ctx context.Context, agentName string) (*domain.HeartbeatConfig, string, error)
	TriggerHeartbeatNow(ctx context.Context, agentName string) error
}
