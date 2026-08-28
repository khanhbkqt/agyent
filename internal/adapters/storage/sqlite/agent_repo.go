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

// GetAgent retrieves an agent profile by unique name.
func (s *SQLiteStore) GetAgent(ctx context.Context, name string) (*domain.Agent, error) {
	query := `
		SELECT name, description, status, workspace_path,
		       COALESCE(default_model, '') AS default_model,
		       COALESCE(default_effort, '') AS default_effort,
		       COALESCE(security_preset, 'balanced') AS security_preset,
		       COALESCE(owner_id, '') AS owner_id,
		       COALESCE(is_public, 0) AS is_public,
		       created_at, updated_at
		FROM agents
		WHERE name = ?
	`
	var (
		agentName      string
		desc           string
		status         string
		wsPath         string
		defaultModel   string
		defaultEffort  string
		securityPreset string
		ownerID        string
		isPublic       int
		createdAt      FlexTime
		updatedAt      FlexTime
	)

	err := s.reader().QueryRowContext(ctx, query, name).Scan(
		&agentName, &desc, &status, &wsPath, &defaultModel, &defaultEffort, &securityPreset, &ownerID, &isPublic, &createdAt, &updatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: %s", ports.ErrAgentNotFound, name)
		}
		return nil, fmt.Errorf("failed to query agent %s: %w", name, err)
	}

	if securityPreset == "" {
		securityPreset = string(domain.PresetBalanced)
	}

	return &domain.Agent{
		Name:           agentName,
		Description:    desc,
		Status:         domain.AgentStatus(status),
		WorkspacePath:  wsPath,
		DefaultModel:   defaultModel,
		DefaultEffort:  defaultEffort,
		SecurityPreset: domain.SecurityPreset(securityPreset),
		OwnerID:        ownerID,
		IsPublic:       isPublic == 1,
		CreatedAt:      createdAt.Time,
		UpdatedAt:      updatedAt.Time,
	}, nil
}

