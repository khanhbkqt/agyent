package plugin_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"agyent/builtin"
	pluginAdapter "agyent/internal/adapters/plugin"
	"agyent/internal/core/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPluginManager_ListAndAssemble(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("USERPROFILE", fakeHome)
	t.Setenv("HOME", fakeHome)

	globalDir := filepath.Join(fakeHome, ".agyent")
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

	mgr := pluginAdapter.NewPluginManager(builtinDir, nil)
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

func TestPluginManager_SyncEmbeddedPlugins(t *testing.T) {
	destDir := t.TempDir()
	mgr := pluginAdapter.NewPluginManager("", &builtin.EmbeddedPluginsFS)
	ctx := context.Background()

	// 1. List embedded plugins
	embedded, err := mgr.ListEmbeddedPlugins(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, embedded)

	names := make([]string, len(embedded))
	for i, ep := range embedded {
		names[i] = ep.Manifest.Name
	}
	assert.Contains(t, names, "browser-camoufox")
	assert.Contains(t, names, "database-sqlite")

	// 2. Initial Sync
	results, err := mgr.SyncPlugins(ctx, destDir, false)
	require.NoError(t, err)
	require.NotEmpty(t, results)

	for _, r := range results {
		assert.True(t, r.Updated, "expected plugin %s to be updated on initial sync", r.Name)
		assert.DirExists(t, r.Path)
		assert.FileExists(t, filepath.Join(r.Path, "plugin.json"))
	}

	// 3. Re-sync without force (should skip because already up-to-date)
	resyncResults, err := mgr.SyncPlugins(ctx, destDir, false)
	require.NoError(t, err)
	for _, r := range resyncResults {
		assert.True(t, r.Skipped, "expected plugin %s to be skipped on resync", r.Name)
		assert.Equal(t, "already up-to-date", r.Reason)
	}

	// 4. Re-sync with force=true (should force update)
	forceResults, err := mgr.SyncPlugins(ctx, destDir, true)
	require.NoError(t, err)
	for _, r := range forceResults {
		assert.True(t, r.Updated, "expected plugin %s to be force-updated", r.Name)
	}
}

func TestPluginManager_ProvenanceAndDowngradeProtection(t *testing.T) {
	destDir := t.TempDir()
	mgr := pluginAdapter.NewPluginManager("", &builtin.EmbeddedPluginsFS)
	ctx := context.Background()

	// 1. Create a custom user plugin with same name "browser-camoufox" but publisher: "custom-corp"
	customPluginDir := filepath.Join(destDir, "browser-camoufox")
	require.NoError(t, os.MkdirAll(customPluginDir, 0755))

	customManifest := domain.PluginManifest{
		Name:        "browser-camoufox",
		Version:     "0.1.0",
		Description: "My custom private version",
		Publisher:   "custom-corp",
		Enabled:     true,
	}
	data, err := json.Marshal(customManifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(customPluginDir, "plugin.json"), data, 0644))

	// Also put a custom private file in the custom plugin
	privateFile := filepath.Join(customPluginDir, "private_token.txt")
	require.NoError(t, os.WriteFile(privateFile, []byte("super-secret-token"), 0600))

	// 2. Sync without force -> Should skip the custom plugin!
	res, err := mgr.ExtractPluginAtomic(ctx, "browser-camoufox", destDir, false)
	require.NoError(t, err)
	assert.True(t, res.Skipped)
	assert.Contains(t, res.Reason, "custom-corp")

	// Verify private file is preserved intact
	assert.FileExists(t, privateFile)

	// 3. Test Downgrade Protection:
	// Set publisher="agyent" but version="99.0.0"
	customManifest.Publisher = "agyent"
	customManifest.Version = "99.0.0"
	data, err = json.Marshal(customManifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(customPluginDir, "plugin.json"), data, 0644))

	resDowngrade, err := mgr.ExtractPluginAtomic(ctx, "browser-camoufox", destDir, false)
	require.NoError(t, err)
	assert.True(t, resDowngrade.Skipped)
	assert.Equal(t, "already up-to-date", resDowngrade.Reason)
}

