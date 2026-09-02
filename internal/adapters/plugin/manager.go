package plugin

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	contextAdapter "agyent/internal/adapters/context"
	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
	"agyent/internal/updater"
)

var _ ports.PluginManagerPort = (*PluginManager)(nil)

// PluginManager implements PluginManagerPort for managing plugin lifecycles, scanning, capability extraction,
// and embedded plugin synchronization.
type PluginManager struct {
	builtinDir string
	embeddedFS *embed.FS
}

// NewPluginManager creates a new PluginManager instance.
func NewPluginManager(builtinDir string, embeddedFS *embed.FS) *PluginManager {
	return &PluginManager{
		builtinDir: builtinDir,
		embeddedFS: embeddedFS,
	}
}

// ListPlugins scans Builtin (disk or embedded), Global, and Workspace plugin directories.
func (m *PluginManager) ListPlugins(ctx context.Context, globalHome, workspaceDir string) ([]domain.Plugin, error) {
	pluginMap := make(map[string]domain.Plugin)

	// 1. Base / Fallback: Embedded Plugins
	if m.embeddedFS != nil {
		embeddedPlugins, err := m.ListEmbeddedPlugins(ctx)
		if err == nil {
			for _, ep := range embeddedPlugins {
				pluginMap[ep.Manifest.Name] = ep
			}
		}
	}

	// 2. Built-in Plugins from disk if available (e.g. dev repository root)
	if m.builtinDir != "" {
		builtinRoot := m.builtinDir
		if !filepath.IsAbs(builtinRoot) {
			if abs, err := filepath.Abs(builtinRoot); err == nil {
				builtinRoot = abs
			}
		}
		if stat, err := os.Stat(builtinRoot); err == nil && stat.IsDir() {
			m.scanPluginsInDir(builtinRoot, domain.ScopeGlobal, pluginMap)
		}
	}

	// 3. Standard User Global Plugins (~/.agyent/plugins/)
	if home, err := os.UserHomeDir(); err == nil {
		defaultGlobalDir := filepath.Join(home, ".agyent", "plugins")
		m.scanPluginsInDir(defaultGlobalDir, domain.ScopeGlobal, pluginMap)
	}

	// 4. Global Home / Agent Workspace Plugins (e.g. ~/.agyent/agents/<name>/)
	if globalHome != "" {
		globalPluginRoot := filepath.Join(globalHome, "plugins")
		m.scanPluginsInDir(globalPluginRoot, domain.ScopeGlobal, pluginMap)

		dotAgentsPluginRoot := filepath.Join(globalHome, ".agents", "plugins")
		m.scanPluginsInDir(dotAgentsPluginRoot, domain.ScopeGlobal, pluginMap)
	}

	// 5. Workspace / Project Plugins (<project>/.agents/plugins/ and <project>/plugins/)
	if workspaceDir != "" && workspaceDir != globalHome {
		wsDotAgentsRoot := filepath.Join(workspaceDir, ".agents", "plugins")
		m.scanPluginsInDir(wsDotAgentsRoot, domain.ScopeWorkspace, pluginMap)

		wsPluginRoot := filepath.Join(workspaceDir, "plugins")
		m.scanPluginsInDir(wsPluginRoot, domain.ScopeWorkspace, pluginMap)
	}

	result := make([]domain.Plugin, 0, len(pluginMap))
	for _, p := range pluginMap {
		result = append(result, p)
	}

	return result, nil
}

