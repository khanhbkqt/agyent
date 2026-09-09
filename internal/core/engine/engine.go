package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"agyent/internal/config"
	"agyent/internal/core/auth"
	"agyent/internal/core/concurrency"
	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

// Engine is the central orchestrator connecting Storage, EventBus, Debouncer,
// Subprocess Runner, LockManager, ContextResolver, MCPRegistry, PluginManager, and Channel adapters.
type Engine struct {
	cfg                *config.Config
	storage            ports.StoragePort
	runner             ports.RunnerPort
	channel            ports.ChannelPort
	eventBus           ports.EventBusPort
	debouncer          ports.DebouncerPort
	lockManager        ports.LockManager
	contextResolver    ports.ContextResolverPort
	mcpRegistry        ports.MCPRegistryPort
	pluginManager      ports.PluginManagerPort
	temporal           ports.TemporalContextPort
	evolution          ports.EvolutionOrchestratorPort
	subagentDispatcher ports.SubagentDispatcherPort
	securityManager    ports.SecurityManagerPort
	workspaceManager   ports.WorkspacePort
	scheduler          ports.SchedulerPort
	attachmentFetcher  ports.AttachmentFetcherPort
	policyEngine       ports.PolicyEngine
	executionService   ports.ExecutionServicePort

	streamingEnabled atomic.Bool
	startTime        time.Time

	turnsMu     sync.Mutex
	activeTurns map[string]activeTurnEntry

	compactionMu            sync.RWMutex
	pendingCompactedDigests map[string]string

	inboundChan chan domain.CanonicalMessage
	ctx         context.Context
	cancel      context.CancelFunc
	running     atomic.Bool
	wg          sync.WaitGroup
}