func TestPluginManager_MultiAgentScopedDiscovery(t *testing.T) {
	// Create mock global ~/.agyent/plugins and agent ~/.agyent/agents/lyly
	fakeHome := t.TempDir()
	t.Setenv("USERPROFILE", fakeHome)
	t.Setenv("HOME", fakeHome)

	globalPluginDir := filepath.Join(fakeHome, ".agyent", "plugins", "browser-camoufox")
	require.NoError(t, os.MkdirAll(filepath.Join(globalPluginDir, "skills", "web-browse-camoufox"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(globalPluginDir, "rules"), 0755))

	manifest := domain.PluginManifest{
		Name:        "browser-camoufox",
		Version:     "1.2.1",
		Description: "Stealth browser plugin",
		Enabled:     true,
	}
	manifestData, _ := json.MarshalIndent(manifest, "", "  ")
	require.NoError(t, os.WriteFile(filepath.Join(globalPluginDir, "plugin.json"), manifestData, 0644))

	mcpCfg := map[string]any{
		"mcpServers": map[string]any{
			"camoufox-browser": map[string]any{
				"command": "python",
				"args":    []string{"server.py"},
			},
		},
	}
	mcpData, _ := json.MarshalIndent(mcpCfg, "", "  ")
	require.NoError(t, os.WriteFile(filepath.Join(globalPluginDir, "mcp_config.json"), mcpData, 0644))
	require.NoError(t, os.WriteFile(filepath.Join(globalPluginDir, "server.py"), []byte("# server"), 0644))

	skillContent := "---\nname: web-browse-camoufox\ndescription: Stealth browser\n---\n# Camoufox"
	require.NoError(t, os.WriteFile(filepath.Join(globalPluginDir, "skills", "web-browse-camoufox", "SKILL.md"), []byte(skillContent), 0644))

	// Mock agent directory for secondary agent "lyly"
	agentDir := filepath.Join(fakeHome, ".agyent", "agents", "lyly")
	require.NoError(t, os.MkdirAll(agentDir, 0755))

	mgr := pluginAdapter.NewPluginManager("", &builtin.EmbeddedPluginsFS)
	ctx := context.Background()

	// 1. ListPlugins when caller passes agentDir as globalHome
	plugins, err := mgr.ListPlugins(ctx, agentDir, agentDir)
	require.NoError(t, err)
	require.NotEmpty(t, plugins)

	var camoufoxPlugin *domain.Plugin
	for _, p := range plugins {
		if p.Manifest.Name == "browser-camoufox" {
			camoufoxPlugin = &p
			break
		}
	}
	require.NotNil(t, camoufoxPlugin)
	assert.Equal(t, globalPluginDir, camoufoxPlugin.Path)
	assert.True(t, camoufoxPlugin.Manifest.Enabled)

	// Verify MCP server args converted to absolute on-disk path
	require.NotEmpty(t, camoufoxPlugin.MCPServers)
	assert.Equal(t, "camoufox-browser", camoufoxPlugin.MCPServers[0].ServerName)
	assert.Equal(t, filepath.Join(globalPluginDir, "server.py"), camoufoxPlugin.MCPServers[0].Args[0])

	// 2. AssembleActivePlugins for agent
	resolved, err := mgr.AssembleActivePlugins(ctx, agentDir, agentDir)
	require.NoError(t, err)
	var camoufoxMCPServer *domain.MCPServerConfig
	for _, srv := range resolved.ActiveMCPServers {
		if srv.ServerName == "camoufox-browser" {
			srvCopy := srv
			camoufoxMCPServer = &srvCopy
			break
		}
	}
	require.NotNil(t, camoufoxMCPServer)
	assert.Equal(t, filepath.Join(globalPluginDir, "server.py"), camoufoxMCPServer.Args[0])

	// 3. Test TogglePlugin on disk
	err = mgr.TogglePlugin(ctx, "browser-camoufox", false, domain.ScopeGlobal, agentDir)
	require.NoError(t, err)

	resolvedAfterDisable, err := mgr.AssembleActivePlugins(ctx, agentDir, agentDir)
	require.NoError(t, err)

	var hasCamoufox bool
	for _, p := range resolvedAfterDisable.ActivePlugins {
		if p.Manifest.Name == "browser-camoufox" {
			hasCamoufox = true
		}
	}
	assert.False(t, hasCamoufox)
}

func TestPluginManager_ToggleEmbeddedPluginAutoExtract(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("USERPROFILE", fakeHome)
	t.Setenv("HOME", fakeHome)

	mgr := pluginAdapter.NewPluginManager("", &builtin.EmbeddedPluginsFS)
	ctx := context.Background()

	// Toggle plugin that only exists in embeddedFS -> should auto-extract to ~/.agyent/plugins and toggle
	err := mgr.TogglePlugin(ctx, "browser-camoufox", true, domain.ScopeGlobal, "")
	require.NoError(t, err)

	extractedManifest := filepath.Join(fakeHome, ".agyent", "plugins", "browser-camoufox", "plugin.json")
	assert.FileExists(t, extractedManifest)

	data, err := os.ReadFile(extractedManifest)
	require.NoError(t, err)
	var manifest domain.PluginManifest
	require.NoError(t, json.Unmarshal(data, &manifest))
	assert.True(t, manifest.Enabled)
}
