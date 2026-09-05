package scheduler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"agyent/internal/config"
	"agyent/internal/core/concurrency"
	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

var _ ports.SchedulerPort = (*Scheduler)(nil)

// Scheduler coordinates daemon-level cron evaluation, one-time schedules, and agent heartbeats.
type Scheduler struct {
	cfg       *config.Config
	storage   ports.ScheduleRepository
	workspace ports.WorkspacePort
	runner    ports.RunnerPort
	eventBus  ports.EventBusPort
	executor  *TaskExecutor
	logger    *slog.Logger
	timezone  *time.Location

	inFlight sync.Map // map[string]*inFlightEntry
	running  atomic.Bool
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	mu       sync.Mutex
}

// NewScheduler constructs a new Scheduler instance.
func NewScheduler(
	cfg *config.Config,
	storage ports.ScheduleRepository,
	workspace ports.WorkspacePort,
	runner ports.RunnerPort,
	eventBus ports.EventBusPort,
	logger *slog.Logger,
) *Scheduler {
	if logger == nil {
		logger = slog.Default()
	}

	executor := NewTaskExecutor(cfg, runner, workspace, eventBus, logger)

	return &Scheduler{
		cfg:       cfg,
		storage:   storage,
		workspace: workspace,
		runner:    runner,
		eventBus:  eventBus,
		executor:  executor,
		logger:    logger,
		timezone:  time.Local,
	}
}

// SetLocation sets the timezone location for cron and schedule evaluations.
func (s *Scheduler) SetLocation(loc *time.Location) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if loc != nil {
		s.timezone = loc
	}
}

// SetExecutionService injects the execution service chokepoint into the scheduler executor.
func (s *Scheduler) SetExecutionService(svc ports.ExecutionServicePort) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.executor != nil {
		s.executor.SetExecutionService(svc)
	}
}

// Start launches the background scheduler poller loop.
func (s *Scheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running.Swap(true) {
		return errors.New("scheduler is already running")
	}

	s.ctx, s.cancel = context.WithCancel(ctx)

	// 1. Startup Sanitation Sweep: Recover tasks that were left in RUNNING on daemon crash
	if s.storage != nil {
		if err := s.storage.SanitizeInterruptedSchedules(s.ctx); err != nil {
			s.logger.Warn("Failed to sanitize interrupted schedules on startup", "error", err)
		}
		if err := s.storage.SanitizeInterruptedHeartbeats(s.ctx); err != nil {
			s.logger.Warn("Failed to sanitize interrupted heartbeats on startup", "error", err)
		}
	}

	// 2. Start Background Poller Loop
	s.wg.Add(1)
	concurrency.SafeGo(func() {
		defer s.wg.Done()
		s.pollerLoop()
	})

	s.logger.Info("Scheduler daemon started")
	return nil
}

type inFlightEntry struct {
	cancel context.CancelFunc
}

// Stop gracefully stops the scheduler poller and signals cancellation to in-flight tasks within deadline.
func (s *Scheduler) Stop(ctx context.Context) error {
	s.mu.Lock()
	if !s.running.CompareAndSwap(true, false) {
		s.mu.Unlock()
		return nil
	}
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()

	s.logger.Info("Stopping scheduler and aborting in-flight executions...")

	// Cancel all in-flight task contexts
	s.inFlight.Range(func(key, value any) bool {
		if entry, ok := value.(*inFlightEntry); ok && entry != nil && entry.cancel != nil {
			entry.cancel()
		}
		s.inFlight.Delete(key)
		return true
	})

	done := make(chan struct{})
	concurrency.SafeGo(func() {
		s.wg.Wait()
		close(done)
	})

	select {
	case <-done:
		s.logger.Info("Scheduler stopped cleanly")
		return nil
	case <-ctx.Done():
		s.logger.Warn("Scheduler stop deadline exceeded", "error", ctx.Err())
		return ctx.Err()
	}
}

func (s *Scheduler) pollerLoop() {
	ticker := time.NewTicker(1000 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.pollDueTasks()
		}
	}
}

