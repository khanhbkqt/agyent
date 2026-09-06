package agy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	preflightCanaryCommand = "echo AGYENT_PREFLIGHT_CANARY"
	preflightTurnID        = "agyent-preflight-invalid-turn"
)

type preflightCommandRunner func(
	ctx context.Context,
	binaryPath string,
	args []string,
	workspaceDir string,
	env []string,
	stdin []byte,
) (stdout []byte, stderr []byte, err error)

// PreflightCanaryCheck verifies that AGY recognizes the project-scoped native
// command grant and then reaches the fail-closed agyent PreToolUse hook. The
// canary deliberately uses an invalid turn ID, so the command must never run.
func PreflightCanaryCheck(ctx context.Context, binaryPath, projectID, workspaceDir string) error {
	return preflightCanaryCheck(ctx, binaryPath, projectID, workspaceDir, runPreflightCommand)
}

func preflightCanaryCheck(
	ctx context.Context,
	binaryPath, projectID, workspaceDir string,
	run preflightCommandRunner,
) error {
	if strings.TrimSpace(binaryPath) == "" {
		return errors.New("AGY preflight: binary path is empty")
	}
	if strings.TrimSpace(projectID) == "" {
		return errors.New("AGY preflight: project ID is empty")
	}
	if strings.TrimSpace(workspaceDir) == "" {
		return errors.New("AGY preflight: workspace directory is empty")
	}

	prompt := "Call run_command exactly once with this exact command, then stop: " + preflightCanaryCommand
	inbound, err := json.Marshal(map[string]any{
		"event":   "user",
		"message": map[string]any{"content": prompt},
	})
	if err != nil {
		return fmt.Errorf("AGY preflight: encode input: %w", err)
	}
	inbound = append(inbound, '\n')

	args := []string{
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--project", projectID,
		"--sandbox",
		"--add-dir", workspaceDir,
		"--mode", "accept-edits",
		"--disable-slash-commands",
		"--print-timeout", "45s",
	}
	env := append(os.Environ(),
		"AGYENT_TURN_ID="+preflightTurnID,
		"AGYENT_AGENT_WORKSPACE="+workspaceDir,
		"AGYENT_AGENT_NAME=preflight-canary",
		"AGYENT_SESSION_KEY=preflight-canary",
		"AGYENT_USER_ID=preflight-canary",
	)

	stdout, stderr, runErr := run(ctx, binaryPath, args, workspaceDir, env, inbound)
	return validatePreflightCanaryOutput(stdout, stderr, runErr)
}

func runPreflightCommand(
	ctx context.Context,
	binaryPath string,
	args []string,
	workspaceDir string,
	env []string,
	stdin []byte,
) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, binaryPath, args...)
	cmd.Dir = workspaceDir
	cmd.Env = env
	cmd.Stdin = bytes.NewReader(stdin)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	configureCmd(cmd)
	cmd.Cancel = func() error { return killProcessTree(cmd) }
	cmd.WaitDelay = 3 * time.Second

	jobGuard, _ := CreateProcessJobGuard()
	if jobGuard != nil {
		defer jobGuard.Close()
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	if jobGuard != nil && cmd.Process != nil {
		_ = jobGuard.AttachProcess(cmd.Process)
	}
	err := cmd.Wait()
	return stdout.Bytes(), stderr.Bytes(), err
}

func validatePreflightCanaryOutput(stdout, stderr []byte, runErr error) error {
	combined := strings.ToLower(string(stdout) + "\n" + string(stderr))
	if strings.Contains(combined, "denied_actions") ||
		strings.Contains(combined, "headless mode cannot prompt") ||
		strings.Contains(combined, "tool required the \"command\" permission") {
		return errors.New("AGY preflight: native project grant was rejected")
	}

	hookDenied := false
	for _, line := range bytes.Split(stdout, []byte{'\n'}) {
		var evt StreamEvent
		if json.Unmarshal(bytes.TrimSpace(line), &evt) != nil || evt.StepUpdate == nil {
			continue
		}
		step := evt.StepUpdate
		if step.StepType != "tool" || step.ToolInfo == nil {
			continue
		}
		toolName := step.ToolName
		if toolName == "" {
			toolName = step.ToolInfo.Name
		}
		if toolName != "run_command" {
			continue
		}
		errorData, _ := json.Marshal(step.ToolInfo.Error)
		errorMessage := strings.ToLower(string(errorData))
		if strings.Contains(errorMessage, "pre-tool hook") ||
			strings.Contains(errorMessage, "invalid or expired turnid") ||
			strings.Contains(errorMessage, "security service unavailable") {
			hookDenied = true
		}
		output, _ := json.Marshal(step.ToolInfo.Output)
		if strings.Contains(strings.ToLower(string(output)), strings.ToLower("AGYENT_PREFLIGHT_CANARY")) {
			return errors.New("AGY preflight: canary command executed instead of being denied by the security hook")
		}
	}
	if hookDenied {
		return nil
	}
	if runErr != nil {
		return fmt.Errorf("AGY preflight: process failed before verified hook denial: %w", runErr)
	}
	return errors.New("AGY preflight: could not verify project grant reached the security hook")
}
