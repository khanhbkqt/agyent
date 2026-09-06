package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
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
	securityManager  ports.SecurityManagerPort
	workspace        ports.WorkspacePort
	eventBus         ports.EventBusPort
	pluginManager    ports.PluginManagerPort
	mcpRegistry      ports.MCPRegistryPort
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

// SetSecurityManager injects the security manager into the TaskExecutor.
func (e *TaskExecutor) SetSecurityManager(sec ports.SecurityManagerPort) {
	e.securityManager = sec
}

// SetPluginManager injects the plugin manager into the TaskExecutor.
func (e *TaskExecutor) SetPluginManager(pm ports.PluginManagerPort) {
	e.pluginManager = pm
}

// SetMCPRegistry injects the MCP registry syncer into the TaskExecutor.
func (e *TaskExecutor) SetMCPRegistry(mcp ports.MCPRegistryPort) {
	e.mcpRegistry = mcp
}

// ExecuteSchedule runs a single scheduled task in an isolated, non-blocking session turn.
func (e *TaskExecutor) ExecuteSchedule(ctx context.Context, task domain.ScheduleTask) (*domain.ExecutionResult, error) {
	agentWS := config.ResolveAgentWorkspace(e.cfg.Storage.AgentsDir, task.AgentName)

	// Build or preserve a channel-routable session key so HITL can deliver approval cards to the interactive channel
	sessionKey := task.TargetSessionKey
	if sessionKey == "" || strings.HasPrefix(sessionKey, "sched:") {
		if task.Channel != "" && task.ChatID != "" {
			var threadID int64
			if task.ThreadID != "" {
				threadID, _ = strconv.ParseInt(task.ThreadID, 10, 64)
			}
			sessionKey = domain.FormatSessionKey(task.Channel, task.ChatID, threadID)
		} else {
			sessionKey = fmt.Sprintf("sched:%s:%s", task.AgentName, task.ID)
		}
	}

	// Invalidate session grants and ensure workspace security hooks
	if e.securityManager != nil {
		defer e.securityManager.ClearSessionGrants(sessionKey)
		_ = e.securityManager.EnsureWorkspaceHooks(agentWS)
	}

	// Dynamic MCP mounting for this scheduled turn
	if e.pluginManager != nil && e.mcpRegistry != nil {
		resolvedCtx, err := e.pluginManager.AssembleActivePlugins(ctx, "", agentWS)
		if err == nil && resolvedCtx != nil && len(resolvedCtx.ActiveMCPServers) > 0 {
			releaseLease, leaseErr := e.mcpRegistry.AcquireExclusiveTurn(ctx)
			if leaseErr == nil {
				defer releaseLease()
				activeServers := make([]domain.MCPServerConfig, len(resolvedCtx.ActiveMCPServers))
				copy(activeServers, resolvedCtx.ActiveMCPServers)
				for i := range activeServers {
					if activeServers[i].Env == nil {
						activeServers[i].Env = make(map[string]string)
					}
					if _, exists := activeServers[i].Env["AGYENT_AGENT_NAME"]; !exists {
						activeServers[i].Env["AGYENT_AGENT_NAME"] = task.AgentName
					}
					if _, exists := activeServers[i].Env["AGYENT_AGENT_WORKSPACE"]; !exists {
						activeServers[i].Env["AGYENT_AGENT_WORKSPACE"] = agentWS
					}
					if _, exists := activeServers[i].Env["AGYENT_SESSION_KEY"]; !exists {
						activeServers[i].Env["AGYENT_SESSION_KEY"] = sessionKey
					}
					if _, exists := activeServers[i].Env["AGYENT_USER_ID"]; !exists {
						activeServers[i].Env["AGYENT_USER_ID"] = task.CreatedBy
					}
				}
				if mountErr := e.mcpRegistry.MountServers(ctx, sessionKey, activeServers); mountErr == nil {
					defer func() {
						_ = e.mcpRegistry.UnmountServers(context.Background(), sessionKey, activeServers)
					}()
				}
			}
		}
	}

	timeout := 1800 * time.Second
	if e.cfg != nil {
		if e.cfg.Scheduler.DefaultTaskTimeoutSeconds > 0 {
			timeout = time.Duration(e.cfg.Scheduler.DefaultTaskTimeoutSeconds) * time.Second
		} else if e.cfg.AGY.DefaultTimeoutSeconds > 0 {
			timeout = time.Duration(e.cfg.AGY.DefaultTimeoutSeconds) * time.Second
		}
	}
	if timeout < 30*time.Second {
		timeout = 30 * time.Second
	}

	// Dynamic Timeout for Image / Multimedia Tasks:
	// Generating images via Diffusion/Gemini backends consumes 35s - 65s without token emission.
	// Automatically elevate timeout to at least 300s to prevent premature watchdog/deadline cancellation.
	if isImageOrMediaTask(task.Title, task.Prompt) {
		if timeout < 300*time.Second {
			timeout = 300 * time.Second
		}
	}

	promptText := formatBackgroundPrompt(task.Title, task.Prompt)
	model, effort := e.resolveModelAndEffort(task.AgentName)

	skipPerms := false
	if e.cfg != nil && e.cfg.AGY.DangerouslySkipPermissions {
		skipPerms = true
	}

	req := domain.ExecutionRequest{
		Prompt:                     promptText,
		ConversationID:             "", // Fresh stateless turn: prevents transcript accumulation
		TurnID:                     fmt.Sprintf("turn-%s-%d", task.ID, time.Now().UnixNano()),
		WorkspaceDir:               agentWS,
		Timeout:                    timeout,
		Model:                      model,
		Effort:                     effort,
		Mode:                       "accept-edits",
		DangerouslySkipPermissions: skipPerms,
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
		"timeout", timeout,
	)

	var result *domain.ExecutionResult
	var execErr error

	if e.executionService != nil {
		// Delegated User Principal: Inherit identity of task creator + agent persona
		var principal domain.Principal
		if task.CreatedBy != "" && task.CreatedBy != "system" {
			provider := task.Channel
			if provider == "" && strings.Contains(sessionKey, ":") {
				provider = strings.Split(sessionKey, ":")[0]
			}
			if provider == "" {
				provider = "telegram"
			}
			principal = domain.Principal{
				Kind:      domain.PrincipalUser,
				Provider:  provider,
				SubjectID: task.CreatedBy,
				AccountID: task.AgentName,
			}
		} else {
			principal = domain.Principal{
				Kind:      domain.PrincipalSystem,
				Provider:  "internal",
				SubjectID: "system:scheduler",
			}
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

	// Quality Assertion: Validate output integrity and guard against false positive completion
	if execErr == nil && result != nil && result.Success {
		trimmed := strings.TrimSpace(result.ResponseText)
		if trimmed == "" {
			result.Success = false
			result.Error = "scheduled task completed with empty output"
		} else if isSoftDenyResponse(trimmed) {
			result.Success = false
			result.Error = fmt.Sprintf("scheduled task was blocked by security gate: %s", truncateText(trimmed, 150))
		} else if isImageOrMediaTask(task.Title, task.Prompt) {
			if len(result.Artifacts) == 0 && !containsImageMarkdown(trimmed) {
				result.Success = false
				result.Error = "multimedia generation task finished without producing media artifacts or image links"
			}
		}
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
	if sessionKey == "" || strings.HasPrefix(sessionKey, "hb:") || strings.HasPrefix(sessionKey, "heartbeat:") {
		if hb.Channel != "" && hb.ChatID != "" {
			var threadID int64
			if hb.ThreadID != "" {
				threadID, _ = strconv.ParseInt(hb.ThreadID, 10, 64)
			}
			sessionKey = domain.FormatSessionKey(hb.Channel, hb.ChatID, threadID)
		} else {
			sessionKey = fmt.Sprintf("heartbeat:%s", hb.AgentName)
		}
	}

	// Invalidate session grants and ensure workspace security hooks
	if e.securityManager != nil {
		defer e.securityManager.ClearSessionGrants(sessionKey)
		_ = e.securityManager.EnsureWorkspaceHooks(agentWS)
	}

	// Dynamic MCP mounting for this heartbeat turn
	if e.pluginManager != nil && e.mcpRegistry != nil {
		resolvedCtx, err := e.pluginManager.AssembleActivePlugins(ctx, "", agentWS)
		if err == nil && resolvedCtx != nil && len(resolvedCtx.ActiveMCPServers) > 0 {
			releaseLease, leaseErr := e.mcpRegistry.AcquireExclusiveTurn(ctx)
			if leaseErr == nil {
				defer releaseLease()
				activeServers := make([]domain.MCPServerConfig, len(resolvedCtx.ActiveMCPServers))
				copy(activeServers, resolvedCtx.ActiveMCPServers)
				for i := range activeServers {
					if activeServers[i].Env == nil {
						activeServers[i].Env = make(map[string]string)
					}
					if _, exists := activeServers[i].Env["AGYENT_AGENT_NAME"]; !exists {
						activeServers[i].Env["AGYENT_AGENT_NAME"] = hb.AgentName
					}
					if _, exists := activeServers[i].Env["AGYENT_AGENT_WORKSPACE"]; !exists {
						activeServers[i].Env["AGYENT_AGENT_WORKSPACE"] = agentWS
					}
					if _, exists := activeServers[i].Env["AGYENT_SESSION_KEY"]; !exists {
						activeServers[i].Env["AGYENT_SESSION_KEY"] = sessionKey
					}
				}
				if mountErr := e.mcpRegistry.MountServers(ctx, sessionKey, activeServers); mountErr == nil {
					defer func() {
						_ = e.mcpRegistry.UnmountServers(context.Background(), sessionKey, activeServers)
					}()
				}
			}
		}
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
	if e.cfg != nil {
		if e.cfg.Scheduler.HeartbeatTimeoutSeconds > 0 {
			timeout = time.Duration(e.cfg.Scheduler.HeartbeatTimeoutSeconds) * time.Second
		} else if e.cfg.AGY.DefaultTimeoutSeconds > 0 {
			timeout = time.Duration(e.cfg.AGY.DefaultTimeoutSeconds) * time.Second
		}
	}
	if timeout < 30*time.Second {
		timeout = 30 * time.Second
	}

	promptText := formatHeartbeatPrompt(hb.AgentName, promptDirectives)
	model, effort := e.resolveModelAndEffort(hb.AgentName)

	skipPerms := false
	if e.cfg != nil && e.cfg.AGY.DangerouslySkipPermissions {
		skipPerms = true
	}

	req := domain.ExecutionRequest{
		Prompt:                     promptText,
		ConversationID:             "", // Fresh turn
		TurnID:                     fmt.Sprintf("turn-hb-%s-%d", hb.AgentName, time.Now().UnixNano()),
		WorkspaceDir:               agentWS,
		Timeout:                    timeout,
		Model:                      model,
		Effort:                     effort,
		Mode:                       "accept-edits",
		DangerouslySkipPermissions: skipPerms,
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
			AccountID: hb.AgentName,
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

	// Quality Assertion: Validate output integrity for heartbeat
	if execErr == nil && result != nil && result.Success {
		trimmed := strings.TrimSpace(result.ResponseText)
		if isSoftDenyResponse(trimmed) {
			result.Success = false
			result.Error = fmt.Sprintf("heartbeat check was blocked by security gate: %s", truncateText(trimmed, 150))
		}
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

// isImageOrMediaTask checks whether a scheduled task prompt or title requests image generation or multimedia processing.
func isImageOrMediaTask(title, prompt string) bool {
	combined := strings.ToLower(title + " " + prompt)
	keywords := []string{
		"generate_image",
		"image",
		"photo",
		"picture",
		"ảnh",
		"hình ảnh",
		"vẽ ảnh",
		"sinh ảnh",
		"tạo ảnh",
		"gửi ảnh",
		"multimedia",
		"media",
		"diffusion",
		"illustration",
		"diagram",
		"avatar",
	}
	for _, kw := range keywords {
		if strings.Contains(combined, kw) {
			return true
		}
	}
	return false
}

func isSoftDenyResponse(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "soft-denying") ||
		strings.Contains(lower, "action rejected by user") ||
		strings.Contains(lower, "user denied permission") ||
		strings.Contains(lower, "permission check failed") ||
		strings.Contains(lower, "[security gate]: action rejected")
}

func containsImageMarkdown(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(text, "![") ||
		strings.Contains(lower, ".png") ||
		strings.Contains(lower, ".jpg") ||
		strings.Contains(lower, ".jpeg") ||
		strings.Contains(lower, ".webp")
}

func truncateText(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

