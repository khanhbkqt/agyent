package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

// GetConversation retrieves a conversation by its UUID.
func (s *SQLiteStore) GetConversation(ctx context.Context, id string) (*domain.Conversation, error) {
	if strings.TrimSpace(id) == "" {
		return nil, errors.New("conversation id cannot be empty")
	}

	query := `
		SELECT id, session_key, agent_name, project_name, title, alias_index, turn_count,
		       is_pinned, is_archived, last_reflected_step, created_at, updated_at
		FROM conversations
		WHERE id = ?
	`

	var (
		conv          domain.Conversation
		isPinnedInt   int
		isArchivedInt int
		createdAt     FlexTime
		updatedAt     FlexTime
	)

	err := s.reader().QueryRowContext(ctx, query, id).Scan(
		&conv.ID, &conv.SessionKey, &conv.AgentName, &conv.ProjectName,
		&conv.Title, &conv.AliasIndex, &conv.TurnCount,
		&isPinnedInt, &isArchivedInt, &conv.LastReflectedStep,
		&createdAt, &updatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: conversation %s", ports.ErrNotFound, id)
		}
		return nil, fmt.Errorf("failed to query conversation %s: %w", id, err)
	}

	conv.IsPinned = (isPinnedInt == 1)
	conv.IsArchived = (isArchivedInt == 1)
	conv.CreatedAt = createdAt.Time
	conv.UpdatedAt = updatedAt.Time

	return &conv, nil
}

// GetConversationScoped retrieves a conversation by its UUID within a verified scope.
func (s *SQLiteStore) GetConversationScoped(ctx context.Context, scope domain.ConversationScope, id string) (*domain.Conversation, error) {
	if strings.TrimSpace(id) == "" {
		return nil, errors.New("conversation id cannot be empty")
	}

	query := `
		SELECT id, session_key, agent_name, project_name, title, alias_index, turn_count,
		       is_pinned, is_archived, last_reflected_step, created_at, updated_at
		FROM conversations
		WHERE id = ? AND session_key = ? AND agent_name = ? AND project_name = ?
	`

	var (
		conv          domain.Conversation
		isPinnedInt   int
		isArchivedInt int
		createdAt     FlexTime
		updatedAt     FlexTime
	)

	err := s.reader().QueryRowContext(ctx, query, id, scope.SessionKey, scope.AgentName, scope.ProjectName).Scan(
		&conv.ID, &conv.SessionKey, &conv.AgentName, &conv.ProjectName,
		&conv.Title, &conv.AliasIndex, &conv.TurnCount,
		&isPinnedInt, &isArchivedInt, &conv.LastReflectedStep,
		&createdAt, &updatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: conversation %s in scope", ports.ErrNotFound, id)
		}
		return nil, fmt.Errorf("failed to query conversation %s: %w", id, err)
	}

	conv.IsPinned = (isPinnedInt == 1)
	conv.IsArchived = (isArchivedInt == 1)
	conv.CreatedAt = createdAt.Time
	conv.UpdatedAt = updatedAt.Time

	return &conv, nil
}

