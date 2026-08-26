package security

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"agyent/internal/config"
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
// This scopes security hooks strictly to agyent's subprocess workspaces without affecting the global Antigravity IDE or CLI.
func EnsureWorkspaceHooksProvisioned(workspaceDir string, agyentBinPath string, logger *slog.Logger) (string, error) {
	if logger == nil {
		logger = slog.Default()
	}

	if workspaceDir == "" {
		return "", fmt.Errorf("workspace directory cannot be empty")
	}

	agentsDir := filepath.Join(workspaceDir, ".agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create workspace .agents directory %s: %w", agentsDir, err)
	}

	hookFilePath := filepath.Join(agentsDir, "hooks.json")

	if agyentBinPath == "" {
		if exe, err := os.Executable(); err == nil && exe != "" {
			agyentBinPath = exe
		} else {
			agyentBinPath = "agyent"
		}
	}

	preCmd := FormatHookCommand(agyentBinPath, "pre")
	postCmd := FormatHookCommand(agyentBinPath, "post")

	hookPayload := map[string]NamedHookConfig{
		"agyent-security-gate": {
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
		},
	}

	data, err := json.MarshalIndent(hookPayload, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal hooks.json: %w", err)
	}

	if err := os.WriteFile(hookFilePath, data, 0644); err != nil {
		return "", fmt.Errorf("failed to write %s: %w", hookFilePath, err)
	}

	logger.Debug("Provisioned workspace-scoped Antigravity hooks", "path", hookFilePath, "workspace", workspaceDir)
	return hookFilePath, nil
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
			_ = os.WriteFile(hookFilePath, updated, 0644)
			logger.Info("Cleaned agyent-security-gate from global hooks", "path", hookFilePath)
		}
	}

	return nil
}
