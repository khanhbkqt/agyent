package agy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"agyent/internal/config"
	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

// Harness implements ports.RunnerPort to execute AGY CLI subprocesses with STDIN streaming,
// cross-platform process tree cleanup, JSON boundary extraction, and artifacts detection.
type Harness struct {
	binaryPath                 string
	defaultTimeout             time.Duration
	defaultEffort              string
	defaultMode                string
	dangerouslySkipPermissions bool
	watcher                    *SnapshotWatcher
	eventBus                   ports.EventBusPort
}

// NewHarness constructs a new Harness runner adapter from AGYConfig.
func NewHarness(cfg config.AGYConfig, bus ...ports.EventBusPort) *Harness {
	timeout := time.Duration(cfg.DefaultTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 300 * time.Second
	}
	effort := cfg.DefaultEffort
	if effort == "" {
		effort = "high"
	}
	mode := cfg.DefaultMode
	if mode == "" {
		mode = "accept-edits"
	}

	var eb ports.EventBusPort
	if len(bus) > 0 {
		eb = bus[0]
	}

	return &Harness{
		binaryPath:                 cfg.BinaryPath,
		defaultTimeout:             timeout,
		defaultEffort:              effort,
		defaultMode:                mode,
		dangerouslySkipPermissions: cfg.DangerouslySkipPermissions,
		watcher:                    NewSnapshotWatcher(),
		eventBus:                   eb,
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
	args := []string{"--output-format", "json", "--project", "outside-of-project"}
	if req.WorkspaceDir != "" {
		args = append(args, "--add-dir", req.WorkspaceDir)
	}
	if req.DangerouslySkipPermissions || h.dangerouslySkipPermissions {
		args = append(args, "--dangerously-skip-permissions")
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

	effort := req.Effort
	if effort == "" {
		effort = h.defaultEffort
	}
	if effort != "" {
		args = append(args, "--effort", effort)
	}

	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}

	cmd := exec.CommandContext(execCtx, h.binaryPath, args...)
	if req.WorkspaceDir != "" {
		cmd.Dir = req.WorkspaceDir
	}
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "TERM=dumb")

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

	// 1. Take workspace snapshot before execution
	beforeSnapshot, _ := h.watcher.TakeSnapshot(req.WorkspaceDir)

	slog.DebugContext(ctx, "Executing AGY CLI subprocess",
		slog.String("binary", h.binaryPath),
		slog.String("workspace", req.WorkspaceDir),
		slog.String("conversation_id", req.ConversationID),
		slog.String("mode", mode),
		slog.String("effort", effort),
	)

	// 2. Execute process
	runErr := cmd.Run()

	// 3. Handle Context Timeout or Cancellation
	if execCtx.Err() != nil {
		if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
			slog.ErrorContext(ctx, "AGY execution timed out", slog.Duration("timeout", timeout), slog.String("workspace", req.WorkspaceDir))
			return nil, fmt.Errorf("agy execution timed out after %v: %w", timeout, execCtx.Err())
		}
		slog.WarnContext(ctx, "AGY execution cancelled", slog.String("workspace", req.WorkspaceDir))
		return nil, fmt.Errorf("agy execution cancelled: %w", execCtx.Err())
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
		if runErr != nil {
			return nil, fmt.Errorf("%w: process error (%v), stderr: %s", ports.ErrProcessExecution, runErr, stderrBuf.String())
		}
		return nil, parseErr
	}

	// 5. Detect artifacts created or modified during the run
	if req.WorkspaceDir != "" {
		artifacts, err := h.watcher.DetectArtifacts(req.WorkspaceDir, beforeSnapshot)
		if err == nil {
			res.Artifacts = artifacts
		}
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

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Build CLI arguments for streaming
	args := []string{"--output-format", "stream-json", "--project", "outside-of-project"}
	if req.WorkspaceDir != "" {
		args = append(args, "--add-dir", req.WorkspaceDir)
	}
	if req.DangerouslySkipPermissions || h.dangerouslySkipPermissions {
		args = append(args, "--dangerously-skip-permissions")
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

	effort := req.Effort
	if effort == "" {
		effort = h.defaultEffort
	}
	if effort != "" {
		args = append(args, "--effort", effort)
	}

	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}

	cmd := exec.CommandContext(execCtx, h.binaryPath, args...)
	if req.WorkspaceDir != "" {
		cmd.Dir = req.WorkspaceDir
	}
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "TERM=dumb")

	// STDIN Streaming
	cmd.Stdin = strings.NewReader(req.Prompt)

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

	// Take workspace snapshot before execution
	beforeSnapshot, _ := h.watcher.TakeSnapshot(req.WorkspaceDir)

	slog.DebugContext(ctx, "Starting AGY CLI stream subprocess",
		slog.String("binary", h.binaryPath),
		slog.String("session_key", sessionKey),
		slog.String("workspace", req.WorkspaceDir),
		slog.String("conversation_id", req.ConversationID),
	)

	// Start subprocess
	if err := cmd.Start(); err != nil {
		slog.ErrorContext(ctx, "Failed to start AGY stream subprocess", slog.String("error", err.Error()))
		return nil, fmt.Errorf("%w: failed to start agy process: %v", ports.ErrProcessExecution, err)
	}

	parser := NewStreamParser(h.eventBus)
	streamRes, parseErr := parser.ParseAndEmitStream(execCtx, sessionKey, stdoutPipe)

	// Ensure stdoutPipe is closed so subprocess unblocks if still writing, preventing cmd.Wait() deadlock
	_ = stdoutPipe.Close()

	waitErr := cmd.Wait()

	if execCtx.Err() != nil {
		if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
			slog.ErrorContext(ctx, "AGY stream execution timed out", slog.Duration("timeout", timeout), slog.String("session_key", sessionKey))
			return nil, fmt.Errorf("agy stream execution timed out after %v: %w", timeout, execCtx.Err())
		}
		slog.WarnContext(ctx, "AGY stream execution cancelled", slog.String("session_key", sessionKey))
		return nil, fmt.Errorf("agy stream execution cancelled: %w", execCtx.Err())
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
		if waitErr != nil {
			return nil, fmt.Errorf("%w: process error (%v), stderr: %s", ports.ErrProcessExecution, waitErr, stderrBuf.String())
		}
		return nil, parseErr
	}

	res := &domain.ExecutionResult{
		Success:        streamRes.Status == "SUCCESS",
		ConversationID: streamRes.ConversationID,
		ResponseText:   streamRes.Response,
		DurationSec:    streamRes.DurationSeconds,
		Usage:          streamRes.Usage,
		Error:          streamRes.Error,
	}

	// Detect artifacts
	if req.WorkspaceDir != "" {
		artifacts, err := h.watcher.DetectArtifacts(req.WorkspaceDir, beforeSnapshot)
		if err == nil {
			res.Artifacts = artifacts
		}
	}

	return res, nil
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

	parsed := domain.ParseModelsOutput(string(output))
	if len(parsed) > 0 {
		domain.SetDynamicModelCapabilities(parsed)
		return parsed, nil
	}

	return domain.ListAvailableModels(), nil
}