// GetConversationByAlias retrieves the N-th active conversation (1-indexed) ordered by [is_pinned DESC, updated_at DESC].
func (s *SQLiteStore) GetConversationByAlias(ctx context.Context, sessionKey, agentName, projectName string, aliasIndex int) (*domain.Conversation, error) {
	if aliasIndex <= 0 {
		return nil, fmt.Errorf("invalid alias index %d: must be 1 or greater", aliasIndex)
	}

	offset := aliasIndex - 1
	query := `
		SELECT id, session_key, agent_name, project_name, title, alias_index, turn_count,
		       is_pinned, is_archived, last_reflected_step, created_at, updated_at
		FROM conversations
		WHERE session_key = ? AND agent_name = ? AND project_name = ? AND is_archived = 0
		ORDER BY is_pinned DESC, updated_at DESC
		LIMIT 1 OFFSET ?
	`

	var (
		cID, sessKey, agent, proj, title string
		aliasIdx, turnCount, lastRefStep int
		isPinned, isArchived             int
		createdAt, updatedAt             FlexTime
	)

	err := s.reader().QueryRowContext(ctx, query, sessionKey, agentName, projectName, offset).Scan(
		&cID, &sessKey, &agent, &proj, &title, &aliasIdx, &turnCount,
		&isPinned, &isArchived, &lastRefStep, &createdAt, &updatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: conversation #%d not found", ports.ErrNotFound, aliasIndex)
		}
		return nil, fmt.Errorf("failed to query conversation #%d: %w", aliasIndex, err)
	}

	return &domain.Conversation{
		ID:                cID,
		SessionKey:        sessKey,
		AgentName:         agent,
		ProjectName:       proj,
		Title:             title,
		AliasIndex:        aliasIndex,
		TurnCount:         turnCount,
		IsPinned:          isPinned == 1,
		IsArchived:        isArchived == 1,
		LastReflectedStep: lastRefStep,
		CreatedAt:         createdAt.Time,
		UpdatedAt:         updatedAt.Time,
	}, nil
}

// ListRecentConversations returns active (non-archived) conversations for a scope, ordered by pinned first then recently updated.
func (s *SQLiteStore) ListRecentConversations(ctx context.Context, sessionKey, agentName, projectName string, limit int, offset int) ([]domain.Conversation, int, error) {
	if limit <= 0 {
		limit = 10
	}
	if offset < 0 {
		offset = 0
	}

	whereClauses := []string{"is_archived = 0"}
	var args []any

	if strings.TrimSpace(sessionKey) != "" {
		whereClauses = append(whereClauses, "session_key = ?")
		args = append(args, sessionKey)
	}
	if strings.TrimSpace(agentName) != "" {
		whereClauses = append(whereClauses, "agent_name = ?")
		args = append(args, agentName)
	}
	if strings.TrimSpace(projectName) != "" {
		whereClauses = append(whereClauses, "project_name = ?")
		args = append(args, projectName)
	}

	whereSQL := strings.Join(whereClauses, " AND ")

	countQuery := "SELECT COUNT(*) FROM conversations WHERE " + whereSQL
	var totalCount int
	if err := s.reader().QueryRowContext(ctx, countQuery, args...).Scan(&totalCount); err != nil {
		return nil, 0, fmt.Errorf("failed to count active conversations: %w", err)
	}

	query := fmt.Sprintf(`
		SELECT id, session_key, agent_name, project_name, title, alias_index, turn_count,
		       is_pinned, is_archived, last_reflected_step, created_at, updated_at
		FROM conversations
		WHERE %s
		ORDER BY is_pinned DESC, updated_at DESC
		LIMIT ? OFFSET ?
	`, whereSQL)

	queryArgs := append(append([]any{}, args...), limit, offset)
	rows, err := s.reader().QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list recent conversations: %w", err)
	}
	defer rows.Close()

	var result []domain.Conversation
	currentIndex := offset + 1
	for rows.Next() {
		var (
			cID, sessKey, agent, proj, title string
			aliasIdx, turnCount, lastRefStep int
			isPinned, isArchived             int
			createdAt, updatedAt             FlexTime
		)

		if err := rows.Scan(
			&cID, &sessKey, &agent, &proj, &title, &aliasIdx, &turnCount,
			&isPinned, &isArchived, &lastRefStep, &createdAt, &updatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("failed to scan conversation row: %w", err)
		}

		result = append(result, domain.Conversation{
			ID:                cID,
			SessionKey:        sessKey,
			AgentName:         agent,
			ProjectName:       proj,
			Title:             title,
			AliasIndex:        currentIndex,
			TurnCount:         turnCount,
			IsPinned:          isPinned == 1,
			IsArchived:        isArchived == 1,
			LastReflectedStep: lastRefStep,
			CreatedAt:         createdAt.Time,
			UpdatedAt:         updatedAt.Time,
		})
		currentIndex++
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("error iterating conversation rows: %w", err)
	}

	return result, totalCount, nil
}

