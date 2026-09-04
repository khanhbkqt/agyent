package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

// SaveSchedule creates or updates a scheduled task.
func (s *SQLiteStore) SaveSchedule(ctx context.Context, task *domain.ScheduleTask) error {
	if task == nil {
		return errors.New("schedule task cannot be nil")
	}
	if strings.TrimSpace(task.ID) == "" {
		return errors.New("schedule task id cannot be empty")
	}
	if strings.TrimSpace(task.AgentName) == "" {
		return errors.New("schedule task agent_name cannot be empty")
	}

	query := `
		INSERT INTO agent_schedules (
			id, agent_name, title, schedule_type, schedule_expr, prompt,
			target_session_key, chat_id, thread_id, channel,
			status, overlap_policy, misfire_policy,
			next_run_at, last_run_at, run_count, max_runs,
			last_error, created_by, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			agent_name = excluded.agent_name,
			title = excluded.title,
			schedule_type = excluded.schedule_type,
			schedule_expr = excluded.schedule_expr,
			prompt = excluded.prompt,
			target_session_key = excluded.target_session_key,
			chat_id = excluded.chat_id,
			thread_id = excluded.thread_id,
			channel = excluded.channel,
			status = excluded.status,
			overlap_policy = excluded.overlap_policy,
			misfire_policy = excluded.misfire_policy,
			next_run_at = excluded.next_run_at,
			last_run_at = excluded.last_run_at,
			run_count = excluded.run_count,
			max_runs = excluded.max_runs,
			last_error = excluded.last_error,
			created_by = excluded.created_by,
			updated_at = excluded.updated_at
	`

	nowMs := time.Now().UnixMilli()
	createdMs := task.CreatedAt.UnixMilli()
	if createdMs <= 0 {
		createdMs = nowMs
	}
	updatedMs := task.UpdatedAt.UnixMilli()
	if updatedMs <= 0 {
		updatedMs = nowMs
	}
	nextRunMs := task.NextRunAt.UnixMilli()
	lastRunMs := task.LastRunAt.UnixMilli()
	if lastRunMs < 0 {
		lastRunMs = 0
	}

	overlapPolicy := string(task.OverlapPolicy)
	if overlapPolicy == "" {
		overlapPolicy = string(domain.OverlapPolicySkip)
	}
	misfirePolicy := string(task.MisfirePolicy)
	if misfirePolicy == "" {
		misfirePolicy = string(domain.MisfirePolicySkipToLatest)
	}
	status := string(task.Status)
	if status == "" {
		status = string(domain.ScheduleStatusActive)
	}
	channel := task.Channel
	if channel == "" {
		channel = "telegram"
	}

	_, err := s.writer().ExecContext(ctx, query,
		task.ID, task.AgentName, task.Title, string(task.ScheduleType), task.ScheduleExpr, task.Prompt,
		task.TargetSessionKey, task.ChatID, task.ThreadID, channel,
		status, overlapPolicy, misfirePolicy,
		nextRunMs, lastRunMs, task.RunCount, task.MaxRuns,
		task.LastError, task.CreatedBy, createdMs, updatedMs,
	)
	if err != nil {
		return fmt.Errorf("failed to save schedule task %s: %w", task.ID, err)
	}

	return nil
}

// GetSchedule retrieves a scheduled task by its unique ID.
func (s *SQLiteStore) GetSchedule(ctx context.Context, id string) (*domain.ScheduleTask, error) {
	if strings.TrimSpace(id) == "" {
		return nil, errors.New("schedule task id cannot be empty")
	}

	query := `
		SELECT id, agent_name, title, schedule_type, schedule_expr, prompt,
		       target_session_key, chat_id, thread_id, channel,
		       status, overlap_policy, misfire_policy,
		       next_run_at, last_run_at, run_count, max_runs,
		       last_error, created_by, created_at, updated_at
		FROM agent_schedules
		WHERE id = ?
	`

	var (
		t             domain.ScheduleTask
		schedTypeStr  string
		statusStr     string
		overlapStr    string
		misfireStr    string
		nextRunAt     FlexTime
		lastRunAt     FlexTime
		createdAt     FlexTime
		updatedAt     FlexTime
	)

	err := s.reader().QueryRowContext(ctx, query, id).Scan(
		&t.ID, &t.AgentName, &t.Title, &schedTypeStr, &t.ScheduleExpr, &t.Prompt,
		&t.TargetSessionKey, &t.ChatID, &t.ThreadID, &t.Channel,
		&statusStr, &overlapStr, &misfireStr,
		&nextRunAt, &lastRunAt, &t.RunCount, &t.MaxRuns,
		&t.LastError, &t.CreatedBy, &createdAt, &updatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: schedule task %s", ports.ErrNotFound, id)
		}
		return nil, fmt.Errorf("failed to query schedule task %s: %w", id, err)
	}

	t.ScheduleType = domain.ScheduleType(schedTypeStr)
	t.Status = domain.ScheduleStatus(statusStr)
	t.OverlapPolicy = domain.OverlapPolicy(overlapStr)
	t.MisfirePolicy = domain.MisfirePolicy(misfireStr)
	t.NextRunAt = nextRunAt.Time
	t.LastRunAt = lastRunAt.Time
	t.CreatedAt = createdAt.Time
	t.UpdatedAt = updatedAt.Time

	return &t, nil
}

