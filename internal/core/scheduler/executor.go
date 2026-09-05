package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"agyent/internal/config"
	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

// TaskExecutor handles asynchronous background execution of scheduled tasks and heartbeats.
type TaskExecutor struct {
	cfg              *config.Config
	runner           ports.RunnerPort
	executionService ports.ExecutionServicePort
	workspace        ports.WorkspacePort
	eventBus         ports.EventBusPort
	logger           *slog.Logger
}

// NewTaskExecutor creates a new background TaskExecutor.
func NewTaskExecutor(
	cfg *config.Config,
	runner ports.RunnerPort,
	workspace ports.WorkspacePort,
	eventBus ports.EventBusPort,
	logger *slog.Logger,
) *TaskExecutor {
	if logger == nil {
		logger = slog.Default()
	}
	return &TaskExecutor{
		cfg:       cfg,
		runner:    runner,
		workspace: workspace,
		eventBus:  eventBus,
		logger:    logger,
	}
}

// SetExecutionService injects the execution service chokepoint into the TaskExecutor.
func (e *TaskExecutor) SetExecutionService(svc ports.ExecutionServicePort) {
	e.executionService = svc
}

// ExecuteSchedule runs a single scheduled task in an isolated, non-blocking session turn.
func (e *TaskExecutor) ExecuteSchedule(ctx context.Context, task domain.ScheduleTask) (*domain.ExecutionResult, error) {
	agentWS := config.ResolveAgentWorkspace(e.cfg.Storage.AgentsDir, task.AgentName)
	sessionKey := task.TargetSessionKey
	if sessionKey == "" {
		sessionKey = fmt.Sprintf("sched:%s:%s", task.AgentName, task.ID)
	}

	timeout := 300 * time.Second
	if e.cfg != nil && e.cfg.AGY.DefaultTimeoutSeconds > 0 {
		timeout = time.Duration(e.cfg.AGY.DefaultTimeoutSeconds) * time.Second
	}

	promptText := formatBackgroundPrompt(task.Title, task.Prompt)
	model, effort := e.resolveModelAndEffort(task.AgentName)

	req := domain.ExecutionRequest{
		Prompt:                     promptText,
		ConversationID:             "", // Fresh stateless turn: prevents transcript accumulation
		TurnID:                     fmt.Sprintf("turn-%s-%d", task.ID, time.Now().UnixNano()),
		WorkspaceDir:               agentWS,
		Timeout:                    timeout,
		Model:                      model,
		Effort:                     effort,
		Mode:                       "accept-edits",
		DangerouslySkipPermissions: false,
		AgentName:                  task.AgentName,
		SessionKey:                 sessionKey,
		UserID:                     task.CreatedBy,
	}

	e.logger.Info("Executing scheduled task",
		"task_id", task.ID,
		"agent", task.AgentName,
		"title", task.Title,
		"session_key", sessionKey,
		"model", model,
		"effort", effort,
	)

	var result *domain.ExecutionResult
	var execErr error

	if e.executionService != nil {
		principal := domain.Principal{
			Kind:      domain.PrincipalSystem,
			Provider:  "internal",
			SubjectID: "system:scheduler",
		}
		result, execErr = e.executionService.ExecuteTurn(ctx, principal, req, sessionKey, false)
	} else if e.runner != nil {
		// Use Execute in background worker to execute task prompt
		result, execErr = e.runner.Execute(ctx, req)
		if domain.IsEffortError(execErr, result) && req.Effort != "" {
			e.logger.Warn("Model rejected reasoning effort in scheduled task, retrying without --effort flag",
				"task_id", task.ID, "model", req.Model, "effort", req.Effort)
			req.Effort = ""
			result, execErr = e.runner.Execute(ctx, req)
		}
	} else {
		execErr = fmt.Errorf("runner and execution service ports are not initialized")
	}

	// Publish completion or failure event to EventBus
	if e.eventBus != nil {
		if execErr != nil || (result != nil && !result.Success) {
			errMsg := ""
			if execErr != nil {
				errMsg = execErr.Error()
			} else if result != nil {
				errMsg = result.Error
			}
			task.LastError = errMsg
			e.eventBus.AsyncEmit(ctx, domain.NewEvent(domain.EventScheduleFailed, domain.ScheduleEventPayload{
				Task:  task,
				Error: errMsg,
			}))
		} else {
			task.LastError = ""
			responseText := ""
			convID := ""
			var artifacts []domain.Attachment
			if result != nil {
				responseText = result.ResponseText
				convID = result.ConversationID
				artifacts = result.Artifacts
			}
			e.eventBus.AsyncEmit(ctx, domain.NewEvent(domain.EventScheduleCompleted, domain.ScheduleEventPayload{
				Task:           task,
				Response:       responseText,
				ConversationID: convID,
				WorkspaceDir:   agentWS,
				Artifacts:      artifacts,
			}))
		}
	}

	return result, execErr
}

