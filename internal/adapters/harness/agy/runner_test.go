package agy_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"agyent/internal/adapters/harness/agy"
	"agyent/internal/config"
	"agyent/internal/core/domain"
	"agyent/internal/core/ports"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var mockBinaryPath string

func TestMain(m *testing.M) {
	// If invoked as helper process by exec.Command
	if os.Getenv("GO_WANT_MOCK_AGY_HELPER") == "1" {
		runMockAGYHelper()
		return
	}

	exe, err := os.Executable()
	if err == nil {
		mockBinaryPath = exe
	}

	code := m.Run()
	os.Exit(code)
}

func runMockAGYHelper() {
	scenario := os.Getenv("MOCK_SCENARIO")
	args := os.Args[1:]

	for _, arg := range args {
		if arg == "--version" {
			fmt.Println("agy version 0.0.0-mock")
			os.Exit(0)
		}
	}

	switch scenario {
	case "sleep_hang":
		time.Sleep(5 * time.Second)
		fmt.Println(`{"conversation_id":"c-sleep","status":"SUCCESS","response":"done","duration_seconds":5.0}`)
		os.Exit(0)

	case "corrupt_json":
		fmt.Println("Fatal error: unexpected CLI crash")
		os.Exit(1)

	case "conv_not_found":
		fmt.Fprintln(os.Stderr, `warning: conversation "invalid-conv-id" not found`)
		fmt.Fprintln(os.Stderr, `ERROR: transcript not found`)
		os.Exit(1)

	case "create_artifacts":
		// Read prompt from STDIN
		var promptBuf strings.Builder
		buf := make([]byte, 1024)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				promptBuf.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}

		// Create artifact files in working dir
		_ = os.WriteFile("generated_chart.png", []byte("pngdata"), 0644)
		_ = os.WriteFile("generated_report.pdf", []byte("pdfdata"), 0644)
		_ = os.WriteFile("ignored_code.go", []byte("package main"), 0644)

		fmt.Println(`{"conversation_id":"c-artifacts","status":"SUCCESS","response":"Files created successfully","duration_seconds":1.0,"usage":{"input_tokens":100,"output_tokens":50,"thinking_tokens":10,"total_tokens":160}}`)
		os.Exit(0)

	case "large_stdin":
		// Read all stdin
		var totalRead int64
		buf := make([]byte, 32*1024)
		for {
			n, err := os.Stdin.Read(buf)
			totalRead += int64(n)
			if err != nil {
				break
			}
		}
		fmt.Printf(`{"conversation_id":"c-large","status":"SUCCESS","response":"Received bytes: %d","duration_seconds":0.5,"usage":{"input_tokens":500,"output_tokens":20,"thinking_tokens":0,"total_tokens":520}}`+"\n", totalRead)
		os.Exit(0)

	case "verify_workspace_flags":
		var hasProject, hasAddDir bool
		for i, arg := range args {
			if arg == "--project" && i+1 < len(args) && args[i+1] != "" {
				hasProject = true
			}
			if arg == "--add-dir" && i+1 < len(args) && args[i+1] != "" {
				hasAddDir = true
			}
		}
		if !hasProject || !hasAddDir {
			fmt.Fprintf(os.Stderr, "missing expected workspace isolation flags, args: %v\n", args)
			os.Exit(1)
		}
		fmt.Println(`{"conversation_id":"c-iso-success","status":"SUCCESS","response":"Workspace isolation verified","duration_seconds":0.5}`)
		os.Exit(0)

	case "verify_project_sandbox_flags":
		var hasProject, hasSandbox, hasAddDir bool
		for i, arg := range args {
			if arg == "--project" && i+1 < len(args) && args[i+1] == "agy-proj-alpha-99" {
				hasProject = true
			}
			if arg == "--sandbox" {
				hasSandbox = true
			}
			if arg == "--add-dir" && i+1 < len(args) && args[i+1] != "" {
				hasAddDir = true
			}
		}
		if !hasProject || !hasSandbox || !hasAddDir {
			fmt.Fprintf(os.Stderr, "missing expected project sandbox flags, args: %v\n", args)
			os.Exit(1)
		}
		fmt.Println(`{"conversation_id":"c-proj-sandbox-success","status":"SUCCESS","response":"Project sandbox flags verified","duration_seconds":0.5}`)
		os.Exit(0)

	case "verify_apis4d_env":
		agentName := os.Getenv("AGYENT_AGENT_NAME")
		workspace := os.Getenv("AGYENT_AGENT_WORKSPACE")
		sessionKey := os.Getenv("AGYENT_SESSION_KEY")
		userID := os.Getenv("AGYENT_USER_ID")
		customVar := os.Getenv("CUSTOM_INJECTED_VAR")
		if agentName == "" || workspace == "" || sessionKey == "" || userID == "" || customVar != "apis4d_val" {
			fmt.Fprintf(os.Stderr, "missing APIS-4D env vars: agentName=%s, ws=%s, sess=%s, user=%s, custom=%s\n",
				agentName, workspace, sessionKey, userID, customVar)
			os.Exit(1)
		}
		respMap := map[string]any{
			"conversation_id":  "c-apis4d",
			"status":           "SUCCESS",
			"response":         fmt.Sprintf("APIS-4D env verified: %s|%s|%s|%s|%s", agentName, workspace, sessionKey, userID, customVar),
			"duration_seconds": 0.2,
		}
		data, _ := json.Marshal(respMap)
		fmt.Println(string(data))
		os.Exit(0)

	case "verify_claude_flags":
		for i, arg := range args {
			if arg == "--effort" {
				fmt.Fprintf(os.Stderr, "Error: invalid model selection: --effort is not supported for Claude model, args: %v\n", args)
				os.Exit(1)
			}
			if arg == "--model" && i+1 < len(args) {
				m := args[i+1]
				if m == "sonnet" || m == "opus" || m == "claude" {
					fmt.Fprintf(os.Stderr, "Error: uncanonical alias passed directly to CLI: %s\n", m)
					os.Exit(1)
				}
				if m != "claude-sonnet-4-6" && m != "claude-opus-4-6-thinking" {
					fmt.Fprintf(os.Stderr, "Error: unexpected model passed: %s\n", m)
					os.Exit(1)
				}
			}
		}
		fmt.Println(`{"conversation_id":"c-claude-success","status":"SUCCESS","response":"Claude model executed cleanly without effort","duration_seconds":0.5}`)
		os.Exit(0)

	case "verify_print_timeout_flags":
		var timeoutVal string
		isStream := false
		for i, arg := range args {
			if arg == "--print-timeout" && i+1 < len(args) {
				timeoutVal = args[i+1]
			}
			if arg == "--output-format" && i+1 < len(args) && args[i+1] == "stream-json" {
				isStream = true
			}
		}
		if timeoutVal == "" {
			fmt.Fprintf(os.Stderr, "missing expected --print-timeout flag, args: %v\n", args)
			os.Exit(1)
		}
		if isStream {
			fmt.Println(`{"event":"init","conversation_id":"c-timeout-stream"}`)
			fmt.Printf(`{"event":"result","result":{"conversation_id":"c-timeout-stream","status":"SUCCESS","response":"Print timeout stream verified: %s"}}`+"\n", timeoutVal)
			os.Exit(0)
		}
		respMap := map[string]any{
			"conversation_id":  "c-timeout-verified",
			"status":           "SUCCESS",
			"response":         fmt.Sprintf("Print timeout verified: %s", timeoutVal),
			"duration_seconds": 0.2,
		}
		data, _ := json.Marshal(respMap)
		fmt.Println(string(data))
		os.Exit(0)

	default: // "success" or standard
		fmt.Println(`{"conversation_id":"c-mock-success","status":"SUCCESS","response":"Hello from mock AGY!","duration_seconds":0.8,"num_turns":1,"usage":{"input_tokens":150,"output_tokens":45,"thinking_tokens":20,"total_tokens":215}}`)
		os.Exit(0)
	}
}

