package agy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"agyent/internal/config"
	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

type streamControlEntry struct {
	mu          sync.Mutex
	stdinWriter io.WriteCloser
	cancelFunc  context.CancelFunc
	interrupted atomic.Bool
}

// Harness implements ports.RunnerPort to execute AGY CLI subprocesses with STDIN streaming,
// cross-platform process tree cleanup, JSON boundary extraction, and artifacts detection.
type Harness struct {
	binaryPath     string
	defaultTimeout time.Duration
	defaultEffort  string
	defaultMode    string
	graceTimeout   time.Duration
	modelAliases   map[string]string
	watcher        *SnapshotWatcher
	eventBus       ports.EventBusPort
	activeStreams  sync.Map // map[string]*streamControlEntry
}

// NewHarness constructs a new Harness runner adapter from AGYConfig.
func NewHarness(cfg config.AGYConfig, bus ...ports.EventBusPort) *Harness {
	timeout := time.Duration(cfg.DefaultTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 1800 * time.Second
	}
	effort := cfg.DefaultEffort
	if effort == "" {
		effort = "high"
	}
	mode := cfg.DefaultMode
	if mode == "" {
		mode = "accept-edits"
	}
	graceTimeout := time.Duration(cfg.GraceTimeoutSeconds * float64(time.Second))
	if graceTimeout <= 0 {
		graceTimeout = 3 * time.Second
	}

	var eb ports.EventBusPort
	if len(bus) > 0 {
		eb = bus[0]
	}

	return &Harness{
		binaryPath:     cfg.BinaryPath,
		defaultTimeout: timeout,
		defaultEffort:  effort,
		defaultMode:    mode,
		graceTimeout:   graceTimeout,
		modelAliases:   cfg.ModelAliases,
		watcher:        NewSnapshotWatcher(),
		eventBus:       eb,
	}
}

// SetEventBus updates or sets the EventBusPort on the Harness runner.
func (h *Harness) SetEventBus(bus ports.EventBusPort) {
	h.eventBus = bus
}

// Name returns the backend identifier ("agy-cli").
func (h *Harness) Name() string {
	return "agy-cli"
}

