package subagent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"agyent/internal/adapters/harness/agy"
	"agyent/internal/core/domain"
)

// TurnResult represents the output of a single subprocess execution turn.
type TurnResult struct {
	ConversationID  string
	Status          domain.SubagentTaskStatus
	Question        string
	Response        string
	Usage           domain.TokenUsage
	DurationSeconds float64
	Artifacts       []domain.Attachment
	ToolsExecuted   []string
	ErrorMessage    string
}

// taskExecutor coordinates spawning and NDJSON stream scanning for a subagent OS subprocess.
type taskExecutor struct {
	binaryPath     string
	defaultTimeout time.Duration
}

func newTaskExecutor(binaryPath string, defaultTimeout time.Duration) *taskExecutor {
	if defaultTimeout <= 0 {
		defaultTimeout = 300 * time.Second
	}
	return &taskExecutor{
		binaryPath:     binaryPath,
		defaultTimeout: defaultTimeout,
	}
}

func (e *taskExecutor) executeTurn(
	parentCtx context.Context,
	tCtx *taskRuntimeContext,
	prompt string,
	convID string,
	onEvent func(evt agy.StreamEvent),
) (*TurnResult, error) {
	task := tCtx.snapshot()

	execCtx, cancel := context.WithTimeout(parentCtx, e.defaultTimeout)
	tCtx.mu.Lock()
	tCtx.cancel = cancel
	tCtx.mu.Unlock()
	defer cancel()

	args := []string{
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--project", "outside-of-project",
		"--dangerously-skip-permissions",
		"--mode", "accept-edits",
	}

	workspaceDir := ""
	if task.WorkspaceMode == "share" && task.ProjectName != "" {
		if fi, err := os.Stat(task.ProjectName); err == nil && fi.IsDir() {
			workspaceDir = task.ProjectName
		}
	}
	if workspaceDir != "" {
		args = append(args, "--add-dir", workspaceDir)
	}

	modelInput := task.Model
	if modelInput == "" {
		modelInput = "flash"
	}
	resolvedModel := modelInput
	resolvedEffort := task.Effort
	if cap, inferredEffort, ok := domain.LookupModelCapability(modelInput, nil); ok {
		resolvedModel = cap.ID
		if inferredEffort != "" {
			resolvedEffort = inferredEffort
		} else if resolvedEffort == "" {
			resolvedEffort = cap.DefaultEffort
		}
		if len(cap.SupportedEfforts) == 0 {
			resolvedEffort = ""
		}
	}
	if resolvedModel != "" {
		args = append(args, "--model", resolvedModel)
	}
	if resolvedEffort != "" {
		args = append(args, "--effort", resolvedEffort)
	}

	if convID != "" {
		args = append(args, "--conversation", convID)
	}

	inboundMsg := map[string]any{
		"event": "user",
		"message": map[string]any{
			"content": prompt,
		},
	}
	inboundJSON, err := json.Marshal(inboundMsg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal inbound stream message: %w", err)
	}

	cmd := exec.CommandContext(execCtx, e.binaryPath, args...)
	if workspaceDir != "" {
		cmd.Dir = workspaceDir
	}
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "TERM=dumb")
	cmd.Stdin = strings.NewReader(string(inboundJSON) + "\n")

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to open stdout pipe: %w", err)
	}

	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	agy.ConfigureCmd(cmd)
	jobGuard, err := agy.CreateProcessJobGuard()
	if err == nil {
		tCtx.mu.Lock()
		tCtx.jobGuard = jobGuard
		tCtx.mu.Unlock()
		defer jobGuard.Close()
	}
	cmd.Cancel = func() error { return agy.KillProcessTree(cmd) }
	cmd.WaitDelay = 3 * time.Second

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start subagent process: %w", err)
	}

	tCtx.mu.Lock()
	tCtx.cmd = cmd
	tCtx.mu.Unlock()

	if jobGuard != nil && cmd.Process != nil {
		_ = jobGuard.AttachProcess(cmd.Process)
	}

	scanner := bufio.NewScanner(stdoutPipe)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 10*1024*1024)

	var lastResponseBuilder strings.Builder
	var turnConvID = convID
	var turnUsage domain.TokenUsage
	var toolsExecuted []string
	var isWaitingInput bool
	var pendingQuestion string

	for scanner.Scan() {
		if execCtx.Err() != nil {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		cleanLine := agy.StripANSI(line)
		if !strings.HasPrefix(cleanLine, "{") {
			continue
		}

		var rawEvt agy.StreamEvent
		if err := json.Unmarshal([]byte(cleanLine), &rawEvt); err != nil {
			continue
		}

		if rawEvt.ConversationID != "" {
			turnConvID = rawEvt.ConversationID
			tCtx.mu.Lock()
			tCtx.conversationID = turnConvID
			tCtx.task.SubConversationID = turnConvID
			tCtx.mu.Unlock()
		}

		if onEvent != nil {
			onEvent(rawEvt)
		}

		switch rawEvt.Event {
		case "step_update":
			if rawEvt.StepUpdate != nil {
				step := rawEvt.StepUpdate
				name := step.ToolName
				if name == "" && step.ToolInfo != nil {
					name = step.ToolInfo.Name
				}

				if name == "ask_question" || step.State == "WAITING_FOR_INPUT" {
					isWaitingInput = true
					if step.ToolInfo != nil && step.ToolInfo.Parameters != nil {
						if q, ok := step.ToolInfo.Parameters["question"].(string); ok && q != "" {
							pendingQuestion = q
						}
					}
					tCtx.updateProgress(step.StepIndex, "ask_question", "Waiting for input from user/main agent...")
				} else if step.StepType == "tool" {
					if step.State == "ACTIVE" {
						tCtx.updateProgress(step.StepIndex, name, fmt.Sprintf("Executing tool %s...", name))
					} else if step.State == "DONE" {
						toolsExecuted = append(toolsExecuted, name)
					}
				} else if step.StepType == "agent_response" && step.TextDelta != "" {
					lastResponseBuilder.WriteString(step.TextDelta)
				}
			}
		case "result":
			if rawEvt.Result != nil {
				turnUsage = rawEvt.Result.Usage
				if turnUsage.TotalTokens == 0 {
					turnUsage.TotalTokens = turnUsage.InputTokens + turnUsage.OutputTokens + turnUsage.ThinkingTokens
				}
				if rawEvt.Result.Response != "" {
					lastResponseBuilder.Reset()
					lastResponseBuilder.WriteString(rawEvt.Result.Response)
				}
			}
		case "error":
			if rawEvt.Error != "" {
				return &TurnResult{
					ConversationID:  turnConvID,
					Status:          domain.TaskStatusFailed,
					ErrorMessage:    rawEvt.Error,
					DurationSeconds: time.Since(tCtx.startedAt).Seconds(),
				}, fmt.Errorf("stream execution error: %s", rawEvt.Error)
			}
		}
	}

	_ = stdoutPipe.Close()
	scanErr := scanner.Err()
	waitErr := cmd.Wait()

	response := strings.TrimSpace(lastResponseBuilder.String())
	durationSec := time.Since(tCtx.startedAt).Seconds()

	if execCtx.Err() != nil {
		return &TurnResult{
			ConversationID:  turnConvID,
			Status:          domain.TaskStatusFailed,
			ErrorMessage:    "task execution timed out or was cancelled",
			DurationSeconds: durationSec,
		}, execCtx.Err()
	}

	if scanErr != nil {
		return &TurnResult{
			ConversationID:  turnConvID,
			Status:          domain.TaskStatusFailed,
			ErrorMessage:    fmt.Sprintf("scanner error: %v", scanErr),
			DurationSeconds: durationSec,
		}, scanErr
	}

	if isWaitingInput {
		if pendingQuestion == "" {
			pendingQuestion = response
		}
		return &TurnResult{
			ConversationID:  turnConvID,
			Status:          domain.TaskStatusWaitingInput,
			Question:        pendingQuestion,
			Response:        response,
			Usage:           turnUsage,
			DurationSeconds: durationSec,
			ToolsExecuted:   toolsExecuted,
		}, nil
	}

	if waitErr != nil && response == "" {
		errMsg := strings.TrimSpace(stderrBuf.String())
		if errMsg == "" {
			errMsg = waitErr.Error()
		}
		return &TurnResult{
			ConversationID:  turnConvID,
			Status:          domain.TaskStatusFailed,
			ErrorMessage:    errMsg,
			DurationSeconds: durationSec,
		}, waitErr
	}

	return &TurnResult{
		ConversationID:  turnConvID,
		Status:          domain.TaskStatusCompleted,
		Response:        response,
		Usage:           turnUsage,
		DurationSeconds: durationSec,
		ToolsExecuted:   toolsExecuted,
	}, nil
}