func newTestHarness(scenario string, timeoutSeconds int) *agy.Harness {
	cfg := config.AGYConfig{
		BinaryPath:                 mockBinaryPath,
		DefaultTimeoutSeconds:      timeoutSeconds,
		DefaultEffort:              "high",
		DefaultMode:                "accept-edits",
		DangerouslySkipPermissions: true,
	}
	return agy.NewHarness(cfg)
}

func testAdmission(agentName, workspace string) *domain.ExecutionAdmission {
	if agentName == "" {
		agentName = "test-agent"
	}
	return &domain.ExecutionAdmission{
		AdmissionID:  "adm-test-1",
		AgentName:    agentName,
		AGYProjectID: "agy-proj-" + agentName,
		WorkspaceDir: workspace,
		TurnID:       "turn-test-1",
	}
}

func TestHarness_UnadmittedRequest_FailsClosed(t *testing.T) {
	harness := newTestHarness("success", 5)
	req := domain.ExecutionRequest{
		Prompt:       "hello without admission",
		WorkspaceDir: t.TempDir(),
	}

	res, err := harness.Execute(context.Background(), req)
	assert.Nil(t, res)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ports.ErrExecutionRefused))

	resStream, errStream := harness.ExecuteStream(context.Background(), req, "test-session")
	assert.Nil(t, resStream)
	require.Error(t, errStream)
	assert.True(t, errors.Is(errStream, ports.ErrExecutionRefused))
}

