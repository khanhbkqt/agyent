package zalo

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// ScheduledJob represents an automated scheduled notification task.
type ScheduledJob struct {
	ID        string
	Schedule  string // e.g. "0 9 * * *"
	ExecuteFn func(ctx context.Context) error
}

// VietnamScheduler manages automated recurring jobs anchored to Vietnam timezone (UTC+7).
type VietnamScheduler struct {
	location *time.Location
	jobs     []ScheduledJob
	mu       sync.Mutex
	cancelFn context.CancelFunc
}

// NewVietnamScheduler creates a new scheduler set to Asia/Ho_Chi_Minh timezone.
func NewVietnamScheduler() *VietnamScheduler {
	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		// Fallback fixed zone UTC+7
		loc = time.FixedZone("UTC+7", 7*3600)
	}
	return &VietnamScheduler{
		location: loc,
		jobs:     make([]ScheduledJob, 0),
	}
}

// AddJob registers a scheduled notification task.
func (s *VietnamScheduler) AddJob(job ScheduledJob) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs = append(s.jobs, job)
}

// Start runs background scheduler monitoring.
func (s *VietnamScheduler) Start(parentCtx context.Context) {
	s.mu.Lock()
	ctx, cancel := context.WithCancel(parentCtx)
	s.cancelFn = cancel
	s.mu.Unlock()

	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				nowVN := time.Now().In(s.location)
				s.evaluateAndRunJobs(ctx, nowVN)
			}
		}
	}()
}

// Stop terminates background scheduling.
func (s *VietnamScheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancelFn != nil {
		s.cancelFn()
	}
}

func (s *VietnamScheduler) evaluateAndRunJobs(ctx context.Context, now time.Time) {
	s.mu.Lock()
	jobsCopy := make([]ScheduledJob, len(s.jobs))
	copy(jobsCopy, s.jobs)
	s.mu.Unlock()

	for _, job := range jobsCopy {
		// Non-blocking asynchronous job trigger
		go func(j ScheduledJob) {
			if err := j.ExecuteFn(ctx); err != nil {
				slog.ErrorContext(ctx, "scheduled job failed", "job_id", j.ID, "error", err)
			}
		}(job)
	}
}