// ExecuteHeartbeat runs an agent's periodic heartbeat directives.
func (e *TaskExecutor) ExecuteHeartbeat(ctx context.Context, hb domain.HeartbeatConfig) (*domain.ExecutionResult, error) {
	agentWS := config.ResolveAgentWorkspace(e.cfg.Storage.AgentsDir, hb.AgentName)
	sessionKey := hb.TargetSessionKey
	if sessionKey == "" {
		sessionKey = fmt.Sprintf("heartbeat:%s", hb.AgentName)
	}

	var promptDirectives string
	if e.workspace != nil {
		_, prompt, err := e.workspace.ReadHeartbeat(ctx, agentWS)
		if err == nil && strings.TrimSpace(prompt) != "" {
			promptDirectives = prompt
		}
	}

	if promptDirectives == "" {
		promptDirectives = "# Heartbeat Directives\nPerform periodic status check and report any noteworthy updates."
	}

	timeout := 180 * time.Second
	if e.cfg != nil && e.cfg.AGY.DefaultTimeoutSeconds > 0 {
		timeout = time.Duration(e.cfg.AGY.DefaultTimeoutSeconds) * time.Second
	}

	promptText := formatHeartbeatPrompt(hb.AgentName, promptDirectives)
	model, effort := e.resolveModelAndEffort(hb.AgentName)

	req := domain.ExecutionRequest{
		Prompt:                     promptText,
		ConversationID:             "", // Fresh turn
		TurnID:                     fmt.Sprintf("turn-hb-%s-%d", hb.AgentName, time.Now().UnixNano()),
		WorkspaceDir:               agentWS,
		Timeout:                    timeout,
		Model:                      model,
		Effort:                     effort,
		Mode:                       "accept-edits",
		DangerouslySkipPermissions: false,
		AgentName:                  hb.AgentName,
		SessionKey:                 sessionKey,
		UserID:                     "",
	}

	e.logger.Info("Executing agent heartbeat",
		"agent", hb.AgentName,
		"interval", hb.IntervalSeconds,
		"session_key", sessionKey,
		"model", model,
		"effort", effort,
	)

	var result *domain.ExecutionResult
	var execErr error

	if e.executionService != nil {
		principal := domain.Principal{
			Kind:      domain.PrincipalSystem,
			Provider:  "internal",
			SubjectID: "system:heartbeat",
		}
		result, execErr = e.executionService.ExecuteTurn(ctx, principal, req, sessionKey, false)
	} else if e.runner != nil {
		result, execErr = e.runner.Execute(ctx, req)
		if domain.IsEffortError(execErr, result) && req.Effort != "" {
			e.logger.Warn("Model rejected reasoning effort in heartbeat task, retrying without --effort flag",
				"agent", hb.AgentName, "model", req.Model, "effort", req.Effort)
			req.Effort = ""
			result, execErr = e.runner.Execute(ctx, req)
		}
	} else {
		execErr = fmt.Errorf("runner and execution service ports are not initialized")
	}

	// Publish heartbeat event to EventBus
	if e.eventBus != nil {
		responseText := ""
		errMsg := ""
		if execErr != nil {
			errMsg = execErr.Error()
		} else if result != nil {
			if !result.Success {
				errMsg = result.Error
			} else {
				responseText = result.ResponseText
			}
		}

		if errMsg != "" {
			hb.LastError = errMsg
			e.eventBus.AsyncEmit(ctx, domain.NewEvent(domain.EventHeartbeatFailed, domain.HeartbeatEventPayload{
				Config: hb,
				Error:  errMsg,
			}))
		} else {
			hb.LastError = ""
			convID := ""
			var artifacts []domain.Attachment
			if result != nil {
				convID = result.ConversationID
				artifacts = result.Artifacts
			}
			e.eventBus.AsyncEmit(ctx, domain.NewEvent(domain.EventHeartbeatCompleted, domain.HeartbeatEventPayload{
				Config:         hb,
				Response:       responseText,
				ConversationID: convID,
				WorkspaceDir:   agentWS,
				Artifacts:      artifacts,
			}))
		}
	}

	return result, execErr
}