type activeTurnEntry struct {
	turnID string
	cancel context.CancelFunc
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
		cfg:                     cfg,
		storage:                 storage,
		runner:                  runner,
		channel:                 channel,
		eventBus:                eventBus,
		debouncer:               debouncer,
		lockManager:             lockManager,
		contextResolver:         contextResolver,
		mcpRegistry:             mcpRegistry,
		pluginManager:           pluginManager,
		activeTurns:             make(map[string]activeTurnEntry),
		pendingCompactedDigests: make(map[string]string),
		inboundChan:             make(chan domain.CanonicalMessage, 200),
		ctx:                     ctx,
		cancel:                  cancel,
		startTime:               time.Now(),
	}
	// A command/turn engine without a policy evaluator must never silently
	// become permissive. The composition root may replace this instance with a
	// decorated policy engine, but every normally constructed Engine has the
	// default-deny policy available from its first request.
	if storage != nil && cfg != nil {
		e.policyEngine = auth.NewEngine(storage, cfg)
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

// SetSecurityManager injects the universal security gateway manager.
func (e *Engine) SetSecurityManager(sec ports.SecurityManagerPort) {
	e.securityManager = sec
}

// GetSecurityManager returns the active security manager instance.
func (e *Engine) GetSecurityManager() ports.SecurityManagerPort {
	return e.securityManager
}

// SetPolicyEngine injects the centralized authorization policy engine.
func (e *Engine) SetPolicyEngine(p ports.PolicyEngine) {
	e.policyEngine = p
}

// SetExecutionService injects the execution service chokepoint.
func (e *Engine) SetExecutionService(s ports.ExecutionServicePort) {
	e.executionService = s
}

// SetWorkspaceManager injects the workspace manager for handling directory trees and inbound attachments.
func (e *Engine) SetWorkspaceManager(w ports.WorkspacePort) {
	e.workspaceManager = w
}

// GetWorkspaceManager returns the active workspace manager instance.
func (e *Engine) GetWorkspaceManager() ports.WorkspacePort {
	return e.workspaceManager
}

// SetScheduler injects the background task and cron scheduler.
func (e *Engine) SetScheduler(s ports.SchedulerPort) {
	e.scheduler = s
}

// GetScheduler returns the active scheduler instance.
func (e *Engine) GetScheduler() ports.SchedulerPort {
	return e.scheduler
}

// SetAttachmentFetcher injects the lazy inbound attachment fetcher.
func (e *Engine) SetAttachmentFetcher(fetcher ports.AttachmentFetcherPort) {
	e.attachmentFetcher = fetcher
}

// GetAttachmentFetcher returns the active attachment fetcher instance.
func (e *Engine) GetAttachmentFetcher() ports.AttachmentFetcherPort {
	return e.attachmentFetcher
}

// SetPendingCompactionDigest records a continuity digest for a session to be injected on the next turn.
func (e *Engine) SetPendingCompactionDigest(sessionKey, digest string) {
	e.compactionMu.Lock()
	defer e.compactionMu.Unlock()
	if e.pendingCompactedDigests == nil {
		e.pendingCompactedDigests = make(map[string]string)
	}
	e.pendingCompactedDigests[sessionKey] = digest
}

// GetAndClearPendingCompactionDigest retrieves and clears any staged continuity digest for a session.
func (e *Engine) GetAndClearPendingCompactionDigest(sessionKey string) string {
	e.compactionMu.Lock()
	defer e.compactionMu.Unlock()
	if e.pendingCompactedDigests == nil {
		return ""
	}
	digest := e.pendingCompactedDigests[sessionKey]
	delete(e.pendingCompactedDigests, sessionKey)
	return digest
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

	// 2.1. Start scheduler daemon if present
	if e.scheduler != nil {
		if err := e.scheduler.Start(e.ctx); err != nil {
			slog.Warn("failed to start scheduler daemon", "error", err)
		}
	}
	e.subscribeSchedulerEvents()

	// 2.2. Start background Turn Auto-Recovery Worker
	e.startRecoveryWorker(e.ctx)

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

	// 4. Start background Autonomous Janitor Worker (runs every 30 mins)
	e.StartBackgroundJanitor(e.ctx, 30*time.Minute)

	// 4. Start background Evolution Orchestrator (if configured)
	if e.evolution != nil {
		_ = e.evolution.Start(e.ctx)
	}

	// 5. Discover available models from AGY CLI in background
	if e.runner != nil {
		concurrency.SafeGo(func() {
			discoverCtx, cancel := context.WithTimeout(e.ctx, 10*time.Second)
			defer cancel()
			_, _ = e.runner.ListAvailableModels(discoverCtx)
		})
	}

	// 5. Subscribe to Force Kill & Cancellation events from EventBus
	if e.eventBus != nil {
		e.eventBus.SubscribeSync(domain.EventForceKillRequested, func(ctx context.Context, evt domain.Event) error {
			if payload, ok := evt.Payload.(domain.ForceKillPayload); ok && payload.SessionKey != "" {
				slog.WarnContext(ctx, "Force kill requested via EventBus",
					slog.String("session_key", payload.SessionKey),
					slog.String("reason", payload.Reason),
				)
				e.ForceUnlockSession(payload.SessionKey)
			}
			return nil
		})
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
		cmdLower := strings.ToLower(cmd)

		if cmdLower == "/ask" {
			if len(args) == 0 {
				if e.channel != nil {
					_ = e.channel.Send(ctx, domain.OutboundMessage{
						Channel:          msg.Channel,
						BotID:            msg.BotID,
						ChatID:           msg.Chat.ID,
						ThreadID:         msg.Chat.ThreadID,
						Text:             "⚠️ **Usage:** `/ask <prompt>`\n*Example:* `/ask Write regex to validate IPv6 in Go`\n\n_Questions are answered independently without polluting the main conversation history._",
						ParseMode:        "Markdown",
						ReplyToMessageID: msg.ID,
					})
				}
				return nil
			}
			msg.Text = strings.Join(args, " ")
			return e.executeTurn(ctx, msg, true)
		}

		if cmdLower == "/new" {
			return e.handleNewSessionTurn(ctx, msg, args)
		}

		if (cmdLower == "/c" || cmdLower == "/conversations") && len(args) > 0 && strings.ToLower(args[0]) == "new" {
			return e.handleNewSessionTurn(ctx, msg, args[1:])
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

	sessionKey := msg.SessionKey()
	isAppendMode := false
	if e.cfg != nil && strings.ToLower(e.cfg.AGY.QueueMode) == "append" {
		isAppendMode = true
	}

	if isAppendMode && e.HasActiveTurn(sessionKey) {
		slog.InfoContext(ctx, "Active turn detected in append mode, signaling soft interrupt",
			slog.String("session_key", sessionKey),
			slog.String("sender", msg.Sender.Username),
		)
		if e.executionService != nil {
			_ = e.executionService.InterruptTurn(ctx, sessionKey)
		} else if e.runner != nil {
			_ = e.runner.InterruptStream(ctx, sessionKey)
		}
		// Wait briefly for previous turn to release lock, enabling clean turn handover
		waitDeadline := time.Now().Add(500 * time.Millisecond)
		for e.HasActiveTurn(sessionKey) && time.Now().Before(waitDeadline) {
			time.Sleep(20 * time.Millisecond)
		}
	}

	return e.executeTurn(ctx, msg, false)
}

func (e *Engine) handleNewSessionTurn(ctx context.Context, msg domain.CanonicalMessage, args []string) error {
	sessionKey := msg.SessionKey()

	// 1. Reset active conversation in session
	defaultAgent := "agyent"
	if msg.BindAgent != "" {
		defaultAgent = msg.BindAgent
	}
	session, err := e.storage.GetOrCreateSession(ctx, sessionKey, defaultAgent)
	if err != nil {
		return fmt.Errorf("load session for new conversation: %w", err)
	}
	authSession := *session
	if msg.BindAgent != "" {
		authSession.ActiveAgent = msg.BindAgent
	}
	if err := e.authorizeAgentAction(ctx, msg.Sender, domain.ActionConvoSwitch, domain.ResourceKindConversation, "", &authSession); err != nil {
		if e.channel != nil {
			_ = e.channel.Send(ctx, *e.commandDeniedMessage(msg))
		}
		return nil
	}

	// 2. Proactively cancel and unlock any running turn for this session (authorized caller only)
	if e.HasActiveTurn(sessionKey) {
		slog.InfoContext(ctx, "Active turn detected during authorized /new, forcefully releasing session mutex and terminating running turn",
			slog.String("session_key", sessionKey),
			slog.String("sender", msg.Sender.Username),
		)
		e.ForceUnlockSession(sessionKey)
	}

	if msg.BindAgent != "" {
		session.ActiveAgent = msg.BindAgent
		session.ActiveProject = ""
	}
	oldConvID := session.GetActiveConversationID()
	if oldConvID != "" && e.evolution != nil {
		bgCtx := context.WithoutCancel(ctx)
		concurrency.SafeGo(func() {
			_ = e.evolution.TriggerConversationEvolution(bgCtx, oldConvID, domain.TriggerExplicitSwitch)
		})
	}
	session.ResetActiveConversationID()
	if e.securityManager != nil {
		e.securityManager.ClearSessionGrants(sessionKey)
	}
	if err := e.storage.SaveSession(ctx, session); err != nil {
		return fmt.Errorf("reset active conversation: %w", err)
	}

	// 3. Prepare proactive greeting prompt with optional topic
	topic := strings.TrimSpace(strings.Join(args, " "))
	msg.Text = BuildNewSessionGreetingPrompt(topic)

	// 4. Execute Turn to proactively greet user and establish the new conversation
	return e.executeTurn(ctx, msg, false)
}

// IsSuperAdmin checks if a user ID is listed in the administrator whitelist.
func (e *Engine) IsSuperAdmin(senderID string) bool {
	return e.IsSuperAdminForProvider(senderID, "telegram")
}

// IsSuperAdminForProvider resolves a superadmin without allowing provider-local
// administrator identifiers to cross channel boundaries.
func (e *Engine) IsSuperAdminForProvider(senderID, provider string) bool {
	if e.cfg == nil {
		return false
	}
	return e.cfg.IsAdminForProvider(senderID, provider)
}

func (e *Engine) isSenderSuperAdmin(sender domain.SenderUser) bool {
	provider := sender.Provider
	if provider == "" {
		provider = "telegram"
	}
	return e.IsSuperAdminForProvider(sender.ID, provider)
}

// CheckAccess evaluates whether a sender has access to interact with an agent profile.
func (e *Engine) CheckAccess(ctx context.Context, agent *domain.Agent, senderID string) (bool, string, error) {
	return e.CheckAccessForProvider(ctx, agent, senderID, "telegram")
}

// CheckAccessForProvider keeps provider-local administrator identities scoped
// to the channel where they were authenticated.
func (e *Engine) CheckAccessForProvider(ctx context.Context, agent *domain.Agent, senderID, provider string) (bool, string, error) {
	if agent == nil {
		return false, "", nil
	}

	// 1. Check if public agent
	if agent.IsPublic {
		return true, "public", nil
	}

	// 2. Check if verified creator/owner
	if agent.OwnerID != "" && agent.OwnerID == senderID {
		return true, "owner", nil
	}

	// 3. Check if SuperAdmin
	if e.IsSuperAdminForProvider(senderID, provider) {
		return true, "admin", nil
	}

	// 4. Check collaborator permissions table in storage
	return e.storage.CheckAgentAccess(ctx, agent.Name, senderID)
}

// resolveDefaultAgent returns bindAgent if provided. If not provided and exactly one custom agent is configured in config.yaml, it returns that agent. Otherwise, returns "agyent".
func (e *Engine) resolveDefaultAgent(bindAgent string) string {
	if bindAgent != "" {
		return bindAgent
	}
	if e.cfg != nil && len(e.cfg.Agents) > 0 {
		var customAgents []string
		for name := range e.cfg.Agents {
			if name != "agyent" && name != "default" {
				customAgents = append(customAgents, name)
			}
		}
		if len(customAgents) == 1 {
			return customAgents[0]
		}
	}
	return "agyent"
}

// IsGroupAllowed checks if a group ID is allowed either via config.yaml or SQLite database.
func (e *Engine) IsGroupAllowed(ctx context.Context, groupID string, provider string) bool {
	if e.cfg != nil && e.cfg.IsGroupAllowed(groupID, provider) {
		return true
	}
	if e.storage != nil {
		if allowed, err := e.storage.IsGroupAllowed(ctx, groupID); err == nil && allowed {
			return true
		}
	}
	return false
}

// CheckAccessForChat evaluates access taking into account whether the message is in a whitelisted group chat or direct message (1-1).
// In a whitelisted group chat: anyone in the group has "member" access (allowed to chat).
// In direct message (1-1): if agent is private (is_public: false), only admin/owner has access; strangers are dropped/denied.
func (e *Engine) CheckAccessForChat(ctx context.Context, agent *domain.Agent, senderID, provider, chatID, chatType string) (bool, string, error) {
	if agent == nil {
		return false, "", nil
	}

	// 1. Group Chat Whitelist Check
	isGroup := chatType == "group" || chatType == "supergroup" || chatType == "channel" || strings.HasPrefix(chatID, "-")
	if isGroup && chatID != "" && e.IsGroupAllowed(ctx, chatID, provider) {
		return true, "member", nil
	}

	// 2. Direct Message (or non-group): fallback to standard provider check (admin, owner, collaborator, public)
	return e.CheckAccessForProvider(ctx, agent, senderID, provider)
}

// AuthorizeInbound evaluates whether an inbound message sender has permission to interact
// with an agent persona before message processing or side effects occur.
func (e *Engine) AuthorizeInbound(ctx context.Context, senderID string, bindAgent string, chatType string) (bool, error) {
	return e.AuthorizeInboundSession(ctx, senderID, bindAgent, chatType, "")
}

// AuthorizeInboundSession resolves an existing session's active agent before
// ingress. This keeps a user who has selected a shared/dedicated agent from
// being incorrectly evaluated against the unrelated default persona, while a
// dedicated bot binding remains authoritative.
func (e *Engine) AuthorizeInboundSession(ctx context.Context, senderID string, bindAgent string, chatType string, sessionKey string) (bool, error) {
	provider := "telegram"
	chatID := ""
	if parsed, err := domain.ParseSessionKey(sessionKey); err == nil && parsed.Channel != "" {
		provider = parsed.Channel
		chatID = parsed.ChatID
	}
	if e.IsSuperAdminForProvider(senderID, provider) {
		return true, nil
	}

	// Group check: If it's a whitelisted group, allow any sender to interact
	isGroup := chatType == "group" || chatType == "supergroup" || chatType == "channel" || strings.HasPrefix(chatID, "-")
	if isGroup && chatID != "" && e.IsGroupAllowed(ctx, chatID, provider) {
		return true, nil
	}

	if e.storage == nil {
		return false, fmt.Errorf("inbound authorization unavailable: storage is not initialized")
	}
	targetAgent := e.resolveDefaultAgent(bindAgent)
	if bindAgent != "" {
		targetAgent = bindAgent
	} else if sessionKey != "" {
		if session, err := e.storage.GetSession(ctx, sessionKey); err == nil && session != nil && session.ActiveAgent != "" {
			targetAgent = session.ActiveAgent
		}
	}
	agent, err := e.storage.GetAgent(ctx, targetAgent)
	if err != nil || agent == nil {
		return false, nil
	}
	allowed, _, err := e.CheckAccessForChat(ctx, agent, senderID, provider, chatID, chatType)
	return allowed, err
}

func (e *Engine) executeTurn(ctx context.Context, msg domain.CanonicalMessage, isEphemeralOpt ...bool) error {
	isEphemeral := len(isEphemeralOpt) > 0 && isEphemeralOpt[0]
	sessionKey := msg.SessionKey()
	timeout := 1800 * time.Second
	if e.cfg != nil && e.cfg.AGY.DefaultTimeoutSeconds > 0 {
		timeout = time.Duration(e.cfg.AGY.DefaultTimeoutSeconds) * time.Second
	}

	slog.DebugContext(ctx, "Starting execution turn",
		slog.String("session_key", sessionKey),
		slog.String("sender", msg.Sender.Username),
		slog.String("chat_id", msg.Chat.ID),
		slog.Bool("is_ephemeral", isEphemeral),
	)

	// 1. Acquire Per-Session FIFO Lock (maintain typing while queued)
	queueTypingStop := make(chan struct{})
	var closeOnce sync.Once
	stopQueueTyping := func() {
		closeOnce.Do(func() {
			close(queueTypingStop)
		})
	}
	defer stopQueueTyping()

	concurrency.SafeGo(func() {
		if e.channel != nil {
			_ = e.channel.SendTyping(ctx, msg.TargetContext())
		}
		ticker := time.NewTicker(4 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-queueTypingStop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if e.channel != nil {
					_ = e.channel.SendTyping(ctx, msg.TargetContext())
				}
			}
		}
	})
	unlock, err := e.lockManager.Acquire(ctx, sessionKey, timeout)
	stopQueueTyping()
	if err != nil {
		slog.ErrorContext(ctx, "Failed to acquire session lock",
			slog.String("session_key", sessionKey),
			slog.String("error", err.Error()),
		)
		if e.channel != nil {
			_ = e.channel.Send(ctx, domain.OutboundMessage{
				Channel:          msg.Channel,
				BotID:            msg.BotID,
				ChatID:           msg.Chat.ID,
				ThreadID:         msg.Chat.ThreadID,
				Text:             fmt.Sprintf("⚠️ Could not acquire session lock: %v. Please try again or use `/force_unlock`.", err),
				ReplyToMessageID: msg.ID,
			})
		}
		return fmt.Errorf("failed to acquire session lock: %w", err)
	}
	defer unlock()

	// 2. Setup Turn Context with Cancellation Map for /force_unlock
	turnID := fmt.Sprintf("turn-%d", time.Now().UnixNano())
	turnCtx, turnCancel := context.WithCancel(ctx)
	defer turnCancel()

	e.registerActiveTurn(sessionKey, turnID, turnCancel)
	defer e.unregisterActiveTurn(sessionKey, turnID)

	// Refresh typing indicator while resolving session and context
	_ = e.channel.SendTyping(turnCtx, msg.TargetContext())

	// 3. Inbound RBAC Checkpoint: Evaluate access BEFORE mutating session or agent state
	defaultAgent := e.resolveDefaultAgent(msg.BindAgent)
	if msg.BindAgent != "" {
		defaultAgent = msg.BindAgent
	}
	targetAgent := defaultAgent
	existingSession, _ := e.storage.GetSession(turnCtx, sessionKey)
	if existingSession != nil && existingSession.ActiveAgent != "" {
		targetAgent = existingSession.ActiveAgent
	}
	if msg.BindAgent != "" {
		targetAgent = msg.BindAgent
	}

	agent, getErr := e.storage.GetAgent(turnCtx, targetAgent)
	if getErr == nil && agent != nil {
		allowed, _, checkErr := e.CheckAccessForChat(turnCtx, agent, msg.Sender.ID, msg.Channel, msg.Chat.ID, msg.Chat.Type)
		if checkErr != nil || !allowed {
			slog.WarnContext(turnCtx, "RBAC Access Denied to agent",
				slog.String("agent", agent.Name),
				slog.String("sender_id", msg.Sender.ID),
				slog.String("owner_id", agent.OwnerID),
			)
			ownerDesc := agent.OwnerID
			if ownerDesc == "" {
				ownerDesc = "admin"
			}
			_ = e.channel.Send(turnCtx, domain.OutboundMessage{
				Channel:          msg.Channel,
				BotID:            msg.BotID,
				ChatID:           msg.Chat.ID,
				ThreadID:         msg.Chat.ThreadID,
				Text:             fmt.Sprintf("⛔ **Access Denied (403):** You do not have permission to access agent `@%s`.\nAsk the agent owner (User ID: `%s`) to grant you access via:\n`/a share %s %s [role]`", agent.Name, ownerDesc, agent.Name, msg.Sender.ID),
				ParseMode:        "Markdown",
				ReplyToMessageID: msg.ID,
			})
			return nil
		}
	} else if !e.IsSuperAdminForProvider(msg.Sender.ID, msg.Channel) {
		_ = e.channel.Send(turnCtx, domain.OutboundMessage{
			Channel:          msg.Channel,
			BotID:            msg.BotID,
			ChatID:           msg.Chat.ID,
			ThreadID:         msg.Chat.ThreadID,
			Text:             fmt.Sprintf("⛔ **Access Denied (403):** Agent `@%s` does not exist. Only an administrator can bootstrap new agents.", targetAgent),
			ParseMode:        "Markdown",
			ReplyToMessageID: msg.ID,
		})
		return nil
	}

	// 4. Load Session from Storage (only after authorized)
	session, err := e.storage.GetOrCreateSession(turnCtx, sessionKey, defaultAgent)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to load session state",
			slog.String("session_key", sessionKey),
			slog.String("error", err.Error()),
		)
		_ = e.channel.Send(ctx, domain.OutboundMessage{
			Channel:          msg.Channel,
			BotID:            msg.BotID,
			ChatID:           msg.Chat.ID,
			ThreadID:         msg.Chat.ThreadID,
			Text:             fmt.Sprintf("⚠️ Failed to load session state: %v", err),
			ReplyToMessageID: msg.ID,
		})
		return fmt.Errorf("failed to load session: %w", err)
	}

	// If dedicated bot is bound to a specific agent, ensure session uses that agent
	if msg.BindAgent != "" && session.ActiveAgent != msg.BindAgent {
		session.ActiveAgent = msg.BindAgent
		session.ActiveProject = ""
	}

	session.UpdatedAt = time.Now()
	_ = e.storage.SaveSession(turnCtx, session)

	// 5. Resolve Active Agent Record
	if agent == nil {
		agentPath := config.ResolveAgentWorkspace(e.cfg.Storage.AgentsDir, session.ActiveAgent)
		_ = os.MkdirAll(agentPath, 0755)
		isPublic := false
		ownerID := ""
		if session.ActiveAgent != "agyent" && e.IsSuperAdminForProvider(msg.Sender.ID, msg.Channel) {
			ownerID = msg.Sender.ID
		}
		defaultPreset := domain.SecurityPreset(e.cfg.ResolveAgentPreset(session.ActiveAgent))
		if defaultPreset == "" {
			defaultPreset = domain.PresetBalanced
		}
		agent = &domain.Agent{
			Name:           session.ActiveAgent,
			Description:    "Agyent - Trợ lý AI cá nhân đa năng",
			Status:         domain.StatusUninitialized,
			WorkspacePath:  agentPath,
			SecurityPreset: defaultPreset,
			OwnerID:        ownerID,
			IsPublic:       isPublic,
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		}
		_ = e.storage.SaveAgent(turnCtx, agent)
	}

	if agent.WorkspacePath == "" || agent.SecurityPreset == "" || (agent.Status == domain.StatusUninitialized && agent.OwnerID == "" && agent.Name != "agyent") {
		if e.cfg != nil {
			if agent.WorkspacePath == "" {
				agent.WorkspacePath = config.ResolveAgentWorkspace(e.cfg.Storage.AgentsDir, agent.Name)
			}
			if agent.SecurityPreset == "" || agent.Status == domain.StatusUninitialized {
				agent.SecurityPreset = domain.SecurityPreset(e.cfg.ResolveAgentPreset(agent.Name))
			}
			if agent.OwnerID == "" && agent.Name != "agyent" && e.IsSuperAdminForProvider(msg.Sender.ID, msg.Channel) {
				agent.OwnerID = msg.Sender.ID
			}
		}
	}

	isBootstrap := !agent.IsInitialized()
	if isBootstrap && e.workspaceManager != nil && e.workspaceManager.HasDirectives(agent.WorkspacePath) {
		isBootstrap = false
		agent.Status = domain.StatusInitialized
		_ = e.storage.SaveAgent(turnCtx, agent)
	}

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
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		slog.ErrorContext(turnCtx, "Failed to prepare turn workspace", "workspace", workspaceDir, "error", err)
		if e.channel != nil {
			_ = e.channel.Send(turnCtx, domain.OutboundMessage{
				Channel: msg.Channel, BotID: msg.BotID, ChatID: msg.Chat.ID, ThreadID: msg.Chat.ThreadID,
				Text:             fmt.Sprintf("⛔ Security setup failed: unable to prepare the isolated workspace (%v). The turn was not executed.", err),
				ReplyToMessageID: msg.ID,
			})
		}
		return fmt.Errorf("prepare workspace: %w", err)
	}
	if e.securityManager != nil {
		if err := e.securityManager.EnsureWorkspaceHooks(workspaceDir); err != nil {
			slog.ErrorContext(turnCtx, "Security hook provisioning failed; refusing execution", "workspace", workspaceDir, "error", err)
			if e.channel != nil {
				_ = e.channel.Send(turnCtx, domain.OutboundMessage{
					Channel: msg.Channel, BotID: msg.BotID, ChatID: msg.Chat.ID, ThreadID: msg.Chat.ThreadID,
					Text:             "⛔ Security hooks could not be provisioned. This turn was blocked to avoid unguarded tool execution.",
					ReplyToMessageID: msg.ID,
				})
			}
			return fmt.Errorf("provision security hooks: %w", err)
		}
	}

	// 5.1. Lazily Materialize Inbound AttachmentRefs to Active Workspace uploads/
	if len(msg.AttachmentRefs) > 0 && e.attachmentFetcher != nil {
		uploadsDir := filepath.Join(workspaceDir, "uploads")
		for _, ref := range msg.AttachmentRefs {
			att, fetchErr := e.attachmentFetcher.FetchAttachment(turnCtx, ref, uploadsDir)
			if fetchErr != nil {
				slog.WarnContext(turnCtx, "Failed to lazily materialize attachment",
					slog.String("file_name", ref.FileName),
					slog.String("error", fetchErr.Error()),
				)
				continue
			}
			msg.Attachments = append(msg.Attachments, att)
		}
	} else if len(msg.Attachments) > 0 && e.workspaceManager != nil {
		preparedAtts, prepErr := e.workspaceManager.PrepareInboundAttachments(turnCtx, workspaceDir, msg.Attachments)
		if prepErr == nil && len(preparedAtts) > 0 {
			msg.Attachments = preparedAtts
		} else if prepErr != nil {
			slog.WarnContext(turnCtx, "Failed to relocate inbound attachments to workspace uploads",
				slog.String("workspace", workspaceDir),
				slog.String("error", prepErr.Error()),
			)
		}
	}

	// 5.2. Persist in-flight turn for crash resilience and auto-recovery
	if e.storage != nil {
		inboundMsgID, _ := strconv.ParseInt(msg.ID, 10, 64)
		_ = e.storage.SaveInFlightTurn(turnCtx, &domain.InFlightTurn{
			TurnID:           turnID,
			SessionKey:       sessionKey,
			ConversationID:   session.GetActiveConversationID(),
			AgentName:        agent.Name,
			ProjectName:      session.ActiveProject,
			Channel:          msg.Channel,
			ChatID:           msg.Chat.ID,
			ThreadID:         strconv.FormatInt(msg.Chat.ThreadID, 10),
			InboundMessageID: inboundMsgID,
			BotID:            msg.BotID,
			UserID:           msg.Sender.ID,
			UserName:         msg.Sender.Username,
			Prompt:           msg.Text,
			IsEphemeral:      isEphemeral,
			Status:           domain.TurnStatusExecuting,
			RetryCount:       0,
			MaxRetries:       1,
			RecoveryMode:     "auto",
			CreatedAt:        time.Now(),
			UpdatedAt:        time.Now(),
		})
	}

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

		// Ensure deterministic KV-cache prefix ordering for skills and MCP servers
		if resolved != nil {
			if len(resolved.SkillHeaders) > 1 {
				sort.Slice(resolved.SkillHeaders, func(i, j int) bool {
					if resolved.SkillHeaders[i].Scope != resolved.SkillHeaders[j].Scope {
						return resolved.SkillHeaders[i].Scope < resolved.SkillHeaders[j].Scope
					}
					if resolved.SkillHeaders[i].Name != resolved.SkillHeaders[j].Name {
						return resolved.SkillHeaders[i].Name < resolved.SkillHeaders[j].Name
					}
					return resolved.SkillHeaders[i].FilePath < resolved.SkillHeaders[j].FilePath
				})
			}
			if len(resolved.ActiveMCPServers) > 1 {
				sort.Slice(resolved.ActiveMCPServers, func(i, j int) bool {
					return resolved.ActiveMCPServers[i].ServerName < resolved.ActiveMCPServers[j].ServerName
				})
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
			pendingDigest := e.GetAndClearPendingCompactionDigest(sessionKey)
			if resolved != nil {
				promptText = ComposeResolvedTurnPrompt(resolved, msg, temporalTag, pendingDigest)
			} else {
				knowledgeDirectives := LoadAgentKnowledgeDirectives(agent.WorkspacePath)
				promptText = ComposeTurnPrompt(knowledgeDirectives, msg)
				if pendingDigest != "" {
					promptText = pendingDigest + "\n\n" + promptText
				}
			}
		}
	}

	if strings.TrimSpace(promptText) == "" {
		if len(msg.Attachments) > 0 {
			promptText = "[Người dùng gửi ảnh/tệp đính kèm. Em hãy kiểm tra và phân tích tệp này.]"
		} else {
			promptText = "Xin chào!"
		}
	}

	// 7. Dynamic MCP Mounting (with deferred unmount for Zero Context Leakage)
	if len(activeMCPServers) > 0 && e.mcpRegistry != nil {
		releaseMCPLease, err := e.mcpRegistry.AcquireExclusiveTurn(turnCtx, sessionKey)
		if err != nil {
			slog.ErrorContext(turnCtx, "MCP turn lease failed; refusing execution", "session_key", sessionKey, "error", err)
			if e.channel != nil {
				_ = e.channel.Send(turnCtx, domain.OutboundMessage{
					Channel:          msg.Channel,
					BotID:            msg.BotID,
					ChatID:           msg.Chat.ID,
					ThreadID:         msg.Chat.ThreadID,
					Text:             fmt.Sprintf("⚠️ Resource busy: %v. Please try again in a moment.", err),
					ReplyToMessageID: msg.ID,
				})
			}
			return fmt.Errorf("acquire MCP turn lease: %w", err)
		}
		defer releaseMCPLease()

		for i := range activeMCPServers {
			envCopy := make(map[string]string, len(activeMCPServers[i].Env)+5)
			for k, v := range activeMCPServers[i].Env {
				envCopy[k] = v
			}
			if _, exists := envCopy["AGYENT_AGENT_NAME"]; !exists && agent != nil {
				envCopy["AGYENT_AGENT_NAME"] = agent.Name
			}
			if _, exists := envCopy["AGYENT_AGENT_WORKSPACE"]; !exists {
				envCopy["AGYENT_AGENT_WORKSPACE"] = workspaceDir
			}
			if _, exists := envCopy["AGYENT_SESSION_KEY"]; !exists && sessionKey != "" {
				envCopy["AGYENT_SESSION_KEY"] = sessionKey
			}
			if _, exists := envCopy["AGYENT_USER_ID"]; !exists && msg.Sender.ID != "" {
				envCopy["AGYENT_USER_ID"] = msg.Sender.ID
			}
			if _, exists := envCopy["AGYENT_TURN_ID"]; !exists && turnID != "" {
				envCopy["AGYENT_TURN_ID"] = turnID
			}
			activeMCPServers[i].Env = envCopy
		}
		if err := e.mcpRegistry.MountServers(turnCtx, sessionKey, activeMCPServers); err != nil {
			slog.ErrorContext(turnCtx, "MCP mount failed; refusing execution", "session_key", sessionKey, "error", err)
			if e.channel != nil {
				_ = e.channel.Send(turnCtx, domain.OutboundMessage{
					Channel:          msg.Channel,
					BotID:            msg.BotID,
					ChatID:           msg.Chat.ID,
					ThreadID:         msg.Chat.ThreadID,
					Text:             fmt.Sprintf("⚠️ Plugin setup error: %v. The turn was not executed.", err),
					ReplyToMessageID: msg.ID,
				})
			}
			return fmt.Errorf("mount MCP servers: %w", err)
		}
		defer func() {
			if err := e.mcpRegistry.UnmountServers(context.Background(), sessionKey, activeMCPServers); err != nil {
				slog.Error("MCP unmount failed", "session_key", sessionKey, "error", err)
			}
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
		TurnID:                     turnID,
		WorkspaceDir:               workspaceDir,
		Timeout:                    timeout,
		Model:                      resolvedModel,
		Effort:                     resolvedEffort,
		Mode:                       e.cfg.AGY.DefaultMode,
		DangerouslySkipPermissions: e.cfg.AGY.DangerouslySkipPermissions,
		AgentName:                  agent.Name,
		ProjectName:                session.ActiveProject,
		SessionKey:                 sessionKey,
		UserID:                     msg.Sender.ID,
	}

	isStream := e.IsStreamingEnabled()

	if e.eventBus != nil {
		_ = e.eventBus.SyncEmit(turnCtx, domain.NewEvent(domain.EventPreExecution, req))
	}

	var execResult *domain.ExecutionResult
	var execErr error

	principal := domain.Principal{
		Kind:      domain.PrincipalUser,
		Provider:  msg.Channel,
		SubjectID: msg.Sender.ID,
	}

	if e.executionService == nil && e.securityManager != nil {
		e.securityManager.RegisterActiveTurn(domain.TurnSecurityContext{
			TurnID:         turnID,
			ConversationID: activeConvID,
			SessionKey:     sessionKey,
			WorkspaceDir:   workspaceDir,
			Preset:         agent.SecurityPreset,
			AgentName:      agent.Name,
			CreatedAt:      time.Now(),
		})
		defer e.securityManager.UnregisterTurnByID(turnID)
	}

	if isStream {
		if e.executionService != nil {
			execResult, execErr = e.executionService.ExecuteTurn(turnCtx, principal, req, sessionKey, true)
		} else {
			execResult, execErr = e.runner.ExecuteStream(turnCtx, req, sessionKey)

			// Edge Case: If agy fails due to unsupported effort flag, retry once with effort stripped
			if isEffortError(execErr, execResult) && req.Effort != "" {
				slog.WarnContext(turnCtx, "Effort flag rejected by model/CLI, retrying without --effort",
					slog.String("model", req.Model),
					slog.String("effort", req.Effort),
				)
				req.Effort = domain.EffortNone
				req.DisableEffort = true
				resolvedEffort = ""
				execResult, execErr = e.runner.ExecuteStream(turnCtx, req, sessionKey)
			}
		}

		if errors.Is(execErr, ports.ErrConversationNotFound) {
			if !isEphemeral && session.GetActiveConversationID() != "" {
				scope := domain.ConversationScope{
					SessionKey:  session.SessionKey,
					AgentName:   session.ActiveAgent,
					ProjectName: session.ActiveProject,
				}
				_ = e.storage.SetConversationArchivedScoped(turnCtx, scope, session.GetActiveConversationID(), true)
			}
			session.ResetActiveConversationID()
			_ = e.storage.SaveSession(turnCtx, session)
			req.ConversationID = ""
			if e.executionService != nil {
				execResult, execErr = e.executionService.ExecuteTurn(turnCtx, principal, req, sessionKey, true)
			} else {
				execResult, execErr = e.runner.ExecuteStream(turnCtx, req, sessionKey)
			}
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
			_ = e.channel.SendTyping(turnCtx, msg.TargetContext())
			ticker := time.NewTicker(4 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-heartbeatStop:
					return
				case <-turnCtx.Done():
					return
				case <-ticker.C:
					_ = e.channel.SendTyping(turnCtx, msg.TargetContext())
				}
			}
		})

		// Execute Runner with Transient Error Retry & Exponential Backoff (1s, 2s, 4s)
		for attempt := 0; attempt < 3; attempt++ {
			if e.executionService != nil {
				execResult, execErr = e.executionService.ExecuteTurn(turnCtx, principal, req, sessionKey, false)
			} else {
				execResult, execErr = e.runner.Execute(turnCtx, req)
			}
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
				req.Effort = domain.EffortNone
				req.DisableEffort = true
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
				scope := domain.ConversationScope{
					SessionKey:  session.SessionKey,
					AgentName:   session.ActiveAgent,
					ProjectName: session.ActiveProject,
				}
				_ = e.storage.SetConversationArchivedScoped(turnCtx, scope, session.GetActiveConversationID(), true)
			}
			session.ResetActiveConversationID()
			_ = e.storage.SaveSession(turnCtx, session)
			req.ConversationID = ""
			if e.executionService != nil {
				execResult, execErr = e.executionService.ExecuteTurn(turnCtx, principal, req, sessionKey, false)
			} else {
				execResult, execErr = e.runner.Execute(turnCtx, req)
			}
		}

		if execErr == nil && execResult != nil && execResult.Success {
			responseText := execResult.ResponseText
			if (msg.Chat.Type != "private" || isEphemeral) && msg.Channel != "zalo" {
				responseText = fmt.Sprintf("%s\n\n%s", contextTag, responseText)
			}

			// Outbound media and artifacts are extracted by the channel adapter
			// strictly from what the agent explicitly reports in its response text.
			_ = e.channel.Send(turnCtx, domain.OutboundMessage{
				Channel:          msg.Channel,
				BotID:            msg.BotID,
				ChatID:           msg.Chat.ID,
				ThreadID:         msg.Chat.ThreadID,
				Text:             responseText,
				ParseMode:        "Markdown",
				ReplyToMessageID: msg.ID,
				WorkspaceDir:     req.WorkspaceDir,
				ConversationID:   req.ConversationID,
			})
		}
	}

	// 9. Record Audit Log & Update State
	auditStatus := domain.StatusSuccess
	var errMsg string
	isInterrupted := false
	if errors.Is(turnCtx.Err(), context.Canceled) || (execErr != nil && strings.Contains(strings.ToLower(execErr.Error()), "cancelled")) {
		isInterrupted = true
	}

	if execErr != nil {
		if isInterrupted {
			auditStatus = domain.StatusInterrupted
			errMsg = "Turn execution gracefully interrupted by incoming user message"
		} else {
			auditStatus = domain.StatusError
			errMsg = execErr.Error()
		}
	} else if execResult != nil && !execResult.Success {
		if execResult.Error == "INTERRUPTED" {
			auditStatus = domain.StatusInterrupted
			errMsg = "Turn execution interrupted by user"
			isInterrupted = true
		} else {
			auditStatus = domain.StatusError
			errMsg = execResult.Error
		}
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

	// ExecutionService is the authoritative audit producer for protected turns.
	// Retain this fallback only for lightweight unit/embedded callers that have
	// not been wired through the production execution chokepoint.
	if e.executionService == nil {
		_ = e.storage.LogAudit(turnCtx, audit)
	}

	if auditStatus == domain.StatusError {
		slog.ErrorContext(ctx, "Turn execution failed",
			slog.String("session_key", sessionKey),
			slog.String("agent", agent.Name),
			slog.String("project", session.ActiveProject),
			slog.String("error", errMsg),
			slog.Float64("duration_sec", audit.DurationSeconds),
		)
	} else if auditStatus == domain.StatusInterrupted {
		slog.InfoContext(ctx, "Turn execution interrupted",
			slog.String("session_key", sessionKey),
			slog.String("agent", agent.Name),
			slog.String("project", session.ActiveProject),
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

	if isBootstrap && auditStatus == domain.StatusSuccess {
		agent.Status = domain.StatusInitialized
		agent.UpdatedAt = time.Now()
		_ = e.storage.SaveAgent(turnCtx, agent)
	}

	// Auto-Compact Watchdog Trigger: Check if input_tokens reached threshold
	if auditStatus == domain.StatusSuccess && !isEphemeral && e.cfg != nil && e.cfg.AGY.AutoCompact && audit.Usage.InputTokens > 0 {
		customAliases := make(map[string]string)
		if e.cfg != nil {
			customAliases = e.cfg.AGY.ModelAliases
		}
		capability, _, _ := domain.LookupModelCapability(resolvedModel, customAliases)
		threshold := capability.EffectiveCompactThreshold()
		if threshold > 0 && audit.Usage.InputTokens >= threshold {
			slog.WarnContext(turnCtx, "Session context exceeded compact threshold, triggering auto-compaction",
				slog.String("session_key", sessionKey),
				slog.Int("input_tokens", audit.Usage.InputTokens),
				slog.Int("threshold", threshold),
				slog.String("model", resolvedModel),
			)
			compactCtx, compactCancel := context.WithTimeout(context.WithoutCancel(turnCtx), 60*time.Second)
			defer compactCancel()
			if compRes, compErr := e.CompactSessionContext(compactCtx, session, agent, "auto-threshold"); compErr == nil && compRes != nil {
				_ = e.channel.Send(compactCtx, domain.OutboundMessage{
					Channel:  msg.Channel,
					BotID:    msg.BotID,
					ChatID:   msg.Chat.ID,
					ThreadID: msg.Chat.ThreadID,
					Text: fmt.Sprintf("🧹 **Auto-Compact Triggered:** Context utilization reached %.1f%% (%s / %s tokens).\nConversation has been archived and continuity digest preserved for subsequent turns.",
						float64(audit.Usage.InputTokens)/float64(capability.EffectiveMaxContext())*100.0,
						formatNumber(audit.Usage.InputTokens),
						formatNumber(capability.EffectiveMaxContext()),
					),
				})
			}
		}
	}

	hasFailed := execErr != nil || execResult == nil || !execResult.Success
	outcome := domain.ExecutionOutcomeFromError(execErr)
	if execResult != nil && execResult.Outcome != "" {
		outcome = execResult.Outcome
	}
	if hasFailed && outcome == "" && !isInterrupted {
		outcome = domain.StatusExecutionFailed
	}
	isDenied := outcome == domain.StatusNativePermissionDenied || outcome == domain.StatusPolicyDenied
	errMsg = ""
	if hasFailed {
		if execErr != nil {
			errMsg = execErr.Error()
		} else if execResult != nil && execResult.Error != "" {
			errMsg = execResult.Error
		} else {
			errMsg = "turn execution failed without specific error message"
		}
	}

	if e.eventBus != nil {
		emitCtx, emitCancel := context.WithTimeout(context.WithoutCancel(turnCtx), 5*time.Second)
		defer emitCancel()

		if hasFailed {
			_ = e.eventBus.SyncEmit(emitCtx, domain.NewEvent(domain.EventErrorOccurred, errMsg))
		}

		if isStream {
			conversationID := session.GetActiveConversationID()
			if execResult != nil && execResult.ConversationID != "" {
				conversationID = execResult.ConversationID
			}
			switch {
			case isInterrupted:
				_ = e.eventBus.SyncEmit(emitCtx, domain.NewEvent(domain.EventStreamInterrupted, domain.StreamInterruptedPayload{
					SessionKey:     sessionKey,
					ConversationID: conversationID,
					TurnID:         turnID,
					Reason:         "Preempted by incoming user message",
					Timestamp:      time.Now(),
				}))
			case isDenied:
				response := denialResponse(outcome, execResult)
				_ = e.eventBus.SyncEmit(emitCtx, domain.NewEvent(domain.EventStreamResult, domain.StreamResultPayload{
					SessionKey:      sessionKey,
					ConversationID:  conversationID,
					TurnID:          turnID,
					Status:          string(outcome),
					Outcome:         outcome,
					Response:        response,
					DurationSeconds: executionDuration(execResult),
					NumTurns:        executionNumTurns(execResult),
					Usage:           executionUsage(execResult),
					Artifacts:       executionArtifacts(execResult),
				}))
			case hasFailed:
				_ = e.eventBus.SyncEmit(emitCtx, domain.NewEvent(domain.EventStreamError, domain.StreamErrorPayload{
					SessionKey:     sessionKey,
					ConversationID: conversationID,
					TurnID:         turnID,
					Error:          errMsg,
				}))
			default:
				_ = e.eventBus.SyncEmit(emitCtx, domain.NewEvent(domain.EventStreamResult, domain.StreamResultPayload{
					SessionKey:      sessionKey,
					ConversationID:  conversationID,
					TurnID:          turnID,
					Status:          domain.StatusSuccess,
					Response:        execResult.ResponseText,
					DurationSeconds: execResult.DurationSec,
					NumTurns:        execResult.NumTurns,
					Usage:           execResult.Usage,
					Artifacts:       execResult.Artifacts,
				}))
			}
		}
		if !hasFailed {
			_ = e.eventBus.SyncEmit(emitCtx, domain.NewEvent(domain.EventPostExecution, execResult))
		}
	}

	if e.storage != nil {
		if hasFailed {
			_ = e.storage.UpdateInFlightTurnStatus(context.WithoutCancel(turnCtx), turnID, domain.TurnStatusFailed, errMsg)
		} else {
			_ = e.storage.UpdateInFlightTurnStatus(context.WithoutCancel(turnCtx), turnID, domain.TurnStatusCompleted, "")
		}
	}

	if hasFailed && !isInterrupted {
		if !isStream {
			if e.channel != nil {
				outCtx, outCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
				defer outCancel()
				failureMsg := errMsg
				if isDenied {
					failureMsg = denialResponse(outcome, execResult)
				}
				if failureMsg == "" {
					failureMsg = "Turn execution encountered an internal error."
				}
				messageText := fmt.Sprintf("⚠️ Execution failed: %s", failureMsg)
				if isDenied {
					messageText = failureMsg
				}
				_ = e.channel.Send(outCtx, domain.OutboundMessage{
					Channel:          msg.Channel,
					BotID:            msg.BotID,
					ChatID:           msg.Chat.ID,
					ThreadID:         msg.Chat.ThreadID,
					Text:             messageText,
					ReplyToMessageID: msg.ID,
				})
			}
		}
		if execErr != nil {
			return execErr
		}
		return fmt.Errorf("turn execution failed: %s", errMsg)
	}

	return nil
}

func denialResponse(outcome domain.ExecutionOutcome, result *domain.ExecutionResult) string {
	if result != nil && strings.TrimSpace(result.ResponseText) != "" {
		return result.ResponseText
	}
	if outcome == domain.StatusPolicyDenied {
		return "Thao tác chưa được thực hiện vì chính sách bảo mật của agyent đã từ chối yêu cầu."
	}
	return "Thao tác chưa được thực hiện vì cấu hình quyền của AGY chưa cho phép chạy tự động."
}

func executionDuration(result *domain.ExecutionResult) float64 {
	if result == nil {
		return 0
	}
	return result.DurationSec
}

func executionNumTurns(result *domain.ExecutionResult) int {
	if result == nil {
		return 0
	}
	return result.NumTurns
}

func executionUsage(result *domain.ExecutionResult) domain.TokenUsage {
	if result == nil {
		return domain.TokenUsage{}
	}
	return result.Usage
}

func executionArtifacts(result *domain.ExecutionResult) []domain.Attachment {
	if result == nil {
		return nil
	}
	return result.Artifacts
}

func (e *Engine) registerActiveTurn(sessionKey string, turnID string, cancel context.CancelFunc) {
	e.turnsMu.Lock()
	defer e.turnsMu.Unlock()
	e.activeTurns[sessionKey] = activeTurnEntry{turnID: turnID, cancel: cancel}
}

func (e *Engine) unregisterActiveTurn(sessionKey string, turnID string) {
	e.turnsMu.Lock()
	defer e.turnsMu.Unlock()
	if entry, exists := e.activeTurns[sessionKey]; exists && entry.turnID == turnID {
		delete(e.activeTurns, sessionKey)
	}
}

func (e *Engine) cancelActiveTurn(sessionKey string) {
	e.turnsMu.Lock()
	defer e.turnsMu.Unlock()
	if entry, exists := e.activeTurns[sessionKey]; exists {
		entry.cancel()
		delete(e.activeTurns, sessionKey)
	}
}

// ForceUnlockSession cancels any running turn subprocess, resets the lock manager,
// cancels any active subagent tasks for the session, cancels any pending HITL security approvals,
// and emits stream cleanup events.
func (e *Engine) ForceUnlockSession(sessionKey string) {
	e.cancelActiveTurn(sessionKey)
	e.lockManager.ForceUnlock(sessionKey)

	if e.subagentDispatcher != nil {
		if tasks, err := e.subagentDispatcher.ListActiveTasks(context.Background(), sessionKey); err == nil {
			for _, t := range tasks {
				_ = e.subagentDispatcher.CancelTaskScoped(context.Background(), sessionKey, t.ID)
			}
		}
	}

	if e.securityManager != nil {
		e.securityManager.CancelSessionApprovals(sessionKey)
	}

	if e.eventBus != nil {
		_ = e.eventBus.SyncEmit(context.Background(), domain.NewEvent(domain.EventStreamError, domain.StreamErrorPayload{
			SessionKey: sessionKey,
			Error:      "Session forcefully unlocked by user",
		}))
	}
}

// EventBus returns the attached EventBusPort instance.
func (e *Engine) EventBus() ports.EventBusPort {
	return e.eventBus
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

	if e.scheduler != nil {
		_ = e.scheduler.Stop(ctx)
	}

	e.turnsMu.Lock()
	for k, entry := range e.activeTurns {
		entry.cancel()
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
	return domain.IsEffortError(err, result)
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

		parsedKey, _ := domain.ParseSessionKey(task.ParentSessionKey)
		channel := parsedKey.Channel
		if channel == "" {
			if e.channel != nil && e.channel.Name() != "composite" {
				channel = e.channel.Name()
			} else {
				channel = "telegram" // Safe canonical default fallback
			}
		}
		chatID := parsedKey.ChatID
		if chatID == "" {
			chatID = domain.ExtractChatIDFromSessionKey(task.ParentSessionKey)
		}

		if task.CallbackMode == domain.CallbackInvokeMain {
			syntheticMsg := domain.CanonicalMessage{
				ID:      fmt.Sprintf("sub-synth-%s", task.ID),
				Channel: channel,
				BotID:   parsedKey.BotID,
				Chat: domain.ChatContext{
					ID:       chatID,
					ThreadID: parsedKey.ThreadID,
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
			_ = e.channel.Send(ctx, domain.OutboundMessage{
				Channel:    channel,
				SessionKey: task.ParentSessionKey,
				BotID:      parsedKey.BotID,
				ChatID:     chatID,
				ThreadID:   parsedKey.ThreadID,
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

		parsedKey, _ := domain.ParseSessionKey(task.ParentSessionKey)
		channel := parsedKey.Channel
		if channel == "" {
			if e.channel != nil && e.channel.Name() != "composite" {
				channel = e.channel.Name()
			} else {
				channel = "telegram" // Safe canonical default fallback
			}
		}
		chatID := parsedKey.ChatID
		if chatID == "" {
			chatID = domain.ExtractChatIDFromSessionKey(task.ParentSessionKey)
		}

		if task.CallbackMode == domain.CallbackInvokeMain {
			syntheticMsg := domain.CanonicalMessage{
				ID:        fmt.Sprintf("sub-synth-%s", task.ID),
				Timestamp: time.Now(),
				Channel:   channel,
				BotID:     parsedKey.BotID,
				Chat: domain.ChatContext{
					ID:       chatID,
					ThreadID: parsedKey.ThreadID,
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
			_ = e.channel.Send(ctx, domain.OutboundMessage{
				Channel:    channel,
				SessionKey: task.ParentSessionKey,
				BotID:      parsedKey.BotID,
				ChatID:     chatID,
				ThreadID:   parsedKey.ThreadID,
				Text: fmt.Sprintf("✅ **Sub-Agent @%s completed!**\n📌 **Task:** %s (`%s`)\n⏱️ **Duration:** %.2fs | 🪙 **Tokens:** %d\n\n%s",
					task.AgentName, task.Title, task.ID, task.DurationSeconds, task.Usage.TotalTokens, task.ResultSummary),
				ParseMode: "Markdown",
			})
		}
	})

	e.eventBus.SubscribeAsync(domain.EventSubagentFailed, func(ctx context.Context, evt domain.Event) {
		payload, ok := evt.Payload.(domain.SubagentEventPayload)
		if !ok {
			return
		}
		task := payload.Task

		parsedKey, _ := domain.ParseSessionKey(task.ParentSessionKey)
		channel := parsedKey.Channel
		if channel == "" {
			if e.channel != nil && e.channel.Name() != "composite" {
				channel = e.channel.Name()
			} else {
				channel = "telegram" // Safe canonical default fallback
			}
		}
		chatID := parsedKey.ChatID
		if chatID == "" {
			chatID = domain.ExtractChatIDFromSessionKey(task.ParentSessionKey)
		}

		if task.CallbackMode == domain.CallbackInvokeMain {
			syntheticMsg := domain.CanonicalMessage{
				ID:        fmt.Sprintf("sub-synth-%s", task.ID),
				Timestamp: time.Now(),
				Channel:   channel,
				BotID:     parsedKey.BotID,
				Chat: domain.ChatContext{
					ID:       chatID,
					ThreadID: parsedKey.ThreadID,
				},
				Text: fmt.Sprintf("[SYSTEM NOTIFICATION: Subagent Task #%s (@%s) FAILED]\nTask Title: %s\nError: %s\nDuration: %.2fs\n\nPlease evaluate this failure and report or handle it appropriately.",
					task.ID, task.AgentName, task.Title, task.ErrorMessage, task.DurationSeconds),
			}
			select {
			case e.inboundChan <- syntheticMsg:
			default:
				slog.Warn("inbound queue full for subagent failure message", "task_id", task.ID)
			}
		} else if task.CallbackMode == domain.CallbackNotifyUser && e.channel != nil {
			_ = e.channel.Send(ctx, domain.OutboundMessage{
				Channel:  channel,
				BotID:    parsedKey.BotID,
				ChatID:   chatID,
				ThreadID: parsedKey.ThreadID,
				Text: fmt.Sprintf("❌ **Sub-Agent @%s failed!**\n📌 **Task:** %s (`%s`)\n⚠️ **Error:** %s\n⏱️ **Duration:** %.2fs",
					task.AgentName, task.Title, task.ID, task.ErrorMessage, task.DurationSeconds),
				ParseMode: "Markdown",
			})
		}
	})
}

func (e *Engine) subscribeSchedulerEvents() {
	if e.eventBus == nil || e.channel == nil {
		return
	}

	// 1. Scheduled Task Completed
	e.eventBus.SubscribeAsync(domain.EventScheduleCompleted, func(ctx context.Context, evt domain.Event) {
		payload, ok := evt.Payload.(domain.ScheduleEventPayload)
		if !ok {
			return
		}
		task := payload.Task
		if task.ChatID == "" {
			return
		}

		channel := task.Channel
		if channel == "" {
			channel = "telegram"
		}

		var botID int64
		if task.TargetSessionKey != "" {
			if parsed, err := domain.ParseSessionKey(task.TargetSessionKey); err == nil {
				botID = parsed.BotID
			}
		}

		threadID, _ := strconv.ParseInt(task.ThreadID, 10, 64)

		body := strings.TrimSpace(payload.Response)
		msgText := fmt.Sprintf("⏰ **Scheduled Task #%s Completed:** %s\n🤖 **Agent:** `@%s`",
			task.ID, task.Title, task.AgentName)
		if body != "" {
			msgText += fmt.Sprintf("\n\n%s", body)
		} else {
			msgText += fmt.Sprintf("\n\n📌 **Instructions:** %s", task.Prompt)
		}

		_ = e.channel.Send(ctx, domain.OutboundMessage{
			Channel:        channel,
			BotID:          botID,
			AgentName:      task.AgentName,
			ChatID:         task.ChatID,
			ThreadID:       threadID,
			Text:           msgText,
			ParseMode:      "Markdown",
			WorkspaceDir:   payload.WorkspaceDir,
			ConversationID: payload.ConversationID,
			Attachments:    domain.ToOutboundAttachments(payload.Artifacts),
		})
	})

	// 2. Scheduled Task Failed
	e.eventBus.SubscribeAsync(domain.EventScheduleFailed, func(ctx context.Context, evt domain.Event) {
		payload, ok := evt.Payload.(domain.ScheduleEventPayload)
		if !ok {
			return
		}
		task := payload.Task
		if task.ChatID == "" {
			return
		}

		channel := task.Channel
		if channel == "" {
			channel = "telegram"
		}

		var botID int64
		if task.TargetSessionKey != "" {
			if parsed, err := domain.ParseSessionKey(task.TargetSessionKey); err == nil {
				botID = parsed.BotID
			}
		}

		threadID, _ := strconv.ParseInt(task.ThreadID, 10, 64)

		errMsg := formatScheduleFailureError(payload.Error)
		if errMsg == "" || errMsg == "Execution timed out or aborted without diagnostic output" {
			if task.LastError != "" {
				errMsg = formatScheduleFailureError(task.LastError)
			}
		}

		_ = e.channel.Send(ctx, domain.OutboundMessage{
			Channel:   channel,
			BotID:     botID,
			AgentName: task.AgentName,
			ChatID:    task.ChatID,
			ThreadID:  threadID,
			Text:      fmt.Sprintf("⚠️ **Scheduled Task #%s Failed:** %s\n🤖 **Agent:** `@%s`\n❌ **Error:** %s", task.ID, task.Title, task.AgentName, errMsg),
			ParseMode: "Markdown",
		})
	})

	// 3. Heartbeat Completed
	e.eventBus.SubscribeAsync(domain.EventHeartbeatCompleted, func(ctx context.Context, evt domain.Event) {
		payload, ok := evt.Payload.(domain.HeartbeatEventPayload)
		if !ok {
			return
		}
		hb := payload.Config
		if hb.ChatID == "" {
			return
		}

		channel := hb.Channel
		if channel == "" {
			channel = "telegram"
		}

		var botID int64
		if hb.TargetSessionKey != "" {
			if parsed, err := domain.ParseSessionKey(hb.TargetSessionKey); err == nil {
				botID = parsed.BotID
			}
		}

		respText := strings.TrimSpace(payload.Response)
		if respText == "" {
			respText = "No findings to report. All checks completed successfully."
		}

		threadID, _ := strconv.ParseInt(hb.ThreadID, 10, 64)

		_ = e.channel.Send(ctx, domain.OutboundMessage{
			Channel:        channel,
			BotID:          botID,
			AgentName:      hb.AgentName,
			ChatID:         hb.ChatID,
			ThreadID:       threadID,
			Text:           fmt.Sprintf("💓 **Heartbeat Report (@%s):**\n\n%s", hb.AgentName, respText),
			ParseMode:      "Markdown",
			WorkspaceDir:   payload.WorkspaceDir,
			ConversationID: payload.ConversationID,
			Attachments:    domain.ToOutboundAttachments(payload.Artifacts),
		})
	})

	// 4. Heartbeat Failed
	e.eventBus.SubscribeAsync(domain.EventHeartbeatFailed, func(ctx context.Context, evt domain.Event) {
		payload, ok := evt.Payload.(domain.HeartbeatEventPayload)
		if !ok {
			return
		}
		hb := payload.Config
		if hb.ChatID == "" {
			return
		}

		channel := hb.Channel
		if channel == "" {
			channel = "telegram"
		}

		var botID int64
		if hb.TargetSessionKey != "" {
			if parsed, err := domain.ParseSessionKey(hb.TargetSessionKey); err == nil {
				botID = parsed.BotID
			}
		}

		threadID, _ := strconv.ParseInt(hb.ThreadID, 10, 64)

		_ = e.channel.Send(ctx, domain.OutboundMessage{
			Channel:   channel,
			BotID:     botID,
			AgentName: hb.AgentName,
			ChatID:    hb.ChatID,
			ThreadID:  threadID,
			Text: fmt.Sprintf("💔 **Heartbeat Failed (@%s):**\n⚠️ %s",
				hb.AgentName, payload.Error),
			ParseMode: "Markdown",
		})
	})
}

func extractChatIDFromSessionKey(sessionKey string) string {
	return domain.ExtractChatIDFromSessionKey(sessionKey)
}

func formatScheduleFailureError(rawErr string) string {
	raw := strings.TrimSpace(rawErr)
	if raw == "" {
		return "Execution timed out or aborted without diagnostic output"
	}
	switch {
	case strings.Contains(raw, "context deadline exceeded") || strings.Contains(raw, "signal: killed"):
		return fmt.Sprintf("Execution timed out: %s", raw)
	case strings.Contains(raw, "TOOL_DEADLINE_EXCEEDED"):
		return fmt.Sprintf("Tool deadline exceeded: %s", raw)
	case strings.Contains(raw, "database is locked") || strings.Contains(raw, "busy_timeout"):
		return fmt.Sprintf("Database lock contention: %s", raw)
	case strings.Contains(raw, "session lock"):
		return fmt.Sprintf("Session lock acquisition failed: %s", raw)
	default:
		return raw
	}
}