func (s *Scheduler) pollDueTasks() {
	if s.storage == nil {
		return
	}

	now := time.Now()
	nowMs := now.UnixMilli()

	// 1. Process Due Schedules
	dueTasks, err := s.storage.AcquireDueSchedules(s.ctx, nowMs, 10)
	if err != nil && !errors.Is(err, context.Canceled) {
		s.logger.Error("Failed to acquire due schedules", "error", err)
	} else {
		for _, task := range dueTasks {
			s.dispatchScheduleTask(task, now)
		}
	}

	// 2. Process Due Heartbeats
	dueHBs, err := s.storage.AcquireDueHeartbeats(s.ctx, nowMs, 10)
	if err != nil && !errors.Is(err, context.Canceled) {
		s.logger.Error("Failed to acquire due heartbeats", "error", err)
	} else {
		for _, hb := range dueHBs {
			s.dispatchHeartbeatTask(hb, now)
		}
	}
}

func (s *Scheduler) dispatchScheduleTask(task domain.ScheduleTask, now time.Time) {
	inFlightKey := fmt.Sprintf("sched:%s", task.ID)

	// Overlap Check: Is this task already running?
	if existingCancel, isRunning := s.inFlight.Load(inFlightKey); isRunning {
		switch task.OverlapPolicy {
		case domain.OverlapPolicyCancelPrevious:
			s.logger.Warn("Task overlap: cancelling previous execution", "task_id", task.ID)
			if entry, ok := existingCancel.(*inFlightEntry); ok && entry != nil && entry.cancel != nil {
				entry.cancel()
			}
		case domain.OverlapPolicyQueue:
			s.logger.Debug("Task overlap: queueing for next run", "task_id", task.ID)
			return
		default: // domain.OverlapPolicySkip
			nextRun, parseErr := ParseNextRun(task.ScheduleType, task.ScheduleExpr, now, s.timezone)
			if parseErr != nil || !nextRun.After(now) {
				s.logger.Error("Task overlap: cannot calculate valid next run, marking failed", "task_id", task.ID, "error", parseErr)
				_ = s.storage.UpdateScheduleRun(s.ctx, task.ID, 0, "invalid schedule expression or next run calculation failed", domain.ScheduleStatusFailed)
				return
			}
			s.logger.Warn("Task overlap: skipping concurrent execution", "task_id", task.ID, "next_run", nextRun)
			_ = s.storage.AdvanceScheduleNextRun(s.ctx, task.ID, nextRun.UnixMilli())
			return
		}
	}

	// Misfire Policy Check: Did we miss the execution by a long duration (> 15m) due to downtime?
	gracePeriod := 15 * time.Minute
	if now.Sub(task.NextRunAt) > gracePeriod && task.MisfirePolicy == domain.MisfirePolicySkipToLatest && task.ScheduleType == domain.ScheduleTypeCron {
		nextRun, parseErr := ParseNextRun(task.ScheduleType, task.ScheduleExpr, now, s.timezone)
		if parseErr != nil || !nextRun.After(now) {
			s.logger.Error("Task misfired: cannot calculate valid next run, marking failed", "task_id", task.ID, "error", parseErr)
			_ = s.storage.UpdateScheduleRun(s.ctx, task.ID, 0, "invalid schedule expression or next run calculation failed", domain.ScheduleStatusFailed)
			return
		}
		s.logger.Warn("Task misfired during downtime: skipping backlog to latest", "task_id", task.ID, "delay", now.Sub(task.NextRunAt), "next_run", nextRun)
		_ = s.storage.AdvanceScheduleNextRun(s.ctx, task.ID, nextRun.UnixMilli())
		return
	}

	// For recurring cron tasks, advance next_run_at in DB immediately so that
	// subsequent poller ticks do not repeatedly claim this execution while in-flight.
	if task.ScheduleType == domain.ScheduleTypeCron {
		if nextOccur, err := ParseNextRun(task.ScheduleType, task.ScheduleExpr, now, s.timezone); err == nil && nextOccur.After(now) {
			_ = s.storage.AdvanceScheduleNextRun(s.ctx, task.ID, nextOccur.UnixMilli())
		}
	}

	taskCtx, taskCancel := context.WithCancel(s.ctx)
	entry := &inFlightEntry{cancel: taskCancel}
	s.inFlight.Store(inFlightKey, entry)

	s.wg.Add(1)
	concurrency.SafeGo(func() {
		defer s.wg.Done()
		defer func() {
			taskCancel()
			s.inFlight.CompareAndDelete(inFlightKey, entry)
		}()

		result, execErr := s.executor.ExecuteSchedule(taskCtx, task)

		// Calculate next run or mark completed
		nextStatus := domain.ScheduleStatusActive
		var nextRunTime time.Time
		errMsg := ""

		if execErr != nil {
			errMsg = execErr.Error()
		} else if result != nil && !result.Success {
			errMsg = result.Error
		}

		if task.ScheduleType == domain.ScheduleTypeOnce {
			if execErr == nil && (result == nil || result.Success) {
				nextStatus = domain.ScheduleStatusCompleted
			} else {
				nextStatus = domain.ScheduleStatusFailed
			}
			nextRunTime = task.NextRunAt
		} else {
			// Recurring cron
			var parseErr error
			nextRunTime, parseErr = ParseNextRun(task.ScheduleType, task.ScheduleExpr, time.Now(), s.timezone)
			if parseErr != nil || !nextRunTime.After(time.Now()) {
				s.logger.Error("Failed to calculate next cron run", "task_id", task.ID, "error", parseErr)
				nextStatus = domain.ScheduleStatusFailed
				if parseErr != nil {
					errMsg = parseErr.Error()
				} else {
					errMsg = "calculated next run time is in the past"
				}
			} else {
				nextStatus = domain.ScheduleStatusActive
			}
		}

		_ = s.storage.UpdateScheduleRun(context.Background(), task.ID, nextRunTime.UnixMilli(), errMsg, nextStatus)
	})
}

