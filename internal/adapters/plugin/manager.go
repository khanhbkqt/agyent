package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	contextAdapter "agyent/internal/adapters/context"
	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

var _ ports.PluginManagerPort = (*PluginManager)(nil)

// PluginManager implements PluginManagerPort for managing plugin lifecycles, scanning, and capability extraction.
type PluginManager struct {
	builtinDir string
}

// NewPluginManager creates a new PluginManager instance.
func NewPluginManager(builtinDir string) *PluginManager {
	return &PluginManager{
		builtinDir: builtinDir,
	}
}

// ListPlugins scans both Global and Workspace plugin directories and returns all discovered plugins.
func (m *PluginManager) ListPlugins(ctx context.Context, globalHome, workspaceDir string) ([]domain.Plugin, error) {
	pluginMap := make(map[string]domain.Plugin)

	// 1. Scan Built-in Plugins (builtin/plugins/)
	if m.builtinDir != "" {
		builtinRoot := m.builtinDir
		if !filepath.IsAbs(builtinRoot) {
			if abs, err := filepath.Abs(builtinRoot); err == nil {
				builtinRoot = abs
			}
		}
		m.scanPluginsInDir(builtinRoot, domain.ScopeGlobal, pluginMap)
	}

	// 2. Scan Global Plugins (~/.agyent/plugins/)
	if globalHome != "" {
		globalPluginRoot := filepath.Join(globalHome, "plugins")
		m.scanPluginsInDir(globalPluginRoot, domain.ScopeGlobal, pluginMap)
	}

	// 3. Scan Workspace Plugins (<project>/.agents/plugins/)
	if workspaceDir != "" {
		wsPluginRoot := filepath.Join(workspaceDir, ".agents", "plugins")
		m.scanPluginsInDir(wsPluginRoot, domain.ScopeWorkspace, pluginMap)
	}

	result := make([]domain.Plugin, 0, len(pluginMap))
	for _, p := range pluginMap {
		result = append(result, p)
	}

	return result, nil
}

func (m *PluginManager) scanPluginsInDir(root string, scope domain.ContextScope, targetMap map[string]domain.Plugin) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pluginDir := filepath.Join(root, entry.Name())
		p, err := m.loadPluginFromDir(pluginDir, scope)
		if err == nil && p != nil {
			targetMap[p.Manifest.Name] = *p
		}
	}
}

func (m *PluginManager) loadPluginFromDir(pluginDir string, scope domain.ContextScope) (*domain.Plugin, error) {
	manifestPath := filepath.Join(pluginDir, "plugin.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("missing or unreadable plugin.json in %s: %w", pluginDir, err)
	}

	manifest, err := StrictDecodeManifest(data)
	if err != nil {
		return nil, err
	}

	p := &domain.Plugin{
		Manifest:    *manifest,
		Path:        pluginDir,
		Scope:       scope,
		InstalledAt: time.Now(),
	}

	// 1. Load MCP Config (mcp_config.json) if present
	mcpPath := filepath.Join(pluginDir, "mcp_config.json")
	if mcpData, err := os.ReadFile(mcpPath); err == nil {
		var cfg struct {
			MCPServers map[string]domain.MCPServerConfig `json:"mcpServers"`
		}
		if err := json.Unmarshal(mcpData, &cfg); err == nil {
			for name, srv := range cfg.MCPServers {
				srv.ServerName = name
				srv.Scope = scope
				// Convert relative script/command paths to absolute paths within pluginDir
				if len(srv.Args) > 0 {
					for i, arg := range srv.Args {
						if !filepath.IsAbs(arg) && (strings.HasSuffix(arg, ".py") || strings.HasSuffix(arg, ".js") || strings.HasSuffix(arg, ".sh")) {
							localTarget := filepath.Join(pluginDir, arg)
							if _, statErr := os.Stat(localTarget); statErr == nil {
								srv.Args[i] = localTarget
							}
						}
					}
				}
				p.MCPServers = append(p.MCPServers, srv)
			}
		}
	}

	// 2. Load Skills (skills/*/SKILL.md)
	skillsRoot := filepath.Join(pluginDir, "skills")
	if skillEntries, err := os.ReadDir(skillsRoot); err == nil {
		for _, se := range skillEntries {
			if !se.IsDir() {
				continue
			}
			skillFile := filepath.Join(skillsRoot, se.Name(), "SKILL.md")
			header, err := contextAdapter.SafeParseSkillHeader(skillFile, scope)
			if err == nil && header != nil {
				p.Skills = append(p.Skills, *header)
			}
		}
	}

	// 3. Load Rules (rules/AGENTS.md)
	rulesPath := filepath.Join(pluginDir, "rules", "AGENTS.md")
	if ruleData, err := os.ReadFile(rulesPath); err == nil && len(strings.TrimSpace(string(ruleData))) > 0 {
		p.Rules = strings.TrimSpace(string(ruleData))
	}

	return p, nil
}

