package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agyent/internal/adapters/security"
	"agyent/internal/adapters/security/ipc"
	"agyent/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type HookOutput struct {
	Decision  string                 `json:"decision"`
	Reason    string                 `json:"reason"`
	Overwrite map[string]interface{} `json:"overwrite,omitempty"`
}

func TestBinary_LiveRealWorldScenarios(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live binary e2e tests in short mode")
	}
	candidates := []string{
		filepath.Join("..", "..", "bin", "agyent"),
		filepath.Join("bin", "agyent"),
		filepath.Join("..", "..", "bin", "agyent.exe"),
		filepath.Join("bin", "agyent.exe"),
	}
	var exePath string
	for _, cand := range candidates {
		if _, err := os.Stat(cand); err == nil {
			exePath = cand
			break
		}
	}
	if exePath == "" {
		exePath = filepath.Join("bin", "agyent")
	}
	require.FileExists(t, exePath, "compiled binary must exist at %s", exePath)

	// 1. Version command test
	t.Run("VersionCommand", func(t *testing.T) {
		out, err := exec.Command(exePath, "version").CombinedOutput()
		require.NoError(t, err)
		assert.Contains(t, string(out), "agyent version")
		assert.Contains(t, string(out), "Go Version:")
		t.Logf("Binary version output:\n%s", string(out))
	})

	// 1.1. Security commands e2e test
	t.Run("SecurityListPresetsCommand", func(t *testing.T) {
		out, err := exec.Command(exePath, "security", "list-presets").CombinedOutput()
		require.NoError(t, err)
		assert.Contains(t, string(out), "Agyent Security Presets Matrix")
		assert.Contains(t, string(out), "unrestricted")
		assert.Contains(t, string(out), "developer")
		assert.Contains(t, string(out), "balanced")
		assert.Contains(t, string(out), "strict")
		assert.Contains(t, string(out), "read_only")
		t.Logf("Security list-presets output:\n%s", string(out))
	})

	// 1.2. Init help flags test
	t.Run("InitFlagsHelp", func(t *testing.T) {
		out, err := exec.Command(exePath, "init", "--help").CombinedOutput()
		require.NoError(t, err)
		assert.Contains(t, string(out), "--security-preset")
		assert.Contains(t, string(out), "--approval-timeout")
		assert.Contains(t, string(out), "--plugins")
		assert.Contains(t, string(out), "--enable-all-plugins")
		assert.Contains(t, string(out), "--skip-plugins")
		t.Logf("Init help output:\n%s", string(out))
	})

	// 2. Offline Default-Deny test
	t.Run("OfflineFailSafeDefaultDeny", func(t *testing.T) {
		payload := `{"toolCall":{"name":"run_command","args":{"CommandLine":"dir"}},"stepIdx":1,"conversationId":"test-offline"}`
		cmd := exec.Command(exePath, "hook-bridge", "pre")
		cmd.Env = append(os.Environ(), "AGYENT_SECURITY_IPC_ADDR=127.0.0.1:49992")
		cmd.Stdin = strings.NewReader(payload)
		var outBuf bytes.Buffer
		cmd.Stdout = &outBuf
		err := cmd.Run()
		require.NoError(t, err)

		var resp HookOutput
		require.NoError(t, json.Unmarshal(outBuf.Bytes(), &resp))
		assert.Equal(t, "deny", resp.Decision)
		assert.Contains(t, resp.Reason, "Fail-safe Default-Deny engaged")
		t.Logf("Offline Fail-Safe verdict: %+v", resp)
	})

	// 3. Live IPC Server testing with active daemon
	t.Run("LiveGatewayInterception", func(t *testing.T) {
		cfg := config.GetEffectiveSecurityPreset("balanced")
		cfg.Filesystem.AllowedPaths = []string{filepath.Clean(".")}

		testIPCAddr := "127.0.0.1:49991"
		logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))
		secMgr := security.NewManager(cfg, nil, logger)
		ipcServer := ipc.NewServer(secMgr, testIPCAddr, logger)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		require.NoError(t, ipcServer.Start(ctx))
		defer ipcServer.Stop()

		time.Sleep(100 * time.Millisecond)

		// 3.1. Blacklisted Command
		t.Run("Blacklist_DestructiveCommand", func(t *testing.T) {
			payload := `{"toolCall":{"name":"run_command","args":{"CommandLine":"rm -rf /"}},"stepIdx":1,"conversationId":"c1"}`
			resp := executeHookWithAddr(t, exePath, "pre", payload, testIPCAddr)
			assert.Equal(t, "deny", resp.Decision)
			assert.Contains(t, resp.Reason, "forbidden pattern")
			t.Logf("Blacklist block reason: %s", resp.Reason)
		})

		// 3.2. Whitelisted Command
		t.Run("Whitelist_SafeCommand", func(t *testing.T) {
			payload := `{"toolCall":{"name":"run_command","args":{"CommandLine":"go test ./..."}},"stepIdx":2,"conversationId":"c1"}`
			resp := executeHookWithAddr(t, exePath, "pre", payload, testIPCAddr)
			assert.Equal(t, "allow", resp.Decision)
			t.Logf("Whitelist allow: %+v", resp)
		})

		// 3.3. SSRF Cloud Metadata Block
		t.Run("SSRF_CloudMetadata", func(t *testing.T) {
			payload := `{"toolCall":{"name":"read_url_content","args":{"Url":"http://169.254.169.254/latest/meta-data/"}},"stepIdx":3,"conversationId":"c1"}`
			resp := executeHookWithAddr(t, exePath, "pre", payload, testIPCAddr)
			assert.Equal(t, "deny", resp.Decision)
			assert.Contains(t, resp.Reason, "SSRF Guardrail")
			t.Logf("SSRF block reason: %s", resp.Reason)
		})

		// 3.4. SSRF Octal Dotted IP (0177.0.0.1)
		t.Run("SSRF_OctalDottedIP", func(t *testing.T) {
			payload := `{"toolCall":{"name":"read_url_content","args":{"Url":"http://0177.0.0.1:8080/api"}},"stepIdx":4,"conversationId":"c1"}`
			resp := executeHookWithAddr(t, exePath, "pre", payload, testIPCAddr)
			assert.Equal(t, "deny", resp.Decision)
			assert.Contains(t, resp.Reason, "SSRF Guardrail")
			t.Logf("SSRF octal block reason: %s", resp.Reason)
		})

		// 3.5. Path Jail Block External File
		t.Run("PathJail_ExternalAccess", func(t *testing.T) {
			payload := `{"toolCall":{"name":"view_file","args":{"TargetFile":"C:\\Windows\\System32\\cmd.exe"}},"stepIdx":5,"conversationId":"c1","workspacePaths":["` + strings.ReplaceAll(filepath.Clean("."), "\\", "\\\\") + `"]}`
			resp := executeHookWithAddr(t, exePath, "pre", payload, testIPCAddr)
			assert.Equal(t, "deny", resp.Decision)
			assert.Contains(t, resp.Reason, "Path Jail")
			t.Logf("Path jail block reason: %s", resp.Reason)
		})

		// 3.6. PostToolUse Secret Redaction
		t.Run("PostToolUse_SecretRedaction", func(t *testing.T) {
			rawSecretOutput := "Found sk-proj-1234567890abcdef1234567890 and DB_PASSWORD=\"supersecret123\" in output"
			payload := fmt.Sprintf(`{"toolCall":{"name":"run_command","args":{"output":%q}},"stepIdx":6,"conversationId":"c1"}`, rawSecretOutput)
			resp := executeHookWithAddr(t, exePath, "post", payload, testIPCAddr)
			require.NotNil(t, resp.Overwrite)
			out, ok := resp.Overwrite["output"].(string)
			require.True(t, ok)
			assert.Contains(t, out, "[REDACTED_SECRET]")
			assert.NotContains(t, out, "sk-proj-")
			assert.NotContains(t, out, "supersecret123")
			t.Logf("PostToolUse sanitized output: %s", out)
		})

		// 3.7. Python Inline Script Execution Interception
		t.Run("PythonInline_HITLInterception", func(t *testing.T) {
			payload := `{"toolCall":{"name":"run_command","args":{"CommandLine":"python -c \"print('hello')\""}},"stepIdx":7,"conversationId":"c1"}`
			resp := executeHookWithAddr(t, exePath, "pre", payload, testIPCAddr)
			assert.NotEmpty(t, resp.Decision)
			t.Logf("Python inline execution decision: %+v", resp)
		})
	})

	// 4. Test Workspace-Scoped Hook Provisioning & Global Isolation
	t.Run("WorkspaceScopedHookIsolation", func(t *testing.T) {
		tempWS := t.TempDir()
		hookPath, err := security.EnsureWorkspaceHooksProvisioned(tempWS, exePath, nil)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(tempWS, ".agents", "hooks.json"), hookPath)
		assert.FileExists(t, hookPath)

		content, err := os.ReadFile(hookPath)
		require.NoError(t, err)
		assert.Contains(t, string(content), "agyent-security-gate")
		assert.Contains(t, string(content), "hook-bridge pre")
		assert.Contains(t, string(content), "hook-bridge post")
		// Verify no broken escaped quotes in Windows command path
		assert.NotContains(t, string(content), "\\\"")
		t.Logf("Workspace-scoped hooks.json verified:\n%s", string(content))
	})
}

func executeHookWithAddr(t *testing.T, binPath, hookType, payload, addr string) HookOutput {
	t.Helper()
	cmd := exec.Command(binPath, "hook-bridge", hookType)
	cmd.Env = append(os.Environ(), "AGYENT_SECURITY_IPC_ADDR="+addr)
	cmd.Stdin = strings.NewReader(payload)
	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	err := cmd.Run()
	require.NoError(t, err)

	var resp HookOutput
	require.NoError(t, json.Unmarshal(outBuf.Bytes(), &resp))
	return resp
}
