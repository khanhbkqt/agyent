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

// GetSubagentTask retrieves a subagent task by its unique ID.
func (s *SQLiteStore) GetSubagentTask(ctx context.Context, id string) (*domain.SubagentTask, error) {
	if strings.TrimSpace(id) == "" {
		return nil, errors.New("subagent task id cannot be empty")
	}

	query := `
		SELECT id, parent_session_key, parent_conversation_id, sub_conversation_id,
		       agent_name, project_name, title, prompt, model, effort, workspace_mode, callback_mode,
		       status, current_step, current_tool, progress_message, pending_question,
		       result_summary, artifacts_json, error_message, total_tokens, duration_seconds,
		       created_at, updated_at
		FROM subagent_tasks
		WHERE id = ?
	`

	var (
		t             domain.SubagentTask
		statusStr     string
		cbModeStr     string
		artifactsJSON string
		createdAt     FlexTime
		updatedAt     FlexTime
	)

	err := s.reader().QueryRowContext(ctx, query, id).Scan(
		&t.ID, &t.ParentSessionKey, &t.ParentConversationID, &t.SubConversationID,
		&t.AgentName, &t.ProjectName, &t.Title, &t.Prompt, &t.Model, &t.Effort, &t.WorkspaceMode, &cbModeStr,
		&statusStr, &t.CurrentStep, &t.CurrentTool, &t.ProgressMessage, &t.PendingQuestion,
		&t.ResultSummary, &artifactsJSON, &t.ErrorMessage, &t.Usage.TotalTokens, &t.DurationSeconds,
		&createdAt, &updatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: subagent task %s", ports.ErrNotFound, id)
		}
		return nil, fmt.Errorf("failed to query subagent task %s: %w", id, err)
	}

	t.Status = domain.SubagentTaskStatus(statusStr)
	t.CallbackMode = domain.SubagentCallbackMode(cbModeStr)
	t.CreatedAt = createdAt.Time
	t.UpdatedAt = updatedAt.Time
	t.ParseArtifactsJSON(artifactsJSON)

	return &t, nil
}

// ListSubagentTasks returns paginated tasks for a session, ordered by created_at DESC.
func (s *SQLiteStore) ListSubagentTasks(ctx context.Context, parentSessionKey string, limit, offset int) ([]domain.SubagentTask, int, error) {
	if limit <= 0 {
		limit = 10
	}
	if offset < 0 {
		offset = 0
	}

	countQuery := `SELECT COUNT(*) FROM subagent_tasks WHERE parent_session_key = ?`
	var totalCount int
	if err := s.reader().QueryRowContext(ctx, countQuery, parentSessionKey).Scan(&totalCount); err != nil {
		return nil, 0, fmt.Errorf("failed to count subagent tasks: %w", err)
	}

	query := `
		SELECT id, parent_session_key, parent_conversation_id, sub_conversation_id,
		       agent_name, project_name, title, prompt, model, effort, workspace_mode, callback_mode,
		       status, current_step, current_tool, progress_message, pending_question,
		       result_summary, artifacts_json, error_message, total_tokens, duration_seconds,
		       created_at, updated_at
		FROM subagent_tasks
		WHERE parent_session_key = ?
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?
	`

	rows, err := s.reader().QueryContext(ctx, query, parentSessionKey, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list subagent tasks: %w", err)
	}
	defer rows.Close()

	var results []domain.SubagentTask
	for rows.Next() {
		var (
			t             domain.SubagentTask
			statusStr     string
			cbModeStr     string
			artifactsJSON string
			createdAt     FlexTime
			updatedAt     FlexTime
		)

		if err := rows.Scan(
			&t.ID, &t.ParentSessionKey, &t.ParentConversationID, &t.SubConversationID,
			&t.AgentName, &t.ProjectName, &t.Title, &t.Prompt, &t.Model, &t.Effort, &t.WorkspaceMode, &cbModeStr,
			&statusStr, &t.CurrentStep, &t.CurrentTool, &t.ProgressMessage, &t.PendingQuestion,
			&t.ResultSummary, &artifactsJSON, &t.ErrorMessage, &t.Usage.TotalTokens, &t.DurationSeconds,
			&createdAt, &updatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("failed to scan subagent task row: %w", err)
		}

		t.Status = domain.SubagentTaskStatus(statusStr)
		t.CallbackMode = domain.SubagentCallbackMode(cbModeStr)
		t.CreatedAt = createdAt.Time
		t.UpdatedAt = updatedAt.Time
		t.ParseArtifactsJSON(artifactsJSON)

		results = append(results, t)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("error iterating subagent task rows: %w", err)
	}

	return results, totalCount, nil
}

