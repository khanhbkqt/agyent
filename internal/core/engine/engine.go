package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"agyent/internal/config"
	"agyent/internal/core/concurrency"
	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

// Engine is the central orchestrator connecting Storage, EventBus, Debouncer,
// Subprocess Runner, LockManager, ContextResolver, MCPRegistry, PluginManager, and Channel adapters.
type Engine struct {
	cfg             *config.Config
	storage         ports.StoragePort
	runner          ports.RunnerPort
	channel         ports.ChannelPort
	eventBus        ports.EventBusPort
	debouncer       ports.DebouncerPort
	lockManager     ports.LockManager
	contextResolver ports.ContextResolverPort
	mcpRegistry     ports.MCPRegistryPort
	pluginManager   ports.PluginManagerPort
	temporal           ports.TemporalContextPort
	evolution          ports.EvolutionOrchestratorPort
	subagentDispatcher ports.SubagentDispatcherPort

	streamingEnabled atomic.Bool
	startTime        time.Time

	turnsMu     sync.Mutex
	activeTurns map[string]context.CancelFunc

	inboundChan chan domain.CanonicalMessage
	ctx         context.Context
	cancel      context.CancelFunc
	running     atomic.Bool
	wg          sync.WaitGroup
}

// NewEngine constructs and wires a new Engine orchestrator instance.
func NewEngine(
	cfg *config.Config,
	storage ports.StoragePort,
	runner ports.RunnerPort,
	channel ports.ChannelPort,
	eventBus ports.EventBusPort,
	debouncer ports.DebouncerPort,
	lockManager ports.LockManager,
	contextResolver ports.ContextResolverPort,
	mcpRegistry ports.MCPRegistryPort,
	pluginManager ports.PluginManagerPort,
) *Engine {
	ctx, cancel := context.WithCancel(context.Background())

	e := &Engine{
		cfg:             cfg,
		storage:         storage,
		runner:          runner,
		channel:         channel,
		eventBus:        eventBus,
		debouncer:       debouncer,
		lockManager:     lockManager,
		contextResolver: contextResolver,
		mcpRegistry:     mcpRegistry,
		pluginManager:   pluginManager,
		activeTurns:     make(map[string]context.CancelFunc),
		inboundChan:     make(chan domain.CanonicalMessage, 200),
		ctx:             ctx,
		cancel:          cancel,
		startTime:       time.Now(),
	}

	if cfg != nil {
		e.streamingEnabled.Store(cfg.AGY.StreamingEnabled)
	} else {
		e.streamingEnabled.Store(true)
	}

	return e
}

// SetSubagentDispatcher injects the background subagent dispatcher.
func (e *Engine) SetSubagentDispatcher(s ports.SubagentDispatcherPort) {
	e.subagentDispatcher = s
}

// GetSubagentDispatcher returns the active subagent dispatcher instance.
func (e *Engine) GetSubagentDispatcher() ports.SubagentDispatcherPort {
	return e.subagentDispatcher
}

// IsStreamingEnabled returns the current dynamic streaming mode status.
func (e *Engine) IsStreamingEnabled() bool {
	return e.streamingEnabled.Load()
}

// SetStreamingEnabled dynamically toggles real-time streaming mode on or off.
func (e *Engine) SetStreamingEnabled(enabled bool) {
	e.streamingEnabled.Store(enabled)
}

// SetTemporalContext injects the temporal gap context formatter.
func (e *Engine) SetTemporalContext(t ports.TemporalContextPort) {
	e.temporal = t
}

// SetEvolutionOrchestrator injects the self-learning evolution orchestrator.
func (e *Engine) SetEvolutionOrchestrator(evo ports.EvolutionOrchestratorPort) {
	e.evolution = evo
}

