package subagent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"agyent/internal/adapters/harness/agy"
	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
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
	binaryPath      string
	defaultTimeout  time.Duration
	securityManager ports.SecurityManagerPort
	policy          ports.PolicyEngine
	storage         ports.StoragePort
}

func newTaskExecutor(binaryPath string, defaultTimeout time.Duration) *taskExecutor {
	if defaultTimeout <= 0 {
		defaultTimeout = 1800 * time.Second
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
		"--mode", "accept-edits",
	}

	workspaceDir, agent, cleanupWorkspace, err := e.resolveWorkspace(parentCtx, task)
	if err != nil {
		return &TurnResult{
			ConversationID:  convID,
			Status:          domain.TaskStatusFailed,
			ErrorMessage:    err.Error(),
			DurationSeconds: time.Since(tCtx.startedAt).Seconds(),
		}, err
	}
	defer cleanupWorkspace()
	args = append(args, "--add-dir", workspaceDir)

	// Policy authorization check for system:subagent principal
	principal := domain.Principal{
		Kind:      domain.PrincipalSystem,
		Provider:  "internal",
		SubjectID: "system:subagent",
	}
	if e.policy == nil {
		err := errors.New("subagent execution denied: policy engine is not initialized")
		return &TurnResult{ConversationID: convID, Status: domain.TaskStatusFailed, ErrorMessage: err.Error()}, err
	}
	res := domain.Resource{
		Kind:        domain.ResourceKindAgent,
		ID:          task.AgentName,
		AgentName:   task.AgentName,
		SessionKey:  task.ParentSessionKey,
		ProjectName: task.ProjectName,
		OwnerID:     agent.OwnerID,
		IsPublic:    agent.IsPublic,
	}
	if err := e.policy.Authorize(parentCtx, principal, domain.ActionTaskDispatch, res); err != nil {
		errMsg := fmt.Sprintf("unauthorized: policy denied subagent execution: %v", err)
		return &TurnResult{
			ConversationID:  convID,
			Status:          domain.TaskStatusFailed,
			ErrorMessage:    errMsg,
			DurationSeconds: 0,
		}, errors.New(errMsg)
	}
	if e.securityManager == nil {
		err := errors.New("subagent execution denied: security manager is not initialized")
		return &TurnResult{ConversationID: convID, Status: domain.TaskStatusFailed, ErrorMessage: err.Error()}, err
	}
	if err := e.securityManager.EnsureWorkspaceHooks(workspaceDir); err != nil {
		err = fmt.Errorf("subagent security hook provisioning failed: %w", err)
		return &TurnResult{ConversationID: convID, Status: domain.TaskStatusFailed, ErrorMessage: err.Error()}, err
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

	turnID := fmt.Sprintf("turn-sub-%s-%d", task.ID, time.Now().UnixNano())
	e.securityManager.RegisterActiveTurn(domain.TurnSecurityContext{
		TurnID:         turnID,
		ConversationID: convID,
		SessionKey:     task.ParentSessionKey,
		Principal:      principal,
		Action:         domain.ActionTaskDispatch,
		Resource:       res,
		WorkspaceDir:   workspaceDir,
		AgentName:      task.AgentName,
		ProjectName:    task.ProjectName,
		Preset:         agent.SecurityPreset,
		CreatedAt:      time.Now(),
	})
	defer e.securityManager.UnregisterTurnByID(turnID)

	cmd := exec.CommandContext(execCtx, e.binaryPath, args...)
	cmd.Dir = workspaceDir
	env := append(os.Environ(),
		"NO_COLOR=1",
		"TERM=dumb",
		"AGYENT_TURN_ID="+turnID,
		"AGYENT_SESSION_KEY="+task.ParentSessionKey,
		"AGYENT_AGENT_NAME="+task.AgentName,
		"AGYENT_PROJECT_NAME="+task.ProjectName,
	)
	cmd.Env = env
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

// resolveWorkspace selects a workspace solely from authoritative agent/project
// records. In particular, a task's ProjectName is an identifier, never an
// arbitrary filesystem path supplied by a model or plugin.
func (e *taskExecutor) resolveWorkspace(ctx context.Context, task domain.SubagentTask) (string, *domain.Agent, func(), error) {
	if e.storage == nil {
		return "", nil, nil, errors.New("subagent execution denied: storage is not initialized")
	}
	agent, err := e.storage.GetAgent(ctx, task.AgentName)
	if err != nil || agent == nil {
		return "", nil, nil, fmt.Errorf("subagent execution denied: unable to resolve agent %q: %w", task.AgentName, err)
	}
	if strings.TrimSpace(agent.WorkspacePath) == "" {
		return "", nil, nil, fmt.Errorf("subagent execution denied: agent %q has no workspace", task.AgentName)
	}

	workspaceDir := agent.WorkspacePath
	cleanup := func() {}
	switch task.WorkspaceMode {
	case "share":
		if task.ProjectName != "" {
			projectID := domain.FormatProjectID(task.AgentName, task.ProjectName)
			project, err := e.storage.GetProject(ctx, projectID)
			if err != nil || project == nil || project.AgentName != task.AgentName || project.ProjectPath == "" {
				return "", nil, nil, fmt.Errorf("subagent execution denied: unable to resolve project %q", task.ProjectName)
			}
			workspaceDir = project.ProjectPath
		}
	case "persona":
		// Agent workspace selected above.
	case "scratch":
		scratchRoot := filepath.Join(os.TempDir(), "agyent-subagent-scratch")
		if err := os.MkdirAll(scratchRoot, 0700); err != nil {
			return "", nil, nil, fmt.Errorf("create subagent scratch root: %w", err)
		}
		scratchDir, err := os.MkdirTemp(scratchRoot, task.ID+"-")
		if err != nil {
			return "", nil, nil, fmt.Errorf("create subagent scratch workspace: %w", err)
		}
		workspaceDir = scratchDir
		cleanup = func() { _ = os.RemoveAll(scratchDir) }
	default:
		return "", nil, nil, fmt.Errorf("subagent execution denied: unsupported workspace mode %q", task.WorkspaceMode)
	}

	absoluteWorkspace, err := filepath.Abs(workspaceDir)
	if err != nil {
		cleanup()
		return "", nil, nil, fmt.Errorf("resolve subagent workspace: %w", err)
	}
	info, err := os.Stat(absoluteWorkspace)
	if err != nil || !info.IsDir() {
		cleanup()
		return "", nil, nil, fmt.Errorf("subagent execution denied: workspace is unavailable")
	}
	return absoluteWorkspace, agent, cleanup, nil
}
