package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"agyent/internal/adapters/storage/sqlite"
	"agyent/internal/core/domain"
	"agyent/internal/core/ports"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAGYProjectRegistry_CRUD(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_agy_project.db")

	store, err := sqlite.Open(dbPath)
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// 1. Initial lookup -> Not Found
	mapping, err := store.GetAGYProjectMapping(ctx, "tenant-1", "agent-alpha")
	assert.Error(t, err)
	assert.ErrorIs(t, err, ports.ErrNotFound)
	assert.Nil(t, mapping)

	// 2. Save new mapping
	now := time.Now()
	newMapping := &domain.AGYProjectMapping{
		TenantID:             "tenant-1",
		AgentName:            "agent-alpha",
		AgentGeneration:      1,
		ExecutionHostID:      "host-mac-01",
		AGYConfigNamespaceID: "ns-default",
		AGYProjectID:         "proj-agy-alpha-123",
		WorkspaceDir:         "/Users/test/workspace-alpha",
		Status:               domain.AGYProjectStatusActive,
		CreatedAt:            now,
	}
	err = store.SaveAGYProjectMapping(ctx, newMapping)
	require.NoError(t, err)

	// 3. Query saved mapping
	got, err := store.GetAGYProjectMapping(ctx, "tenant-1", "agent-alpha")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "tenant-1", got.TenantID)
	assert.Equal(t, "agent-alpha", got.AgentName)
	assert.Equal(t, "proj-agy-alpha-123", got.AGYProjectID)
	assert.Equal(t, "/Users/test/workspace-alpha", got.WorkspaceDir)
	assert.Equal(t, domain.AGYProjectStatusActive, got.Status)

	// 4. Update mapping
	got.AGYProjectID = "proj-agy-alpha-456"
	err = store.SaveAGYProjectMapping(ctx, got)
	require.NoError(t, err)

	updated, err := store.GetAGYProjectMapping(ctx, "tenant-1", "agent-alpha")
	require.NoError(t, err)
	assert.Equal(t, "proj-agy-alpha-456", updated.AGYProjectID)

	// 5. Revoke mapping
	err = store.RevokeAGYProjectMapping(ctx, "tenant-1", "agent-alpha")
	require.NoError(t, err)

	revoked, err := store.GetAGYProjectMapping(ctx, "tenant-1", "agent-alpha")
	require.NoError(t, err)
	require.NotNil(t, revoked)
	assert.Equal(t, domain.AGYProjectStatusRevoked, revoked.Status)
}