// Start initializes inbound channel consumption and begins background turn processing.
func (e *Engine) Start(ctx context.Context) error {
	if !e.running.CompareAndSwap(false, true) {
		return errors.New("engine is already running")
	}

	// 1. Start channel adapter with engine's inbound queue
	if err := e.channel.Start(ctx, e.inboundChan); err != nil {
		e.running.Store(false)
		return fmt.Errorf("failed to start channel adapter: %w", err)
	}

	// 2. Start subagent dispatcher worker pool if present
	if e.subagentDispatcher != nil {
		if err := e.subagentDispatcher.Start(e.ctx); err != nil {
			slog.Warn("failed to start subagent dispatcher", "error", err)
		}
		e.subscribeSubagentEvents()
	}

	// 2. Consume from inbound queue and ingest into Debouncer
	e.wg.Add(1)
	concurrency.SafeGo(func() {
		defer e.wg.Done()
		for {
			select {
			case <-e.ctx.Done():
				return
			case msg, ok := <-e.inboundChan:
				if !ok {
					return
				}
				_ = e.debouncer.Ingest(e.ctx, msg)
			}
		}
	})

	// 3. Start background Conversation Lifecycle GC Worker (runs daily, purge threshold 30 days)
	e.StartConversationGCWorker(e.ctx, 24*time.Hour, 30)

	// 4. Start background Evolution Orchestrator (if configured)
	if e.evolution != nil {
		_ = e.evolution.Start(e.ctx)
	}

	return nil
}

// HasActiveTurn returns true if a turn execution is currently in-flight for sessionKey.
func (e *Engine) HasActiveTurn(sessionKey string) bool {
	e.turnsMu.Lock()
	defer e.turnsMu.Unlock()
	_, exists := e.activeTurns[sessionKey]
	return exists
}

// HandleDebouncedMessage is the callback invoked by the debouncer upon sliding window expiration or command fast-path.
func (e *Engine) HandleDebouncedMessage(ctx context.Context, msg domain.CanonicalMessage) error {
	if e.evolution != nil {
		e.evolution.NotifyUserActivity(msg.SessionKey())
	}

	if msg.IsCommand() {
		cmd, args := msg.CommandArgs()
		if strings.ToLower(cmd) == "/ask" {
			if len(args) == 0 {
				if e.channel != nil {
					_ = e.channel.Send(ctx, domain.OutboundMessage{
						ChatID:           msg.Chat.ID,
						ThreadID:         msg.Chat.ThreadID,
						Text:             "⚠️ **Usage:** `/ask <prompt>`\n*Example:* `/ask Write regex to validate IPv6 in Go`\n\n_Questions are answered independently without polluting the main conversation history._",
						ParseMode:        "HTML",
						ReplyToMessageID: msg.ID,
					})
				}
				return nil
			}
			msg.Text = strings.Join(args, " ")
			return e.executeTurn(ctx, msg, true)
		}

		outbound, err := e.HandleCommand(ctx, msg)
		if err != nil {
			return err
		}
		if outbound != nil && e.channel != nil {
			return e.channel.Send(ctx, *outbound)
		}
		return nil
	}

	return e.executeTurn(ctx, msg, false)
}

