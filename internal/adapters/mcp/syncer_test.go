package mcp_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"agyent/internal/adapters/mcp"
	"agyent/internal/core/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMCPSyncer_MountAndUnmount(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "mcp_config.json")

	// Pre-create a base config with one permanent server
	baseConfig := map[string]any{
		"mcpServers": map[string]any{
			"permanent-tool": map[string]any{
				"command": "node",
				"args":    []string{"server.js"},
			},
		},
	}
	baseData, err := json.MarshalIndent(baseConfig, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(configPath, baseData, 0644))

	syncer, err := mcp.NewMCPSyncer(configPath)
	require.NoError(t, err)

	ctx := context.Background()
	testServers := []domain.MCPServerConfig{
		{
			ServerName: "camoufox-test",
			Command:    "python",
			Args:       []string{"server.py"},
		},
	}

	// Mount
	err = syncer.MountServers(ctx, "session-1", testServers)
	require.NoError(t, err)

	// Verify file content after mount
	content, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "__agyent_ephemeral_session-_camoufox-test")
	assert.Contains(t, string(content), "permanent-tool")

	// Unmount
	err = syncer.UnmountServers(ctx, "session-1", testServers)
	require.NoError(t, err)

	// Verify ephemeral tool removed and permanent preserved
	content, err = os.ReadFile(configPath)
	require.NoError(t, err)
	assert.NotContains(t, string(content), "__agyent_ephemeral_session-_camoufox-test")
	assert.Contains(t, string(content), "permanent-tool")
}

func TestMCPSyncer_ConcurrentMounts(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "mcp_config.json")

	syncer, err := mcp.NewMCPSyncer(configPath)
	require.NoError(t, err)

	ctx := context.Background()
	var wg sync.WaitGroup
	numWorkers := 10

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			servers := []domain.MCPServerConfig{
				{
					ServerName: "shared-tool",
					Command:    "python",
					Args:       []string{"script.py"},
				},
			}
			err := syncer.MountServers(ctx, "session-concurrent", servers)
			assert.NoError(t, err)

			err = syncer.UnmountServers(ctx, "session-concurrent", servers)
			assert.NoError(t, err)
		}(i)
	}

	wg.Wait()

	// After all unmounts, ephemeral shared-tool should be cleaned up
	content, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.NotContains(t, string(content), "__agyent_ephemeral_shared-tool")
}

func TestMCPSyncer_CrashRecovery(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "mcp_config.json")

	// Simulate a previous crash with lingering ephemeral server
	corruptedState := map[string]any{
		"mcpServers": map[string]any{
			"base-calc": map[string]any{
				"command": "calc.exe",
			},
			"__agyent_ephemeral_leaked_browser": map[string]any{
				"command": "python",
				"args":    []string{"leak.py"},
			},
		},
	}
	data, err := json.MarshalIndent(corruptedState, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(configPath, data, 0644))

	// Initializing new syncer must auto-clean leaked ephemeral server
	syncer, err := mcp.NewMCPSyncer(configPath)
	require.NoError(t, err)
	require.NotNil(t, syncer)

	content, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "base-calc")
	assert.NotContains(t, string(content), "__agyent_ephemeral_leaked_browser")
}