// SaveConversation inserts or updates a full conversation record.
func (s *SQLiteStore) SaveConversation(ctx context.Context, conv *domain.Conversation) error {
	if conv == nil || conv.ID == "" {
		return errors.New("cannot save nil or empty conversation")
	}

	now := time.Now()
	if conv.CreatedAt.IsZero() {
		conv.CreatedAt = now
	}
	if conv.UpdatedAt.IsZero() {
		conv.UpdatedAt = now
	}

	query := `
		INSERT INTO conversations (
			id, session_key, agent_name, project_name, title, alias_index, turn_count,
			is_pinned, is_archived, last_reflected_step, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			session_key = excluded.session_key,
			agent_name = excluded.agent_name,
			project_name = excluded.project_name,
			title = excluded.title,
			alias_index = excluded.alias_index,
			turn_count = excluded.turn_count,
			is_pinned = excluded.is_pinned,
			is_archived = excluded.is_archived,
			last_reflected_step = excluded.last_reflected_step,
			updated_at = excluded.updated_at
	`

	isPinnedInt := 0
	if conv.IsPinned {
		isPinnedInt = 1
	}
	isArchivedInt := 0
	if conv.IsArchived {
		isArchivedInt = 1
	}

	_, err := s.writer().ExecContext(
		ctx, query,
		conv.ID, conv.SessionKey, conv.AgentName, conv.ProjectName, conv.Title, conv.AliasIndex, conv.TurnCount,
		isPinnedInt, isArchivedInt, conv.LastReflectedStep, timeToMilli(conv.CreatedAt), timeToMilli(conv.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("failed to save conversation %s: %w", conv.ID, err)
	}

	return nil
}

// UpdateConversationReflectedStep updates the last_reflected_step cursor for incremental evolution.
func (s *SQLiteStore) UpdateConversationReflectedStep(ctx context.Context, id string, step int) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("conversation id cannot be empty")
	}

	nowMs := timeToMilli(time.Now())
	query := `UPDATE conversations SET last_reflected_step = ?, updated_at = ? WHERE id = ?`
	res, err := s.writer().ExecContext(ctx, query, step, nowMs, id)
	if err != nil {
		return fmt.Errorf("failed to update last_reflected_step for conversation %s: %w", id, err)
	}

	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("%w: conversation %s", ports.ErrNotFound, id)
	}

	return nil
}

// TouchConversation updates turn count, timestamps, unarchives if needed, and auto-generates a title on first turn.
func (s *SQLiteStore) TouchConversation(ctx context.Context, sessionKey, agentName, projectName, convID, promptSnippet string) error {
	if strings.TrimSpace(convID) == "" {
		return nil
	}

	now := time.Now()
	nowMs := timeToMilli(now)

	// Check if conversation exists (Read pool)
	var existingTitle string
	var currentTurnCount int
	err := s.reader().QueryRowContext(ctx, "SELECT title, turn_count FROM conversations WHERE id = ?", convID).Scan(&existingTitle, &currentTurnCount)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Generate auto-title
			title := generateAutoTitle(promptSnippet)
			insertQuery := `
				INSERT INTO conversations (
					id, session_key, agent_name, project_name, title, alias_index, turn_count,
					is_pinned, is_archived, created_at, updated_at
				) VALUES (?, ?, ?, ?, ?, 1, 1, 0, 0, ?, ?)
			`
			_, err = s.writer().ExecContext(ctx, insertQuery, convID, sessionKey, agentName, projectName, title, nowMs, nowMs)
			if err != nil {
				return fmt.Errorf("failed to insert new touched conversation %s: %w", convID, err)
			}
			return nil
		}
		return fmt.Errorf("failed to check conversation %s: %w", convID, err)
	}

	// Existing conversation: increment turn_count, unarchive, update timestamp (Write pool)
	updateQuery := `
		UPDATE conversations
		SET turn_count = turn_count + 1,
		    is_archived = 0,
		    updated_at = ?
		WHERE id = ?
	`
	_, err = s.writer().ExecContext(ctx, updateQuery, nowMs, convID)
	if err != nil {
		return fmt.Errorf("failed to update touched conversation %s: %w", convID, err)
	}

	return nil
}

