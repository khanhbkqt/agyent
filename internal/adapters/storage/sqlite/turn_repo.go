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

// SaveInFlightTurn inserts or updates an in-flight conversation turn record.
func (s *SQLiteStore) SaveInFlightTurn(ctx context.Context, turn *domain.InFlightTurn) error {
	if turn == nil || strings.TrimSpace(turn.TurnID) == "" {
		return errors.New("cannot save nil or empty in-flight turn")
	}

	now := time.Now()
	if turn.CreatedAt.IsZero() {
		turn.CreatedAt = now
	}
	if turn.UpdatedAt.IsZero() {
		turn.UpdatedAt = now
	}
	if turn.Status == "" {
		turn.Status = domain.TurnStatusPending
	}
	if turn.MaxRetries <= 0 {
		turn.MaxRetries = 1
	}
	if turn.RecoveryMode == "" {
		turn.RecoveryMode = "auto"
	}

	query := `
		INSERT INTO in_flight_turns (
			turn_id, session_key, conversation_id, agent_name, project_name,
			channel, chat_id, thread_id, inbound_message_id, bot_id,
			user_id, user_name, prompt, is_ephemeral, status,
			retry_count, max_retries, recovery_mode, error_message,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(turn_id) DO UPDATE SET
			session_key = excluded.session_key,
			conversation_id = excluded.conversation_id,
			agent_name = excluded.agent_name,
			project_name = excluded.project_name,
			channel = excluded.channel,
			chat_id = excluded.chat_id,
			thread_id = excluded.thread_id,
			inbound_message_id = excluded.inbound_message_id,
			bot_id = excluded.bot_id,
			user_id = excluded.user_id,
			user_name = excluded.user_name,
			prompt = excluded.prompt,
			is_ephemeral = excluded.is_ephemeral,
			status = excluded.status,
			retry_count = excluded.retry_count,
			max_retries = excluded.max_retries,
			recovery_mode = excluded.recovery_mode,
			error_message = excluded.error_message,
			updated_at = excluded.updated_at
	`

	ephemeralInt := 0
	if turn.IsEphemeral {
		ephemeralInt = 1
	}

	_, err := s.writer().ExecContext(
		ctx, query,
		turn.TurnID, turn.SessionKey, turn.ConversationID, turn.AgentName, turn.ProjectName,
		turn.Channel, turn.ChatID, turn.ThreadID, turn.InboundMessageID, turn.BotID,
		turn.UserID, turn.UserName, turn.Prompt, ephemeralInt, string(turn.Status),
		turn.RetryCount, turn.MaxRetries, turn.RecoveryMode, turn.ErrorMessage,
		timeToMilli(turn.CreatedAt), timeToMilli(turn.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("failed to save in-flight turn %s: %w", turn.TurnID, err)
	}

	return nil
}

// GetInFlightTurn retrieves a tracked in-flight turn by TurnID.
func (s *SQLiteStore) GetInFlightTurn(ctx context.Context, turnID string) (*domain.InFlightTurn, error) {
	if strings.TrimSpace(turnID) == "" {
		return nil, errors.New("turn ID cannot be empty")
	}

	query := `
		SELECT turn_id, session_key, conversation_id, agent_name, project_name,
		       channel, chat_id, thread_id, inbound_message_id, bot_id,
		       user_id, user_name, prompt, is_ephemeral, status,
		       retry_count, max_retries, recovery_mode, error_message,
		       created_at, updated_at
		FROM in_flight_turns
		WHERE turn_id = ?
	`

	var (
		t            domain.InFlightTurn
		statusStr    string
		ephemeralInt int
		createdAt    FlexTime
		updatedAt    FlexTime
	)

	err := s.reader().QueryRowContext(ctx, query, turnID).Scan(
		&t.TurnID, &t.SessionKey, &t.ConversationID, &t.AgentName, &t.ProjectName,
		&t.Channel, &t.ChatID, &t.ThreadID, &t.InboundMessageID, &t.BotID,
		&t.UserID, &t.UserName, &t.Prompt, &ephemeralInt, &statusStr,
		&t.RetryCount, &t.MaxRetries, &t.RecoveryMode, &t.ErrorMessage,
		&createdAt, &updatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: in-flight turn %s", ports.ErrNotFound, turnID)
		}
		return nil, fmt.Errorf("failed to query in-flight turn %s: %w", turnID, err)
	}

	t.Status = domain.TurnStatus(statusStr)
	t.IsEphemeral = (ephemeralInt == 1)
	t.CreatedAt = createdAt.Time
	t.UpdatedAt = updatedAt.Time

	return &t, nil
}