func (e *Engine) executeTurn(ctx context.Context, msg domain.CanonicalMessage, isEphemeralOpt ...bool) error {
	isEphemeral := len(isEphemeralOpt) > 0 && isEphemeralOpt[0]
	sessionKey := msg.SessionKey()
	timeout := 300 * time.Second
	if e.cfg != nil && e.cfg.AGY.DefaultTimeoutSeconds > 0 {
		timeout = time.Duration(e.cfg.AGY.DefaultTimeoutSeconds) * time.Second
	}

	slog.DebugContext(ctx, "Starting execution turn",
		slog.String("session_key", sessionKey),
		slog.String("sender", msg.Sender.Username),
		slog.String("chat_id", msg.Chat.ID),
		slog.Bool("is_ephemeral", isEphemeral),
	)

	// 1. Acquire Per-Session FIFO Lock
	unlock, err := e.lockManager.Acquire(ctx, sessionKey, timeout)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to acquire session lock",
			slog.String("session_key", sessionKey),
			slog.String("error", err.Error()),
		)
		_ = e.channel.Send(ctx, domain.OutboundMessage{
			ChatID:           msg.Chat.ID,
			ThreadID:         msg.Chat.ThreadID,
			Text:             fmt.Sprintf("⚠️ Could not acquire session lock: %v. Please try again or use `/force_unlock`.", err),
			ReplyToMessageID: msg.ID,
		})
		return fmt.Errorf("failed to acquire session lock: %w", err)
	}
	defer unlock()

	// 2. Setup Turn Context with Cancellation Map for /force_unlock
	turnCtx, turnCancel := context.WithTimeout(ctx, timeout)
	defer turnCancel()

	e.registerActiveTurn(sessionKey, turnCancel)
	defer e.unregisterActiveTurn(sessionKey)

	// Refresh typing indicator while resolving session and context
	_ = e.channel.SendTyping(turnCtx, msg.Chat.ID, msg.Chat.ThreadID)

	// 3. Load Session from Storage
	defaultAgent := "agyent"
	session, err := e.storage.GetOrCreateSession(turnCtx, sessionKey, defaultAgent)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to load session state",
			slog.String("session_key", sessionKey),
			slog.String("error", err.Error()),
		)
		_ = e.channel.Send(ctx, domain.OutboundMessage{
			ChatID:           msg.Chat.ID,
			ThreadID:         msg.Chat.ThreadID,
			Text:             fmt.Sprintf("⚠️ Failed to load session state: %v", err),
			ReplyToMessageID: msg.ID,
		})
		return fmt.Errorf("failed to load session: %w", err)
	}
	session.UpdatedAt = time.Now()
	_ = e.storage.SaveSession(turnCtx, session)

	// 4. Resolve Active Agent & Check Bootstrap
	agent, err := e.storage.GetAgent(turnCtx, session.ActiveAgent)
	if err != nil {
		agentPath := config.ResolveAgentWorkspace(e.cfg.Storage.AgentsDir, session.ActiveAgent)
		_ = os.MkdirAll(agentPath, 0755)
		agent = &domain.Agent{
			Name:          session.ActiveAgent,
			Description:   "Agyent - Trợ lý AI cá nhân đa năng",
			Status:        domain.StatusUninitialized,
			WorkspacePath: agentPath,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		}
		_ = e.storage.SaveAgent(turnCtx, agent)
	}

	if agent.WorkspacePath == "" {
		agent.WorkspacePath = config.ResolveAgentWorkspace(e.cfg.Storage.AgentsDir, agent.Name)
	}

	isBootstrap := !agent.IsInitialized()

	// 5. Resolve CWD (In-Project Workspace vs Global Agent Workspace)
	workspaceDir := agent.WorkspacePath
	contextTag := fmt.Sprintf("🌐 [%s • Global]", agent.Name)

	if isEphemeral {
		contextTag = fmt.Sprintf("🌐 [%s • Hỏi nhanh]", agent.Name)
	} else if session.ActiveProject != "" {
		projID := domain.FormatProjectID(session.ActiveAgent, session.ActiveProject)
		if proj, err := e.storage.GetProject(turnCtx, projID); err == nil && proj != nil {
			workspaceDir = proj.ProjectPath
			contextTag = fmt.Sprintf("📁 [%s • %s]", agent.Name, proj.ProjectName)
		}
	}
	_ = os.MkdirAll(workspaceDir, 0755)

	// 6. Context Resolution & Plugin Assembly
	var promptText string
	var activeMCPServers []domain.MCPServerConfig

	if isBootstrap {
		promptText = BuildBootstrapPrompt(agent, msg.Sender, msg.Text)
	} else {
		var resolved *domain.ResolvedContext
		if e.contextResolver != nil {
			resolved, _ = e.contextResolver.Resolve(turnCtx, agent.WorkspacePath, workspaceDir)
		}

		if e.pluginManager != nil {
			pluginResolved, _ := e.pluginManager.AssembleActivePlugins(turnCtx, agent.WorkspacePath, workspaceDir)
			if pluginResolved != nil {
				if resolved == nil {
					resolved = pluginResolved
				} else {
					if pluginResolved.WorkspaceDirectives != "" {
						resolved.CombinedDirectives += "\n\n" + pluginResolved.WorkspaceDirectives
					}
					resolved.SkillHeaders = append(resolved.SkillHeaders, pluginResolved.SkillHeaders...)
					resolved.ActiveMCPServers = append(resolved.ActiveMCPServers, pluginResolved.ActiveMCPServers...)
				}
			}
		}

		// Compute Temporal Gap Marker (~6 tokens) using resolved user location
		var temporalTag string
		if e.temporal != nil && !session.UpdatedAt.IsZero() {
			loc := time.Local
			if resolved != nil && resolved.UserLocation != nil {
				loc = resolved.UserLocation
			}
			temporalTag = e.temporal.FormatTemporalTag(session.UpdatedAt, time.Now(), loc)
		}

		activeConvID := session.GetActiveConversationID()
		if isEphemeral {
			activeConvID = ""
		}

		if resolved != nil {
			activeMCPServers = resolved.ActiveMCPServers
		}

		if activeConvID != "" {
			// Subsequent Turn: AGY CLI maintains context, history, and workspace files in its brain.
			// Send only continuation prompt (attachments, temporal tag, and user text) to prevent transcript duplication & cache busting.
			promptText = ComposeContinuationPrompt(msg, temporalTag)
		} else {
			// Fresh Turn (ConversationID is empty or Ephemeral mode): inject full Level 0-4 foundation and directives.
			if resolved != nil {
				promptText = ComposeResolvedTurnPrompt(resolved, msg, temporalTag)
			} else {
				knowledgeDirectives := LoadAgentKnowledgeDirectives(agent.WorkspacePath)
				promptText = ComposeTurnPrompt(knowledgeDirectives, msg)
			}
		}
	}

	// 7. Dynamic MCP Mounting (with deferred unmount for Zero Context Leakage)
	if len(activeMCPServers) > 0 && e.mcpRegistry != nil {
		_ = e.mcpRegistry.MountServers(turnCtx, sessionKey, activeMCPServers)
		defer func() {
			_ = e.mcpRegistry.UnmountServers(context.Background(), sessionKey, activeMCPServers)
		}()
	}

	activeConvID := session.GetActiveConversationID()
	if isEphemeral {
		activeConvID = ""
	}

	// 8. Resolve Model & Reasoning Effort
	resolvedModel, resolvedEffort, _ := e.ResolveExecutionParams("", "", session, agent)

	// Construct ExecutionRequest
	req := domain.ExecutionRequest{
		Prompt:                     promptText,
		ConversationID:             activeConvID,
		WorkspaceDir:               workspaceDir,
		Timeout:                    timeout,
		Model:                      resolvedModel,
		Effort:                     resolvedEffort,
		Mode:                       e.cfg.AGY.DefaultMode,
		DangerouslySkipPermissions: e.cfg.AGY.DangerouslySkipPermissions,
	}

	isStream := e.IsStreamingEnabled()

	if e.eventBus != nil {
		_ = e.eventBus.SyncEmit(turnCtx, domain.NewEvent(domain.EventPreExecution, req))
	}

	var execResult *domain.ExecutionResult
	var execErr error

	if isStream {
		execResult, execErr = e.runner.ExecuteStream(turnCtx, req, sessionKey)

		// Edge Case: If agy fails due to unsupported effort flag, retry once with effort stripped
		if isEffortError(execErr, execResult) && req.Effort != "" {
			slog.WarnContext(turnCtx, "Effort flag rejected by model/CLI, retrying without --effort",
				slog.String("model", req.Model),
				slog.String("effort", req.Effort),
			)
			req.Effort = ""
			resolvedEffort = ""
			execResult, execErr = e.runner.ExecuteStream(turnCtx, req, sessionKey)
		}

		if errors.Is(execErr, ports.ErrConversationNotFound) {
			if !isEphemeral && session.GetActiveConversationID() != "" {
				_ = e.storage.SetConversationArchived(turnCtx, session.GetActiveConversationID(), true)
			}
			session.ResetActiveConversationID()
			_ = e.storage.SaveSession(turnCtx, session)
			req.ConversationID = ""
			execResult, execErr = e.runner.ExecuteStream(turnCtx, req, sessionKey)
		}
	} else {
		heartbeatStop := make(chan struct{})
		var heartbeatOnce sync.Once
		stopHeartbeat := func() {
			heartbeatOnce.Do(func() {
				close(heartbeatStop)
			})
		}
		defer stopHeartbeat()

		concurrency.SafeGo(func() {
			_ = e.channel.SendTyping(turnCtx, msg.Chat.ID, msg.Chat.ThreadID)
			ticker := time.NewTicker(4 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-heartbeatStop:
					return
				case <-turnCtx.Done():
					return
				case <-ticker.C:
					_ = e.channel.SendTyping(turnCtx, msg.Chat.ID, msg.Chat.ThreadID)
				}
			}
		})

		// Execute Runner with Transient Error Retry & Exponential Backoff (1s, 2s, 4s)
		for attempt := 0; attempt < 3; attempt++ {
			execResult, execErr = e.runner.Execute(turnCtx, req)
			if execErr == nil && execResult != nil && execResult.Success {
				break
			}
			if errors.Is(execErr, ports.ErrConversationNotFound) {
				break
			}
			if isEffortError(execErr, execResult) && req.Effort != "" {
				slog.WarnContext(turnCtx, "Effort flag rejected by model/CLI in batch mode, retrying without --effort",
					slog.String("model", req.Model),
					slog.String("effort", req.Effort),
				)
				req.Effort = ""
				resolvedEffort = ""
				continue
			}
			if isTransientError(execErr, execResult) && attempt < 2 {
				backoff := time.Duration(1<<attempt) * time.Second
				slog.WarnContext(turnCtx, "Transient error in runner execution, retrying with exponential backoff",
					slog.Int("attempt", attempt+1),
					slog.Duration("backoff", backoff),
				)
				select {
				case <-turnCtx.Done():
					break
				case <-time.After(backoff):
				}
				continue
			}
			break
		}
		stopHeartbeat()

		if errors.Is(execErr, ports.ErrConversationNotFound) {
			if !isEphemeral && session.GetActiveConversationID() != "" {
				_ = e.storage.SetConversationArchived(turnCtx, session.GetActiveConversationID(), true)
			}
			session.ResetActiveConversationID()
			_ = e.storage.SaveSession(turnCtx, session)
			req.ConversationID = ""
			execResult, execErr = e.runner.Execute(turnCtx, req)
		}

		if execErr == nil && execResult != nil && execResult.Success {
			// In-Memory Tool Output Pruning on response text if oversized
			responseText := PruneToolOutput(execResult.ResponseText)
			if msg.Chat.Type != "private" || isEphemeral {
				responseText = fmt.Sprintf("%s\n\n%s", contextTag, responseText)
			}

			var outboundAtts []domain.OutboundAttachment
			for _, att := range execResult.Artifacts {
				outboundAtts = append(outboundAtts, domain.OutboundAttachment{
					FilePath: att.FilePath,
					FileName: att.FileName,
					MIMEType: att.MIMEType,
					Type:     att.Type,
					Caption:  att.FileName,
				})
			}

			_ = e.channel.Send(turnCtx, domain.OutboundMessage{
				ChatID:           msg.Chat.ID,
				ThreadID:         msg.Chat.ThreadID,
				Text:             responseText,
				ParseMode:        "HTML",
				Attachments:      outboundAtts,
				ReplyToMessageID: msg.ID,
			})
		}
	}

	// 9. Record Audit Log & Update State
	auditStatus := "SUCCESS"
	var errMsg string
	if execErr != nil {
		auditStatus = "ERROR"
		errMsg = execErr.Error()
	} else if execResult != nil && !execResult.Success {
		auditStatus = "ERROR"
		errMsg = execResult.Error
	}

	audit := &domain.AuditLog{
		SessionKey:     sessionKey,
		AgentName:      agent.Name,
		ProjectName:    session.ActiveProject,
		ConversationID: session.GetActiveConversationID(),
		Model:          resolvedModel,
		Effort:         resolvedEffort,
		PromptLength:   len(msg.Text),
		Status:         auditStatus,
		ErrorMessage:   errMsg,
		CreatedAt:      time.Now(),
	}

	if execResult != nil {
		audit.ResponseLength = len(execResult.ResponseText)
		audit.DurationSeconds = execResult.DurationSec
		audit.Usage = execResult.Usage
		if execResult.ConversationID != "" && !isEphemeral {
			audit.ConversationID = execResult.ConversationID
			session.SetActiveConversationID(execResult.ConversationID)
			_ = e.storage.SaveSession(turnCtx, session)
			_ = e.storage.TouchConversation(turnCtx, sessionKey, agent.Name, session.ActiveProject, execResult.ConversationID, msg.Text)
		}
	}

	_ = e.storage.LogAudit(turnCtx, audit)

	if auditStatus == "ERROR" {
		slog.ErrorContext(ctx, "Turn execution failed",
			slog.String("session_key", sessionKey),
			slog.String("agent", agent.Name),
			slog.String("project", session.ActiveProject),
			slog.String("error", errMsg),
			slog.Float64("duration_sec", audit.DurationSeconds),
		)
	} else {
		slog.InfoContext(ctx, "Turn execution succeeded",
			slog.String("session_key", sessionKey),
			slog.String("agent", agent.Name),
			slog.String("project", session.ActiveProject),
			slog.Int("total_tokens", audit.Usage.TotalTokens),
			slog.Float64("duration_sec", audit.DurationSeconds),
		)
	}

	if isBootstrap && auditStatus == "SUCCESS" {
		agent.Status = domain.StatusInitialized
		agent.UpdatedAt = time.Now()
		_ = e.storage.SaveAgent(turnCtx, agent)
	}

	if e.eventBus != nil {
		if execErr != nil {
			_ = e.eventBus.SyncEmit(turnCtx, domain.NewEvent(domain.EventErrorOccurred, execErr.Error()))
		} else {
			_ = e.eventBus.SyncEmit(turnCtx, domain.NewEvent(domain.EventPostExecution, execResult))
		}
	}

	if execErr != nil {
		if !isStream {
			_ = e.channel.Send(ctx, domain.OutboundMessage{
				ChatID:           msg.Chat.ID,
				ThreadID:         msg.Chat.ThreadID,
				Text:             fmt.Sprintf("⚠️ Execution failed: %v", execErr),
				ReplyToMessageID: msg.ID,
			})
		}
		return execErr
	}

	return nil
}

