package sqlite

import (
	"context"
	"errors"
	"fmt"
	"time"

	"agyent/internal/core/domain"
)

// LogAudit persists an execution audit log entry and populates log.ID with the generated ID.
func (s *SQLiteStore) LogAudit(ctx context.Context, log *domain.AuditLog) error {
	if log == nil {
		return errors.New("cannot log nil audit entry")
	}

	query := `
		INSERT INTO audit_logs (
			session_key, agent_name, project_name, conversation_id,
			prompt_length, response_length, duration_seconds,
			input_tokens, output_tokens, thinking_tokens, cache_read_tokens, total_tokens,
			status, error_message, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	createdAt := log.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}

	status := log.Status
	if status == "" {
		status = "SUCCESS"
	}

	res, err := s.writer().ExecContext(ctx, query,
		log.SessionKey,
		log.AgentName,
		log.ProjectName,
		log.ConversationID,
		log.PromptLength,
		log.ResponseLength,
		log.DurationSeconds,
		log.Usage.InputTokens,
		log.Usage.OutputTokens,
		log.Usage.ThinkingTokens,
		log.Usage.CacheReadTokens,
		log.Usage.TotalTokens,
		status,
		log.ErrorMessage,
		timeToMilli(createdAt),
	)
	if err != nil {
		return fmt.Errorf("failed to insert audit log: %w", err)
	}

	id, err := res.LastInsertId()
	if err == nil {
		log.ID = id
	}

	return nil
}

// ListAuditLogs retrieves recent audit logs for a given session key, ordered by created_at DESC.
func (s *SQLiteStore) ListAuditLogs(ctx context.Context, sessionKey string, limit int) ([]domain.AuditLog, error) {
	if limit <= 0 {
		limit = 50
	}

	query := `
		SELECT id, session_key, agent_name, project_name, conversation_id,
		       prompt_length, response_length, duration_seconds,
		       input_tokens, output_tokens, thinking_tokens, cache_read_tokens, total_tokens,
		       status, error_message, created_at
		FROM audit_logs
		WHERE session_key = ?
		ORDER BY created_at DESC, id DESC
		LIMIT ?
	`
	rows, err := s.reader().QueryContext(ctx, query, sessionKey, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list audit logs for session %s: %w", sessionKey, err)
	}
	defer rows.Close()

	var logs = make([]domain.AuditLog, 0)
	for rows.Next() {
		var (
			id              int64
			sessKey         string
			agentName       string
			projName        string
			convID          string
			promptLen       int
			respLen         int
			durationSec     float64
			inputTokens     int
			outputTokens    int
			thinkingTokens  int
			cacheReadTokens int
			totalTokens     int
			status          string
			errorMsg        string
			createdAt       FlexTime
		)

		err := rows.Scan(
			&id, &sessKey, &agentName, &projName, &convID,
			&promptLen, &respLen, &durationSec,
			&inputTokens, &outputTokens, &thinkingTokens, &cacheReadTokens, &totalTokens,
			&status, &errorMsg, &createdAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan audit log row: %w", err)
		}

		logs = append(logs, domain.AuditLog{
			ID:              id,
			SessionKey:      sessKey,
			AgentName:       agentName,
			ProjectName:     projName,
			ConversationID:  convID,
			PromptLength:    promptLen,
			ResponseLength:  respLen,
			DurationSeconds: durationSec,
			Usage: domain.TokenUsage{
				InputTokens:     inputTokens,
				OutputTokens:    outputTokens,
				ThinkingTokens:  thinkingTokens,
				CacheReadTokens: cacheReadTokens,
				TotalTokens:     totalTokens,
			},
			Status:       status,
			ErrorMessage: errorMsg,
			CreatedAt:    createdAt.Time,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating audit logs: %w", err)
	}

	return logs, nil
}

// GetTokenStats computes aggregated token metrics for a session and optional conversation ID.
func (s *SQLiteStore) GetTokenStats(ctx context.Context, sessionKey string, convID string) (*domain.TokenUsage, error) {
	var query string
	var args []any

	if convID != "" {
		query = `
			SELECT COALESCE(SUM(input_tokens), 0),
			       COALESCE(SUM(output_tokens), 0),
			       COALESCE(SUM(thinking_tokens), 0),
			       COALESCE(SUM(cache_read_tokens), 0),
			       COALESCE(SUM(total_tokens), 0)
			FROM audit_logs
			WHERE session_key = ? AND conversation_id = ?
		`
		args = []any{sessionKey, convID}
	} else {
		query = `
			SELECT COALESCE(SUM(input_tokens), 0),
			       COALESCE(SUM(output_tokens), 0),
			       COALESCE(SUM(thinking_tokens), 0),
			       COALESCE(SUM(cache_read_tokens), 0),
			       COALESCE(SUM(total_tokens), 0)
			FROM audit_logs
			WHERE session_key = ?
		`
		args = []any{sessionKey}
	}

	var usage domain.TokenUsage
	err := s.reader().QueryRowContext(ctx, query, args...).Scan(
		&usage.InputTokens,
		&usage.OutputTokens,
		&usage.ThinkingTokens,
		&usage.CacheReadTokens,
		&usage.TotalTokens,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to compute token stats: %w", err)
	}

	return &usage, nil
}

