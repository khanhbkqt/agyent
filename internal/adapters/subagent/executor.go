package subagent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// taskExecutor coordinates executing subagent turns through the authorized execution service chokepoint.
type taskExecutor struct {
	binaryPath       string
	defaultTimeout   time.Duration
	securityManager  ports.SecurityManagerPort
	policy           ports.PolicyEngine
	storage          ports.StoragePort
	executionService ports.ExecutionServicePort
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
) (*TurnResult, error) {
	task := tCtx.snapshot()

	execCtx, cancel := context.WithTimeout(parentCtx, e.defaultTimeout)
	tCtx.mu.Lock()
	tCtx.cancel = cancel
	tCtx.mu.Unlock()
	defer cancel()

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

	// 1. Fail-closed policy verification
	if e.policy == nil {
		err := errors.New("subagent execution denied: policy engine is not initialized")
		return &TurnResult{ConversationID: convID, Status: domain.TaskStatusFailed, ErrorMessage: err.Error()}, err
	}

	principal := domain.Principal{
		Kind:      domain.PrincipalSystem,
		Provider:  "internal",
		SubjectID: "system:subagent",
		TenantID:  task.TenantID,
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

	if e.securityManager != nil {
		if err := e.securityManager.EnsureWorkspaceHooks(workspaceDir); err != nil {
			err = fmt.Errorf("subagent security hook provisioning failed: %w", err)
			return &TurnResult{ConversationID: convID, Status: domain.TaskStatusFailed, ErrorMessage: err.Error()}, err
		}
	}

	// 2. Chokepoint verification (ARCH-02)
	if e.executionService == nil {
		err := errors.New("subagent execution denied: execution service is not initialized")
		return &TurnResult{ConversationID: convID, Status: domain.TaskStatusFailed, ErrorMessage: err.Error()}, err
	}

	// 3. Resolve Model and Effort
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

	subagentSessionKey := fmt.Sprintf("subagent:%s", task.ID)

	req := domain.ExecutionRequest{
		Prompt:         prompt,
		WorkspaceDir:   workspaceDir,
		ConversationID: convID,
		AgentName:      task.AgentName,
		ProjectName:    task.ProjectName,
		Model:          resolvedModel,
		Effort:         resolvedEffort,
		Timeout:        e.defaultTimeout,
		Mode:           "accept-edits",
		SessionKey:     subagentSessionKey,
		UserID:         task.TenantID,
	}

	// 4. Route through unified execution chokepoint (ARCH-02)
	execRes, execErr := e.executionService.ExecuteTurn(execCtx, principal, req, subagentSessionKey, true)
	durationSec := time.Since(tCtx.startedAt).Seconds()

	if execCtx.Err() != nil {
		return &TurnResult{
			ConversationID:  convID,
			Status:          domain.TaskStatusFailed,
			ErrorMessage:    "task execution timed out or was cancelled",
			DurationSeconds: durationSec,
		}, execCtx.Err()
	}

	if execErr != nil {
		return &TurnResult{
			ConversationID:  convID,
			Status:          domain.TaskStatusFailed,
			ErrorMessage:    execErr.Error(),
			DurationSeconds: durationSec,
		}, execErr
	}

	if execRes == nil {
		return &TurnResult{
			ConversationID:  convID,
			Status:          domain.TaskStatusFailed,
			ErrorMessage:    "empty execution result",
			DurationSeconds: durationSec,
		}, errors.New("empty execution result")
	}

	turnConvID := execRes.ConversationID
	if turnConvID == "" {
		turnConvID = convID
	} else {
		tCtx.mu.Lock()
		tCtx.conversationID = turnConvID
		tCtx.task.SubConversationID = turnConvID
		tCtx.mu.Unlock()
	}

	tCtx.mu.RLock()
	isWaiting := tCtx.task.Status == domain.TaskStatusWaitingInput
	pendingQ := tCtx.pendingQuestion
	tCtx.mu.RUnlock()

	if isWaiting {
		if pendingQ == "" {
			pendingQ = execRes.ResponseText
		}
		return &TurnResult{
			ConversationID:  turnConvID,
			Status:          domain.TaskStatusWaitingInput,
			Question:        pendingQ,
			Response:        execRes.ResponseText,
			Usage:           execRes.Usage,
			DurationSeconds: durationSec,
			Artifacts:       execRes.Artifacts,
		}, nil
	}

	if !execRes.Success {
		errMsg := execRes.Error
		if errMsg == "" {
			errMsg = "subagent execution failed"
		}
		return &TurnResult{
			ConversationID:  turnConvID,
			Status:          domain.TaskStatusFailed,
			ErrorMessage:    errMsg,
			DurationSeconds: durationSec,
		}, errors.New(errMsg)
	}

	return &TurnResult{
		ConversationID:  turnConvID,
		Status:          domain.TaskStatusCompleted,
		Response:        execRes.ResponseText,
		Usage:           execRes.Usage,
		DurationSeconds: durationSec,
		Artifacts:       execRes.Artifacts,
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