// Execute runs an Antigravity prompt turn via subprocess and returns the execution result.
func (h *Harness) Execute(ctx context.Context, req domain.ExecutionRequest) (*domain.ExecutionResult, error) {
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = h.defaultTimeout
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Build CLI arguments
	if req.Admission == nil || req.Admission.AGYProjectID == "" {
		return nil, fmt.Errorf("%w: missing or invalid execution admission ticket", ports.ErrExecutionRefused)
	}
	projectID := req.Admission.AGYProjectID

	args := []string{"--output-format", "json", "--project", projectID, "--sandbox"}
	if req.WorkspaceDir != "" {
		args = append(args, "--add-dir", req.WorkspaceDir)
	}
	if req.ConversationID != "" {
		args = append(args, "--conversation", req.ConversationID)
	}

	mode := req.Mode
	if mode == "" {
		mode = h.defaultMode
	}
	if mode != "" {
		args = append(args, "--mode", mode)
	}

	if req.DangerouslySkipPermissions {
		args = append(args, "--dangerously-skip-permissions")
	}
	args = append(args, "--disable-slash-commands")

	model := req.Model
	effort := req.Effort
	if req.DisableEffort || effort == domain.EffortNone {
		effort = ""
	} else if effort == "" {
		effort = h.defaultEffort
	}
	if model != "" || effort != "" {
		canonical, normEffort, _ := domain.NormalizeModelAndEffort(model, effort, h.modelAliases)
		if canonical != "" {
			model = canonical
		}
		effort = normEffort
	}
	if req.DisableEffort || req.Effort == domain.EffortNone {
		effort = ""
	}

	if effort != "" {
		args = append(args, "--effort", effort)
	}

	if model != "" {
		args = append(args, "--model", model)
	}

	if timeout > 0 {
		args = append(args, "--print-timeout", fmt.Sprintf("%ds", int(timeout.Seconds())))
	}

	cmd := exec.CommandContext(execCtx, h.binaryPath, args...)
	if req.WorkspaceDir != "" {
		cmd.Dir = req.WorkspaceDir
	}
	cmd.Env = buildCommandEnv(req)

	// STDIN Streaming (Eliminates 32KB/128KB OS CLI argument limits)
	cmd.Stdin = strings.NewReader(req.Prompt)

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	// Process Group setup, Process Tree cancellation & pipe hang prevention
	configureCmd(cmd)
	cmd.Cancel = func() error {
		return killProcessTree(cmd)
	}
	cmd.WaitDelay = 3 * time.Second

	// 1. Take workspace and brain snapshots before execution
	beforeSnapshot, _ := h.watcher.TakeSnapshot(req.WorkspaceDir)
	beforeBrainSnapshot := h.watcher.TakeBrainSnapshot(req.ConversationID)

	slog.DebugContext(ctx, "Executing AGY CLI subprocess",
		slog.String("binary", h.binaryPath),
		slog.String("workspace", req.WorkspaceDir),
		slog.String("conversation_id", req.ConversationID),
		slog.String("mode", mode),
		slog.String("effort", effort),
	)

	// 2. Execute process with JobGuard process tree isolation
	jobGuard, err := CreateProcessJobGuard()
	if err != nil {
		return nil, fmt.Errorf("%w: failed to initialize process job guard: %v", ports.ErrProcessExecution, err)
	}
	if jobGuard != nil {
		defer jobGuard.Close()
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%w: failed to start agy process: %v", ports.ErrProcessExecution, err)
	}
	if jobGuard != nil && cmd.Process != nil {
		if attachErr := jobGuard.AttachProcess(cmd.Process); attachErr != nil {
			slog.ErrorContext(ctx, "Failed to attach process to JobGuard", slog.String("error", attachErr.Error()))
			_ = killProcessTree(cmd)
			return nil, fmt.Errorf("%w: failed to attach process to JobGuard: %v", ports.ErrProcessExecution, attachErr)
		}
	}

	runErr := cmd.Wait()

	// 3. Handle Context Timeout or Cancellation
	if execCtx.Err() != nil {
		if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
			slog.ErrorContext(ctx, "AGY execution timed out",
				slog.Duration("timeout", timeout),
				slog.String("workspace", req.WorkspaceDir),
				slog.String("stderr", stderrBuf.String()),
			)
			return nil, domain.NewExecutionOutcomeError(
				domain.StatusExecutionFailed,
				fmt.Sprintf("AGY execution timed out after %v (workspace: %s)", timeout, req.WorkspaceDir),
				execCtx.Err(),
			)
		}
		slog.WarnContext(ctx, "AGY execution cancelled",
			slog.String("workspace", req.WorkspaceDir),
			slog.String("stderr", stderrBuf.String()),
		)
		return nil, domain.NewExecutionOutcomeError(
			domain.StatusExecutionFailed,
			fmt.Sprintf("AGY execution cancelled (workspace: %s)", req.WorkspaceDir),
			execCtx.Err(),
		)
	}

	// 4. Parse JSON boundary output
	res, parseErr := ParseOutput(stdoutBuf.Bytes(), stderrBuf.Bytes())
	if parseErr != nil {
		slog.ErrorContext(ctx, "AGY output parsing failed",
			slog.String("error", parseErr.Error()),
			slog.String("stderr", stderrBuf.String()),
		)
		// If parse fails and process had an exit error, check for conversation not found or process crash
		if errors.Is(parseErr, ports.ErrConversationNotFound) {
			return nil, parseErr
		}
		if outcome := domain.ExecutionOutcomeFromError(parseErr); outcome == domain.StatusNativePermissionDenied || outcome == domain.StatusPolicyDenied {
			return res, parseErr
		}
		if runErr != nil {
			return res, domain.NewExecutionOutcomeError(
				domain.StatusExecutionFailed,
				fmt.Sprintf("process error (%v), stderr: %s", runErr, stderrBuf.String()),
				ports.ErrProcessExecution,
			)
		}
		return res, parseErr
	}

	// 5. Detect artifacts created or modified during the run
	if req.WorkspaceDir != "" {
		artifacts, err := h.watcher.DetectArtifacts(req.WorkspaceDir, beforeSnapshot)
		if err == nil {
			res.Artifacts = artifacts
		}
	}
	targetConvID := req.ConversationID
	if targetConvID == "" && res != nil {
		targetConvID = res.ConversationID
	}
	if targetConvID != "" {
		brainArts := h.watcher.DetectBrainArtifacts(targetConvID, beforeBrainSnapshot)
		res.Artifacts = append(res.Artifacts, brainArts...)
	}

	return res, nil
}