func (e *TaskExecutor) resolveModelAndEffort(agentName string) (string, string) {
	var rawModel string
	var rawEffort string

	if e.cfg != nil {
		if a, ok := e.cfg.Agents[agentName]; ok {
			rawModel = a.DefaultModel
			rawEffort = a.DefaultEffort
		}
		if rawModel == "" && strings.TrimSpace(e.cfg.AGY.DefaultModel) != "" {
			rawModel = e.cfg.AGY.DefaultModel
		}
		if rawEffort == "" && strings.TrimSpace(e.cfg.AGY.DefaultEffort) != "" {
			rawEffort = e.cfg.AGY.DefaultEffort
		}
	}

	if rawModel == "" {
		rawModel = "flash"
	}
	if rawEffort == "" {
		rawEffort = "low"
	}

	var customAliases map[string]string
	if e.cfg != nil {
		customAliases = e.cfg.AGY.ModelAliases
	}

	canonicalModel, normalizedEffort, _ := domain.NormalizeModelAndEffort(rawModel, rawEffort, customAliases)
	return canonicalModel, normalizedEffort
}

func (e *TaskExecutor) resolveModel(agentName string) string {
	m, _ := e.resolveModelAndEffort(agentName)
	return m
}

func (e *TaskExecutor) resolveEffort(agentName string) string {
	_, eff := e.resolveModelAndEffort(agentName)
	return eff
}

// formatBackgroundPrompt injects the scheduled task into Level 4 preserving Levels 0-3 KV-cache.
func formatBackgroundPrompt(title, prompt string) string {
	var sb strings.Builder
	sb.WriteString("[SYSTEM DIRECTIVE: SCHEDULED BACKGROUND TASK EXECUTION]\n")
	if title != "" {
		sb.WriteString(fmt.Sprintf("Task Title: %s\n\n", title))
	}
	sb.WriteString("Instructions:\n")
	sb.WriteString(prompt)
	sb.WriteString("\n\nExecute all requested steps, generate required output or updates, and summarize your final report clearly. If you generate images, charts, or deliverable files, embed or link them directly in your response text (e.g. ![Description](image_path) or [Document Title](file_path)) so they are delivered directly to the user.")
	return sb.String()
}

// formatHeartbeatPrompt injects the heartbeat into Level 4.
func formatHeartbeatPrompt(agentName, directives string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[SYSTEM DIRECTIVE: PERIODIC AGENT HEARTBEAT WAKEUP (@%s)]\n\n", agentName))
	sb.WriteString("Active Heartbeat Directives from HEARTBEAT.md:\n")
	sb.WriteString(directives)
	sb.WriteString("\n\nExecute your checks according to these directives. Formulate your report to be sent to the user. If you generate reports, charts, or images, embed or link them directly in your response text so they are delivered directly to the user.")
	return sb.String()
}
