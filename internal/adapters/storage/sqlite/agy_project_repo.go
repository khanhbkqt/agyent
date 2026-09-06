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

// GetAGYProjectMapping retrieves the active AGY project mapping for the given tenant, agent, host, and config namespace.
func (s *SQLiteStore) GetAGYProjectMapping(ctx context.Context, tenantID, agentName, hostID, configNamespace string) (*domain.AGYProjectMapping, error) {
	if tenantID == "" {
		tenantID = "default"
	}
	if agentName == "" {
		agentName = "agyent"
	}
	if hostID == "" {
		hostID = "local-host"
	}
	if configNamespace == "" {
		configNamespace = "default"
	}

	query := `
		SELECT tenant_id, agent_name, agent_generation, execution_host_id, agy_config_namespace_id,
		       agy_project_id, workspace_dir, status, created_at, updated_at
		FROM agy_project_registry
		WHERE tenant_id = ? AND agent_name = ? AND execution_host_id = ? AND agy_config_namespace_id = ?
		ORDER BY agent_generation DESC, updated_at DESC
		LIMIT 1
	`

	var (
		tID, aName, hID, nsID, projID, wsDir, status string
		gen                                          int
		createdAt, updatedAt                         FlexTime
	)

	err := s.reader().QueryRowContext(ctx, query, tenantID, agentName, hostID, configNamespace).Scan(
		&tID, &aName, &gen, &hID, &nsID, &projID, &wsDir, &status, &createdAt, &updatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: agy project mapping for agent %s", ports.ErrNotFound, agentName)
		}
		return nil, fmt.Errorf("failed to query agy project mapping for agent %s: %w", agentName, err)
	}

	return &domain.AGYProjectMapping{
		TenantID:             tID,
		AgentName:            aName,
		AgentGeneration:      gen,
		ExecutionHostID:      hID,
		AGYConfigNamespaceID: nsID,
		AGYProjectID:         projID,
		WorkspaceDir:         wsDir,
		Status:               domain.AGYProjectStatus(status),
		CreatedAt:            createdAt.Time,
		UpdatedAt:            updatedAt.Time,
	}, nil
}

// SaveAGYProjectMapping inserts or updates the AGY project mapping.
func (s *SQLiteStore) SaveAGYProjectMapping(ctx context.Context, mapping *domain.AGYProjectMapping) error {
	if mapping == nil {
		return errors.New("cannot save nil agy project mapping")
	}
	if mapping.TenantID == "" {
		mapping.TenantID = "default"
	}
	if mapping.AgentName == "" {
		mapping.AgentName = "agyent"
	}
	if mapping.ExecutionHostID == "" {
		mapping.ExecutionHostID = "local-host"
	}
	if mapping.AGYConfigNamespaceID == "" {
		mapping.AGYConfigNamespaceID = "default"
	}
	if mapping.AgentGeneration <= 0 {
		mapping.AgentGeneration = 1
	}
	if mapping.Status == "" {
		mapping.Status = domain.AGYProjectStatusActive
	}
	now := time.Now()
	if mapping.CreatedAt.IsZero() {
		mapping.CreatedAt = now
	}
	mapping.UpdatedAt = now

	query := `
		INSERT INTO agy_project_registry (
			tenant_id, agent_name, agent_generation, execution_host_id, agy_config_namespace_id,
			agy_project_id, workspace_dir, status, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(tenant_id, agent_name, agent_generation, execution_host_id, agy_config_namespace_id)
		DO UPDATE SET
			agy_project_id = excluded.agy_project_id,
			workspace_dir = excluded.workspace_dir,
			status = excluded.status,
			updated_at = excluded.updated_at
	`

	_, err := s.writer().ExecContext(
		ctx,
		query,
		mapping.TenantID,
		mapping.AgentName,
		mapping.AgentGeneration,
		mapping.ExecutionHostID,
		mapping.AGYConfigNamespaceID,
		mapping.AGYProjectID,
		mapping.WorkspaceDir,
		string(mapping.Status),
		mapping.CreatedAt.UnixMilli(),
		mapping.UpdatedAt.UnixMilli(),
	)
	if err != nil {
		return fmt.Errorf("failed to save agy project mapping for agent %s: %w", mapping.AgentName, err)
	}

	return nil
}

// RevokeAGYProjectMapping marks any active AGY project mapping for the tenant, agent, host, and config namespace as revoked.
func (s *SQLiteStore) RevokeAGYProjectMapping(ctx context.Context, tenantID, agentName, hostID, configNamespace string) error {
	if tenantID == "" {
		tenantID = "default"
	}
	if agentName == "" {
		agentName = "agyent"
	}
	if hostID == "" {
		hostID = "local-host"
	}
	if configNamespace == "" {
		configNamespace = "default"
	}

	query := `
		UPDATE agy_project_registry
		SET status = 'REVOKED', updated_at = ?
		WHERE tenant_id = ? AND agent_name = ? AND execution_host_id = ? AND agy_config_namespace_id = ? AND status != 'REVOKED'
	`

	_, err := s.writer().ExecContext(ctx, query, time.Now().UnixMilli(), tenantID, agentName, hostID, configNamespace)
	if err != nil {
		return fmt.Errorf("failed to revoke agy project mapping for agent %s: %w", agentName, err)
	}

	return nil
}
