package domain

import (
	"time"
)

// ScheduleType defines whether a task executes once or periodically.
type ScheduleType string

const (
	ScheduleTypeOnce ScheduleType = "once"
	ScheduleTypeCron ScheduleType = "cron"
)

// ScheduleStatus represents the lifecycle state of a scheduled task.
type ScheduleStatus string

const (
	ScheduleStatusActive    ScheduleStatus = "ACTIVE"
	ScheduleStatusRunning   ScheduleStatus = "RUNNING"
	ScheduleStatusPaused    ScheduleStatus = "PAUSED"
	ScheduleStatusCompleted ScheduleStatus = "COMPLETED"
	ScheduleStatusFailed    ScheduleStatus = "FAILED"
	ScheduleStatusCancelled ScheduleStatus = "CANCELLED"
)

// OverlapPolicy governs concurrency when a cron task triggers while its previous run is still in-flight.
type OverlapPolicy string

const (
	OverlapPolicySkip           OverlapPolicy = "skip"
	OverlapPolicyCancelPrevious OverlapPolicy = "cancel_previous"
	OverlapPolicyQueue          OverlapPolicy = "queue"
)

// MisfirePolicy defines behavior when a scheduled run was missed due to daemon downtime or system sleep.
type MisfirePolicy string

const (
	MisfirePolicySkipToLatest MisfirePolicy = "skip_to_latest"
	MisfirePolicyRunOnce      MisfirePolicy = "run_once"
)

// ScheduleTask represents an individual scheduled or recurring cron job for an agent persona.
type ScheduleTask struct {
	ID               string         `json:"id"`
	AgentName        string         `json:"agent_name"`
	Title            string         `json:"title"`
	ScheduleType     ScheduleType   `json:"schedule_type"`
	ScheduleExpr     string         `json:"schedule_expr"`
	Prompt           string         `json:"prompt"`
	TargetSessionKey string         `json:"target_session_key"`
	ChatID           string         `json:"chat_id"`
	ThreadID         string         `json:"thread_id,omitempty"`
	Channel          string         `json:"channel"`
	Status           ScheduleStatus `json:"status"`
	OverlapPolicy    OverlapPolicy  `json:"overlap_policy"`
	MisfirePolicy    MisfirePolicy  `json:"misfire_policy"`
	NextRunAt        time.Time      `json:"next_run_at"`
	LastRunAt        time.Time      `json:"last_run_at,omitempty"`
	RunCount         int            `json:"run_count"`
	MaxRuns          int            `json:"max_runs"`
	LastError        string         `json:"last_error,omitempty"`
	CreatedBy        string         `json:"created_by"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

// HeartbeatStatus represents the execution state of an agent's periodic heartbeat.
type HeartbeatStatus string

const (
	HeartbeatStatusIdle    HeartbeatStatus = "IDLE"
	HeartbeatStatusRunning HeartbeatStatus = "RUNNING"
)

// HeartbeatConfig represents the runtime configuration and schedule state for an agent's heartbeat.
type HeartbeatConfig struct {
	AgentName        string          `yaml:"agent_name" json:"agent_name"`
	Enabled          bool            `yaml:"enabled" json:"enabled"`
	IntervalSeconds  int             `yaml:"interval_seconds" json:"interval_seconds"`
	TargetSessionKey string          `yaml:"target_session_key" json:"target_session_key"`
	ChatID           string          `yaml:"chat_id" json:"chat_id"`
	ThreadID         string          `yaml:"thread_id" json:"thread_id,omitempty"`
	Channel          string          `yaml:"channel" json:"channel"`
	Status           HeartbeatStatus `yaml:"-" json:"status"`
	LastRunAt        time.Time       `yaml:"-" json:"last_run_at,omitempty"`
	NextRunAt        time.Time       `yaml:"-" json:"next_run_at,omitempty"`
	LastError        string          `yaml:"-" json:"last_error,omitempty"`
	UpdatedAt        time.Time       `yaml:"-" json:"updated_at"`
}

// ScheduleEventPayload carries event data for scheduled tasks.
type ScheduleEventPayload struct {
	Task     ScheduleTask `json:"task"`
	Response string       `json:"response,omitempty"`
	Error    string       `json:"error,omitempty"`
}

func (p ScheduleEventPayload) GetSessionKey() string {
	return p.Task.TargetSessionKey
}

// HeartbeatEventPayload carries event data for heartbeat tasks.
type HeartbeatEventPayload struct {
	Config   HeartbeatConfig `json:"config"`
	Response string          `json:"response,omitempty"`
	Error    string          `json:"error,omitempty"`
}

func (p HeartbeatEventPayload) GetSessionKey() string {
	return p.Config.TargetSessionKey
}