func (s *Scheduler) dispatchHeartbeatTask(hb domain.HeartbeatConfig, now time.Time) {
	inFlightKey := fmt.Sprintf("hb:%s", hb.AgentName)

	if _, isRunning := s.inFlight.Load(inFlightKey); isRunning {
		s.logger.Debug("Heartbeat overlap: previous run in progress, skipping", "agent", hb.AgentName)
		nextRun := now.Add(time.Duration(hb.IntervalSeconds) * time.Second)
		_ = s.storage.UpdateHeartbeatRun(s.ctx, hb.AgentName, nextRun.UnixMilli(), "skipped due to overlap", domain.HeartbeatStatusIdle)
		return
	}

	hbCtx, hbCancel := context.WithCancel(s.ctx)
	entry := &inFlightEntry{cancel: hbCancel}
	s.inFlight.Store(inFlightKey, entry)

	s.wg.Add(1)
	concurrency.SafeGo(func() {
		defer s.wg.Done()
		defer func() {
			hbCancel()
			s.inFlight.CompareAndDelete(inFlightKey, entry)
		}()

		result, execErr := s.executor.ExecuteHeartbeat(hbCtx, hb)

		errMsg := ""
		if execErr != nil {
			errMsg = execErr.Error()
		} else if result != nil && !result.Success {
			errMsg = result.Error
		}

		nextInterval := hb.IntervalSeconds
		if nextInterval <= 0 {
			nextInterval = 3600
		}
		nextRun := time.Now().Add(time.Duration(nextInterval) * time.Second)

		_ = s.storage.UpdateHeartbeatRun(context.Background(), hb.AgentName, nextRun.UnixMilli(), errMsg, domain.HeartbeatStatusIdle)
	})
}

// CreateSchedule registers a new scheduled or recurring task.
func (s *Scheduler) CreateSchedule(ctx context.Context, task domain.ScheduleTask) (*domain.ScheduleTask, error) {
	if strings.TrimSpace(task.AgentName) == "" {
		return nil, errors.New("agent_name is required")
	}
	if strings.TrimSpace(task.Title) == "" {
		return nil, errors.New("title is required")
	}
	if strings.TrimSpace(task.Prompt) == "" {
		return nil, errors.New("prompt is required")
	}

	if task.ID == "" {
		task.ID = generateTaskID(string(task.ScheduleType))
	}

	now := time.Now()
	nextRun, err := ParseNextRun(task.ScheduleType, task.ScheduleExpr, now, s.timezone)
	if err != nil {
		return nil, fmt.Errorf("invalid schedule expression %q: %w", task.ScheduleExpr, err)
	}

	task.NextRunAt = nextRun
	task.Status = domain.ScheduleStatusActive
	task.CreatedAt = now
	task.UpdatedAt = now

	if task.TargetSessionKey == "" {
		task.TargetSessionKey = fmt.Sprintf("sched:%s:%s", task.AgentName, task.ID)
	}

	if err := s.storage.SaveSchedule(ctx, &task); err != nil {
		return nil, fmt.Errorf("failed to save schedule: %w", err)
	}

	s.logger.Info("Schedule registered",
		"id", task.ID,
		"agent", task.AgentName,
		"type", task.ScheduleType,
		"next_run", task.NextRunAt,
	)

	return &task, nil
}

