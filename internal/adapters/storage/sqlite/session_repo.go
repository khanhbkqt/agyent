package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

// GetSession retrieves an active session by its unique session key.
// If an active project is set, it automatically joins and populates ProjectConversationID.
// Returns nil and ports.ErrSessionNotFound if the session does not exist.
func (s *SQLiteStore) GetSession(ctx context.Context, key string) (*domain.Session, error) {
	query := `
		SELECT s.session_key, s.active_agent, s.active_project, s.global_conversation_id,
		       COALESCE(s.active_model, '') AS active_model,
		       COALESCE(s.active_effort, '') AS active_effort,
		       s.updated_at,
		       COALESCE(spc.conversation_id, '') AS project_conversation_id
		FROM sessions s
		LEFT JOIN session_project_conversations spc
		       ON s.active_project != ''
		      AND spc.session_key = s.session_key
		      AND spc.project_id = (s.active_agent || ':' || s.active_project)
		WHERE s.session_key = ?
	`
	var (
		sessKey      string
		agent        string
		proj         string
		globalCID    string
		activeModel  string
		activeEffort string
		updatedAt    FlexTime
		projectCID   string
	)

	err := s.reader().QueryRowContext(ctx, query, key).Scan(
		&sessKey, &agent, &proj, &globalCID, &activeModel, &activeEffort, &updatedAt, &projectCID,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: %s", ports.ErrSessionNotFound, key)
		}
		return nil, fmt.Errorf("failed to query session %s: %w", key, err)
	}

	return &domain.Session{
		SessionKey:            sessKey,
		ActiveAgent:           agent,
		ActiveProject:         proj,
		ActiveModel:           activeModel,
		ActiveEffort:          activeEffort,
		GlobalConversationID:  globalCID,
		ProjectConversationID: projectCID,
		UpdatedAt:             updatedAt.Time,
	}, nil
}

// GetOrCreateSession retrieves an existing session or initializes a new one with defaultAgent in Global mode.
func (s *SQLiteStore) GetOrCreateSession(ctx context.Context, key string, defaultAgent string) (*domain.Session, error) {
	sess, err := s.GetSession(ctx, key)
	if err == nil {
		return sess, nil
	}
	if !errors.Is(err, ports.ErrNotFound) {
		return nil, err
	}

	newSession := &domain.Session{
		SessionKey:            key,
		ActiveAgent:           defaultAgent,
		ActiveProject:         "",
		ActiveModel:           "",
		ActiveEffort:          "",
		GlobalConversationID:  "",
		ProjectConversationID: "",
		UpdatedAt:             time.Now(),
	}

	if err := s.SaveSession(ctx, newSession); err != nil {
		return nil, fmt.Errorf("failed to create default session: %w", err)
	}

	return newSession, nil
}

// SaveSession inserts or updates an interactive session state in an atomic transaction.
// If in project mode and ProjectConversationID is set, it atomically synchronizes session_project_conversations.
func (s *SQLiteStore) SaveSession(ctx context.Context, session *domain.Session) error {
	if session == nil {
		return errors.New("cannot save nil session")
	}

	tx, err := s.writer().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	updatedAt := session.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now()
	}
	updatedAtMs := timeToMilli(updatedAt)

	query := `
		INSERT INTO sessions (
			session_key, active_agent, active_project, global_conversation_id, active_model, active_effort, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_key) DO UPDATE SET
			active_agent = excluded.active_agent,
			active_project = excluded.active_project,
			global_conversation_id = excluded.global_conversation_id,
			active_model = excluded.active_model,
			active_effort = excluded.active_effort,
			updated_at = excluded.updated_at
	`
	_, err = tx.ExecContext(
		ctx, query,
		session.SessionKey, session.ActiveAgent, session.ActiveProject, session.GlobalConversationID,
		session.ActiveModel, session.ActiveEffort, updatedAtMs,
	)
	if err != nil {
		return fmt.Errorf("failed to upsert session %s: %w", session.SessionKey, err)
	}

	// If in Project Mode, atomically synchronize or clear project conversation mapping
	if session.ActiveProject != "" {
		projectID := domain.FormatProjectID(session.ActiveAgent, session.ActiveProject)
		if session.ProjectConversationID != "" {
			projConvQuery := `
				INSERT INTO session_project_conversations (
					session_key, project_id, conversation_id, updated_at
				) VALUES (?, ?, ?, ?)
				ON CONFLICT(session_key, project_id) DO UPDATE SET
					conversation_id = excluded.conversation_id,
					updated_at = excluded.updated_at
			`
			_, err = tx.ExecContext(ctx, projConvQuery, session.SessionKey, projectID, session.ProjectConversationID, updatedAtMs)
			if err != nil {
				return fmt.Errorf("failed to upsert project conversation mapping for %s: %w", projectID, err)
			}
		} else {
			delQuery := `DELETE FROM session_project_conversations WHERE session_key = ? AND project_id = ?`
			if _, err = tx.ExecContext(ctx, delQuery, session.SessionKey, projectID); err != nil {
				return fmt.Errorf("failed to clear project conversation mapping for %s: %w", projectID, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit session save transaction: %w", err)
	}

	return nil
}

// DeleteSession deletes a session and cascades deletes to project conversations.
func (s *SQLiteStore) DeleteSession(ctx context.Context, key string) error {
	query := `DELETE FROM sessions WHERE session_key = ?`
	res, err := s.writer().ExecContext(ctx, query, key)
	if err != nil {
		return fmt.Errorf("failed to delete session %s: %w", key, err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("%w: session %s", ports.ErrSessionNotFound, key)
	}
	return nil
}

// GetProjectConversationID fetches the isolated conversation ID for a specific session and project.
func (s *SQLiteStore) GetProjectConversationID(ctx context.Context, sessionKey, projectID string) (string, error) {
	query := `
		SELECT conversation_id
		FROM session_project_conversations
		WHERE session_key = ? AND project_id = ?
	`
	var convID string
	err := s.reader().QueryRowContext(ctx, query, sessionKey, projectID).Scan(&convID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("failed to get project conversation ID: %w", err)
	}
	return convID, nil
}

// SetProjectConversationID stores or updates the conversation ID for a specific session and project.
func (s *SQLiteStore) SetProjectConversationID(ctx context.Context, sessionKey, projectID, convID string) error {
	query := `
		INSERT INTO session_project_conversations (session_key, project_id, conversation_id, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(session_key, project_id) DO UPDATE SET
			conversation_id = excluded.conversation_id,
			updated_at = excluded.updated_at
	`
	_, err := s.writer().ExecContext(ctx, query, sessionKey, projectID, convID, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("failed to set project conversation ID: %w", err)
	}
	return nil
}

// ClearProjectConversationID deletes the conversation mapping for a specific session and project.
func (s *SQLiteStore) ClearProjectConversationID(ctx context.Context, sessionKey, projectID string) error {
	query := `DELETE FROM session_project_conversations WHERE session_key = ? AND project_id = ?`
	_, err := s.writer().ExecContext(ctx, query, sessionKey, projectID)
	if err != nil {
		return fmt.Errorf("failed to clear project conversation ID: %w", err)
	}
	return nil
}