// ListEmbeddedPlugins scans all plugins packaged inside embeddedFS.
func (m *PluginManager) ListEmbeddedPlugins(ctx context.Context) ([]domain.Plugin, error) {
	if m.embeddedFS == nil {
		return nil, nil
	}

	entries, err := m.embeddedFS.ReadDir("plugins")
	if err != nil {
		return nil, fmt.Errorf("failed to read embedded plugins directory: %w", err)
	}

	var results []domain.Plugin
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pluginName := entry.Name()
		manifestPath := fmt.Sprintf("plugins/%s/plugin.json", pluginName)
		data, err := m.embeddedFS.ReadFile(manifestPath)
		if err != nil {
			continue
		}

		manifest, err := StrictDecodeManifest(data)
		if err != nil {
			continue
		}

		p := domain.Plugin{
			Manifest:    *manifest,
			Path:        fmt.Sprintf("embedded://plugins/%s", pluginName),
			Scope:       domain.ScopeGlobal,
			InstalledAt: time.Now(),
		}

		// Load embedded MCP Config if present
		mcpPath := fmt.Sprintf("plugins/%s/mcp_config.json", pluginName)
		if mcpData, err := m.embeddedFS.ReadFile(mcpPath); err == nil {
			var cfg struct {
				MCPServers map[string]domain.MCPServerConfig `json:"mcpServers"`
			}
			if err := json.Unmarshal(mcpData, &cfg); err == nil {
				for name, srv := range cfg.MCPServers {
					srv.ServerName = name
					srv.Scope = domain.ScopeGlobal
					srv.Command = resolveCommandPath(srv.Command)

					// If plugin is on disk in ~/.agyent/plugins/<pluginName>, resolve script arg paths
					if home, hErr := os.UserHomeDir(); hErr == nil {
						diskPluginDir := filepath.Join(home, ".agyent", "plugins", pluginName)
						if len(srv.Args) > 0 {
							for i, arg := range srv.Args {
								if !filepath.IsAbs(arg) && (strings.HasSuffix(arg, ".py") || strings.HasSuffix(arg, ".js") || strings.HasSuffix(arg, ".sh")) {
									localTarget := filepath.Join(diskPluginDir, arg)
									if _, statErr := os.Stat(localTarget); statErr == nil {
										srv.Args[i] = localTarget
									}
								}
							}
						}
					}

					p.MCPServers = append(p.MCPServers, srv)
				}
			}
		}

		// Load embedded Skills (plugins/<pluginName>/skills/*/SKILL.md)
		skillsDir := fmt.Sprintf("plugins/%s/skills", pluginName)
		if skillEntries, err := m.embeddedFS.ReadDir(skillsDir); err == nil {
			for _, se := range skillEntries {
				if !se.IsDir() {
					continue
				}
				skillFile := fmt.Sprintf("%s/%s/SKILL.md", skillsDir, se.Name())
				if skillData, err := m.embeddedFS.ReadFile(skillFile); err == nil {
					if header, err := contextAdapter.ParseSkillHeaderFromBytes(skillData, skillFile, domain.ScopeGlobal); err == nil && header != nil {
						p.Skills = append(p.Skills, *header)
					}
				}
			}
		}

		// Load embedded Rules (plugins/<pluginName>/rules/AGENTS.md)
		rulesFile := fmt.Sprintf("plugins/%s/rules/AGENTS.md", pluginName)
		if ruleData, err := m.embeddedFS.ReadFile(rulesFile); err == nil && len(strings.TrimSpace(string(ruleData))) > 0 {
			p.Rules = strings.TrimSpace(string(ruleData))
		}

		results = append(results, p)
	}

	return results, nil
}

// SyncPlugins synchronizes and updates embedded plugins to destDir (default: ~/.agyent/plugins).
func (m *PluginManager) SyncPlugins(ctx context.Context, destDir string, force bool) ([]domain.PluginSyncResult, error) {
	if m.embeddedFS == nil {
		return nil, fmt.Errorf("no embedded plugins filesystem available")
	}

	if destDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to resolve user home directory: %w", err)
		}
		destDir = filepath.Join(home, ".agyent", "plugins")
	}

	cleanDestDir := filepath.Clean(destDir)
	if err := os.MkdirAll(cleanDestDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create plugins directory %s: %w", cleanDestDir, err)
	}

	embeddedEntries, err := m.embeddedFS.ReadDir("plugins")
	if err != nil {
		return nil, fmt.Errorf("failed to read embedded plugins: %w", err)
	}

	var results []domain.PluginSyncResult
	for _, entry := range embeddedEntries {
		if !entry.IsDir() {
			continue
		}
		pluginName := entry.Name()
		res, err := m.ExtractPluginAtomic(ctx, pluginName, cleanDestDir, force)
		if err != nil {
			slog.WarnContext(ctx, "Failed to sync embedded plugin", slog.String("plugin", pluginName), slog.String("error", err.Error()))
			results = append(results, domain.PluginSyncResult{
				Name:    pluginName,
				Skipped: true,
				Reason:  err.Error(),
			})
			continue
		}
		results = append(results, *res)
	}

	return results, nil
}