// DeleteSchedule removes a scheduled task by ID.
func (s *SQLiteStore) DeleteSchedule(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("schedule task id cannot be empty")
	}

	query := `DELETE FROM agent_schedules WHERE id = ?`
	res, err := s.writer().ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete schedule task %s: %w", id, err)
	}

	rows, err := res.RowsAffected()
	if err == nil && rows == 0 {
		return fmt.Errorf("%w: schedule task %s", ports.ErrNotFound, id)
	}

	return nil
}

// ListSchedules returns paginated schedules, optionally filtered by agentName and/or status.
func (s *SQLiteStore) ListSchedules(ctx context.Context, agentName string, status domain.ScheduleStatus, limit, offset int) ([]domain.ScheduleTask, int, error) {
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	var whereClauses []string
	var args []any

	if strings.TrimSpace(agentName) != "" {
		whereClauses = append(whereClauses, "agent_name = ?")
		args = append(args, agentName)
	}
	if strings.TrimSpace(string(status)) != "" {
		whereClauses = append(whereClauses, "status = ?")
		args = append(args, string(status))
	}

	whereSQL := ""
	if len(whereClauses) > 0 {
		whereSQL = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM agent_schedules %s", whereSQL)
	var totalCount int
	if err := s.reader().QueryRowContext(ctx, countQuery, args...).Scan(&totalCount); err != nil {
		return nil, 0, fmt.Errorf("failed to count schedules: %w", err)
	}

	query := fmt.Sprintf(`
		SELECT id, agent_name, title, schedule_type, schedule_expr, prompt,
		       target_session_key, chat_id, thread_id, channel,
		       status, overlap_policy, misfire_policy,
		       next_run_at, last_run_at, run_count, max_runs,
		       last_error, created_by, created_at, updated_at
		FROM agent_schedules
		%s
		ORDER BY next_run_at ASC
		LIMIT ? OFFSET ?
	`, whereSQL)

	queryArgs := append(args, limit, offset)
	rows, err := s.reader().QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list schedules: %w", err)
	}
	defer rows.Close()

	var results []domain.ScheduleTask
	for rows.Next() {
		var (
			t             domain.ScheduleTask
			schedTypeStr  string
			statusStr     string
			overlapStr    string
			misfireStr    string
			nextRunAt     FlexTime
			lastRunAt     FlexTime
			createdAt     FlexTime
			updatedAt     FlexTime
		)

		err := rows.Scan(
			&t.ID, &t.AgentName, &t.Title, &schedTypeStr, &t.ScheduleExpr, &t.Prompt,
			&t.TargetSessionKey, &t.ChatID, &t.ThreadID, &t.Channel,
			&statusStr, &overlapStr, &misfireStr,
			&nextRunAt, &lastRunAt, &t.RunCount, &t.MaxRuns,
			&t.LastError, &t.CreatedBy, &createdAt, &updatedAt,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan schedule task row: %w", err)
		}

		t.ScheduleType = domain.ScheduleType(schedTypeStr)
		t.Status = domain.ScheduleStatus(statusStr)
		t.OverlapPolicy = domain.OverlapPolicy(overlapStr)
		t.MisfirePolicy = domain.MisfirePolicy(misfireStr)
		t.NextRunAt = nextRunAt.Time
		t.LastRunAt = lastRunAt.Time
		t.CreatedAt = createdAt.Time
		t.UpdatedAt = updatedAt.Time

		results = append(results, t)
	}

	return results, totalCount, nil
}