func TestHarness_TC_RUN_01_HugeSTDINPrompt(t *testing.T) {
	t.Setenv("GO_WANT_MOCK_AGY_HELPER", "1")
	t.Setenv("MOCK_SCENARIO", "large_stdin")

	harness := newTestHarness("large_stdin", 10)
	tempDir := t.TempDir()

	// Create a 2MB large prompt
	hugePrompt := strings.Repeat("Antigravity large prompt line with special chars & code \n", 40000)

	req := domain.ExecutionRequest{
		Prompt:       hugePrompt,
		WorkspaceDir: tempDir,
		Admission:    testAdmission("test-agent", tempDir),
	}

	res, err := harness.Execute(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, res.Success)
	assert.Equal(t, "c-large", res.ConversationID)
	assert.Contains(t, res.ResponseText, "Received bytes:")
}

func TestHarness_TC_RUN_02_ProcessTreeTimeout(t *testing.T) {
	t.Setenv("GO_WANT_MOCK_AGY_HELPER", "1")
	t.Setenv("MOCK_SCENARIO", "sleep_hang")

	harness := newTestHarness("sleep_hang", 1) // 1s default timeout
	tempDir := t.TempDir()

	req := domain.ExecutionRequest{
		Prompt:       "test timeout",
		WorkspaceDir: tempDir,
		Timeout:      300 * time.Millisecond, // 300ms timeout
		Admission:    testAdmission("test-agent", tempDir),
	}

	start := time.Now()
	res, err := harness.Execute(context.Background(), req)
	elapsed := time.Since(start)

	assert.Nil(t, res)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")
	assert.Less(t, elapsed, 2500*time.Millisecond, "timeout should cancel and kill process tree rapidly")
}

func TestHarness_TC_RUN_03_ContextCancelAbort(t *testing.T) {
	t.Setenv("GO_WANT_MOCK_AGY_HELPER", "1")
	t.Setenv("MOCK_SCENARIO", "sleep_hang")

	harness := newTestHarness("sleep_hang", 10)
	tempDir := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after 100ms
	time.AfterFunc(100*time.Millisecond, cancel)

	req := domain.ExecutionRequest{
		Prompt:       "test cancel",
		WorkspaceDir: tempDir,
		Admission:    testAdmission("test-agent", tempDir),
	}

	res, err := harness.Execute(ctx, req)
	assert.Nil(t, res)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cancelled")
}

func TestHarness_TC_RUN_04_InvalidBinaryPath(t *testing.T) {
	cfg := config.AGYConfig{
		BinaryPath:            "C:\\non_existent_binary_xyz123.exe",
		DefaultTimeoutSeconds: 5,
	}
	harness := agy.NewHarness(cfg)

	tempDir := t.TempDir()
	req := domain.ExecutionRequest{
		Prompt:       "hello",
		WorkspaceDir: tempDir,
		Admission:    testAdmission("test-agent", tempDir),
	}

	res, err := harness.Execute(context.Background(), req)
	assert.Nil(t, res)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ports.ErrProcessExecution) || errors.Is(err, ports.ErrOutputParse),
		"expected ErrProcessExecution or ErrOutputParse, got: %v", err)
}

