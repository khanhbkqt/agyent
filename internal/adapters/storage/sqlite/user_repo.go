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

// GetUser retrieves a user by ID.
func (s *SQLiteStore) GetUser(ctx context.Context, id string) (*domain.User, error) {
	query := `
		SELECT id, username, full_name, role, created_at
		FROM users
		WHERE id = ?
	`
	var (
		userID    string
		username  sql.NullString
		fullName  sql.NullString
		role      string
		createdAt FlexTime
	)

	err := s.reader().QueryRowContext(ctx, query, id).Scan(&userID, &username, &fullName, &role, &createdAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: %s", ports.ErrUserNotFound, id)
		}
		return nil, fmt.Errorf("failed to query user %s: %w", id, err)
	}

	return &domain.User{
		ID:        userID,
		Username:  fromNullString(username),
		FullName:  fromNullString(fullName),
		Role:      role,
		CreatedAt: createdAt.Time,
	}, nil
}

// ListUsers returns all registered users.
func (s *SQLiteStore) ListUsers(ctx context.Context) ([]domain.User, error) {
	query := `
		SELECT id, username, full_name, role, created_at
		FROM users
		ORDER BY created_at ASC
	`
	rows, err := s.reader().QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list users: %w", err)
	}
	defer rows.Close()

	var users = make([]domain.User, 0)
	for rows.Next() {
		var (
			userID    string
			username  sql.NullString
			fullName  sql.NullString
			role      string
			createdAt FlexTime
		)
		if err := rows.Scan(&userID, &username, &fullName, &role, &createdAt); err != nil {
			return nil, fmt.Errorf("failed to scan user row: %w", err)
		}
		users = append(users, domain.User{
			ID:        userID,
			Username:  fromNullString(username),
			FullName:  fromNullString(fullName),
			Role:      role,
			CreatedAt: createdAt.Time,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating user rows: %w", err)
	}

	return users, nil
}

// SaveUser creates or updates a user profile.
func (s *SQLiteStore) SaveUser(ctx context.Context, user *domain.User) error {
	if user == nil {
		return errors.New("cannot save nil user")
	}

	query := `
		INSERT INTO users (id, username, full_name, role, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			username = excluded.username,
			full_name = excluded.full_name,
			role = excluded.role
	`
	createdAt := user.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}

	role := user.Role
	if role == "" {
		role = "admin"
	}

	_, err := s.writer().ExecContext(ctx, query,
		user.ID,
		toNullString(user.Username),
		toNullString(user.FullName),
		role,
		timeToMilli(createdAt),
	)
	if err != nil {
		return fmt.Errorf("failed to save user %s: %w", user.ID, err)
	}

	return nil
}

// DeleteUser deletes a user from whitelist.
func (s *SQLiteStore) DeleteUser(ctx context.Context, id string) error {
	query := `DELETE FROM users WHERE id = ?`
	res, err := s.writer().ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete user %s: %w", id, err)
	}
	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("%w: %s", ports.ErrUserNotFound, id)
	}
	return nil
}

// IsUserAllowed checks if a user ID is present in the whitelist.
func (s *SQLiteStore) IsUserAllowed(ctx context.Context, id string) (bool, error) {
	query := `SELECT EXISTS(SELECT 1 FROM users WHERE id = ?)`
	var allowed bool
	err := s.reader().QueryRowContext(ctx, query, id).Scan(&allowed)
	if err != nil {
		return false, fmt.Errorf("failed checking user allowed status: %w", err)
	}
	return allowed, nil
}

// GetGroup retrieves a group by ID.
func (s *SQLiteStore) GetGroup(ctx context.Context, groupID string) (*domain.Group, error) {
	query := `
		SELECT group_id, group_title, is_active, created_at
		FROM allowed_groups
		WHERE group_id = ?
	`
	var (
		id        string
		title     sql.NullString
		isActive  int
		createdAt FlexTime
	)

	err := s.reader().QueryRowContext(ctx, query, groupID).Scan(&id, &title, &isActive, &createdAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: %s", ports.ErrGroupNotFound, groupID)
		}
		return nil, fmt.Errorf("failed to query group %s: %w", groupID, err)
	}

	return &domain.Group{
		GroupID:    id,
		GroupTitle: fromNullString(title),
		IsActive:   isActive == 1,
		CreatedAt:  createdAt.Time,
	}, nil
}

// ListGroups returns all allowed groups.
func (s *SQLiteStore) ListGroups(ctx context.Context) ([]domain.Group, error) {
	query := `
		SELECT group_id, group_title, is_active, created_at
		FROM allowed_groups
		ORDER BY created_at ASC
	`
	rows, err := s.reader().QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list groups: %w", err)
	}
	defer rows.Close()

	var groups = make([]domain.Group, 0)
	for rows.Next() {
		var (
			id        string
			title     sql.NullString
			isActive  int
			createdAt FlexTime
		)
		if err := rows.Scan(&id, &title, &isActive, &createdAt); err != nil {
			return nil, fmt.Errorf("failed to scan group row: %w", err)
		}
		groups = append(groups, domain.Group{
			GroupID:    id,
			GroupTitle: fromNullString(title),
			IsActive:   isActive == 1,
			CreatedAt:  createdAt.Time,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating group rows: %w", err)
	}

	return groups, nil
}

// SaveGroup creates or updates a group authorization entry.
func (s *SQLiteStore) SaveGroup(ctx context.Context, group *domain.Group) error {
	if group == nil {
		return errors.New("cannot save nil group")
	}

	query := `
		INSERT INTO allowed_groups (group_id, group_title, is_active, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(group_id) DO UPDATE SET
			group_title = excluded.group_title,
			is_active = excluded.is_active
	`
	createdAt := group.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}

	isActive := 0
	if group.IsActive {
		isActive = 1
	}

	_, err := s.writer().ExecContext(ctx, query,
		group.GroupID,
		toNullString(group.GroupTitle),
		isActive,
		timeToMilli(createdAt),
	)
	if err != nil {
		return fmt.Errorf("failed to save group %s: %w", group.GroupID, err)
	}

	return nil
}

// DeleteGroup deletes a group authorization entry.
func (s *SQLiteStore) DeleteGroup(ctx context.Context, groupID string) error {
	query := `DELETE FROM allowed_groups WHERE group_id = ?`
	res, err := s.writer().ExecContext(ctx, query, groupID)
	if err != nil {
		return fmt.Errorf("failed to delete group %s: %w", groupID, err)
	}
	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("%w: %s", ports.ErrGroupNotFound, groupID)
	}
	return nil
}

// IsGroupAllowed checks if a group ID exists and is currently active.
func (s *SQLiteStore) IsGroupAllowed(ctx context.Context, groupID string) (bool, error) {
	query := `SELECT EXISTS(SELECT 1 FROM allowed_groups WHERE group_id = ? AND is_active = 1)`
	var allowed bool
	err := s.reader().QueryRowContext(ctx, query, groupID).Scan(&allowed)
	if err != nil {
		return false, fmt.Errorf("failed checking group allowed status: %w", err)
	}
	return allowed, nil
}
