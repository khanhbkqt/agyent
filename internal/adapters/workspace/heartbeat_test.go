package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"agyent/internal/core/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspace_HeartbeatReadWrite(t *testing.T) {
	tempDir := t.TempDir()
	agentWS := filepath.Join(tempDir, "dev_agent")
	mgr := NewManager(nil)
	ctx := context.Background()

	// 1. Read non-existing HEARTBEAT.md provisions default template
	cfg, prompt, err := mgr.ReadHeartbeat(ctx, agentWS)
	require.NoError(t, err)
	assert.False(t, cfg.Enabled)
	assert.Equal(t, 3600, cfg.IntervalSeconds)
	assert.Equal(t, "dev_agent", cfg.AgentName)
	assert.Contains(t, prompt, "Periodic background check instructions")

	// Verify file was written
	_, err = os.Stat(filepath.Join(agentWS, "HEARTBEAT.md"))
	require.NoError(t, err)

	// 2. Write custom HEARTBEAT.md
	newCfg := domain.HeartbeatConfig{
		AgentName:        "dev_agent",
		Enabled:          true,
		IntervalSeconds:  1800,
		TargetSessionKey: "telegram:123456",
		Channel:          "telegram",
	}
	customPrompt := "# Custom Directives\nCheck server memory every 30 mins."
	err = mgr.WriteHeartbeat(ctx, agentWS, newCfg, customPrompt)
	require.NoError(t, err)

	// 3. Read back and verify frontmatter + prompt body
	readCfg, readPrompt, err := mgr.ReadHeartbeat(ctx, agentWS)
	require.NoError(t, err)
	assert.True(t, readCfg.Enabled)
	assert.Equal(t, 1800, readCfg.IntervalSeconds)
	assert.Equal(t, "telegram:123456", readCfg.TargetSessionKey)
	assert.Equal(t, "telegram", readCfg.Channel)
	assert.Equal(t, customPrompt, readPrompt)
}