// ListAgents returns all registered agents ordered by created_at.
func (s *SQLiteStore) ListAgents(ctx context.Context) ([]domain.Agent, error) {
	query := `
		SELECT name, description, status, workspace_path,
		       COALESCE(default_model, '') AS default_model,
		       COALESCE(default_effort, '') AS default_effort,
		       COALESCE(security_preset, 'balanced') AS security_preset,
		       COALESCE(owner_id, '') AS owner_id,
		       COALESCE(is_public, 0) AS is_public,
		       created_at, updated_at
		FROM agents
		ORDER BY created_at ASC
	`
	rows, err := s.reader().QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list agents: %w", err)
	}
	defer rows.Close()

	var agents = make([]domain.Agent, 0)
	for rows.Next() {
		var (
			agentName      string
			desc           string
			status         string
			wsPath         string
			defaultModel   string
			defaultEffort  string
			securityPreset string
			ownerID        string
			isPublic       int
			createdAt      FlexTime
			updatedAt      FlexTime
		)
		if err := rows.Scan(&agentName, &desc, &status, &wsPath, &defaultModel, &defaultEffort, &securityPreset, &ownerID, &isPublic, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan agent row: %w", err)
		}
		if securityPreset == "" {
			securityPreset = string(domain.PresetBalanced)
		}
		agents = append(agents, domain.Agent{
			Name:           agentName,
			Description:    desc,
			Status:         domain.AgentStatus(status),
			WorkspacePath:  wsPath,
			DefaultModel:   defaultModel,
			DefaultEffort:  defaultEffort,
			SecurityPreset: domain.SecurityPreset(securityPreset),
			OwnerID:        ownerID,
			IsPublic:       isPublic == 1,
			CreatedAt:      createdAt.Time,
			UpdatedAt:      updatedAt.Time,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating agent rows: %w", err)
	}

	return agents, nil
}

// ListAgentsForUser returns agents visible to a specific user (public, owned, or shared).
func (s *SQLiteStore) ListAgentsForUser(ctx context.Context, userID string) ([]domain.Agent, error) {
	query := `
		SELECT DISTINCT a.name, a.description, a.status, a.workspace_path,
		       COALESCE(a.default_model, '') AS default_model,
		       COALESCE(a.default_effort, '') AS default_effort,
		       COALESCE(a.security_preset, 'balanced') AS security_preset,
		       COALESCE(a.owner_id, '') AS owner_id,
		       COALESCE(a.is_public, 0) AS is_public,
		       a.created_at, a.updated_at
		FROM agents a
		LEFT JOIN agent_permissions p ON a.name = p.agent_name
		WHERE a.is_public = 1 OR a.owner_id = ? OR p.user_id = ?
		ORDER BY a.created_at ASC
	`
	rows, err := s.reader().QueryContext(ctx, query, userID, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list agents for user %s: %w", userID, err)
	}
	defer rows.Close()

	var agents = make([]domain.Agent, 0)
	for rows.Next() {
		var (
			agentName      string
			desc           string
			status         string
			wsPath         string
			defaultModel   string
			defaultEffort  string
			securityPreset string
			ownerID        string
			isPublic       int
			createdAt      FlexTime
			updatedAt      FlexTime
		)
		if err := rows.Scan(&agentName, &desc, &status, &wsPath, &defaultModel, &defaultEffort, &securityPreset, &ownerID, &isPublic, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan agent row for user: %w", err)
		}
		if securityPreset == "" {
			securityPreset = string(domain.PresetBalanced)
		}
		agents = append(agents, domain.Agent{
			Name:           agentName,
			Description:    desc,
			Status:         domain.AgentStatus(status),
			WorkspacePath:  wsPath,
			DefaultModel:   defaultModel,
			DefaultEffort:  defaultEffort,
			SecurityPreset: domain.SecurityPreset(securityPreset),
			OwnerID:        ownerID,
			IsPublic:       isPublic == 1,
			CreatedAt:      createdAt.Time,
			UpdatedAt:      updatedAt.Time,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating agent rows for user: %w", err)
	}

	return agents, nil
}

// SaveAgent creates or updates an agent profile.
func (s *SQLiteStore) SaveAgent(ctx context.Context, agent *domain.Agent) error {
	if agent == nil {
		return errors.New("cannot save nil agent")
	}

	query := `
		INSERT INTO agents (name, description, status, workspace_path, default_model, default_effort, security_preset, owner_id, is_public, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			description = excluded.description,
			status = excluded.status,
			workspace_path = excluded.workspace_path,
			default_model = excluded.default_model,
			default_effort = excluded.default_effort,
			security_preset = excluded.security_preset,
			owner_id = excluded.owner_id,
			is_public = excluded.is_public,
			updated_at = excluded.updated_at
	`
	now := time.Now()
	createdAt := agent.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	updatedAt := agent.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = now
	}

	isPublicInt := 0
	if agent.IsPublic {
		isPublicInt = 1
	}

	secPreset := string(agent.SecurityPreset)
	if secPreset == "" {
		secPreset = string(domain.PresetBalanced)
	}

	_, err := s.writer().ExecContext(ctx, query,
		agent.Name,
		agent.Description,
		string(agent.Status),
		agent.WorkspacePath,
		agent.DefaultModel,
		agent.DefaultEffort,
		secPreset,
		agent.OwnerID,
		isPublicInt,
		timeToMilli(createdAt),
		timeToMilli(updatedAt),
	)
	if err != nil {
		return fmt.Errorf("failed to save agent %s: %w", agent.Name, err)
	}

	return nil
}

// DeleteAgent removes an agent profile. Fails if active sessions reference it (ON DELETE RESTRICT).
func (s *SQLiteStore) DeleteAgent(ctx context.Context, name string) error {
	query := `DELETE FROM agents WHERE name = ?`
	res, err := s.writer().ExecContext(ctx, query, name)
	if err != nil {
		return fmt.Errorf("failed to delete agent %s: %w", name, err)
	}
	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("%w: %s", ports.ErrAgentNotFound, name)
	}
	return nil
}

// ShareAgent grants or updates collaborator permissions for a user on an agent.
func (s *SQLiteStore) ShareAgent(ctx context.Context, perm *domain.AgentPermission) error {
	if perm == nil {
		return errors.New("cannot save nil agent permission")
	}

	query := `
		INSERT INTO agent_permissions (agent_name, user_id, role, granted_by, granted_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(agent_name, user_id) DO UPDATE SET
			role = excluded.role,
			granted_by = excluded.granted_by,
			granted_at = excluded.granted_at
	`
	grantedAt := perm.GrantedAt
	if grantedAt.IsZero() {
		grantedAt = time.Now()
	}

	_, err := s.writer().ExecContext(ctx, query,
		perm.AgentName,
		perm.UserID,
		perm.Role,
		perm.GrantedBy,
		timeToMilli(grantedAt),
	)
	if err != nil {
		return fmt.Errorf("failed to share agent %s with user %s: %w", perm.AgentName, perm.UserID, err)
	}
	return nil
}

// RevokeAgentAccess removes a user's permissions on an agent.
func (s *SQLiteStore) RevokeAgentAccess(ctx context.Context, agentName, userID string) error {
	query := `DELETE FROM agent_permissions WHERE agent_name = ? AND user_id = ?`
	_, err := s.writer().ExecContext(ctx, query, agentName, userID)
	if err != nil {
		return fmt.Errorf("failed to revoke access to %s for user %s: %w", agentName, userID, err)
	}
	return nil
}

// ListAgentPermissions returns all active collaborator permissions for an agent.
func (s *SQLiteStore) ListAgentPermissions(ctx context.Context, agentName string) ([]domain.AgentPermission, error) {
	query := `
		SELECT agent_name, user_id, role, granted_by, granted_at
		FROM agent_permissions
		WHERE agent_name = ?
		ORDER BY granted_at ASC
	`
	rows, err := s.reader().QueryContext(ctx, query, agentName)
	if err != nil {
		return nil, fmt.Errorf("failed to list permissions for agent %s: %w", agentName, err)
	}
	defer rows.Close()

	var perms = make([]domain.AgentPermission, 0)
	for rows.Next() {
		var (
			name      string
			userID    string
			role      string
			grantedBy string
			grantedAt FlexTime
		)
		if err := rows.Scan(&name, &userID, &role, &grantedBy, &grantedAt); err != nil {
			return nil, fmt.Errorf("failed to scan agent permission row: %w", err)
		}
		perms = append(perms, domain.AgentPermission{
			AgentName: name,
			UserID:    userID,
			Role:      role,
			GrantedBy: grantedBy,
			GrantedAt: grantedAt.Time,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating agent permission rows: %w", err)
	}
	return perms, nil
}

// CheckAgentAccess checks whether a user has access to an agent and returns the effective role ("public", "owner", "admin", "operator", "viewer").
func (s *SQLiteStore) CheckAgentAccess(ctx context.Context, agentName, userID string) (bool, string, error) {
	agent, err := s.GetAgent(ctx, agentName)
	if err != nil {
		return false, "", err
	}

	// 1. Check if public or unclaimed system agent
	if agent.IsPublic || agent.OwnerID == "" {
		return true, "public", nil
	}

	// 2. Check if owner
	if agent.OwnerID == userID {
		return true, "owner", nil
	}

	// 3. Check collaborator permissions table
	query := `SELECT role FROM agent_permissions WHERE agent_name = ? AND user_id = ?`
	var role string
	err = s.reader().QueryRowContext(ctx, query, agentName, userID).Scan(&role)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, "", nil
		}
		return false, "", fmt.Errorf("failed to query agent permissions: %w", err)
	}

	return true, role, nil
}