// SetConversationPinned toggles the pinned status of a conversation.
func (s *SQLiteStore) SetConversationPinned(ctx context.Context, id string, isPinned bool) error {
	pinnedInt := 0
	if isPinned {
		pinnedInt = 1
	}

	nowMs := timeToMilli(time.Now())
	res, err := s.writer().ExecContext(ctx, "UPDATE conversations SET is_pinned = ?, updated_at = ? WHERE id = ?", pinnedInt, nowMs, id)
	if err != nil {
		return fmt.Errorf("failed to update pinned status for %s: %w", id, err)
	}

	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("%w: conversation %s", ports.ErrNotFound, id)
	}

	return nil
}

// SetConversationArchived toggles the archived status of a conversation.
func (s *SQLiteStore) SetConversationArchived(ctx context.Context, id string, isArchived bool) error {
	archivedInt := 0
	if isArchived {
		archivedInt = 1
	}

	nowMs := timeToMilli(time.Now())
	res, err := s.writer().ExecContext(ctx, "UPDATE conversations SET is_archived = ?, updated_at = ? WHERE id = ?", archivedInt, nowMs, id)
	if err != nil {
		return fmt.Errorf("failed to update archived status for %s: %w", id, err)
	}

	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("%w: conversation %s", ports.ErrNotFound, id)
	}

	return nil
}

// SetConversationTitle updates the human-readable title of a conversation.
func (s *SQLiteStore) SetConversationTitle(ctx context.Context, id string, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return errors.New("conversation title cannot be empty")
	}

	nowMs := timeToMilli(time.Now())
	res, err := s.writer().ExecContext(ctx, "UPDATE conversations SET title = ?, updated_at = ? WHERE id = ?", title, nowMs, id)
	if err != nil {
		return fmt.Errorf("failed to update title for %s: %w", id, err)
	}

	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("%w: conversation %s", ports.ErrNotFound, id)
	}

	return nil
}

// SetConversationPinnedScoped toggles the pinned status of a conversation within a verified scope.
func (s *SQLiteStore) SetConversationPinnedScoped(ctx context.Context, scope domain.ConversationScope, id string, isPinned bool) error {
	pinnedInt := 0
	if isPinned {
		pinnedInt = 1
	}

	nowMs := timeToMilli(time.Now())
	res, err := s.writer().ExecContext(ctx, "UPDATE conversations SET is_pinned = ?, updated_at = ? WHERE id = ? AND session_key = ? AND agent_name = ? AND project_name = ?", pinnedInt, nowMs, id, scope.SessionKey, scope.AgentName, scope.ProjectName)
	if err != nil {
		return fmt.Errorf("failed to update pinned status for %s: %w", id, err)
	}

	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("%w: conversation %s in scope", ports.ErrNotFound, id)
	}

	return nil
}

// SetConversationArchivedScoped toggles the archived status of a conversation within a verified scope.
func (s *SQLiteStore) SetConversationArchivedScoped(ctx context.Context, scope domain.ConversationScope, id string, isArchived bool) error {
	archivedInt := 0
	if isArchived {
		archivedInt = 1
	}

	nowMs := timeToMilli(time.Now())
	res, err := s.writer().ExecContext(ctx, "UPDATE conversations SET is_archived = ?, updated_at = ? WHERE id = ? AND session_key = ? AND agent_name = ? AND project_name = ?", archivedInt, nowMs, id, scope.SessionKey, scope.AgentName, scope.ProjectName)
	if err != nil {
		return fmt.Errorf("failed to update archived status for %s: %w", id, err)
	}

	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("%w: conversation %s in scope", ports.ErrNotFound, id)
	}

	return nil
}

// SetConversationTitleScoped updates the human-readable title of a conversation within a verified scope.
func (s *SQLiteStore) SetConversationTitleScoped(ctx context.Context, scope domain.ConversationScope, id string, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return errors.New("conversation title cannot be empty")
	}

	nowMs := timeToMilli(time.Now())
	res, err := s.writer().ExecContext(ctx, "UPDATE conversations SET title = ?, updated_at = ? WHERE id = ? AND session_key = ? AND agent_name = ? AND project_name = ?", title, nowMs, id, scope.SessionKey, scope.AgentName, scope.ProjectName)
	if err != nil {
		return fmt.Errorf("failed to update title for %s: %w", id, err)
	}

	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("%w: conversation %s in scope", ports.ErrNotFound, id)
	}

	return nil
}