func TestHarness_ArtifactsDetectionDuringExecution(t *testing.T) {
	t.Setenv("GO_WANT_MOCK_AGY_HELPER", "1")
	t.Setenv("MOCK_SCENARIO", "create_artifacts")

	harness := newTestHarness("create_artifacts", 5)
	tempDir := t.TempDir()

	req := domain.ExecutionRequest{
		Prompt:       "generate artifacts",
		WorkspaceDir: tempDir,
		Admission:    testAdmission("test-agent", tempDir),
	}

	res, err := harness.Execute(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, res.Success)
	assert.Equal(t, "c-artifacts", res.ConversationID)
	assert.Equal(t, 160, res.Usage.TotalTokens)

	// Verify that generated_chart.png and generated_report.pdf were detected, but ignored_code.go was skipped
	require.Len(t, res.Artifacts, 2)
	assert.Equal(t, "generated_chart.png", res.Artifacts[0].FileName)
	assert.Equal(t, "image", res.Artifacts[0].Type)
	assert.Equal(t, "generated_report.pdf", res.Artifacts[1].FileName)
	assert.Equal(t, "document", artifactsType(res.Artifacts[1]))
}

func artifactsType(att domain.Attachment) string {
	return att.Type
}

func TestHarness_WorkspaceIsolationFlags(t *testing.T) {
	t.Setenv("GO_WANT_MOCK_AGY_HELPER", "1")
	t.Setenv("MOCK_SCENARIO", "verify_workspace_flags")

	harness := newTestHarness("verify_workspace_flags", 5)
	tempDir := t.TempDir()

	req := domain.ExecutionRequest{
		Prompt:       "verify workspace isolation",
		WorkspaceDir: tempDir,
		Admission:    testAdmission("iso-agent", tempDir),
	}

	res, err := harness.Execute(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.Success)
	assert.Equal(t, "c-iso-success", res.ConversationID)
	assert.Contains(t, res.ResponseText, "Workspace isolation verified")
}

func TestHarness_ProjectScopedSandboxFlags(t *testing.T) {
	t.Setenv("GO_WANT_MOCK_AGY_HELPER", "1")
	t.Setenv("MOCK_SCENARIO", "verify_project_sandbox_flags")

	harness := newTestHarness("verify_project_sandbox_flags", 5)
	tempDir := t.TempDir()

	req := domain.ExecutionRequest{
		Prompt:       "verify project scoped sandbox",
		WorkspaceDir: tempDir,
		Admission: &domain.ExecutionAdmission{
			AdmissionID:  "adm-123",
			AGYProjectID: "agy-proj-alpha-99",
			WorkspaceDir: tempDir,
			TurnID:       "turn-123",
		},
	}

	res, err := harness.Execute(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.Success)
	assert.Equal(t, "c-proj-sandbox-success", res.ConversationID)
	assert.Contains(t, res.ResponseText, "Project sandbox flags verified")
}

func TestHarness_HealthCheck_Mock(t *testing.T) {
	t.Setenv("GO_WANT_MOCK_AGY_HELPER", "1")
	t.Setenv("MOCK_SCENARIO", "success")

	harness := newTestHarness("success", 5)
	err := harness.HealthCheck(context.Background())
	assert.NoError(t, err)

	// HealthCheck with invalid path
	badHarness := agy.NewHarness(config.AGYConfig{BinaryPath: "invalid_bin_path_xyz"})
	badErr := badHarness.HealthCheck(context.Background())
	assert.Error(t, badErr)
}