// ExecuteStream runs an Antigravity prompt turn in streaming mode (--output-format stream-json)
// and emits real-time events to the EventBus.
func (h *Harness) ExecuteStream(ctx context.Context, req domain.ExecutionRequest, sessionKey string) (*domain.ExecutionResult, error) {
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = h.defaultTimeout
	}

	execCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		timedOut   atomic.Bool
		idleTimer  *time.Timer
		timerMutex sync.Mutex
	)

	idleTimer = time.AfterFunc(timeout, func() {
		timedOut.Store(true)
		cancel()
	})
	defer func() {
		timerMutex.Lock()
		if idleTimer != nil {
			idleTimer.Stop()
		}
		timerMutex.Unlock()
	}()

	resetWatchdog := func() {
		timerMutex.Lock()
		defer timerMutex.Unlock()
		if idleTimer != nil && !timedOut.Load() {
			idleTimer.Reset(timeout)
		}
	}

	// Build CLI arguments for streaming
	if req.Admission == nil || req.Admission.AGYProjectID == "" {
		return nil, fmt.Errorf("%w: missing or invalid execution admission ticket", ports.ErrExecutionRefused)
	}
	projectID := req.Admission.AGYProjectID

	args := []string{"--input-format", "stream-json", "--output-format", "stream-json", "--project", projectID, "--sandbox"}
	if req.WorkspaceDir != "" {
		args = append(args, "--add-dir", req.WorkspaceDir)
	}
	if req.ConversationID != "" {
		args = append(args, "--conversation", req.ConversationID)
	}

	mode := req.Mode
	if mode == "" {
		mode = h.defaultMode
	}
	if mode != "" {
		args = append(args, "--mode", mode)
	}

	if req.DangerouslySkipPermissions {
		args = append(args, "--dangerously-skip-permissions")
	}
	args = append(args, "--disable-slash-commands")

	model := req.Model
	effort := req.Effort
	if req.DisableEffort || effort == domain.EffortNone {
		effort = ""
	} else if effort == "" {
		effort = h.defaultEffort
	}
	if model != "" || effort != "" {
		canonical, normEffort, _ := domain.NormalizeModelAndEffort(model, effort, h.modelAliases)
		if canonical != "" {
			model = canonical
		}
		effort = normEffort
	}
	if req.DisableEffort || req.Effort == domain.EffortNone {
		effort = ""
	}

	if effort != "" {
		args = append(args, "--effort", effort)
	}

	if model != "" {
		args = append(args, "--model", model)
	}

	if timeout > 0 {
		args = append(args, "--print-timeout", fmt.Sprintf("%ds", int(timeout.Seconds())))
	}

	inboundMsg := map[string]any{
		"event": "user",
		"message": map[string]any{
			"content": req.Prompt,
		},
	}
	inboundJSON, err := json.Marshal(inboundMsg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal inbound stream message: %w", err)
	}

	cmd := exec.CommandContext(execCtx, h.binaryPath, args...)
	if req.WorkspaceDir != "" {
		cmd.Dir = req.WorkspaceDir
	}
	cmd.Env = buildCommandEnv(req, sessionKey)

	// STDIN Streaming via dynamic pipe
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdin pipe: %w", err)
	}
	defer stdinPipe.Close()

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	configureCmd(cmd)
	cmd.Cancel = func() error {
		return killProcessTree(cmd)
	}
	cmd.WaitDelay = 3 * time.Second

	// Take workspace and brain snapshot before execution
	beforeSnapshot, _ := h.watcher.TakeSnapshot(req.WorkspaceDir)
	beforeBrainSnapshot := h.watcher.TakeBrainSnapshot(req.ConversationID)

	slog.DebugContext(ctx, "Starting AGY CLI stream subprocess",
		slog.String("binary", h.binaryPath),
		slog.String("session_key", sessionKey),
		slog.String("workspace", req.WorkspaceDir),
		slog.String("conversation_id", req.ConversationID),
	)

	// Register active stream for soft interrupt
	var entry *streamControlEntry
	if sessionKey != "" {
		entry = &streamControlEntry{
			stdinWriter: stdinPipe,
			cancelFunc:  cancel,
		}
		h.activeStreams.Store(sessionKey, entry)
		defer h.activeStreams.Delete(sessionKey)
	}

	// Start subprocess with JobGuard process tree isolation
	jobGuard, err := CreateProcessJobGuard()
	if err != nil {
		slog.ErrorContext(ctx, "Failed to initialize process job guard", slog.String("error", err.Error()))
		return nil, fmt.Errorf("%w: failed to initialize process job guard: %v", ports.ErrProcessExecution, err)
	}
	if jobGuard != nil {
		defer jobGuard.Close()
	}

	if err := cmd.Start(); err != nil {
		slog.ErrorContext(ctx, "Failed to start AGY stream subprocess", slog.String("error", err.Error()))
		return nil, fmt.Errorf("%w: failed to start agy process: %v", ports.ErrProcessExecution, err)
	}
	if jobGuard != nil && cmd.Process != nil {
		if attachErr := jobGuard.AttachProcess(cmd.Process); attachErr != nil {
			slog.ErrorContext(ctx, "Failed to attach process to JobGuard", slog.String("error", attachErr.Error()))
			_ = killProcessTree(cmd)
			return nil, fmt.Errorf("%w: failed to attach process to JobGuard: %v", ports.ErrProcessExecution, attachErr)
		}
	}

	// Write initial turn payload to stdin pipe with synchronization
	stdinDone := make(chan struct{})
	go func() {
		defer close(stdinDone)
		if entry != nil {
			entry.mu.Lock()
			defer entry.mu.Unlock()
		}
		if _, writeErr := stdinPipe.Write(append(inboundJSON, '\n')); writeErr != nil {
			slog.WarnContext(ctx, "Failed to write initial prompt payload to stdin pipe", "error", writeErr, "session_key", sessionKey)
		}
	}()

	parser := NewStreamParser(h.eventBus)
	if req.TurnID != "" {
		parser.SetTurnID(req.TurnID)
	}
	parser.SetOnMilestone(resetWatchdog)
	parser.SetArtifactDetector(func() []domain.Attachment {
		var arts []domain.Attachment
		if req.WorkspaceDir != "" {
			wsArts, err := h.watcher.DetectArtifacts(req.WorkspaceDir, beforeSnapshot)
			if err == nil {
				arts = append(arts, wsArts...)
			}
		}
		if req.ConversationID != "" {
			brainArts := h.watcher.DetectBrainArtifacts(req.ConversationID, beforeBrainSnapshot)
			arts = append(arts, brainArts...)
		}
		return arts
	})
	streamRes, parseErr := parser.ParseAndEmitStream(execCtx, sessionKey, stdoutPipe)

	// Ensure stdin writing is done before closing pipe
	select {
	case <-stdinDone:
	case <-time.After(1 * time.Second):
	}

	if entry != nil {
		entry.mu.Lock()
	}
	_ = stdinPipe.Close()
	if entry != nil {
		entry.mu.Unlock()
	}

	// Ensure stdoutPipe is closed so subprocess unblocks if still writing
	_ = stdoutPipe.Close()

	// Wait for subprocess with bounded timeout to prevent hangs when child MCP processes hold stderr open
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
	}()

	var waitErr error
	select {
	case waitErr = <-waitDone:
		// Subprocess exited cleanly
	case <-time.After(4 * time.Second):
		slog.WarnContext(ctx, "AGY process did not exit within grace period after stream completion, terminating process tree",
			slog.String("session_key", sessionKey),
		)
		_ = killProcessTree(cmd)
		select {
		case waitErr = <-waitDone:
		case <-time.After(1 * time.Second):
			waitErr = fmt.Errorf("subprocess wait timed out")
		}
	case <-execCtx.Done():
		_ = killProcessTree(cmd)
		select {
		case waitErr = <-waitDone:
		case <-time.After(1 * time.Second):
			waitErr = execCtx.Err()
		}
	}

	if execCtx.Err() != nil {
		var timeoutErr error
		if timedOut.Load() {
			slog.ErrorContext(ctx, "AGY stream execution timed out (no milestone activity)",
				slog.Duration("timeout", timeout),
				slog.String("session_key", sessionKey),
				slog.String("stderr", stderrBuf.String()),
			)
			timeoutErr = domain.NewExecutionOutcomeError(
				domain.StatusExecutionFailed,
				fmt.Sprintf("AGY stream execution timed out after %v without milestone activity (session: %s)", timeout, sessionKey),
				context.DeadlineExceeded,
			)
		} else if entry != nil && entry.interrupted.Load() {
			slog.InfoContext(ctx, "AGY stream execution interrupted by request",
				slog.String("session_key", sessionKey),
				slog.String("stderr", stderrBuf.String()),
			)
			timeoutErr = domain.NewExecutionOutcomeError(
				domain.StatusExecutionFailed,
				fmt.Sprintf("AGY stream execution interrupted (session: %s)", sessionKey),
				execCtx.Err(),
			)
		} else if ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			slog.ErrorContext(ctx, "AGY stream caller context deadline exceeded",
				slog.String("session_key", sessionKey),
				slog.String("stderr", stderrBuf.String()),
			)
			timeoutErr = domain.NewExecutionOutcomeError(
				domain.StatusExecutionFailed,
				fmt.Sprintf("AGY stream execution context deadline exceeded (session: %s)", sessionKey),
				ctx.Err(),
			)
		} else {
			slog.WarnContext(ctx, "AGY stream execution cancelled",
				slog.String("session_key", sessionKey),
				slog.String("stderr", stderrBuf.String()),
			)
			timeoutErr = domain.NewExecutionOutcomeError(
				domain.StatusExecutionFailed,
				fmt.Sprintf("AGY stream execution cancelled (session: %s)", sessionKey),
				execCtx.Err(),
			)
		}
		return nil, timeoutErr
	}

	if parseErr != nil {
		slog.ErrorContext(ctx, "AGY stream output parsing failed",
			slog.String("session_key", sessionKey),
			slog.String("error", parseErr.Error()),
			slog.String("stderr", stderrBuf.String()),
		)
		if errors.Is(parseErr, ports.ErrConversationNotFound) {
			return nil, parseErr
		}
		if outcome := domain.ExecutionOutcomeFromError(parseErr); outcome == domain.StatusNativePermissionDenied || outcome == domain.StatusPolicyDenied {
			return executionResultFromStream(streamRes), parseErr
		}
		if waitErr != nil {
			return executionResultFromStream(streamRes), domain.NewExecutionOutcomeError(
				domain.StatusExecutionFailed,
				fmt.Sprintf("process error (%v), stderr: %s", waitErr, stderrBuf.String()),
				ports.ErrProcessExecution,
			)
		}
		return executionResultFromStream(streamRes), parseErr
	}

	res := executionResultFromStream(streamRes)
	if streamRes.Status == "INTERRUPTED" && res.Error == "" {
		res.Error = "INTERRUPTED"
	}
	if waitErr != nil && res.Error == "" {
		res.Error = fmt.Sprintf("process exit error: %v", waitErr)
	}

	// Fallback detect artifacts if not captured during stream
	if len(res.Artifacts) == 0 {
		if req.WorkspaceDir != "" {
			artifacts, err := h.watcher.DetectArtifacts(req.WorkspaceDir, beforeSnapshot)
			if err == nil {
				res.Artifacts = append(res.Artifacts, artifacts...)
			}
		}
		targetConvID := req.ConversationID
		if targetConvID == "" && res != nil {
			targetConvID = res.ConversationID
		}
		if targetConvID != "" {
			brainArts := h.watcher.DetectBrainArtifacts(targetConvID, beforeBrainSnapshot)
			res.Artifacts = append(res.Artifacts, brainArts...)
		}
	}

	return res, nil
}