// UpdateInFlightTurnStatus atomically transitions the status and error message of a turn.
func (s *SQLiteStore) UpdateInFlightTurnStatus(ctx context.Context, turnID string, status domain.TurnStatus, errMsg string) error {
	if strings.TrimSpace(turnID) == "" {
		return errors.New("turn ID cannot be empty")
	}

	nowMs := timeToMilli(time.Now())
	query := `
		UPDATE in_flight_turns
		SET status = ?,
		    error_message = ?,
		    updated_at = ?
		WHERE turn_id = ?
	`
	res, err := s.writer().ExecContext(ctx, query, string(status), errMsg, nowMs, turnID)
	if err != nil {
		return fmt.Errorf("failed to update in-flight turn status %s: %w", turnID, err)
	}

	rows, err := res.RowsAffected()
	if err == nil && rows == 0 {
		return fmt.Errorf("%w: in-flight turn %s", ports.ErrNotFound, turnID)
	}

	return nil
}

// ListInterruptedTurns returns all non-terminal turns in EXECUTING, RECOVERING, or PENDING status.
func (s *SQLiteStore) ListInterruptedTurns(ctx context.Context) ([]domain.InFlightTurn, error) {
	query := `
		SELECT turn_id, session_key, conversation_id, agent_name, project_name,
		       channel, chat_id, thread_id, inbound_message_id, bot_id,
		       user_id, user_name, prompt, is_ephemeral, status,
		       retry_count, max_retries, recovery_mode, error_message,
		       created_at, updated_at
		FROM in_flight_turns
		WHERE status IN ('EXECUTING', 'RECOVERING', 'PENDING')
		ORDER BY created_at ASC
	`

	rows, err := s.reader().QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query interrupted in-flight turns: %w", err)
	}
	defer rows.Close()

	var results []domain.InFlightTurn
	for rows.Next() {
		var (
			t            domain.InFlightTurn
			statusStr    string
			ephemeralInt int
			createdAt    FlexTime
			updatedAt    FlexTime
		)

		if err := rows.Scan(
			&t.TurnID, &t.SessionKey, &t.ConversationID, &t.AgentName, &t.ProjectName,
			&t.Channel, &t.ChatID, &t.ThreadID, &t.InboundMessageID, &t.BotID,
			&t.UserID, &t.UserName, &t.Prompt, &ephemeralInt, &statusStr,
			&t.RetryCount, &t.MaxRetries, &t.RecoveryMode, &t.ErrorMessage,
			&createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan in-flight turn row: %w", err)
		}

		t.Status = domain.TurnStatus(statusStr)
		t.IsEphemeral = (ephemeralInt == 1)
		t.CreatedAt = createdAt.Time
		t.UpdatedAt = updatedAt.Time

		results = append(results, t)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating in-flight turn rows: %w", err)
	}

	return results, nil
}

// PurgeInFlightTurns deletes terminal turns (COMPLETED, FAILED) older than specified days.
func (s *SQLiteStore) PurgeInFlightTurns(ctx context.Context, retentionDays int) (int64, error) {
	if retentionDays < 0 {
		retentionDays = 7
	}

	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	if retentionDays == 0 {
		cutoff = time.Now().Add(time.Second)
	}
	cutoffMs := timeToMilli(cutoff)

	query := `
		DELETE FROM in_flight_turns
		WHERE status IN ('COMPLETED', 'FAILED') AND updated_at < ?
	`

	res, err := s.writer().ExecContext(ctx, query, cutoffMs)
	if err != nil {
		return 0, fmt.Errorf("failed to purge old in-flight turns: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}

	return affected, nil
}