// DeleteConversationScoped permanently deletes a conversation within a verified scope.
func (s *SQLiteStore) DeleteConversationScoped(ctx context.Context, scope domain.ConversationScope, id string) error {
	res, err := s.writer().ExecContext(ctx, "DELETE FROM conversations WHERE id = ? AND session_key = ? AND agent_name = ? AND project_name = ?", id, scope.SessionKey, scope.AgentName, scope.ProjectName)
	if err != nil {
		return fmt.Errorf("failed to delete conversation %s: %w", id, err)
	}

	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("%w: conversation %s in scope", ports.ErrNotFound, id)
	}

	return nil
}

// GetExpiredArchivedConversationIDs retrieves IDs of archived, non-pinned conversations older than olderThanDays.
func (s *SQLiteStore) GetExpiredArchivedConversationIDs(ctx context.Context, olderThanDays int) ([]string, error) {
	if olderThanDays < 0 {
		olderThanDays = 30
	}

	cutoff := time.Now().AddDate(0, 0, -olderThanDays)
	if olderThanDays == 0 {
		cutoff = time.Now().Add(time.Second)
	}
	cutoffMs := timeToMilli(cutoff)

	query := `
		SELECT id
		FROM conversations
		WHERE is_archived = 1 AND is_pinned = 0 AND updated_at < ?
	`

	rows, err := s.reader().QueryContext(ctx, query, cutoffMs)
	if err != nil {
		return nil, fmt.Errorf("failed to query expired conversation IDs: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan expired conversation id: %w", err)
		}
		ids = append(ids, id)
	}

	return ids, nil
}

// PurgeConversations permanently deletes conversation rows from the database.
func (s *SQLiteStore) PurgeConversations(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	// SQLite parameter placeholders
	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf("DELETE FROM conversations WHERE id IN (%s)", strings.Join(placeholders, ","))
	_, err := s.writer().ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to purge conversations: %w", err)
	}

	return nil
}

// generateAutoTitle derives a human-friendly title from the first prompt snippet.
func generateAutoTitle(snippet string) string {
	clean := strings.TrimSpace(snippet)
	if clean == "" {
		return "Cuộc trò chuyện mới"
	}

	// Check for system initialization directives
	if strings.Contains(clean, "[SYSTEM DIRECTIVE: NEW CONVERSATION INITIALIZATION]") {
		if idx := strings.Index(clean, "topic/goal: \""); idx != -1 {
			sub := clean[idx+len("topic/goal: \""):]
			if endIdx := strings.Index(sub, "\""); endIdx != -1 {
				clean = strings.TrimSpace(sub[:endIdx])
			} else {
				clean = "Cuộc trò chuyện mới"
			}
		} else {
			clean = "Cuộc trò chuyện mới"
		}
	} else if strings.Contains(clean, "[SYSTEM BOOTSTRAP PROTOCOL") {
		clean = "Genesis Bootstrap"
	}

	// Take first line only
	if idx := strings.IndexAny(clean, "\r\n"); idx != -1 {
		clean = strings.TrimSpace(clean[:idx])
	}

	// Remove leading slash commands if present
	if strings.HasPrefix(clean, "/") {
		parts := strings.SplitN(clean, " ", 2)
		if len(parts) > 1 {
			clean = strings.TrimSpace(parts[1])
		}
	}

	if clean == "" {
		return "Cuộc trò chuyện mới"
	}

	// Truncate to 45 runes with ellipsis
	runes := []rune(clean)
	if len(runes) > 45 {
		return string(runes[:42]) + "..."
	}

	// Ensure valid UTF-8
	if !utf8.ValidString(clean) {
		return "Cuộc trò chuyện mới"
	}

	return clean
}