// TogglePlugin modifies the 'enabled' boolean field inside plugin.json.
func (m *PluginManager) TogglePlugin(ctx context.Context, pluginName string, enabled bool, scope domain.ContextScope, workspaceDir string) error {
	plugins, err := m.ListPlugins(ctx, "", workspaceDir)
	if err != nil {
		return err
	}

	var targetPlugin *domain.Plugin
	for _, p := range plugins {
		if p.Manifest.Name == pluginName {
			targetPlugin = &p
			break
		}
	}

	if targetPlugin == nil {
		return fmt.Errorf("plugin %q not found", pluginName)
	}

	targetPlugin.Manifest.Enabled = enabled
	manifestPath := filepath.Join(targetPlugin.Path, "plugin.json")

	data, err := json.MarshalIndent(targetPlugin.Manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal updated manifest: %w", err)
	}

	return os.WriteFile(manifestPath, data, 0644)
}

// InstallBuiltinPlugin copies a plugin directory from builtin repository to the target scope.
func (m *PluginManager) InstallBuiltinPlugin(ctx context.Context, pluginName string, targetScope domain.ContextScope, workspaceDir string) error {
	if m.builtinDir == "" {
		return fmt.Errorf("builtin plugins repository directory is not configured")
	}

	srcPluginDir := filepath.Join(m.builtinDir, pluginName)
	if stat, err := os.Stat(srcPluginDir); err != nil || !stat.IsDir() {
		return fmt.Errorf("builtin plugin %q not found in repository %s", pluginName, m.builtinDir)
	}

	var targetDir string
	if targetScope == domain.ScopeWorkspace {
		if workspaceDir == "" {
			return fmt.Errorf("workspace directory required for workspace scope plugin installation")
		}
		targetDir = filepath.Join(workspaceDir, ".agents", "plugins", pluginName)
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		targetDir = filepath.Join(home, ".agyent", "plugins", pluginName)
	}

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create destination plugin dir: %w", err)
	}

	return copyDir(srcPluginDir, targetDir)
}

// ValidatePlugin runs dependency checks on all MCP servers defined within the plugin.
func (m *PluginManager) ValidatePlugin(ctx context.Context, plugin *domain.Plugin) error {
	if plugin == nil {
		return fmt.Errorf("nil plugin passed for validation")
	}

	results := ValidatePluginDependencies(ctx, plugin)
	var errs []string
	for _, r := range results {
		if !r.Available {
			errs = append(errs, r.Error)
		}
	}

	if len(errs) > 0 {
		slog.WarnContext(ctx, "Plugin validation issues",
			slog.String("plugin", plugin.Manifest.Name),
			slog.String("errors", strings.Join(errs, "; ")),
		)
		return fmt.Errorf("plugin validation failed:\n- %s", strings.Join(errs, "\n- "))
	}

	return nil
}

// AssembleActivePlugins extracts all active MCP servers, skills, and rules from enabled plugins.
func (m *PluginManager) AssembleActivePlugins(ctx context.Context, globalHome string, workspaceDir string) (*domain.ResolvedContext, error) {
	plugins, err := m.ListPlugins(ctx, globalHome, workspaceDir)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to list plugins during assembly", slog.String("error", err.Error()))
		return nil, err
	}

	resolved := &domain.ResolvedContext{
		WorkingDir: workspaceDir,
		ResolvedAt: time.Now(),
	}

	var activePlugins []domain.Plugin
	var rulesSb strings.Builder

	for _, p := range plugins {
		if !p.Manifest.Enabled {
			continue
		}
		activePlugins = append(activePlugins, p)
		resolved.ActiveMCPServers = append(resolved.ActiveMCPServers, p.MCPServers...)
		resolved.SkillHeaders = append(resolved.SkillHeaders, p.Skills...)

		if p.Rules != "" {
			rulesSb.WriteString(fmt.Sprintf("\n[PLUGIN RULES: %s]\n%s\n", p.Manifest.Name, p.Rules))
		}
	}

	resolved.ActivePlugins = activePlugins
	resolved.WorkspaceDirectives = strings.TrimSpace(rulesSb.String())

	slog.DebugContext(ctx, "Assembled active plugins",
		slog.Int("active_count", len(activePlugins)),
		slog.Int("mcp_servers", len(resolved.ActiveMCPServers)),
		slog.Int("skills", len(resolved.SkillHeaders)),
	)

	return resolved, nil
}

func copyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := os.MkdirAll(dstPath, 0755); err != nil {
				return err
			}
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