// AcquireDueSchedules atomically claims due ACTIVE schedules up to nowUnixMs and transitions them to RUNNING.
func (s *SQLiteStore) AcquireDueSchedules(ctx context.Context, nowUnixMs int64, limit int) ([]domain.ScheduleTask, error) {
	if limit <= 0 {
		limit = 10
	}

	tx, err := s.writer().BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin tx for acquire due schedules: %w", err)
	}
	defer tx.Rollback()

	selectQuery := `
		SELECT id, agent_name, title, schedule_type, schedule_expr, prompt,
		       target_session_key, chat_id, thread_id, channel,
		       status, overlap_policy, misfire_policy,
		       next_run_at, last_run_at, run_count, max_runs,
		       last_error, created_by, created_at, updated_at
		FROM agent_schedules
		WHERE status = 'ACTIVE' AND next_run_at <= ?
		ORDER BY next_run_at ASC
		LIMIT ?
	`

	rows, err := tx.QueryContext(ctx, selectQuery, nowUnixMs, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query due schedules: %w", err)
	}
	defer rows.Close()

	var dueTasks []domain.ScheduleTask
	var claimedIDs []string

	for rows.Next() {
		var (
			t             domain.ScheduleTask
			schedTypeStr  string
			statusStr     string
			overlapStr    string
			misfireStr    string
			nextRunAt     FlexTime
			lastRunAt     FlexTime
			createdAt     FlexTime
			updatedAt     FlexTime
		)

		err := rows.Scan(
			&t.ID, &t.AgentName, &t.Title, &schedTypeStr, &t.ScheduleExpr, &t.Prompt,
			&t.TargetSessionKey, &t.ChatID, &t.ThreadID, &t.Channel,
			&statusStr, &overlapStr, &misfireStr,
			&nextRunAt, &lastRunAt, &t.RunCount, &t.MaxRuns,
			&t.LastError, &t.CreatedBy, &createdAt, &updatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan due schedule: %w", err)
		}

		t.ScheduleType = domain.ScheduleType(schedTypeStr)
		t.Status = domain.ScheduleStatusRunning // Claimed status
		t.OverlapPolicy = domain.OverlapPolicy(overlapStr)
		t.MisfirePolicy = domain.MisfirePolicy(misfireStr)
		t.NextRunAt = nextRunAt.Time
		t.LastRunAt = lastRunAt.Time
		t.CreatedAt = createdAt.Time
		t.UpdatedAt = updatedAt.Time

		dueTasks = append(dueTasks, t)
		claimedIDs = append(claimedIDs, t.ID)
	}
	rows.Close()

	if len(claimedIDs) > 0 {
		updateQuery := fmt.Sprintf(`
			UPDATE agent_schedules
			SET status = 'RUNNING', updated_at = ?
			WHERE id IN (%s)
		`, strings.Repeat("?,", len(claimedIDs)-1)+"?")

		args := make([]any, 0, len(claimedIDs)+1)
		args = append(args, nowUnixMs)
		for _, id := range claimedIDs {
			args = append(args, id)
		}

		if _, err := tx.ExecContext(ctx, updateQuery, args...); err != nil {
			return nil, fmt.Errorf("failed to claim schedules: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit claimed schedules: %w", err)
	}

	return dueTasks, nil
}

// UpdateScheduleRun updates execution metrics, next_run_at, status, and last_error.
func (s *SQLiteStore) UpdateScheduleRun(ctx context.Context, id string, nextRunAt int64, lastError string, status domain.ScheduleStatus) error {
	nowMs := time.Now().UnixMilli()

	query := `
		UPDATE agent_schedules
		SET status = ?,
		    next_run_at = ?,
		    last_run_at = ?,
		    run_count = run_count + 1,
		    last_error = ?,
		    updated_at = ?
		WHERE id = ?
	`

	_, err := s.writer().ExecContext(ctx, query, string(status), nextRunAt, nowMs, lastError, nowMs, id)
	if err != nil {
		return fmt.Errorf("failed to update schedule run for %s: %w", id, err)
	}

	return nil
}