func TestHarness_TC_CONC_01_50SubprocessWorkersStress(t *testing.T) {
	t.Setenv("GO_WANT_MOCK_AGY_HELPER", "1")
	t.Setenv("MOCK_SCENARIO", "success")

	harness := newTestHarness("success", 10)
	workers := 50
	if testing.Short() {
		workers = 5
	}
	var wg sync.WaitGroup
	errCh := make(chan error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			tempDir, err := os.MkdirTemp("", fmt.Sprintf("harness_stress_%d_*", idx))
			if err != nil {
				errCh <- err
				return
			}
			defer os.RemoveAll(tempDir)

			req := domain.ExecutionRequest{
				Prompt:         fmt.Sprintf("Stress prompt turn %d", idx),
				ConversationID: fmt.Sprintf("conv-stress-%d", idx),
				WorkspaceDir:   tempDir,
				Effort:         "low",
				Mode:           "accept-edits",
				Admission:      testAdmission(fmt.Sprintf("worker-%d", idx), tempDir),
			}

			res, err := harness.Execute(context.Background(), req)
			if err != nil {
				errCh <- fmt.Errorf("worker %d failed: %w", idx, err)
				return
			}
			if !res.Success || res.ConversationID == "" {
				errCh <- fmt.Errorf("worker %d unexpected result: %+v", idx, res)
				return
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		assert.NoError(t, err)
	}
}

func TestHarness_TC_CONC_02_WatcherRaceStress(t *testing.T) {
	tempDir := t.TempDir()
	watcher := agy.NewSnapshotWatcher()

	done := make(chan struct{})
	var wg sync.WaitGroup

	// 10 Mutator goroutines continuously creating, modifying, and deleting files
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					fname := filepath.Join(tempDir, fmt.Sprintf("file_%d.png", id))
					_ = os.WriteFile(fname, []byte("data"), 0644)
					time.Sleep(1 * time.Millisecond)
					_ = os.WriteFile(fname, []byte("updated_data_more_bytes"), 0644)
					time.Sleep(1 * time.Millisecond)
					_ = os.Remove(fname)
				}
			}
		}(i)
	}

	// 30 Scanner goroutines taking snapshots and detecting artifacts concurrently
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				snap, err := watcher.TakeSnapshot(tempDir)
				assert.NoError(t, err)
				_, _ = watcher.DetectArtifacts(tempDir, snap)
				time.Sleep(2 * time.Millisecond)
			}
		}(i)
	}

	time.Sleep(200 * time.Millisecond)
	close(done)
	wg.Wait()
}

// ---------------------------------------------------------------------------
// REAL AGY CLI INTEGRATION TESTS (TC-REAL-01 to TC-REAL-03)
// ---------------------------------------------------------------------------

func findRealAGYBinary() string {
	// 1. Check direct standard paths in user home directory
	var candidates []string

	home, err := os.UserHomeDir()
	if err == nil {
		candidates = append(candidates,
			filepath.Join(home, `AppData\Local\agy\bin\agy.exe`),
			filepath.Join(home, `.agy\bin\agy.exe`),
			filepath.Join(home, `.agy\bin\agy`),
		)
	}

	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c
		}
	}

	// 2. Check PATH
	if p, err := exec.LookPath("agy"); err == nil {
		return p
	}
	if p, err := exec.LookPath("agy.exe"); err == nil {
		return p
	}

	return ""
}

func TestHarness_APIS4D_EnvironmentInjection(t *testing.T) {
	t.Setenv("GO_WANT_MOCK_AGY_HELPER", "1")
	t.Setenv("MOCK_SCENARIO", "verify_apis4d_env")

	h := newTestHarness("verify_apis4d_env", 10)
	tempDir := t.TempDir()
	req := domain.ExecutionRequest{
		Prompt:       "test apis4d environment",
		WorkspaceDir: tempDir,
		AgentName:    "cyber_agent",
		SessionKey:   "telegram:chat-sec-99",
		UserID:       "user-tenant-888",
		Admission:    testAdmission("cyber_agent", tempDir),
		Env: map[string]string{
			"CUSTOM_INJECTED_VAR": "apis4d_val",
		},
	}

	res, err := h.Execute(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.Success)
	assert.Contains(t, res.ResponseText, "APIS-4D env verified: cyber_agent|")
	assert.Contains(t, res.ResponseText, "telegram:chat-sec-99|user-tenant-888|apis4d_val")
}