func executionResultFromStream(streamRes *domain.StreamResultPayload) *domain.ExecutionResult {
	if streamRes == nil {
		return nil
	}
	return &domain.ExecutionResult{
		Success:        streamRes.Status == "SUCCESS",
		Outcome:        streamRes.Outcome,
		ConversationID: streamRes.ConversationID,
		ResponseText:   streamRes.Response,
		DurationSec:    streamRes.DurationSeconds,
		NumTurns:       streamRes.NumTurns,
		Usage:          streamRes.Usage,
		Error:          streamRes.Error,
		Artifacts:      streamRes.Artifacts,
	}
}

// InterruptStream signals an active streaming turn to gracefully finish its current tool/sub-turn and exit.
func (h *Harness) InterruptStream(ctx context.Context, sessionKey string) error {
	val, ok := h.activeStreams.Load(sessionKey)
	if !ok {
		return nil // No active stream for this session
	}
	entry, ok := val.(*streamControlEntry)
	if !ok || entry == nil {
		return nil
	}

	if !entry.interrupted.CompareAndSwap(false, true) {
		return nil // Already interrupted
	}

	slog.InfoContext(ctx, "Sending soft interrupt signal to active stream",
		slog.String("session_key", sessionKey),
		slog.Duration("grace_timeout", h.graceTimeout),
	)

	// 1. Write {"event": "interrupt"}\n to stdin pipe (safely ignoring closed pipe race conditions)
	if entry.stdinWriter != nil {
		entry.mu.Lock()
		_, err := entry.stdinWriter.Write([]byte("{\"event\":\"interrupt\"}\n"))
		entry.mu.Unlock()
		if err != nil && !errors.Is(err, io.ErrClosedPipe) && !errors.Is(err, os.ErrClosed) {
			slog.WarnContext(ctx, "Failed to write interrupt payload to stdin pipe", "error", err)
		}
	}

	// 2. Schedule fallback hard kill if process doesn't exit within GraceTimeout
	graceTimeout := h.graceTimeout
	if graceTimeout <= 0 {
		graceTimeout = 3 * time.Second
	}

	time.AfterFunc(graceTimeout, func() {
		if curVal, stillActive := h.activeStreams.Load(sessionKey); stillActive && curVal == entry {
			slog.WarnContext(context.Background(), "Stream did not exit gracefully within grace period, triggering fallback hard-kill",
				slog.String("session_key", sessionKey),
				slog.Duration("grace_timeout", graceTimeout),
			)
			if entry.cancelFunc != nil {
				entry.cancelFunc()
			}
		}
	})

	return nil
}

