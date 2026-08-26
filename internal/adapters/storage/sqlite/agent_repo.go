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
		       created_at, updated_at
		FROM agents
		WHERE name = ?
	`
	var (
		agentName     string
		desc          string
		status        string
		wsPath        string
		defaultModel  string
		defaultEffort string
		createdAt     FlexTime
		updatedAt     FlexTime
	)

	err := s.reader().QueryRowContext(ctx, query, name).Scan(
		&agentName, &desc, &status, &wsPath, &defaultModel, &defaultEffort, &createdAt, &updatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: %s", ports.ErrAgentNotFound, name)
		}
		return nil, fmt.Errorf("failed to query agent %s: %w", name, err)
	}

	return &domain.Agent{
		Name:          agentName,
		Description:   desc,
		Status:        domain.AgentStatus(status),
		WorkspacePath: wsPath,
		DefaultModel:  defaultModel,
		DefaultEffort: defaultEffort,
		CreatedAt:     createdAt.Time,
		UpdatedAt:     updatedAt.Time,
	}, nil
}

// ListAgents returns all registered agents ordered by created_at.
func (s *SQLiteStore) ListAgents(ctx context.Context) ([]domain.Agent, error) {
	query := `
		SELECT name, description, status, workspace_path,
		       COALESCE(default_model, '') AS default_model,
		       COALESCE(default_effort, '') AS default_effort,
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
			agentName     string
			desc          string
			status        string
			wsPath        string
			defaultModel  string
			defaultEffort string
			createdAt     FlexTime
			updatedAt     FlexTime
		)
		if err := rows.Scan(&agentName, &desc, &status, &wsPath, &defaultModel, &defaultEffort, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan agent row: %w", err)
		}
		agents = append(agents, domain.Agent{
			Name:          agentName,
			Description:   desc,
			Status:        domain.AgentStatus(status),
			WorkspacePath: wsPath,
			DefaultModel:  defaultModel,
			DefaultEffort: defaultEffort,
			CreatedAt:     createdAt.Time,
			UpdatedAt:     updatedAt.Time,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating agent rows: %w", err)
	}

	return agents, nil
}

// SaveAgent creates or updates an agent profile.
func (s *SQLiteStore) SaveAgent(ctx context.Context, agent *domain.Agent) error {
	if agent == nil {
		return errors.New("cannot save nil agent")
	}

	query := `
		INSERT INTO agents (name, description, status, workspace_path, default_model, default_effort, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			description = excluded.description,
			status = excluded.status,
			workspace_path = excluded.workspace_path,
			default_model = excluded.default_model,
			default_effort = excluded.default_effort,
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

	_, err := s.writer().ExecContext(ctx, query,
		agent.Name,
		agent.Description,
		string(agent.Status),
		agent.WorkspacePath,
		agent.DefaultModel,
		agent.DefaultEffort,
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