// CancelSchedule cancels and removes an active task.
func (s *Scheduler) CancelSchedule(ctx context.Context, id string) error {
	inFlightKey := fmt.Sprintf("sched:%s", id)
	if cancelVal, ok := s.inFlight.Load(inFlightKey); ok {
		if entry, ok := cancelVal.(*inFlightEntry); ok && entry != nil && entry.cancel != nil {
			entry.cancel()
		}
		s.inFlight.Delete(inFlightKey)
	}

	return s.storage.DeleteSchedule(ctx, id)
}

// ListSchedules lists schedules for a given agent.
func (s *Scheduler) ListSchedules(ctx context.Context, agentName string, status domain.ScheduleStatus) ([]domain.ScheduleTask, error) {
	tasks, _, err := s.storage.ListSchedules(ctx, agentName, status, 100, 0)
	return tasks, err
}

// ConfigureHeartbeat updates HEARTBEAT.md in workspace and syncs to SQLite.
func (s *Scheduler) ConfigureHeartbeat(ctx context.Context, cfg domain.HeartbeatConfig, prompt string) error {
	if strings.TrimSpace(cfg.AgentName) == "" {
		return errors.New("agent_name is required")
	}

	agentWS := config.ResolveAgentWorkspace(s.cfg.Storage.AgentsDir, cfg.AgentName)

	if s.workspace != nil {
		if err := s.workspace.WriteHeartbeat(ctx, agentWS, cfg, prompt); err != nil {
			return fmt.Errorf("failed to write HEARTBEAT.md: %w", err)
		}
	}

	if cfg.IntervalSeconds <= 0 {
		cfg.IntervalSeconds = 3600
	}
	if cfg.NextRunAt.IsZero() || cfg.NextRunAt.Before(time.Now()) {
		cfg.NextRunAt = time.Now().Add(time.Duration(cfg.IntervalSeconds) * time.Second)
	}
	cfg.UpdatedAt = time.Now()
	cfg.Status = domain.HeartbeatStatusIdle

	if s.storage != nil {
		if err := s.storage.SaveHeartbeat(ctx, &cfg); err != nil {
			return fmt.Errorf("failed to sync heartbeat to database: %w", err)
		}
	}

	s.logger.Info("Heartbeat configured",
		"agent", cfg.AgentName,
		"enabled", cfg.Enabled,
		"interval", cfg.IntervalSeconds,
	)

	return nil
}

// GetHeartbeat retrieves the heartbeat configuration and directives for an agent.
func (s *Scheduler) GetHeartbeat(ctx context.Context, agentName string) (*domain.HeartbeatConfig, string, error) {
	if strings.TrimSpace(agentName) == "" {
		return nil, "", errors.New("agent_name is required")
	}

	agentWS := config.ResolveAgentWorkspace(s.cfg.Storage.AgentsDir, agentName)

	var prompt string
	var cfg *domain.HeartbeatConfig

	if s.workspace != nil {
		c, p, err := s.workspace.ReadHeartbeat(ctx, agentWS)
		if err == nil {
			cfg = c
			prompt = p
		}
	}

	if s.storage != nil {
		dbCfg, err := s.storage.GetHeartbeat(ctx, agentName)
		if err == nil && dbCfg != nil {
			if cfg != nil {
				cfg.Status = dbCfg.Status
				cfg.LastRunAt = dbCfg.LastRunAt
				cfg.NextRunAt = dbCfg.NextRunAt
				cfg.LastError = dbCfg.LastError
			} else {
				cfg = dbCfg
			}
		}
	}

	if cfg == nil {
		cfg = &domain.HeartbeatConfig{
			AgentName:       agentName,
			Enabled:         false,
			IntervalSeconds: 3600,
			Status:          domain.HeartbeatStatusIdle,
		}
	}

	return cfg, prompt, nil
}

// TriggerHeartbeatNow runs a heartbeat check immediately without waiting for interval.
func (s *Scheduler) TriggerHeartbeatNow(ctx context.Context, agentName string) error {
	if !s.running.Load() || s.ctx == nil {
		return errors.New("scheduler daemon is not running")
	}

	cfg, _, err := s.GetHeartbeat(ctx, agentName)
	if err != nil {
		return err
	}

	concurrency.SafeGo(func() {
		s.dispatchHeartbeatTask(*cfg, time.Now())
	})

	return nil
}

func generateTaskID(prefix string) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	if prefix == "cron" {
		return fmt.Sprintf("cron-%s", hex.EncodeToString(b))
	}
	return fmt.Sprintf("sched-%s", hex.EncodeToString(b))
}