// HealthCheck verifies that the AGY CLI binary is accessible and executable.
func (h *Harness) HealthCheck(ctx context.Context) error {
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(checkCtx, h.binaryPath, "--version")
	configureCmd(cmd)
	cmd.Cancel = func() error { return killProcessTree(cmd) }
	cmd.WaitDelay = 2 * time.Second

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("agy healthcheck failed: %w, output: %s", err, string(output))
	}

	if len(bytes.TrimSpace(output)) == 0 {
		return errors.New("agy healthcheck returned empty version info")
	}

	return nil
}

// ListAvailableModels queries available models from the AGY CLI via 'agy models'
// and registers them in the dynamic model registry.
func (h *Harness) ListAvailableModels(ctx context.Context) ([]domain.ModelCapability, error) {
	listCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(listCtx, h.binaryPath, "models")
	cmd.Stdin = bytes.NewReader(nil) // Ensure stdin does not hang in interactive mode
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "TERM=dumb")
	configureCmd(cmd)
	cmd.Cancel = func() error { return killProcessTree(cmd) }
	cmd.WaitDelay = 2 * time.Second

	output, err := cmd.CombinedOutput()
	if err != nil {
		slog.WarnContext(ctx, "Failed to query live models from agy CLI, using cached/fallback catalogue",
			slog.String("error", err.Error()),
		)
		return domain.ListAvailableModels(), nil
	}

	parsed := ParseModelsOutput(string(output))
	if len(parsed) > 0 {
		domain.SetDynamicModelCapabilities(parsed)
		return parsed, nil
	}

	return domain.ListAvailableModels(), nil
}

