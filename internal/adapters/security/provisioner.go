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
			tmpFile := fmt.Sprintf("%s.tmp.%d.%d", hookFilePath, os.Getpid(), time.Now().UnixNano())
			if err := os.WriteFile(tmpFile, updated, 0600); err == nil {
				_ = os.Rename(tmpFile, hookFilePath)
			}
			logger.Info("Cleaned agyent-security-gate from global hooks", "path", hookFilePath)
		}
	}

	return nil
}
