package mcp_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

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
	assert.Contains(t, string(content), "__agyent_ephemeral_")
	assert.Contains(t, string(content), "_camoufox-test")
	assert.Contains(t, string(content), "permanent-tool")

	// Unmount
	err = syncer.UnmountServers(ctx, "session-1", testServers)
	require.NoError(t, err)

	// Verify ephemeral tool removed and permanent preserved
	content, err = os.ReadFile(configPath)
	require.NoError(t, err)
	assert.NotContains(t, string(content), "camoufox-test")
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

func TestMCPSyncer_ExclusiveTurnLeaseSerializesGlobalConfig(t *testing.T) {
	syncer, err := mcp.NewMCPSyncer(filepath.Join(t.TempDir(), "mcp_config.json"))
	require.NoError(t, err)

	release, err := syncer.AcquireExclusiveTurn(context.Background())
	require.NoError(t, err)

	blockedCtx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	_, err = syncer.AcquireExclusiveTurn(blockedCtx)
	require.Error(t, err, "another MCP turn on the global scope must not mount into the shared config")

	release()
	releaseNext, err := syncer.AcquireExclusiveTurn(context.Background())
	require.NoError(t, err)
	releaseNext()
}

func TestMCPSyncer_ConcurrentTurnsDifferentSessionsDoNotBlock(t *testing.T) {
	syncer, err := mcp.NewMCPSyncer(filepath.Join(t.TempDir(), "mcp_config.json"))
	require.NoError(t, err)

	// Session 1 acquires turn lease
	release1, err := syncer.AcquireExclusiveTurn(context.Background(), "session-workspace-1")
	require.NoError(t, err)
	defer release1()

	// Session 2 acquires turn lease concurrently - MUST NOT block
	ctx2, cancel2 := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel2()
	release2, err := syncer.AcquireExclusiveTurn(ctx2, "session-workspace-2")
	require.NoError(t, err, "different sessions in different workspaces must be able to acquire turn leases concurrently")
	defer release2()

	// Session 1 attempting to acquire another turn lease concurrently SHOULD block
	ctx1Dup, cancel1Dup := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel1Dup()
	_, err = syncer.AcquireExclusiveTurn(ctx1Dup, "session-workspace-1")
	require.Error(t, err, "concurrent turns for the same session must serialize")
}

func TestMCPSyncer_MultiSessionMountIsolation(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "mcp_config.json")

	syncer, err := mcp.NewMCPSyncer(configPath)
	require.NoError(t, err)

	ctx := context.Background()

	srv1 := []domain.MCPServerConfig{
		{
			ServerName: "tool-alpha",
			Command:    "node",
			Args:       []string{"server1.js"},
			Env: map[string]string{
				"CUSTOM_VAR": "session1_val",
			},
		},
	}

	srv2 := []domain.MCPServerConfig{
		{
			ServerName: "tool-beta",
			Command:    "node",
			Args:       []string{"server2.js"},
			Env: map[string]string{
				"CUSTOM_VAR": "session2_val",
			},
		},
	}

	// Mount session 1
	err = syncer.MountServers(ctx, "session-1", srv1)
	require.NoError(t, err)

	// Mount session 2
	err = syncer.MountServers(ctx, "session-2", srv2)
	require.NoError(t, err)

	// Verify config file contains both servers isolated with their session keys
	content, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "tool-alpha")
	assert.Contains(t, string(content), "tool-beta")
	assert.Contains(t, string(content), "session1_val")
	assert.Contains(t, string(content), "session2_val")
	assert.Contains(t, string(content), "session-1")
	assert.Contains(t, string(content), "session-2")

	// Unmount session 1
	err = syncer.UnmountServers(ctx, "session-1", srv1)
	require.NoError(t, err)

	// Verify session 1 server is gone but session 2 server remains
	content, err = os.ReadFile(configPath)
	require.NoError(t, err)
	assert.NotContains(t, string(content), "tool-alpha")
	assert.Contains(t, string(content), "tool-beta")
	assert.Contains(t, string(content), "session2_val")

	// Unmount session 2
	err = syncer.UnmountServers(ctx, "session-2", srv2)
	require.NoError(t, err)

	// Verify all ephemeral servers removed
	content, err = os.ReadFile(configPath)
	require.NoError(t, err)
	assert.NotContains(t, string(content), "tool-beta")
}