// buildCommandEnv constructs a subprocess environment injecting APIS-4D isolation variables
// using a strictly sanitized environment variable whitelist to prevent leaking host secrets.
func buildCommandEnv(req domain.ExecutionRequest, sessionKeyOpt ...string) []string {
	safeKeys := map[string]bool{
		"PATH": true, "HOME": true, "TMPDIR": true, "TEMP": true, "TMP": true,
		"LANG": true, "LC_ALL": true, "LC_CTYPE": true, "USER": true, "LOGNAME": true,
		"SHELL": true, "TERM": true, "NO_COLOR": true, "SYSTEMROOT": true, "COMSPEC": true,
		"PATHEXT": true, "WINDIR": true, "APPDATA": true, "LOCALAPPDATA": true,
		"GO_WANT_MOCK_AGY_HELPER": true, "MOCK_SCENARIO": true,
	}

	var envList []string
	for _, env := range os.Environ() {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) > 0 && safeKeys[strings.ToUpper(parts[0])] {
			envList = append(envList, env)
		}
	}
	envList = append(envList, "NO_COLOR=1", "TERM=dumb")
	if req.AgentName != "" {
		envList = append(envList, "AGYENT_AGENT_NAME="+req.AgentName)
	}
	if req.WorkspaceDir != "" {
		envList = append(envList, "AGYENT_AGENT_WORKSPACE="+req.WorkspaceDir)
	}
	sessKey := req.SessionKey
	if sessKey == "" && len(sessionKeyOpt) > 0 {
		sessKey = sessionKeyOpt[0]
	}
	if sessKey != "" {
		envList = append(envList, "AGYENT_SESSION_KEY="+sessKey)
	}
	if req.UserID != "" {
		envList = append(envList, "AGYENT_USER_ID="+req.UserID)
	}
	if req.TurnID != "" {
		envList = append(envList, "AGYENT_TURN_ID="+req.TurnID)
	}
	for k, v := range req.Env {
		if k != "" {
			envList = append(envList, fmt.Sprintf("%s=%s", k, v))
		}
	}
	return envList
}
