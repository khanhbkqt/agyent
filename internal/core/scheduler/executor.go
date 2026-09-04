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
	cfg       *config.Config
	runner    ports.RunnerPort
	workspace ports.WorkspacePort
	eventBus  ports.EventBusPort
	logger    *slog.Logger
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

	req := domain.ExecutionRequest{
		Prompt:                     promptText,
		ConversationID:             "", // Fresh stateless turn: prevents transcript accumulation
		TurnID:                     fmt.Sprintf("turn-%s-%d", task.ID, time.Now().UnixNano()),
		WorkspaceDir:               agentWS,
		Timeout:                    timeout,
		Model:                      e.resolveModel(task.AgentName),
		Effort:                     e.resolveEffort(task.AgentName),
		Mode:                       "accept-edits",
		DangerouslySkipPermissions: true,
		AgentName:                  task.AgentName,
		SessionKey:                 sessionKey,
		UserID:                     task.CreatedBy,
	}

	e.logger.Info("Executing scheduled task",
		"task_id", task.ID,
		"agent", task.AgentName,
		"title", task.Title,
		"session_key", sessionKey,
	)

	var result *domain.ExecutionResult
	var execErr error

	if e.runner != nil {
		// Use Execute in background worker to execute task prompt
		result, execErr = e.runner.Execute(ctx, req)
	} else {
		execErr = fmt.Errorf("runner port is not initialized")
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
			if result != nil {
				responseText = result.ResponseText
			}
			e.eventBus.AsyncEmit(ctx, domain.NewEvent(domain.EventScheduleCompleted, domain.ScheduleEventPayload{
				Task:     task,
				Response: responseText,
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

	req := domain.ExecutionRequest{
		Prompt:                     promptText,
		ConversationID:             "", // Fresh turn
		TurnID:                     fmt.Sprintf("turn-hb-%s-%d", hb.AgentName, time.Now().UnixNano()),
		WorkspaceDir:               agentWS,
		Timeout:                    timeout,
		Model:                      e.resolveModel(hb.AgentName),
		Effort:                     e.resolveEffort(hb.AgentName),
		Mode:                       "accept-edits",
		DangerouslySkipPermissions: true,
		AgentName:                  hb.AgentName,
		SessionKey:                 sessionKey,
		UserID:                     "",
	}

	e.logger.Info("Executing agent heartbeat",
		"agent", hb.AgentName,
		"interval", hb.IntervalSeconds,
		"session_key", sessionKey,
	)

	var result *domain.ExecutionResult
	var execErr error

	if e.runner != nil {
		result, execErr = e.runner.Execute(ctx, req)
	} else {
		execErr = fmt.Errorf("runner port is not initialized")
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
			e.eventBus.AsyncEmit(ctx, domain.NewEvent(domain.EventHeartbeatCompleted, domain.HeartbeatEventPayload{
				Config:   hb,
				Response: responseText,
			}))
		}
	}

	return result, execErr
}

func (e *TaskExecutor) resolveModel(agentName string) string {
	if e.cfg != nil {
		if a, ok := e.cfg.Agents[agentName]; ok && strings.TrimSpace(a.DefaultModel) != "" {
			return a.DefaultModel
		}
		if strings.TrimSpace(e.cfg.AGY.DefaultModel) != "" {
			return e.cfg.AGY.DefaultModel
		}
	}
	return "flash"
}

func (e *TaskExecutor) resolveEffort(agentName string) string {
	if e.cfg != nil {
		if a, ok := e.cfg.Agents[agentName]; ok && strings.TrimSpace(a.DefaultEffort) != "" {
			return a.DefaultEffort
		}
		if strings.TrimSpace(e.cfg.AGY.DefaultEffort) != "" {
			return e.cfg.AGY.DefaultEffort
		}
	}
	return "low"
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
	sb.WriteString("\n\nExecute all requested steps, generate required output or updates, and summarize your final report clearly.")
	return sb.String()
}

// formatHeartbeatPrompt injects the heartbeat into Level 4.
func formatHeartbeatPrompt(agentName, directives string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[SYSTEM DIRECTIVE: PERIODIC AGENT HEARTBEAT WAKEUP (@%s)]\n\n", agentName))
	sb.WriteString("Active Heartbeat Directives from HEARTBEAT.md:\n")
	sb.WriteString(directives)
	sb.WriteString("\n\nExecute your checks according to these directives. Formulate your report to be sent to the user.")
	return sb.String()
}
