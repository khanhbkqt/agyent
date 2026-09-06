package security

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agyent/internal/config"
	"agyent/internal/core/concurrency/oslock"
)

// HookEntry represents a single hook handler object in Antigravity.
type HookEntry struct {
	Type    string `json:"type,omitempty"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

// HookGroup represents a matcher group in hooks.json.
type HookGroup struct {
	Matcher string      `json:"matcher"`
	Hooks   []HookEntry `json:"hooks"`
}

// NamedHookConfig represents a top-level hook definition in hooks.json.
type NamedHookConfig struct {
	Enabled     bool        `json:"enabled"`
	PreToolUse  []HookGroup `json:"PreToolUse,omitempty"`
	PostToolUse []HookGroup `json:"PostToolUse,omitempty"`
}

// FormatHookCommand formats the hook command line safely without broken quotes.
func FormatHookCommand(binPath, subcmd string) string {
	clean := filepath.Clean(binPath)
	if strings.Contains(clean, " ") {
		return fmt.Sprintf("\"%s\" hook-bridge %s", clean, subcmd)
	}
	return fmt.Sprintf("%s hook-bridge %s", clean, subcmd)
}

// EnsureWorkspaceHooksProvisioned guarantees that <workspaceDir>/.agents/hooks.json is properly configured.
// It acquires an OS file lock on hooks.json.lock, merges agyent-security-gate into existing hooks without destroying
// existing user configurations, and writes atomically with 0600 permissions.
func EnsureWorkspaceHooksProvisioned(workspaceDir string, agyentBinPath string, logger *slog.Logger) (string, error) {
	if logger == nil {
		logger = slog.Default()
	}

	if workspaceDir == "" {
		return "", fmt.Errorf("workspace directory cannot be empty")
	}

	agentsDir := filepath.Join(workspaceDir, ".agents")
	if err := os.MkdirAll(agentsDir, 0700); err != nil {
		return "", fmt.Errorf("failed to create workspace .agents directory %s: %w", agentsDir, err)
	}

	hookFilePath := filepath.Join(agentsDir, "hooks.json")
	lockFilePath := filepath.Join(agentsDir, "hooks.json.lock")

	unlock, err := oslock.AcquireOSFileLock(context.Background(), lockFilePath, 10*time.Second)
	if err != nil {
		return "", fmt.Errorf("failed to acquire hooks lock %s: %w", lockFilePath, err)
	}
	defer unlock()

	if agyentBinPath == "" {
		if exe, err := os.Executable(); err == nil && exe != "" {
			agyentBinPath = exe
		} else {
			agyentBinPath = "agyent"
		}
	}

	preCmd := FormatHookCommand(agyentBinPath, "pre")
	postCmd := FormatHookCommand(agyentBinPath, "post")

	gateConfig := NamedHookConfig{
		Enabled: true,
		PreToolUse: []HookGroup{
			{
				Matcher: "*",
				Hooks: []HookEntry{
					{
						Type:    "command",
						Command: preCmd,
						Timeout: 65,
					},
				},
			},
		},
		PostToolUse: []HookGroup{
			{
				Matcher: "run_command|view_file|read_url_content|call_mcp_tool|write_to_file",
				Hooks: []HookEntry{
					{
						Type:    "command",
						Command: postCmd,
						Timeout: 15,
					},
				},
			},
		},
	}

	allHooks := make(map[string]json.RawMessage)
	if existingData, err := os.ReadFile(hookFilePath); err == nil {
		if err := json.Unmarshal(existingData, &allHooks); err != nil {
			// Replacing an invalid configuration would silently discard user hook
			// entries and run the next turn with an ambiguous policy. Refuse the
			// turn instead; operators can repair the file explicitly.
			return "", fmt.Errorf("invalid existing hooks configuration %s: %w", hookFilePath, err)
		}
		if allHooks == nil { // JSON `null` is valid syntax but not a hook map.
			allHooks = make(map[string]json.RawMessage)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("failed to read existing hooks configuration %s: %w", hookFilePath, err)
	}

	gateData, err := json.Marshal(gateConfig)
	if err != nil {
		return "", fmt.Errorf("failed to marshal gate config: %w", err)
	}
	allHooks["agyent-security-gate"] = gateData

	data, err := json.MarshalIndent(allHooks, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal hooks.json: %w", err)
	}

	tmpFile := fmt.Sprintf("%s.tmp.%d.%d", hookFilePath, os.Getpid(), time.Now().UnixNano())
	if err := os.WriteFile(tmpFile, data, 0600); err != nil {
		return "", fmt.Errorf("failed to write tmp %s: %w", tmpFile, err)
	}

	if f, err := os.Open(tmpFile); err == nil {
		_ = f.Sync()
		_ = f.Close()
	}

	if err := os.Rename(tmpFile, hookFilePath); err != nil {
		_ = os.Remove(tmpFile)
		return "", fmt.Errorf("failed to atomically replace %s: %w", hookFilePath, err)
	}
	_ = os.Chmod(hookFilePath, 0444)

	_ = EnsureWorkspaceSettingsProvisioned(workspaceDir, logger)

	logger.Debug("Provisioned workspace-scoped Antigravity hooks and settings", "path", hookFilePath, "workspace", workspaceDir)
	return hookFilePath, nil
}

// DefaultToolPermissions defines the complete set of tool grants provisioned into workspace settings.json
// and project configurations to ensure headless AGY invocations never auto-deny and instead
// delegate evaluation to the PreToolUse hook.
var DefaultToolPermissions = []string{
	"command",
	"command(*)",
	"command(**)",
	"command(.*)",
	"read_file",
	"read_file(*)",
	"read_file(**)",
	"read_file(.*)",
	"write_file",
	"write_file(*)",
	"write_file(**)",
	"write_file(.*)",
	"edit_file",
	"edit_file(*)",
	"edit_file(**)",
	"edit_file(.*)",
	"list_dir",
	"list_dir(*)",
	"list_dir(**)",
	"list_dir(.*)",
	"read_url",
	"read_url(*)",
	"read_url(**)",
	"read_url(.*)",
	"read_url(http*)",
	"read_url(https*)",
	"read_url(http://*)",
	"read_url(https://*)",
	"read_url(http://**)",
	"read_url(https://**)",
	"read_url(http*://*)",
	"read_url(http*://**)",
	"read_url(http*://*/**)",
	"read_url(https://.*)",
	"read_url(http://.*)",
	"view_file",
	"view_file(*)",
	"view_file(**)",
	"view_file(.*)",
	"grep_search",
	"grep_search(*)",
	"grep_search(**)",
	"grep_search(.*)",
	"find_by_name",
	"find_by_name(*)",
	"find_by_name(**)",
	"find_by_name(.*)",
	"generate_image",
	"generate_image(*)",
	"generate_image(**)",
	"generate_image(.*)",
	"mcp",
	"mcp(*)",
	"mcp(**)",
	"mcp(.*)",
	"schedule",
	"schedule(*)",
	"schedule(**)",
	"schedule(.*)",
}

// EnsureWorkspaceSettingsProvisioned provisions settings.json strictly in <workspaceDir>/.agents/settings.json
// and <workspaceDir>/.gemini/settings.json, ensuring permissions.allow contains all requisite headless tools
// without expanding global host settings.
func EnsureWorkspaceSettingsProvisioned(workspaceDir string, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}

	if workspaceDir == "" {
		return nil
	}

	targets := []string{
		filepath.Join(workspaceDir, ".agents", "settings.json"),
		filepath.Join(workspaceDir, ".gemini", "settings.json"),
	}

	for _, target := range targets {
		if err := mergeSettingsPermissions(target); err != nil {
			logger.Warn("Failed to provision workspace settings.json", "path", target, "error", err)
		}
	}

	return nil
}

// RemoveGlobalSettingsPermissions cleans up any stale permissions object from ~/.gemini/settings.json
// and ~/.gemini/antigravity-cli/settings.json to strictly preserve the invariant against expanding global grants.
func RemoveGlobalSettingsPermissions(logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}

	for _, hostPath := range []string{"~/.gemini/settings.json", "~/.gemini/antigravity-cli/settings.json"} {
		expanded, err := config.ExpandPath(hostPath)
		if err != nil {
			continue
		}
		data, err := os.ReadFile(expanded)
		if err != nil {
			continue
		}
		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			continue
		}
		if _, hasPerms := raw["permissions"]; hasPerms {
			delete(raw, "permissions")
			updated, _ := json.MarshalIndent(raw, "", "  ")
			tmpFile := fmt.Sprintf("%s.tmp.%d.%d", expanded, os.Getpid(), time.Now().UnixNano())
			if err := os.WriteFile(tmpFile, updated, 0600); err == nil {
				_ = os.Rename(tmpFile, expanded)
				logger.Info("Cleaned permissions object from host settings", "path", expanded)
			}
		}
	}
	return nil
}

func mergeSettingsPermissions(filePath string) error {
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	raw := make(map[string]any)
	if data, err := os.ReadFile(filePath); err == nil {
		_ = json.Unmarshal(data, &raw)
	}

	permsRaw, ok := raw["permissions"].(map[string]any)
	if !ok {
		permsRaw = make(map[string]any)
	}

	existingAllow, _ := permsRaw["allow"].([]any)
	allowMap := make(map[string]bool)
	for _, item := range existingAllow {
		if str, ok := item.(string); ok {
			allowMap[str] = true
		}
	}

	for _, p := range DefaultToolPermissions {
		allowMap[p] = true
	}

	finalAllow := make([]string, 0, len(allowMap))
	for _, p := range DefaultToolPermissions {
		if allowMap[p] {
			finalAllow = append(finalAllow, p)
			delete(allowMap, p)
		}
	}
	for k := range allowMap {
		finalAllow = append(finalAllow, k)
	}

	permsRaw["allow"] = finalAllow
	raw["permissions"] = permsRaw

	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}

	tmpFile := fmt.Sprintf("%s.tmp.%d.%d", filePath, os.Getpid(), time.Now().UnixNano())
	if err := os.WriteFile(tmpFile, data, 0600); err != nil {
		return err
	}
	_ = os.Rename(tmpFile, filePath)
	return nil
}

// RemoveGlobalHooks cleans up any lingering agyent hooks from ~/.gemini/config/hooks.json to keep the host environment pristine.
func RemoveGlobalHooks(logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}

	configDir, err := config.ExpandPath("~/.gemini/config")
	if err != nil {
		return nil
	}

	hookFilePath := filepath.Join(configDir, "hooks.json")
	if _, err := os.Stat(hookFilePath); os.IsNotExist(err) {
		return nil
	}

	data, err := os.ReadFile(hookFilePath)
	if err != nil {
		return err
	}

	var hooks map[string]json.RawMessage
	if err := json.Unmarshal(data, &hooks); err != nil {
		return nil
	}

	if _, exists := hooks["agyent-security-gate"]; exists {
		delete(hooks, "agyent-security-gate")
		if len(hooks) == 0 {
			_ = os.Remove(hookFilePath)
			logger.Info("Removed stale agyent-security-gate from global hooks", "path", hookFilePath)
		} else {
			updated, _ := json.MarshalIndent(hooks, "", "  ")
			tmpFile := fmt.Sprintf("%s.tmp.%d.%d", hookFilePath, os.Getpid(), time.Now().UnixNano())
			if err := os.WriteFile(tmpFile, updated, 0600); err == nil {
				_ = os.Rename(tmpFile, hookFilePath)
			}
			logger.Info("Cleaned agyent-security-gate from global hooks", "path", hookFilePath)
		}
	}

	return nil
}

// AGYProjectConfig represents the JSON structure of an AGY project in ~/.gemini/config/projects/<id>.json
type AGYProjectConfig struct {
	ID               string                 `json:"id"`
	Name             string                 `json:"name"`
	ProjectResources *AGYProjectResources   `json:"projectResources,omitempty"`
	PermissionGrants AGYPermissionGrantsObj `json:"permissionGrants"`
	Settings         *AGYProjectSettings    `json:"settings,omitempty"`
	UpdatedAt        string                 `json:"updatedAt,omitempty"`
	IsWorkspaceOnly  bool                   `json:"isWorkspaceOnly"`
}

type AGYProjectResources struct {
	Resources []AGYResource `json:"resources"`
}

type AGYResource struct {
	FolderURI string `json:"folderUri"`
}

type AGYProjectSettings struct {
	FileAccessPolicy    string `json:"fileAccessPolicy"`
	InternetPolicy      string `json:"internetPolicy"`
	AutoExecutionPolicy string `json:"autoExecutionPolicy"`
}

// AGYPermissionGrantsObj wraps the nested permissionGrants object.
type AGYPermissionGrantsObj struct {
	PermissionGrants AGYAllowList `json:"permissionGrants"`
}

// AGYAllowList holds the list of allowed tool patterns.
type AGYAllowList struct {
	Allow []string `json:"allow"`
}

// EnsureAGYProjectProvisioned guarantees that ~/.gemini/config/projects/<projectID>.json exists
// with native AGY permission grants so that headless tool calls are admitted to the PreToolUse hook.
func EnsureAGYProjectProvisioned(projectID, agentName, workspaceDir string, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	if projectID == "" {
		return fmt.Errorf("projectID cannot be empty")
	}

	projectsDir, err := config.ExpandPath("~/.gemini/config/projects")
	if err != nil {
		return fmt.Errorf("failed to resolve gemini projects dir: %w", err)
	}

	if err := os.MkdirAll(projectsDir, 0700); err != nil {
		return fmt.Errorf("failed to create gemini projects dir %s: %w", projectsDir, err)
	}

	projFilePath := filepath.Join(projectsDir, fmt.Sprintf("%s.json", projectID))

	// Check if already exists, has projectResources, settings, and full permission grants
	if data, err := os.ReadFile(projFilePath); err == nil {
		var cfg AGYProjectConfig
		if err := json.Unmarshal(data, &cfg); err == nil {
			hasResources := cfg.ProjectResources != nil && len(cfg.ProjectResources.Resources) > 0
			hasSettings := cfg.Settings != nil && cfg.Settings.AutoExecutionPolicy != ""
			hasAllGrants := true
			existingMap := make(map[string]bool)
			for _, item := range cfg.PermissionGrants.PermissionGrants.Allow {
				existingMap[item] = true
			}
			for _, required := range DefaultToolPermissions {
				if !existingMap[required] {
					hasAllGrants = false
					break
				}
			}
			if (workspaceDir == "" || hasResources) && hasSettings && hasAllGrants {
				return nil
			}
		}
	}

	name := agentName
	if name == "" {
		name = projectID
	}

	var res *AGYProjectResources
	if workspaceDir != "" {
		canonWS := filepath.Clean(workspaceDir)
		res = &AGYProjectResources{
			Resources: []AGYResource{
				{
					FolderURI: fmt.Sprintf("file://%s", canonWS),
				},
			},
		}
	}

	projConfig := AGYProjectConfig{
		ID:               projectID,
		Name:             name,
		ProjectResources: res,
		PermissionGrants: AGYPermissionGrantsObj{
			PermissionGrants: AGYAllowList{
				Allow: DefaultToolPermissions,
			},
		},
		Settings: &AGYProjectSettings{
			FileAccessPolicy:    "AGENT_SETTING_POLICY_ALLOW",
			InternetPolicy:      "AGENT_SETTING_POLICY_ALLOW",
			AutoExecutionPolicy: "CASCADE_COMMANDS_AUTO_EXECUTION_EAGER",
		},
		UpdatedAt:       time.Now().UTC().Format(time.RFC3339Nano),
		IsWorkspaceOnly: false,
	}

	data, err := json.MarshalIndent(projConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal project config: %w", err)
	}

	tmpFile := fmt.Sprintf("%s.tmp.%d.%d", projFilePath, os.Getpid(), time.Now().UnixNano())
	if err := os.WriteFile(tmpFile, data, 0600); err != nil {
		return fmt.Errorf("failed to write tmp project file %s: %w", tmpFile, err)
	}

	if err := os.Rename(tmpFile, projFilePath); err != nil {
		_ = os.Remove(tmpFile)
		return fmt.Errorf("failed to atomically replace %s: %w", projFilePath, err)
	}

	logger.Debug("Provisioned AGY project configuration", "project_id", projectID, "path", projFilePath)
	return nil
}