// ExtractPluginAtomic extracts a specific embedded plugin to targetParentDir with full atomic safety.
func (m *PluginManager) ExtractPluginAtomic(ctx context.Context, pluginName, targetParentDir string, force bool) (*domain.PluginSyncResult, error) {
	if m.embeddedFS == nil {
		return nil, fmt.Errorf("no embedded filesystem configured")
	}

	embeddedPluginDir := fmt.Sprintf("plugins/%s", pluginName)
	manifestData, err := m.embeddedFS.ReadFile(fmt.Sprintf("%s/plugin.json", embeddedPluginDir))
	if err != nil {
		return nil, fmt.Errorf("embedded plugin %q missing plugin.json: %w", pluginName, err)
	}

	embeddedManifest, err := StrictDecodeManifest(manifestData)
	if err != nil {
		return nil, fmt.Errorf("invalid embedded manifest for %q: %w", pluginName, err)
	}

	targetDir := filepath.Join(targetParentDir, pluginName)
	cleanTargetDir := filepath.Clean(targetDir)

	// 1. Path Traversal Protection
	if !strings.HasPrefix(cleanTargetDir, filepath.Clean(targetParentDir)) {
		return nil, fmt.Errorf("path traversal attack detected for plugin directory %q", pluginName)
	}

	// 2. Check existing plugin if present
	existingManifestPath := filepath.Join(cleanTargetDir, "plugin.json")
	installedVersion := ""
	if existingData, err := os.ReadFile(existingManifestPath); err == nil {
		if existingManifest, parseErr := StrictDecodeManifest(existingData); parseErr == nil {
			installedVersion = existingManifest.Version

			// Provenance Check: Do not overwrite custom user plugins with different publisher
			if existingManifest.Publisher != "" && existingManifest.Publisher != "agyent" && !force {
				return &domain.PluginSyncResult{
					Name:             pluginName,
					InstalledVersion: installedVersion,
					EmbeddedVersion:  embeddedManifest.Version,
					Skipped:          true,
					Reason:           fmt.Sprintf("skipped custom plugin with publisher %q", existingManifest.Publisher),
					Path:             cleanTargetDir,
				}, nil
			}

			// Downgrade Protection: Only update if embedded is strictly newer unless forced
			if !force && !updater.IsNewerVersion(embeddedManifest.Version, installedVersion) {
				return &domain.PluginSyncResult{
					Name:             pluginName,
					InstalledVersion: installedVersion,
					EmbeddedVersion:  embeddedManifest.Version,
					Skipped:          true,
					Reason:           "already up-to-date",
					Path:             cleanTargetDir,
				}, nil
			}
		}
	}

	if err := os.MkdirAll(cleanTargetDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create plugin directory: %w", err)
	}

	// 3. Extract files selectively with Atomic Rename
	var extractedFiles []string
	walkErr := fs.WalkDir(m.embeddedFS, embeddedPluginDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(embeddedPluginDir, path)
		if err != nil || relPath == "." {
			return nil
		}

		if strings.Contains(relPath, "__pycache__") || strings.HasSuffix(relPath, ".pyc") || strings.HasSuffix(relPath, ".pyo") {
			return nil
		}

		destPath := filepath.Join(cleanTargetDir, relPath)
		cleanDestPath := filepath.Clean(destPath)
		if !strings.HasPrefix(cleanDestPath, cleanTargetDir) {
			return fmt.Errorf("path traversal attempt in embedded file %q", relPath)
		}

		if d.IsDir() {
			return os.MkdirAll(cleanDestPath, 0755)
		}

		// Read embedded file content
		content, err := m.embeddedFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read embedded file %s: %w", path, err)
		}

		// Atomic file write: write to .tmp and rename
		tmpPath := cleanDestPath + ".tmp"
		if err := os.WriteFile(tmpPath, content, 0644); err != nil {
			return fmt.Errorf("failed to write tmp file %s: %w", tmpPath, err)
		}

		// Unix Executable Permissions
		if runtime.GOOS != "windows" {
			if strings.HasSuffix(cleanDestPath, ".py") || strings.HasSuffix(cleanDestPath, ".sh") || !strings.Contains(filepath.Base(cleanDestPath), ".") {
				_ = os.Chmod(tmpPath, 0755)
			}
		}

		// Atomic replacement with Windows File Lock workaround
		if err := atomicReplaceFile(tmpPath, cleanDestPath); err != nil {
			_ = os.Remove(tmpPath)
			return fmt.Errorf("failed to replace file %s: %w", cleanDestPath, err)
		}

		extractedFiles = append(extractedFiles, cleanDestPath)
		return nil
	})

	if walkErr != nil {
		return nil, walkErr
	}

	// 4. Cleanup old .bak files
	m.CleanupOldFiles(cleanTargetDir)

	return &domain.PluginSyncResult{
		Name:             pluginName,
		InstalledVersion: installedVersion,
		EmbeddedVersion:  embeddedManifest.Version,
		Updated:          true,
		Path:             cleanTargetDir,
	}, nil
}

// CleanupOldFiles removes leftover .bak or .tmp files from prior updates.
func (m *PluginManager) CleanupOldFiles(pluginDir string) {
	_ = filepath.WalkDir(pluginDir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if strings.HasSuffix(path, ".tmp") || strings.HasSuffix(path, ".bak") || strings.HasSuffix(path, ".old") {
				_ = os.Remove(path)
			}
		}
		return nil
	})
}