func TestHarness_TC_REAL_01_To_03_RealAGY_Execution(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real AGY CLI integration test in short mode")
	}

	if os.Getenv("AGY_REAL_TEST") != "1" {
		t.Skip("skipping real AGY CLI integration test (set AGY_REAL_TEST=1 to enable)")
	}

	realAGYPath := findRealAGYBinary()
	if realAGYPath == "" {
		t.Skip("real agy CLI not found in environment, skipping real integration test")
	}

	t.Logf("Running real AGY integration tests with: %s", realAGYPath)

	cfg := config.AGYConfig{
		BinaryPath:                 realAGYPath,
		DefaultTimeoutSeconds:      60,
		DefaultEffort:              "low",
		DefaultMode:                "accept-edits",
		DangerouslySkipPermissions: true,
	}
	harness := agy.NewHarness(cfg)

	// TC-REAL-03: Real HealthCheck
	t.Run("TC-REAL-03_HealthCheck", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := harness.HealthCheck(ctx)
		require.NoError(t, err, "real AGY healthcheck should succeed")
	})

	// TC-REAL-01: Sandbox execution prompt "say 999"
	var conversationID string
	sandboxDir := t.TempDir()

	t.Run("TC-REAL-01_InitialPrompt", func(t *testing.T) {
		req := domain.ExecutionRequest{
			Prompt:       "say 999",
			WorkspaceDir: sandboxDir,
			Effort:       "low",
			Admission:    testAdmission("real-agent", sandboxDir),
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		res, err := harness.Execute(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, res)
		assert.True(t, res.Success)
		assert.NotEmpty(t, res.ConversationID)
		assert.Contains(t, res.ResponseText, "999")
		assert.Greater(t, res.Usage.TotalTokens, 0)

		conversationID = res.ConversationID
		t.Logf("Real AGY created conversation ID: %s", conversationID)
	})

	// TC-REAL-02: Resume conversation turn 2
	t.Run("TC-REAL-02_ResumeTurn2", func(t *testing.T) {
		if conversationID == "" {
			t.Skip("previous turn failed, skipping resume test")
		}

		req := domain.ExecutionRequest{
			Prompt:         "what was the number I asked you to say?",
			ConversationID: conversationID,
			WorkspaceDir:   sandboxDir,
			Effort:         "low",
			Admission:      testAdmission("real-agent", sandboxDir),
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		res, err := harness.Execute(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, res)
		assert.True(t, res.Success)
		assert.Equal(t, conversationID, res.ConversationID, "conversation ID must be preserved in turn 2")
		assert.Contains(t, res.ResponseText, "999")
	})

	// TC-REAL-04: Real media / artifact generation and detection
	t.Run("TC-REAL-04_MediaArtifactCreation", func(t *testing.T) {
		mediaSandbox := t.TempDir()

		// Ask AGY to write a CSV file in the sandbox
		req := domain.ExecutionRequest{
			Prompt:                     "create a file named dataset.csv with 3 rows of fruits and prices, then say done",
			WorkspaceDir:               mediaSandbox,
			Effort:                     "low",
			DangerouslySkipPermissions: true,
			Admission:                  testAdmission("real-agent", mediaSandbox),
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		res, err := harness.Execute(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, res)
		assert.True(t, res.Success)

		// Check if dataset.csv was created and detected by the harness
		csvPath := filepath.Join(mediaSandbox, "dataset.csv")
		if _, statErr := os.Stat(csvPath); statErr == nil {
			require.NotEmpty(t, res.Artifacts, "artifacts slice should contain the newly generated dataset.csv")
			assert.Equal(t, "dataset.csv", res.Artifacts[0].FileName)
			assert.Equal(t, "document", res.Artifacts[0].Type)
			t.Logf("Successfully verified real media artifact creation & detection: %+v", res.Artifacts[0])
		}
	})

	// TC-REAL-05: Real Image media outbound generation & detection
	t.Run("TC-REAL-05_ImageMediaOutbound", func(t *testing.T) {
		imageSandbox := t.TempDir()

		req := domain.ExecutionRequest{
			Prompt:                     "write a small test file named chart.png in the current working directory, then say done",
			WorkspaceDir:               imageSandbox,
			Effort:                     "low",
			DangerouslySkipPermissions: true,
			Admission:                  testAdmission("real-agent", imageSandbox),
		}

		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()

		res, err := harness.Execute(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, res)
		assert.True(t, res.Success)

		// Verify image artifact was created and detected as image media type
		imagePath := filepath.Join(imageSandbox, "chart.png")
		if _, statErr := os.Stat(imagePath); statErr == nil {
			require.NotEmpty(t, res.Artifacts, "artifacts slice should contain the newly generated chart.png")

			var foundImage *domain.Attachment
			for i := range res.Artifacts {
				if res.Artifacts[i].FileName == "chart.png" {
					foundImage = &res.Artifacts[i]
					break
				}
			}

			require.NotNil(t, foundImage, "chart.png must be present in artifacts")
			assert.Equal(t, "image", foundImage.Type)
			assert.Equal(t, "image/png", foundImage.MIMEType)
			assert.Greater(t, foundImage.Size, int64(0))
			assert.Equal(t, imagePath, foundImage.FilePath)
			t.Logf("Successfully verified outbound image media artifact: %+v", *foundImage)
		}
	})
}

func TestHarness_InterruptStream(t *testing.T) {
	cfg := config.AGYConfig{
		BinaryPath:          mockBinaryPath,
		GraceTimeoutSeconds: 0.2,
	}
	harness := agy.NewHarness(cfg)

	t.Run("NoActiveStreamReturnsNil", func(t *testing.T) {
		err := harness.InterruptStream(context.Background(), "non-existent-session")
		assert.NoError(t, err)
	})
}

func TestHarness_ClaudeSonnetAndOpus_CanonicalizationAndEffortStripped(t *testing.T) {
	t.Setenv("GO_WANT_MOCK_AGY_HELPER", "1")
	t.Setenv("MOCK_SCENARIO", "verify_claude_flags")

	// Harness is configured with DefaultEffort: "high"
	harness := newTestHarness("verify_claude_flags", 5)
	tempDir := t.TempDir()

	t.Run("Sonnet alias is normalized to claude-sonnet-4-6 and effort is stripped", func(t *testing.T) {
		req := domain.ExecutionRequest{
			Prompt:    "hello sonnet",
			Model:     "sonnet",
			Effort:    "high",
			Admission: testAdmission("claude-test", tempDir),
		}
		res, err := harness.Execute(context.Background(), req)
		require.NoError(t, err)
		require.NotNil(t, res)
		assert.True(t, res.Success)
		assert.Contains(t, res.ResponseText, "Claude model executed cleanly without effort")
	})

	t.Run("Opus alias is normalized to claude-opus-4-6-thinking and effort is stripped", func(t *testing.T) {
		req := domain.ExecutionRequest{
			Prompt:    "hello opus",
			Model:     "opus",
			Effort:    "high",
			Admission: testAdmission("claude-test", tempDir),
		}
		res, err := harness.Execute(context.Background(), req)
		require.NoError(t, err)
		require.NotNil(t, res)
		assert.True(t, res.Success)
		assert.Contains(t, res.ResponseText, "Claude model executed cleanly without effort")
	})
}

func TestHarness_PrintTimeoutForwardedToCLI(t *testing.T) {
	t.Setenv("GO_WANT_MOCK_AGY_HELPER", "1")
	t.Setenv("MOCK_SCENARIO", "verify_print_timeout_flags")

	// Harness configured with DefaultTimeoutSeconds: 3000 (3000s)
	harness := newTestHarness("verify_print_timeout_flags", 3000)
	tempDir := t.TempDir()

	t.Run("Execute batch mode forwards --print-timeout from request", func(t *testing.T) {
		req := domain.ExecutionRequest{
			Prompt:    "test timeout",
			Timeout:   3000 * time.Second,
			Admission: testAdmission("timeout-agent", tempDir),
		}
		res, err := harness.Execute(context.Background(), req)
		require.NoError(t, err)
		require.NotNil(t, res)
		assert.True(t, res.Success)
		assert.Contains(t, res.ResponseText, "Print timeout verified: 3000s")
	})

	t.Run("Execute batch mode falls back to defaultTimeout when req.Timeout is 0", func(t *testing.T) {
		req := domain.ExecutionRequest{
			Prompt:    "test timeout fallback",
			Admission: testAdmission("timeout-agent", tempDir),
		}
		res, err := harness.Execute(context.Background(), req)
		require.NoError(t, err)
		require.NotNil(t, res)
		assert.True(t, res.Success)
		assert.Contains(t, res.ResponseText, "Print timeout verified: 3000s")
	})

	t.Run("ExecuteStream mode forwards --print-timeout", func(t *testing.T) {
		req := domain.ExecutionRequest{
			Prompt:    "test timeout stream",
			Timeout:   2400 * time.Second,
			Admission: testAdmission("timeout-agent", tempDir),
		}
		res, err := harness.ExecuteStream(context.Background(), req, "test-stream-timeout")
		require.NoError(t, err)
		require.NotNil(t, res)
		assert.True(t, res.Success)
		assert.Contains(t, res.ResponseText, "Print timeout stream verified: 2400s")
	})
}

