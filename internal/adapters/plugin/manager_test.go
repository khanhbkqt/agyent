package plugin_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	pluginAdapter "agyent/internal/adapters/plugin"
	"agyent/internal/core/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPluginManager_ListAndAssemble(t *testing.T) {
	globalDir := t.TempDir()
	wsDir := t.TempDir()
	builtinDir := t.TempDir()

	// 1. Create a mock plugin in workspace: "browser-test"
	pluginDir := filepath.Join(wsDir, ".agents", "plugins", "browser-test")
	require.NoError(t, os.MkdirAll(filepath.Join(pluginDir, "skills", "browse"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(pluginDir, "rules"), 0755))

	manifest := domain.PluginManifest{
		Name:        "browser-test",
		Version:     "1.0.0",
		Description: "Browser automation test plugin",
		Enabled:     true,
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(pluginDir, "plugin.json"), manifestData, 0644))

	mcpCfg := map[string]any{
		"mcpServers": map[string]any{
			"puppeteer-mcp": map[string]any{
				"command": "node",
				"args":    []string{"server.js"},
			},
		},
	}
	mcpData, err := json.MarshalIndent(mcpCfg, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(pluginDir, "mcp_config.json"), mcpData, 0644))

	skillContent := "---\nname: browse-web\ndescription: Stealth web browser\n---\n# Browse"
	require.NoError(t, os.WriteFile(filepath.Join(pluginDir, "skills", "browse", "SKILL.md"), []byte(skillContent), 0644))

	ruleContent := "# Browser Stealth Rules\n- No images"
	require.NoError(t, os.WriteFile(filepath.Join(pluginDir, "rules", "AGENTS.md"), []byte(ruleContent), 0644))

	mgr := pluginAdapter.NewPluginManager(builtinDir)
	ctx := context.Background()

	// Test ListPlugins
	plugins, err := mgr.ListPlugins(ctx, globalDir, wsDir)
	require.NoError(t, err)
	require.Len(t, plugins, 1)

	p := plugins[0]
	assert.Equal(t, "browser-test", p.Manifest.Name)
	assert.True(t, p.Manifest.Enabled)
	assert.Len(t, p.MCPServers, 1)
	assert.Equal(t, "puppeteer-mcp", p.MCPServers[0].ServerName)
	assert.Len(t, p.Skills, 1)
	assert.Equal(t, "browse-web", p.Skills[0].Name)
	assert.Contains(t, p.Rules, "Browser Stealth Rules")

	// Test AssembleActivePlugins
	assembled, err := mgr.AssembleActivePlugins(ctx, globalDir, wsDir)
	require.NoError(t, err)
	assert.Len(t, assembled.ActivePlugins, 1)
	assert.Len(t, assembled.ActiveMCPServers, 1)
	assert.Len(t, assembled.SkillHeaders, 1)
	assert.Contains(t, assembled.WorkspaceDirectives, "[PLUGIN RULES: browser-test]")

	// Test TogglePlugin (disable)
	err = mgr.TogglePlugin(ctx, "browser-test", false, domain.ScopeWorkspace, wsDir)
	require.NoError(t, err)

	assembledAfterDisable, err := mgr.AssembleActivePlugins(ctx, globalDir, wsDir)
	require.NoError(t, err)
	assert.Empty(t, assembledAfterDisable.ActivePlugins)
	assert.Empty(t, assembledAfterDisable.ActiveMCPServers)
}