// ListActiveSubagentTasks returns all currently non-terminal tasks (PENDING, RUNNING, WAITING_FOR_INPUT).
func (s *SQLiteStore) ListActiveSubagentTasks(ctx context.Context, parentSessionKey string) ([]domain.SubagentTask, error) {
	query := `
		SELECT id, parent_session_key, parent_conversation_id, sub_conversation_id,
		       agent_name, project_name, title, prompt, model, effort, workspace_mode, callback_mode,
		       status, current_step, current_tool, progress_message, pending_question,
		       result_summary, artifacts_json, error_message, total_tokens, duration_seconds,
		       created_at, updated_at
		FROM subagent_tasks
		WHERE parent_session_key = ? AND status IN ('PENDING', 'RUNNING', 'WAITING_FOR_INPUT')
		ORDER BY created_at ASC
	`

	rows, err := s.reader().QueryContext(ctx, query, parentSessionKey)
	if err != nil {
		return nil, fmt.Errorf("failed to query active subagent tasks: %w", err)
	}
	defer rows.Close()

	var results []domain.SubagentTask
	for rows.Next() {
		var (
			t             domain.SubagentTask
			statusStr     string
			cbModeStr     string
			artifactsJSON string
			createdAt     FlexTime
			updatedAt     FlexTime
		)

		if err := rows.Scan(
			&t.ID, &t.ParentSessionKey, &t.ParentConversationID, &t.SubConversationID,
			&t.AgentName, &t.ProjectName, &t.Title, &t.Prompt, &t.Model, &t.Effort, &t.WorkspaceMode, &cbModeStr,
			&statusStr, &t.CurrentStep, &t.CurrentTool, &t.ProgressMessage, &t.PendingQuestion,
			&t.ResultSummary, &artifactsJSON, &t.ErrorMessage, &t.Usage.TotalTokens, &t.DurationSeconds,
			&createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan active subagent task row: %w", err)
		}

		t.Status = domain.SubagentTaskStatus(statusStr)
		t.CallbackMode = domain.SubagentCallbackMode(cbModeStr)
		t.CreatedAt = createdAt.Time
		t.UpdatedAt = updatedAt.Time
		t.ParseArtifactsJSON(artifactsJSON)

		results = append(results, t)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating active subagent task rows: %w", err)
	}

	return results, nil
}

// ListPendingSubagentTasks returns up to limit tasks in PENDING status ordered by created_at ASC.
func (s *SQLiteStore) ListPendingSubagentTasks(ctx context.Context, limit int) ([]domain.SubagentTask, error) {
	if limit <= 0 {
		limit = 10
	}

	query := `
		SELECT id, parent_session_key, parent_conversation_id, sub_conversation_id,
		       agent_name, project_name, title, prompt, model, effort, workspace_mode, callback_mode,
		       status, current_step, current_tool, progress_message, pending_question,
		       result_summary, artifacts_json, error_message, total_tokens, duration_seconds,
		       created_at, updated_at
		FROM subagent_tasks
		WHERE status = 'PENDING'
		ORDER BY created_at ASC
		LIMIT ?
	`

	rows, err := s.reader().QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query pending subagent tasks: %w", err)
	}
	defer rows.Close()

	var results []domain.SubagentTask
	for rows.Next() {
		var (
			t             domain.SubagentTask
			statusStr     string
			cbModeStr     string
			artifactsJSON string
			createdAt     FlexTime
			updatedAt     FlexTime
		)

		if err := rows.Scan(
			&t.ID, &t.ParentSessionKey, &t.ParentConversationID, &t.SubConversationID,
			&t.AgentName, &t.ProjectName, &t.Title, &t.Prompt, &t.Model, &t.Effort, &t.WorkspaceMode, &cbModeStr,
			&statusStr, &t.CurrentStep, &t.CurrentTool, &t.ProgressMessage, &t.PendingQuestion,
			&t.ResultSummary, &artifactsJSON, &t.ErrorMessage, &t.Usage.TotalTokens, &t.DurationSeconds,
			&createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan pending subagent task row: %w", err)
		}

		t.Status = domain.SubagentTaskStatus(statusStr)
		t.CallbackMode = domain.SubagentCallbackMode(cbModeStr)
		t.CreatedAt = createdAt.Time
		t.UpdatedAt = updatedAt.Time
		t.ParseArtifactsJSON(artifactsJSON)

		results = append(results, t)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating pending subagent task rows: %w", err)
	}

	return results, nil
}