// SanitizeInterruptedSchedules recovers tasks that were in RUNNING state when the daemon shut down or crashed.
func (s *SQLiteStore) SanitizeInterruptedSchedules(ctx context.Context) error {
	nowMs := time.Now().UnixMilli()
	query := `
		UPDATE agent_schedules
		SET status = 'ACTIVE',
		    last_error = 'interrupted by daemon restart',
		    updated_at = ?
		WHERE status = 'RUNNING'
	`
	_, err := s.writer().ExecContext(ctx, query, nowMs)
	if err != nil {
		return fmt.Errorf("failed to sanitize interrupted schedules: %w", err)
	}
	return nil
}

// GetHeartbeat retrieves heartbeat configuration for an agent persona.
func (s *SQLiteStore) GetHeartbeat(ctx context.Context, agentName string) (*domain.HeartbeatConfig, error) {
	if strings.TrimSpace(agentName) == "" {
		return nil, errors.New("agent name cannot be empty")
	}

	query := `
		SELECT agent_name, enabled, interval_seconds, target_session_key, chat_id, thread_id, channel,
		       status, last_run_at, next_run_at, last_error, updated_at
		FROM agent_heartbeats
		WHERE agent_name = ?
	`

	var (
		hb        domain.HeartbeatConfig
		enabledInt int
		statusStr string
		lastRunAt FlexTime
		nextRunAt FlexTime
		updatedAt FlexTime
	)

	err := s.reader().QueryRowContext(ctx, query, agentName).Scan(
		&hb.AgentName, &enabledInt, &hb.IntervalSeconds, &hb.TargetSessionKey, &hb.ChatID, &hb.ThreadID, &hb.Channel,
		&statusStr, &lastRunAt, &nextRunAt, &hb.LastError, &updatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: heartbeat for agent %s", ports.ErrNotFound, agentName)
		}
		return nil, fmt.Errorf("failed to query heartbeat for agent %s: %w", agentName, err)
	}

	hb.Enabled = (enabledInt == 1)
	hb.Status = domain.HeartbeatStatus(statusStr)
	hb.LastRunAt = lastRunAt.Time
	hb.NextRunAt = nextRunAt.Time
	hb.UpdatedAt = updatedAt.Time

	return &hb, nil
}

// SaveHeartbeat creates or updates heartbeat configuration for an agent.
func (s *SQLiteStore) SaveHeartbeat(ctx context.Context, hb *domain.HeartbeatConfig) error {
	if hb == nil {
		return errors.New("heartbeat config cannot be nil")
	}
	if strings.TrimSpace(hb.AgentName) == "" {
		return errors.New("heartbeat agent name cannot be empty")
	}

	query := `
		INSERT INTO agent_heartbeats (
			agent_name, enabled, interval_seconds, target_session_key, chat_id, thread_id, channel,
			status, last_run_at, next_run_at, last_error, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(agent_name) DO UPDATE SET
			enabled = excluded.enabled,
			interval_seconds = excluded.interval_seconds,
			target_session_key = excluded.target_session_key,
			chat_id = excluded.chat_id,
			thread_id = excluded.thread_id,
			channel = excluded.channel,
			status = excluded.status,
			last_run_at = excluded.last_run_at,
			next_run_at = excluded.next_run_at,
			last_error = excluded.last_error,
			updated_at = excluded.updated_at
	`

	enabledInt := 0
	if hb.Enabled {
		enabledInt = 1
	}
	status := string(hb.Status)
	if status == "" {
		status = string(domain.HeartbeatStatusIdle)
	}
	interval := hb.IntervalSeconds
	if interval <= 0 {
		interval = 3600
	}
	channel := hb.Channel
	if channel == "" {
		channel = "telegram"
	}
	nowMs := time.Now().UnixMilli()
	lastRunMs := hb.LastRunAt.UnixMilli()
	if lastRunMs < 0 {
		lastRunMs = 0
	}
	nextRunMs := hb.NextRunAt.UnixMilli()
	if nextRunMs < 0 {
		nextRunMs = 0
	}

	_, err := s.writer().ExecContext(ctx, query,
		hb.AgentName, enabledInt, interval, hb.TargetSessionKey, hb.ChatID, hb.ThreadID, channel,
		status, lastRunMs, nextRunMs, hb.LastError, nowMs,
	)
	if err != nil {
		return fmt.Errorf("failed to save heartbeat for agent %s: %w", hb.AgentName, err)
	}

	return nil
}

