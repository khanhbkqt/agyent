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

// GetProject retrieves a project by its composite ID (agent_name:project_name).
func (s *SQLiteStore) GetProject(ctx context.Context, id string) (*domain.Project, error) {
	query := `
		SELECT id, agent_name, project_name, project_path, created_at
		FROM projects
		WHERE id = ?
	`
	var (
		projID    string
		agentName string
		projName  string
		projPath  string
		createdAt FlexTime
	)

	err := s.reader().QueryRowContext(ctx, query, id).Scan(&projID, &agentName, &projName, &projPath, &createdAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: %s", ports.ErrProjectNotFound, id)
		}
		return nil, fmt.Errorf("failed to query project %s: %w", id, err)
	}

	return &domain.Project{
		ID:          projID,
		AgentName:   agentName,
		ProjectName: projName,
		ProjectPath: projPath,
		CreatedAt:   createdAt.Time,
	}, nil
}

// ListProjects returns all projects, optionally filtered by agentName.
func (s *SQLiteStore) ListProjects(ctx context.Context, agentName string) ([]domain.Project, error) {
	var (
		rows *sql.Rows
		err  error
	)

	if agentName != "" {
		query := `
			SELECT id, agent_name, project_name, project_path, created_at
			FROM projects
			WHERE agent_name = ?
			ORDER BY created_at ASC
		`
		rows, err = s.reader().QueryContext(ctx, query, agentName)
	} else {
		query := `
			SELECT id, agent_name, project_name, project_path, created_at
			FROM projects
			ORDER BY created_at ASC
		`
		rows, err = s.reader().QueryContext(ctx, query)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to list projects: %w", err)
	}
	defer rows.Close()

	var projects = make([]domain.Project, 0)
	for rows.Next() {
		var (
			projID    string
			agent     string
			projName  string
			projPath  string
			createdAt FlexTime
		)
		if err := rows.Scan(&projID, &agent, &projName, &projPath, &createdAt); err != nil {
			return nil, fmt.Errorf("failed to scan project row: %w", err)
		}
		projects = append(projects, domain.Project{
			ID:          projID,
			AgentName:   agent,
			ProjectName: projName,
			ProjectPath: projPath,
			CreatedAt:   createdAt.Time,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating project rows: %w", err)
	}

	return projects, nil
}

// SaveProject inserts or updates a project. Fails if agent_name does not exist in agents (FK constraint).
func (s *SQLiteStore) SaveProject(ctx context.Context, project *domain.Project) error {
	if project == nil {
		return errors.New("cannot save nil project")
	}

	query := `
		INSERT INTO projects (id, agent_name, project_name, project_path, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			agent_name = excluded.agent_name,
			project_name = excluded.project_name,
			project_path = excluded.project_path
	`
	createdAt := project.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}

	if project.ID == "" {
		project.ID = domain.FormatProjectID(project.AgentName, project.ProjectName)
	}

	_, err := s.writer().ExecContext(ctx, query,
		project.ID,
		project.AgentName,
		project.ProjectName,
		project.ProjectPath,
		timeToMilli(createdAt),
	)
	if err != nil {
		return fmt.Errorf("failed to save project %s: %w", project.ID, err)
	}

	return nil
}

// DeleteProject deletes a project and triggers CASCADE cleanup of project conversations.
func (s *SQLiteStore) DeleteProject(ctx context.Context, id string) error {
	query := `DELETE FROM projects WHERE id = ?`
	res, err := s.writer().ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete project %s: %w", id, err)
	}
	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("%w: %s", ports.ErrProjectNotFound, id)
	}
	return nil
}