// SaveSubagentTask inserts or updates a full subagent task record.
func (s *SQLiteStore) SaveSubagentTask(ctx context.Context, task *domain.SubagentTask) error {
	if task == nil || task.ID == "" {
		return errors.New("cannot save nil or empty subagent task")
	}

	now := time.Now()
	if task.CreatedAt.IsZero() {
		task.CreatedAt = now
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = now
	}

	query := `
		INSERT INTO subagent_tasks (
			id, parent_session_key, parent_conversation_id, sub_conversation_id,
			agent_name, project_name, title, prompt, model, effort, workspace_mode, callback_mode,
			status, current_step, current_tool, progress_message, pending_question,
			result_summary, artifacts_json, error_message, total_tokens, duration_seconds,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			parent_session_key = excluded.parent_session_key,
			parent_conversation_id = excluded.parent_conversation_id,
			sub_conversation_id = excluded.sub_conversation_id,
			agent_name = excluded.agent_name,
			project_name = excluded.project_name,
			title = excluded.title,
			prompt = excluded.prompt,
			model = excluded.model,
			effort = excluded.effort,
			workspace_mode = excluded.workspace_mode,
			callback_mode = excluded.callback_mode,
			status = excluded.status,
			current_step = excluded.current_step,
			current_tool = excluded.current_tool,
			progress_message = excluded.progress_message,
			pending_question = excluded.pending_question,
			result_summary = excluded.result_summary,
			artifacts_json = excluded.artifacts_json,
			error_message = excluded.error_message,
			total_tokens = excluded.total_tokens,
			duration_seconds = excluded.duration_seconds,
			updated_at = excluded.updated_at
	`

	_, err := s.writer().ExecContext(
		ctx, query,
		task.ID, task.ParentSessionKey, task.ParentConversationID, task.SubConversationID,
		task.AgentName, task.ProjectName, task.Title, task.Prompt, task.Model, task.Effort, task.WorkspaceMode, string(task.CallbackMode),
		string(task.Status), task.CurrentStep, task.CurrentTool, task.ProgressMessage, task.PendingQuestion,
		task.ResultSummary, task.ArtifactsJSON(), task.ErrorMessage, task.Usage.TotalTokens, task.DurationSeconds,
		timeToMilli(task.CreatedAt), timeToMilli(task.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("failed to save subagent task %s: %w", task.ID, err)
	}

	return nil
}

// UpdateSubagentTaskProgress updates live step, tool, and progress message during execution.
func (s *SQLiteStore) UpdateSubagentTaskProgress(ctx context.Context, id string, step int, tool, progressMsg string) error {
	nowMs := timeToMilli(time.Now())
	query := `
		UPDATE subagent_tasks
		SET status = 'RUNNING',
		    current_step = ?,
		    current_tool = ?,
		    progress_message = ?,
		    updated_at = ?
		WHERE id = ?
	`
	_, err := s.writer().ExecContext(ctx, query, step, tool, progressMsg, nowMs, id)
	if err != nil {
		return fmt.Errorf("failed to update subagent task progress %s: %w", id, err)
	}
	return nil
}

// UpdateSubagentTaskWaitingInput sets task status to WAITING_FOR_INPUT and records the pending question.
func (s *SQLiteStore) UpdateSubagentTaskWaitingInput(ctx context.Context, id string, question, subConvID string) error {
	nowMs := timeToMilli(time.Now())
	query := `
		UPDATE subagent_tasks
		SET status = 'WAITING_FOR_INPUT',
		    pending_question = ?,
		    sub_conversation_id = CASE WHEN ? != '' THEN ? ELSE sub_conversation_id END,
		    updated_at = ?
		WHERE id = ?
	`
	_, err := s.writer().ExecContext(ctx, query, question, subConvID, subConvID, nowMs, id)
	if err != nil {
		return fmt.Errorf("failed to update subagent task waiting input %s: %w", id, err)
	}
	return nil
}

// UpdateSubagentTaskCompleted marks a task as COMPLETED with distilled summary and token metrics.
func (s *SQLiteStore) UpdateSubagentTaskCompleted(ctx context.Context, id string, resultSummary, artifactsJSON string, usage domain.TokenUsage, durationSec float64) error {
	nowMs := timeToMilli(time.Now())
	totalTokens := usage.TotalTokens
	if totalTokens == 0 {
		totalTokens = usage.InputTokens + usage.OutputTokens + usage.ThinkingTokens
	}

	query := `
		UPDATE subagent_tasks
		SET status = 'COMPLETED',
		    result_summary = ?,
		    artifacts_json = ?,
		    total_tokens = ?,
		    duration_seconds = ?,
		    pending_question = '',
		    updated_at = ?
		WHERE id = ?
	`
	_, err := s.writer().ExecContext(ctx, query, resultSummary, artifactsJSON, totalTokens, durationSec, nowMs, id)
	if err != nil {
		return fmt.Errorf("failed to mark subagent task completed %s: %w", id, err)
	}
	return nil
}

// UpdateSubagentTaskFailed marks a task as FAILED with error message.
func (s *SQLiteStore) UpdateSubagentTaskFailed(ctx context.Context, id string, errMsg string, durationSec float64) error {
	nowMs := timeToMilli(time.Now())
	query := `
		UPDATE subagent_tasks
		SET status = 'FAILED',
		    error_message = ?,
		    duration_seconds = ?,
		    updated_at = ?
		WHERE id = ?
	`
	_, err := s.writer().ExecContext(ctx, query, errMsg, durationSec, nowMs, id)
	if err != nil {
		return fmt.Errorf("failed to mark subagent task failed %s: %w", id, err)
	}
	return nil
}

// UpdateSubagentTaskCancelled marks a task as CANCELLED.
func (s *SQLiteStore) UpdateSubagentTaskCancelled(ctx context.Context, id string) error {
	nowMs := timeToMilli(time.Now())
	query := `
		UPDATE subagent_tasks
		SET status = 'CANCELLED',
		    updated_at = ?
		WHERE id = ?
	`
	_, err := s.writer().ExecContext(ctx, query, nowMs, id)
	if err != nil {
		return fmt.Errorf("failed to cancel subagent task %s: %w", id, err)
	}
	return nil
}

// PurgeSubagentTasks deletes completed/failed/cancelled tasks older than specified days.
func (s *SQLiteStore) PurgeSubagentTasks(ctx context.Context, olderThanDays int) (int64, error) {
	if olderThanDays < 0 {
		olderThanDays = 14
	}

	cutoff := time.Now().AddDate(0, 0, -olderThanDays)
	if olderThanDays == 0 {
		cutoff = time.Now().Add(time.Second)
	}
	cutoffMs := timeToMilli(cutoff)

	query := `
		DELETE FROM subagent_tasks
		WHERE status IN ('COMPLETED', 'FAILED', 'CANCELLED') AND updated_at < ?
	`

	res, err := s.writer().ExecContext(ctx, query, cutoffMs)
	if err != nil {
		return 0, fmt.Errorf("failed to purge old subagent tasks: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}

	return affected, nil
}