func (e *Engine) registerActiveTurn(sessionKey string, cancel context.CancelFunc) {
	e.turnsMu.Lock()
	defer e.turnsMu.Unlock()
	e.activeTurns[sessionKey] = cancel
}

func (e *Engine) unregisterActiveTurn(sessionKey string) {
	e.turnsMu.Lock()
	defer e.turnsMu.Unlock()
	delete(e.activeTurns, sessionKey)
}

func (e *Engine) cancelActiveTurn(sessionKey string) {
	e.turnsMu.Lock()
	defer e.turnsMu.Unlock()
	if cancel, exists := e.activeTurns[sessionKey]; exists {
		cancel()
		delete(e.activeTurns, sessionKey)
	}
}

// Stop gracefully shuts down the engine, cancels in-flight turns, and closes background workers.
func (e *Engine) Stop(ctx context.Context) error {
	if !e.running.CompareAndSwap(true, false) {
		return nil
	}

	if e.evolution != nil {
		_ = e.evolution.Stop(ctx)
	}

	if e.subagentDispatcher != nil {
		_ = e.subagentDispatcher.Stop(ctx)
	}

	e.turnsMu.Lock()
	for k, cancel := range e.activeTurns {
		cancel()
		delete(e.activeTurns, k)
	}
	e.turnsMu.Unlock()

	e.cancel()

	done := make(chan struct{})
	concurrency.SafeGo(func() {
		e.wg.Wait()
		close(done)
	})

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// isTransientError identifies temporary retryable errors (Rate Limit 429, 503 Overloaded, broken pipes, network drops).
func isTransientError(err error, result *domain.ExecutionResult) bool {
	if err != nil {
		errStr := strings.ToLower(err.Error())
		if strings.Contains(errStr, "429") ||
			strings.Contains(errStr, "rate limit") ||
			strings.Contains(errStr, "resource exhausted") ||
			strings.Contains(errStr, "503") ||
			strings.Contains(errStr, "service unavailable") ||
			strings.Contains(errStr, "connection reset") ||
			strings.Contains(errStr, "pipe:") {
			return true
		}
	}
	if result != nil && !result.Success && result.Error != "" {
		errStr := strings.ToLower(result.Error)
		if strings.Contains(errStr, "429") ||
			strings.Contains(errStr, "rate limit") ||
			strings.Contains(errStr, "resource exhausted") ||
			strings.Contains(errStr, "503") ||
			strings.Contains(errStr, "service unavailable") {
			return true
		}
	}
	return false
}

// isEffortError identifies cases where the underlying CLI or model rejected the reasoning effort flag.
func isEffortError(err error, result *domain.ExecutionResult) bool {
	var errStr string
	if err != nil {
		errStr += err.Error() + " "
	}
	if result != nil && !result.Success && result.Error != "" {
		errStr += result.Error + " "
	}
	low := strings.ToLower(errStr)
	return strings.Contains(low, "flag provided but not defined: -effort") ||
		strings.Contains(low, "unknown flag: --effort") ||
		strings.Contains(low, "effort not supported") ||
		strings.Contains(low, "unrecognized effort") ||
		strings.Contains(low, "invalid effort")
}

func (e *Engine) subscribeSubagentEvents() {
	if e.eventBus == nil {
		return
	}

	e.eventBus.SubscribeAsync(domain.EventSubagentWaitingInput, func(ctx context.Context, evt domain.Event) {
		payload, ok := evt.Payload.(domain.SubagentEventPayload)
		if !ok {
			return
		}
		task := payload.Task

		if task.CallbackMode == domain.CallbackInvokeMain {
			syntheticMsg := domain.CanonicalMessage{
				ID:        fmt.Sprintf("sub-synth-%s", task.ID),
				Timestamp: time.Now(),
				Channel:   "telegram",
				Chat: domain.ChatContext{
					ID: extractChatIDFromSessionKey(task.ParentSessionKey),
				},
				Text: fmt.Sprintf("[SYSTEM NOTIFICATION: Subagent Task #%s (@%s) is WAITING FOR INPUT]\nTask: %s\nQuestion: %q\n\nPlease evaluate the question. If you know the answer from your rules/memory, call send_subagent_input(task_id: %q, input: \"...\") immediately. Otherwise, ask the user for clarification.",
					task.ID, task.AgentName, task.Title, task.PendingQuestion, task.ID),
			}
			select {
			case e.inboundChan <- syntheticMsg:
			default:
				slog.Warn("inbound queue full for subagent synthetic message", "task_id", task.ID)
			}
		} else if task.CallbackMode == domain.CallbackNotifyUser && e.channel != nil {
			chatID := extractChatIDFromSessionKey(task.ParentSessionKey)
			_ = e.channel.Send(ctx, domain.OutboundMessage{
				ChatID: chatID,
				Text: fmt.Sprintf("⏸️ **Sub-Agent @%s requires clarification:**\n📌 **Task:** %s (`%s`)\n\n❓ **Question:** %s\n\n_Use_ `/task reply %s <your response>` _to continue._",
					task.AgentName, task.Title, task.ID, task.PendingQuestion, task.ID),
				ParseMode: "Markdown",
			})
		}
	})

	e.eventBus.SubscribeAsync(domain.EventSubagentCompleted, func(ctx context.Context, evt domain.Event) {
		payload, ok := evt.Payload.(domain.SubagentEventPayload)
		if !ok {
			return
		}
		task := payload.Task

		if task.CallbackMode == domain.CallbackInvokeMain {
			syntheticMsg := domain.CanonicalMessage{
				ID:        fmt.Sprintf("sub-synth-%s", task.ID),
				Timestamp: time.Now(),
				Channel:   "telegram",
				Chat: domain.ChatContext{
					ID: extractChatIDFromSessionKey(task.ParentSessionKey),
				},
				Text: fmt.Sprintf("[SYSTEM NOTIFICATION: Subagent Task #%s (@%s) COMPLETED]\nTask Title: %s\nDuration: %.2fs | Total Tokens: %d\n\nResult Summary:\n%s\n\nPlease synthesize or report these findings to the user.",
					task.ID, task.AgentName, task.Title, task.DurationSeconds, task.Usage.TotalTokens, task.ResultSummary),
			}
			select {
			case e.inboundChan <- syntheticMsg:
			default:
				slog.Warn("inbound queue full for subagent completion message", "task_id", task.ID)
			}
		} else if task.CallbackMode == domain.CallbackNotifyUser && e.channel != nil {
			chatID := extractChatIDFromSessionKey(task.ParentSessionKey)
			_ = e.channel.Send(ctx, domain.OutboundMessage{
				ChatID: chatID,
				Text: fmt.Sprintf("✅ <b>Sub-Agent @%s completed!</b>\n📌 <b>Task:</b> %s (<code>%s</code>)\n⏱️ <b>Duration:</b> %.2fs | 🪙 <b>Tokens:</b> %d\n\n%s",
					task.AgentName, task.Title, task.ID, task.DurationSeconds, task.Usage.TotalTokens, task.ResultSummary),
				ParseMode: "HTML",
			})
		}
	})
}

func extractChatIDFromSessionKey(sessionKey string) string {
	parts := strings.Split(sessionKey, ":")
	if len(parts) >= 2 {
		return parts[1]
	}
	return sessionKey
}