// AcquireDueHeartbeats atomically claims enabled heartbeats that are IDLE and due.
func (s *SQLiteStore) AcquireDueHeartbeats(ctx context.Context, nowUnixMs int64, limit int) ([]domain.HeartbeatConfig, error) {
	if limit <= 0 {
		limit = 10
	}

	tx, err := s.writer().BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin tx for acquire due heartbeats: %w", err)
	}
	defer tx.Rollback()

	selectQuery := `
		SELECT agent_name, enabled, interval_seconds, target_session_key, chat_id, thread_id, channel,
		       status, last_run_at, next_run_at, last_error, updated_at
		FROM agent_heartbeats
		WHERE enabled = 1 AND status = 'IDLE' AND next_run_at <= ?
		ORDER BY next_run_at ASC
		LIMIT ?
	`

	rows, err := tx.QueryContext(ctx, selectQuery, nowUnixMs, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query due heartbeats: %w", err)
	}
	defer rows.Close()

	var dueHBs []domain.HeartbeatConfig
	var claimedAgents []string

	for rows.Next() {
		var (
			hb        domain.HeartbeatConfig
			enabledInt int
			statusStr string
			lastRunAt FlexTime
			nextRunAt FlexTime
			updatedAt FlexTime
		)

		err := rows.Scan(
			&hb.AgentName, &enabledInt, &hb.IntervalSeconds, &hb.TargetSessionKey, &hb.ChatID, &hb.ThreadID, &hb.Channel,
			&statusStr, &lastRunAt, &nextRunAt, &hb.LastError, &updatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan due heartbeat: %w", err)
		}

		hb.Enabled = (enabledInt == 1)
		hb.Status = domain.HeartbeatStatusRunning
		hb.LastRunAt = lastRunAt.Time
		hb.NextRunAt = nextRunAt.Time
		hb.UpdatedAt = updatedAt.Time

		dueHBs = append(dueHBs, hb)
		claimedAgents = append(claimedAgents, hb.AgentName)
	}
	rows.Close()

	if len(claimedAgents) > 0 {
		updateQuery := fmt.Sprintf(`
			UPDATE agent_heartbeats
			SET status = 'RUNNING', updated_at = ?
			WHERE agent_name IN (%s)
		`, strings.Repeat("?,", len(claimedAgents)-1)+"?")

		args := make([]any, 0, len(claimedAgents)+1)
		args = append(args, nowUnixMs)
		for _, name := range claimedAgents {
			args = append(args, name)
		}

		if _, err := tx.ExecContext(ctx, updateQuery, args...); err != nil {
			return nil, fmt.Errorf("failed to claim heartbeats: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit claimed heartbeats: %w", err)
	}

	return dueHBs, nil
}

// UpdateHeartbeatRun updates the status, next_run_at, and last_error of an agent's heartbeat.
func (s *SQLiteStore) UpdateHeartbeatRun(ctx context.Context, agentName string, nextRunAt int64, lastError string, status domain.HeartbeatStatus) error {
	nowMs := time.Now().UnixMilli()

	query := `
		UPDATE agent_heartbeats
		SET status = ?,
		    next_run_at = ?,
		    last_run_at = ?,
		    last_error = ?,
		    updated_at = ?
		WHERE agent_name = ?
	`

	_, err := s.writer().ExecContext(ctx, query, string(status), nextRunAt, nowMs, lastError, nowMs, agentName)
	if err != nil {
		return fmt.Errorf("failed to update heartbeat run for agent %s: %w", agentName, err)
	}

	return nil
}

// SanitizeInterruptedHeartbeats recovers heartbeats that were in RUNNING state upon daemon restart.
func (s *SQLiteStore) SanitizeInterruptedHeartbeats(ctx context.Context) error {
	nowMs := time.Now().UnixMilli()
	query := `
		UPDATE agent_heartbeats
		SET status = 'IDLE',
		    last_error = 'interrupted by daemon restart',
		    updated_at = ?
		WHERE status = 'RUNNING'
	`
	_, err := s.writer().ExecContext(ctx, query, nowMs)
	if err != nil {
		return fmt.Errorf("failed to sanitize interrupted heartbeats: %w", err)
	}
	return nil
}