// atomicReplaceFile performs atomic rename on POSIX, and handles Windows locked files by renaming the target to .bak first.
func atomicReplaceFile(tmpPath, targetPath string) error {
	err := os.Rename(tmpPath, targetPath)
	if err == nil {
		return nil
	}

	// On Windows, if target exists and is open/locked, os.Rename can fail with Access Denied.
	if runtime.GOOS == "windows" {
		bakPath := targetPath + ".bak"
		_ = os.Remove(bakPath)
		if renameOldErr := os.Rename(targetPath, bakPath); renameOldErr == nil {
			return os.Rename(tmpPath, targetPath)
		}
	}

	// Fallback to copy if rename fails
	data, readErr := os.ReadFile(tmpPath)
	if readErr != nil {
		return err
	}
	_ = os.Remove(tmpPath)
	return os.WriteFile(targetPath, data, 0644)
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
				srv.Command = resolveCommandPath(srv.Command)
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

	var manifestPath string
	if strings.HasPrefix(targetPlugin.Path, "embedded:") || targetPlugin.Path == "" {
		// Plugin is embedded only and not yet on disk.
		// Auto-extract it so that plugin.json can be safely modified on disk.
		var destDir string
		if scope == domain.ScopeWorkspace && workspaceDir != "" {
			destDir = filepath.Join(workspaceDir, ".agents", "plugins")
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("failed to resolve user home: %w", err)
			}
			destDir = filepath.Join(home, ".agyent", "plugins")
		}
		_, err = m.ExtractPluginAtomic(ctx, pluginName, destDir, true)
		if err != nil {
			return fmt.Errorf("failed to extract embedded plugin to disk for toggling: %w", err)
		}
		manifestPath = filepath.Join(destDir, pluginName, "plugin.json")
	} else {
		manifestPath = filepath.Join(targetPlugin.Path, "plugin.json")
	}

	targetPlugin.Manifest.Enabled = enabled
	data, err := json.MarshalIndent(targetPlugin.Manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal updated manifest: %w", err)
	}

	return os.WriteFile(manifestPath, data, 0644)
}

// InstallBuiltinPlugin copies a plugin directory from builtin repository or embedded FS to the target scope.
func (m *PluginManager) InstallBuiltinPlugin(ctx context.Context, pluginName string, targetScope domain.ContextScope, workspaceDir string) error {
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

	// Try from disk first if available
	if m.builtinDir != "" {
		srcPluginDir := filepath.Join(m.builtinDir, pluginName)
		if stat, err := os.Stat(srcPluginDir); err == nil && stat.IsDir() {
			if err := os.MkdirAll(targetDir, 0755); err != nil {
				return fmt.Errorf("failed to create destination plugin dir: %w", err)
			}
			return copyDir(srcPluginDir, targetDir)
		}
	}

	// Extract from embedded FS
	if m.embeddedFS != nil {
		parentDir := filepath.Dir(targetDir)
		_, err := m.ExtractPluginAtomic(ctx, pluginName, parentDir, true)
		return err
	}

	return fmt.Errorf("builtin plugin %q not found", pluginName)
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

func resolveCommandPath(cmd string) string {
	if cmd == "python" || cmd == "python3" {
		// 1. Check AGYENT_PYTHON environment variable
		if envPy := os.Getenv("AGYENT_PYTHON"); envPy != "" {
			if _, err := os.Stat(envPy); err == nil {
				return envPy
			}
		}

		// 2. Check ~/.agyent/venv/bin/python or ~/.agyent/venv/Scripts/python.exe
		if home, err := os.UserHomeDir(); err == nil {
			venvPyUnix := filepath.Join(home, ".agyent", "venv", "bin", "python")
			if _, err := os.Stat(venvPyUnix); err == nil {
				return venvPyUnix
			}
			venvPyWin := filepath.Join(home, ".agyent", "venv", "Scripts", "python.exe")
			if _, err := os.Stat(venvPyWin); err == nil {
				return venvPyWin
			}
		}

		// 3. Fallback to LookPath
		if runtime.GOOS == "windows" {
			if path, err := exec.LookPath("python.exe"); err == nil {
				return path
			}
			if path, err := exec.LookPath("python"); err == nil {
				return path
			}
		} else {
			if path, err := exec.LookPath("python3"); err == nil {
				return path
			}
			if path, err := exec.LookPath("python"); err == nil {
				return path
			}
		}
	} else {
		if path, err := exec.LookPath(cmd); err == nil {
			return path
		}
	}
	return cmd
}
