package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"agyent/internal/config"
	"agyent/internal/core/concurrency"
	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

// HandleCommand processes slash commands and returns an outbound response.
func (e *Engine) HandleCommand(ctx context.Context, msg domain.CanonicalMessage) (*domain.OutboundMessage, error) {
	cmd, args := msg.CommandArgs()
	cmd = strings.ToLower(cmd)
	sessionKey := msg.SessionKey()

	defaultAgent := "agyent"
	if msg.BindAgent != "" {
		defaultAgent = msg.BindAgent
	}
	// Inbound Authorization: Evaluate access to targetAgent BEFORE mutating session state
	// Note: /a and /agents subcommands (new, list, claim, share, revoke) manage their own
	// targeted permissions per-subcommand and per-agent.
	if cmd != "/a" && cmd != "/agents" {
		targetAgent := defaultAgent
		existingSession, _ := e.storage.GetSession(ctx, sessionKey)
		if existingSession != nil && existingSession.ActiveAgent != "" {
			targetAgent = existingSession.ActiveAgent
		}
		if msg.BindAgent != "" {
			targetAgent = msg.BindAgent
		}

		agent, getErr := e.storage.GetAgent(ctx, targetAgent)
		if getErr == nil && agent != nil {
			allowed, _, checkErr := e.CheckAccessForProvider(ctx, agent, msg.Sender.ID, msg.Channel)
			if checkErr != nil || !allowed {
				return &domain.OutboundMessage{
					Channel:          msg.Channel,
					BotID:            msg.BotID,
					ChatID:           msg.Chat.ID,
					ThreadID:         msg.Chat.ThreadID,
					Text:             fmt.Sprintf("⛔ **Access Denied (403):** You do not have permission to interact with agent `@%s`.", targetAgent),
					ParseMode:        "Markdown",
					ReplyToMessageID: msg.ID,
				}, nil
			}
		} else if !e.IsSuperAdminForProvider(msg.Sender.ID, msg.Channel) {
			return &domain.OutboundMessage{
				Channel:          msg.Channel,
				BotID:            msg.BotID,
				ChatID:           msg.Chat.ID,
				ThreadID:         msg.Chat.ThreadID,
				Text:             "⛔ **Access Denied (403):** Unauthorized caller.",
				ParseMode:        "Markdown",
				ReplyToMessageID: msg.ID,
			}, nil
		}
	}

	session, err := e.storage.GetOrCreateSession(ctx, sessionKey, defaultAgent)
	if err != nil {
		return &domain.OutboundMessage{
			Channel:          msg.Channel,
			BotID:            msg.BotID,
			ChatID:           msg.Chat.ID,
			ThreadID:         msg.Chat.ThreadID,
			Text:             fmt.Sprintf("⚠️ Failed to load session: %v", err),
			ReplyToMessageID: msg.ID,
		}, nil
	}

	if msg.BindAgent != "" && session.ActiveAgent != msg.BindAgent {
		session.ActiveAgent = msg.BindAgent
		session.ActiveProject = ""
		_ = e.storage.SaveSession(ctx, session)
	}

	// Commands that mutate session-scoped state are authorized before their
	// handlers can inspect or change the target object. Handler-level checks
	// remain for object ownership (for example a concrete schedule/task ID).
	if action, kind, ok := commandAction(cmd, args); ok {
		if err := e.authorizeAgentAction(ctx, msg.Sender, action, kind, "", session); err != nil {
			return e.commandDeniedMessage(msg), nil
		}
		if action == domain.ActionProjectCreate && len(args) > 2 {
			if err := e.authorizeAgentAction(ctx, msg.Sender, domain.ActionProjectCreatePath, domain.ResourceKindProject, "", session); err != nil {
				return e.commandDeniedMessage(msg), nil
			}
		}
	}

	var responseText string
	var inlineKeyboard domain.InlineKeyboard

	switch cmd {
	case "/help":
		responseText = e.handleHelpCommand()

	case "/status":
		responseText = e.handleStatusCommand(ctx, session)

	case "/tokens", "/token", "/metrics", "/stats", "/analytics":
		responseText = e.handleTokensCommand(ctx, session, args)

	case "/context":
		responseText = e.handleContextCommand(ctx, session)

	case "/skills", "/skill":
		responseText = e.handleSkillsCommand(ctx, session, args)

	case "/plugins", "/plugin":
		if len(args) > 0 && strings.ToLower(args[0]) != "list" {
			if e.policyEngine != nil {
				principal := domain.Principal{Kind: domain.PrincipalUser, Provider: msg.Channel, SubjectID: msg.Sender.ID}
				res := domain.Resource{Kind: domain.ResourceKindPlugin, ID: args[0], AgentName: session.ActiveAgent}
				if err := e.policyEngine.Authorize(ctx, principal, domain.ActionPluginManage, res); err != nil {
					responseText = "⛔ Permission denied: Only SuperAdmins can modify plugins."
					break
				}
			} else if !e.IsSuperAdminForProvider(msg.Sender.ID, msg.Channel) {
				responseText = "⛔ Permission denied: Only SuperAdmins can modify plugins."
				break
			}
		}
		responseText = e.handlePluginsCommand(ctx, session, args)

	case "/browser":
		responseText = "🌐 **Camoufox Browser Plugin**\n\n" +
			"• To check plugin status: `/plugins`\n" +
			"• To enable: `/plugin enable browser-camoufox`\n" +
			"• To browse or scrape: Prompt your agent in natural language (e.g. _\"Mở website https://... và cào danh sách sản phẩm\"_), and the agent will automatically use native `camoufox_*` MCP tools."

	case "/stream":
		if len(args) > 0 {
			if e.policyEngine != nil {
				principal := domain.Principal{Kind: domain.PrincipalUser, Provider: msg.Channel, SubjectID: msg.Sender.ID}
				res := domain.Resource{Kind: domain.ResourceKindSystem, ID: "stream", AgentName: session.ActiveAgent}
				if err := e.policyEngine.Authorize(ctx, principal, domain.ActionStreamModeChange, res); err != nil {
					responseText = "⛔ Permission denied: Only SuperAdmins can modify global stream settings."
					break
				}
			} else if !e.IsSuperAdminForProvider(msg.Sender.ID, msg.Channel) {
				responseText = "⛔ Permission denied: Only SuperAdmins can modify global stream settings."
				break
			}
		}
		responseText = e.handleStreamCommand(args)

	case "/mode", "/queuemode":
		if len(args) > 0 {
			if e.policyEngine != nil {
				principal := domain.Principal{Kind: domain.PrincipalUser, Provider: msg.Channel, SubjectID: msg.Sender.ID}
				res := domain.Resource{Kind: domain.ResourceKindSystem, ID: "mode", AgentName: session.ActiveAgent}
				if err := e.policyEngine.Authorize(ctx, principal, domain.ActionModeChange, res); err != nil {
					responseText = "⛔ Permission denied: Only SuperAdmins can modify global queue mode."
					break
				}
			} else if !e.IsSuperAdminForProvider(msg.Sender.ID, msg.Channel) {
				responseText = "⛔ Permission denied: Only SuperAdmins can modify global queue mode."
				break
			}
		}
		responseText = e.handleModeCommand(args)

	case "/model", "/m", "/models":
		responseText, inlineKeyboard = e.handleModelCommand(ctx, session, args)

	case "/effort", "/eff":
		responseText, inlineKeyboard = e.handleEffortCommand(ctx, session, args)

	case "/agents", "/agent", "/a":
		responseText = e.handleAgentsCommand(ctx, msg.Sender, session, args)

	case "/bootstrap":
		responseText = e.handleBootstrapCommand(ctx, msg.Sender, session, args)

	case "/projects", "/project", "/p":
		responseText = e.handleProjectsCommand(ctx, msg.Sender, session, args)

	case "/security", "/sec":
		responseText, inlineKeyboard = e.handleSecurityCommand(msg.Sender, sessionKey, args)

	case "/whitelist":
		responseText = e.handleWhitelistCommand(msg.Sender, sessionKey, args)

	case "/schedule", "/schedules", "/cron":
		responseText, inlineKeyboard = e.handleScheduleCommand(ctx, msg.Sender, session, args)

	case "/heartbeat", "/hb":
		responseText, inlineKeyboard = e.handleHeartbeatCommand(ctx, msg.Sender, session, args)

	case "/tasks", "/subagents":
		responseText, inlineKeyboard = e.handleTasksCommand(ctx, session, args)

	case "/task":
		responseText, inlineKeyboard = e.handleTaskSubcommand(ctx, session, args)

	case "/conversations", "/c":
		responseText, inlineKeyboard = e.handleConversationsDispatcher(ctx, session, args)

	case "/new":
		responseText = e.handleNewConversationCommand(ctx, session, args)

	case "/compact", "/compress":
		responseText = e.handleCompactCommand(ctx, msg.Sender, session, args)

	case "/pin":
		responseText = e.handlePinCommand(ctx, session, args, true)

	case "/unpin":
		responseText = e.handlePinCommand(ctx, session, args, false)

	case "/reset":
		if e.policyEngine != nil {
			principal := domain.Principal{Kind: domain.PrincipalUser, Provider: msg.Channel, SubjectID: msg.Sender.ID}
			res := domain.Resource{Kind: domain.ResourceKindSession, ID: sessionKey, SessionKey: sessionKey, AgentName: session.ActiveAgent}
			if err := e.policyEngine.Authorize(ctx, principal, domain.ActionSessionReset, res); err != nil {
				responseText = "⛔ Permission denied: You are not authorized to reset this session context."
				break
			}
		}
		if e.HasActiveTurn(sessionKey) {
			responseText = "⚠️ A turn is currently executing in this conversation. Please wait for completion or send `/force_unlock` before resetting."
		} else {
			oldConvID := session.GetActiveConversationID()
			if oldConvID != "" && e.evolution != nil {
				bgCtx := context.WithoutCancel(ctx)
				concurrency.SafeGo(func() {
					_ = e.evolution.TriggerConversationEvolution(bgCtx, oldConvID, domain.TriggerExplicitSwitch)
				})
			}
			session.ResetActiveConversationID()
			if err := e.storage.SaveSession(ctx, session); err != nil {
				responseText = fmt.Sprintf("⚠️ Failed to reset conversation context: %v", err)
			} else {
				scope := "Global Mode"
				if session.ActiveProject != "" {
					scope = fmt.Sprintf("Project: %s", session.ActiveProject)
				}
				responseText = fmt.Sprintf("🧹 **Short-term conversation context reset** for [%s • %s].\nYour next message will begin in a fresh, clean context.", session.ActiveAgent, scope)
			}
		}

	case "/force_unlock", "/unlock":
		if e.policyEngine != nil {
			principal := domain.Principal{Kind: domain.PrincipalUser, Provider: msg.Channel, SubjectID: msg.Sender.ID}
			res := domain.Resource{Kind: domain.ResourceKindSession, ID: sessionKey, SessionKey: sessionKey, AgentName: session.ActiveAgent}
			if err := e.policyEngine.Authorize(ctx, principal, domain.ActionTurnForceUnlock, res); err != nil {
				responseText = "⛔ Permission denied: You are not authorized to force unlock this session."
				break
			}
		}
		e.ForceUnlockSession(sessionKey)
		responseText = "🔓 **Session mutex forcefully released.** Any hanging turn subprocess has been terminated."

	default:
		responseText = fmt.Sprintf("❓ Unknown command `%s`. Type `/help` for available commands.", cmd)
	}

	return &domain.OutboundMessage{
		Channel:          msg.Channel,
		BotID:            msg.BotID,
		ChatID:           msg.Chat.ID,
		ThreadID:         msg.Chat.ThreadID,
		Text:             responseText,
		ParseMode:        "Markdown",
		ReplyToMessageID: msg.ID,
		InlineKeyboard:   inlineKeyboard,
	}, nil
}

func (e *Engine) commandDeniedMessage(msg domain.CanonicalMessage) *domain.OutboundMessage {
	return &domain.OutboundMessage{
		Channel:          msg.Channel,
		BotID:            msg.BotID,
		ChatID:           msg.Chat.ID,
		ThreadID:         msg.Chat.ThreadID,
		Text:             "⛔ **Access Denied (403):** Your role does not permit this action for the active agent.",
		ParseMode:        "Markdown",
		ReplyToMessageID: msg.ID,
	}
}

func (e *Engine) handleHelpCommand() string {
	return `🤖 **agyent Gateway Daemon — Commands Guide**

**🌐 General & System:**
• ` + "`/status`" + ` — View system uptime, active agent, scope, streaming mode, and resource stats.
• ` + "`/tokens`" + ` (or ` + "`/metrics`" + `) — Inspect detailed token usage, KV-cache read tokens, and effective cost savings.
• ` + "`/context`" + ` — Inspect active context directives, token budget, and active MCP servers.
• ` + "`/model [name]`" + ` (or ` + "`/m`" + `) — Inspect or switch active AI model (` + "`pro`" + `, ` + "`flash`" + `, ` + "`flash-lite`" + `, ` + "`reset`" + `).
• ` + "`/effort [level]`" + ` (or ` + "`/eff`" + `) — Inspect or switch reasoning effort (` + "`low`" + `, ` + "`medium`" + `, ` + "`high`" + `, ` + "`none`" + `, ` + "`reset`" + `).
• ` + "`/skills`" + ` — List available Progressive Disclosure skills.
• ` + "`/plugins`" + ` — Manage capability plugins (` + "`/plugins`" + `, ` + "`/plugin enable <name>`" + `, ` + "`/plugin disable <name>`" + `).
• ` + "`/stream [on|off]`" + ` — Query or toggle Real-Time Streaming mode (` + "`stream-json`" + ` vs ` + "`batch`" + `).
• ` + "`/reset`" + ` — Clear short-term conversation context for the active scope.
• ` + "`/bootstrap [name]`" + ` (or ` + "`/a bootstrap`" + `) — Force re-trigger Genesis Bootstrap protocol for an agent.
• ` + "`/force_unlock`" + ` — Emergency unlock session mutex and cancel hanging subprocess.
• ` + "`/help`" + ` — Show this help message.

**⏰ Schedules & Heartbeats:**
• ` + "`/schedule`" + ` (or ` + "`/cron`" + `) — View, list, or manage scheduled and recurring tasks.
• ` + "`/schedule cancel <id>`" + ` — Cancel a scheduled task by ID.
• ` + "`/heartbeat`" + ` — Inspect active heartbeat status, interval, and directives.
• ` + "`/heartbeat [on|off]`" + ` — Enable or disable periodic heartbeat wakeups.
• ` + "`/heartbeat interval <duration>`" + ` — Set heartbeat interval (e.g. ` + "`30m`" + `, ` + "`2h`" + `).
• ` + "`/heartbeat trigger`" + ` — Trigger an immediate heartbeat wakeup now.

**🧵 Conversation & Context:**
• ` + "`/ask <prompt>`" + ` — Ask an isolated ephemeral question without polluting active context.
• ` + "`/c`" + ` (or ` + "`/conversations`" + `) — View interactive conversation list with 1-touch buttons.
• ` + "`/c <#>`" + ` — Fast switch to conversation by number (e.g. ` + "`/c 2`" + `).
• ` + "`/new`" + ` — Start a fresh new conversation context.
• ` + "`/compact [note]`" + ` (or ` + "`/compress`" + `) — Compress bloated context into a structured continuity digest (~99% token reduction).
• ` + "`/pin`" + ` / ` + "`/unpin`" + ` — Pin or unpin the current active conversation.
• ` + "`/c rename <title>`" + ` — Rename the current active conversation.
• ` + "`/c archive`" + ` — Archive the current conversation.
• ` + "`/c clean`" + ` — Trigger garbage collection for old archived sessions.

**🤖 Agent Management & RBAC:**
• ` + "`/agents`" + ` (or ` + "`/a list`" + `) — List all agents accessible to your user.
• ` + "`/a new <name> [description]`" + ` — Register a new private agent persona with verified ownership.
• ` + "`/a share <agent> <user_id> [role]`" + ` — Grant collaborator access (` + "`admin`" + `, ` + "`operator`" + `, ` + "`viewer`" + `).
• ` + "`/a revoke <agent> <user_id>`" + ` — Revoke collaborator access.
• ` + "`/a info [agent]`" + ` — Inspect agent metadata, visibility, owner, and collaborators list.

**📁 Multi-Project Management:**
• ` + "`/projects`" + ` (or ` + "`/p list`" + `) — List all projects attached to active agent.
• ` + "`/p <name>`" + ` (or ` + "`/project use <name>`" + `) — Switch into a project workspace.
• ` + "`/p new <name> [path]`" + ` — Register a new project codebase.
• ` + "`/p exit`" + ` (or ` + "`/p ~`" + `) — Exit project and return to Global Chat mode.
• ` + "`/p info`" + ` — View details of the currently active project.
• ` + "`/p reset`" + ` — Reset conversation history for the current project.

**⚡ Sub-Agent Background Tasks:**
• ` + "`/tasks`" + ` (or ` + "`/subagents`" + `) — List active & recent background sub-agent tasks.
• ` + "`/task <id>`" + ` — Inspect task status, elapsed duration, and step progress.
• ` + "`/task reply <id> <text>`" + ` — Send answer to a sub-agent waiting for clarification.
• ` + "`/task cancel <id>`" + ` — Terminate a running sub-agent task.
• ` + "`/task clean`" + ` — Purge finished/cancelled sub-agent task records.

**🛡️ Security & Guardrails:**
• ` + "`/security`" + ` (or ` + "`/sec`" + `) — View Security Gateway Dashboard and switch presets.
• ` + "`/security preset <unrestricted|developer|balanced|strict|read_only>`" + ` — Switch active security profile.
• ` + "`/security grant <pattern>`" + ` — Grant temporary permission for 15 minutes.
• ` + "`/security redact <strict|permissive|audit_only>`" + ` — Switch DLP secret redaction mode.
• ` + "`/whitelist add \"<command>\"`" + ` — Add permanent custom whitelist rule.`
}

func (e *Engine) handleStatusCommand(ctx context.Context, session *domain.Session) string {
	uptime := time.Since(e.startTime).Round(time.Second)

	streamStatus := "OFF (Classic Batch json)"
	if e.IsStreamingEnabled() {
		streamStatus = "ON (Real-Time stream-json)"
	}

	scope := "🌐 Global Chat Mode"
	cwd := config.ResolveAgentWorkspace(e.cfg.Storage.AgentsDir, session.ActiveAgent)
	if session.ActiveAgent != "" {
		if agent, err := e.storage.GetAgent(ctx, session.ActiveAgent); err == nil && agent != nil && agent.WorkspacePath != "" {
			cwd = agent.WorkspacePath
		}
	}

	convID := session.GlobalConversationID
	if session.ActiveProject != "" {
		scope = fmt.Sprintf("📁 In-Project: %s", session.ActiveProject)
		projID := domain.FormatProjectID(session.ActiveAgent, session.ActiveProject)
		if proj, err := e.storage.GetProject(ctx, projID); err == nil {
			cwd = proj.ProjectPath
		}
		convID = session.ProjectConversationID
	}

	if convID == "" {
		convID = "(none / new)"
	}

	var agentObj *domain.Agent
	if session.ActiveAgent != "" {
		agentObj, _ = e.storage.GetAgent(ctx, session.ActiveAgent)
	}
	resolvedModel, resolvedEffort, modelSource := e.ResolveExecutionParams("", "", session, agentObj)
	if resolvedModel == "" {
		resolvedModel = "default (agy CLI)"
	}
	if resolvedEffort == "" {
		resolvedEffort = "none"
	}

	return fmt.Sprintf(`🚀 **agyent Gateway Status**
• **Uptime:** %s
• **Active Agent:** %s
• **Active Model:** %s (%s)
• **Reasoning Effort:** %s
• **Context Scope:** %s
• **Working Directory:** %s
• **Conversation ID:** %s
• **Streaming Mode:** %s
• **Active Locks:** %d
• **Dropped Events:** %d
• **AGY Binary:** %s (timeout: %ds)`,
		uptime,
		session.ActiveAgent,
		resolvedModel,
		modelSource,
		resolvedEffort,
		scope,
		cwd,
		convID,
		streamStatus,
		e.lockManager.ActiveLockCount(),
		e.eventBus.DroppedEventsCount(),
		e.cfg.AGY.BinaryPath,
		e.cfg.AGY.DefaultTimeoutSeconds,
	)
}

func (e *Engine) handleTokensCommand(ctx context.Context, session *domain.Session, args []string) string {
	if len(args) > 0 {
		sub := strings.ToLower(args[0])
		if sub == "stats" || sub == "analytics" || sub == "report" || sub == "history" || sub == "summary" {
			agentFilter := ""
			if len(args) > 1 {
				target := strings.TrimSpace(args[1])
				if target != "global" && target != "all" {
					agentFilter = target
				}
			}
			return e.handleTokenEfficiencyReportCommand(ctx, session, agentFilter)
		}

		// Direct agent filter or global query, e.g. "/tokens agyent" or "/stats coder" or "/stats global"
		if sub == "global" || sub == "all" {
			return e.handleTokenEfficiencyReportCommand(ctx, session, "")
		} else if sub != "turn" && sub != "session" {
			return e.handleTokenEfficiencyReportCommand(ctx, session, args[0])
		}
	}

	activeConvID := session.GetActiveConversationID()
	scopeLabel := "🌐 Global Chat Mode"
	if session.ActiveProject != "" {
		scopeLabel = fmt.Sprintf("📁 In-Project: %s", session.ActiveProject)
	}

	convStats, err := e.storage.GetTokenStats(ctx, session.SessionKey, activeConvID)
	if err != nil {
		return fmt.Sprintf("⚠️ Failed to query token metrics: %v", err)
	}

	sessionStats, _ := e.storage.GetTokenStats(ctx, session.SessionKey, "")

	if (convStats == nil || convStats.TotalTokens == 0) && (sessionStats == nil || sessionStats.TotalTokens == 0) {
		return fmt.Sprintf("📊 **No token metrics recorded yet** for [%s • %s].\n\nSend a message to start a conversation session and track token metrics.\n\n💡 _Tip: Type `/tokens stats` or `/stats [agent_name]` for full system efficiency report._", session.ActiveAgent, scopeLabel)
	}

	convTag := activeConvID
	if convTag == "" {
		convTag = "(uninitialized / new)"
	} else if len(convTag) > 8 {
		convTag = convTag[:8] + "..."
	}

	var agentObj *domain.Agent
	if session.ActiveAgent != "" {
		agentObj, _ = e.storage.GetAgent(ctx, session.ActiveAgent)
	}
	resolvedModel, _, _ := e.ResolveExecutionParams("", "", session, agentObj)
	customAliases := make(map[string]string)
	if e.cfg != nil {
		customAliases = e.cfg.AGY.ModelAliases
	}
	capability, _, _ := domain.LookupModelCapability(resolvedModel, customAliases)
	maxContext := capability.EffectiveMaxContext()
	compactThreshold := capability.EffectiveCompactThreshold()

	modelDisplayName := capability.DisplayName
	if modelDisplayName == "" {
		if capability.ID != "" {
			modelDisplayName = capability.ID
		} else {
			modelDisplayName = "Gemini 3.8 Flash (Default)"
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📊 **agyent Token Usage & Cache Metrics**\n"))
	sb.WriteString(fmt.Sprintf("• **Active Agent:** %s\n", session.ActiveAgent))
	sb.WriteString(fmt.Sprintf("• **Active Model:** %s (Max Window: %s tokens)\n", modelDisplayName, formatNumber(maxContext)))
	sb.WriteString(fmt.Sprintf("• **Context Scope:** %s\n", scopeLabel))
	recentLogs, _ := e.storage.ListAuditLogs(ctx, session.SessionKey, 5)
	var latestLog *domain.AuditLog
	for i := range recentLogs {
		if recentLogs[i].ConversationID == activeConvID {
			latestLog = &recentLogs[i]
			break
		}
	}

	if latestLog != nil && latestLog.Usage.TotalTokens > 0 {
		latestHitRatio := latestLog.Usage.CacheHitRatio()
		cacheState := "⚪ COLD / UNCACHED"
		if latestLog.Usage.CacheReadTokens > 0 {
			cacheState = "⚡ WARM (KV-Cache Active)"
		}

		utilizationPct := float64(latestLog.Usage.InputTokens) / float64(maxContext) * 100.0
		thresholdPct := capability.CompactThresholdRatio * 100.0
		if thresholdPct <= 0 {
			thresholdPct = 70.0
		}

		sb.WriteString("📐 **Active Turn Context Window:**\n")
		sb.WriteString(fmt.Sprintf("• **Prompt Input Size:** %s (%.1f%% of %s window)\n", formatNumber(latestLog.Usage.InputTokens), utilizationPct, formatNumber(maxContext)))
		sb.WriteString(fmt.Sprintf("• **⚡ KV-Cache Hit:** %s (%.1f%% Cache Hit)\n", formatNumber(latestLog.Usage.CacheReadTokens), latestHitRatio))
		sb.WriteString(fmt.Sprintf("• **Fresh Uncached Input:** %s\n", formatNumber(latestLog.Usage.UncachedInputTokens())))
		sb.WriteString(fmt.Sprintf("• **Turn Output:** %s (Thinking: %s)\n", formatNumber(latestLog.Usage.OutputTokens), formatNumber(latestLog.Usage.ThinkingTokens)))
		sb.WriteString(fmt.Sprintf("• **🧹 Auto-Compact Threshold:** %s tokens (%.0f%% limit)\n", formatNumber(compactThreshold), thresholdPct))
		sb.WriteString(fmt.Sprintf("• **Cache State:** %s\n\n", cacheState))
	}

	if convStats != nil && convStats.TotalTokens > 0 {
		hitRatio := convStats.CacheHitRatio()
		costSaved := convStats.EffectiveCostSavingsRatio()
		grossInput := convStats.GrossInputTokens()
		grossTotal := convStats.EffectiveTotalTokens()

		sb.WriteString("🧵 **Conversation Cumulative Billed:**\n")
		sb.WriteString(fmt.Sprintf("• **Total Input Evaluated:** %s tokens\n", formatNumber(grossInput)))
		sb.WriteString(fmt.Sprintf("• **⚡ Total Cache Read:** %s (%.1f%% Cache Hit)\n", formatNumber(convStats.CacheReadTokens), hitRatio))
		sb.WriteString(fmt.Sprintf("• **Total Fresh Input:** %s tokens\n", formatNumber(convStats.UncachedInputTokens())))
		sb.WriteString(fmt.Sprintf("• **Total Output Billed:** %s (Thinking: %s)\n", formatNumber(convStats.OutputTokens), formatNumber(convStats.ThinkingTokens)))
		sb.WriteString(fmt.Sprintf("• **Total Billed Tokens:** %s\n", formatNumber(grossTotal)))
		sb.WriteString(fmt.Sprintf("• **💰 Estimated Cost Savings:** ~%.1f%% (Gemini 0.25x Cache)\n\n", costSaved))
	}

	if sessionStats != nil && sessionStats.TotalTokens > 0 {
		sessionHitRatio := sessionStats.CacheHitRatio()
		sessionCostSaved := sessionStats.EffectiveCostSavingsRatio()
		grossSessionInput := sessionStats.GrossInputTokens()
		grossSessionTotal := sessionStats.EffectiveTotalTokens()

		sb.WriteString("🌐 **Session Lifetime:**\n")
		sb.WriteString(fmt.Sprintf("• **Lifetime Input:** %s | **Cached:** %s (%.1f%%)\n", formatNumber(grossSessionInput), formatNumber(sessionStats.CacheReadTokens), sessionHitRatio))
		sb.WriteString(fmt.Sprintf("• **Lifetime Output:** %s | **Thinking:** %s\n", formatNumber(sessionStats.OutputTokens), formatNumber(sessionStats.ThinkingTokens)))
		sb.WriteString(fmt.Sprintf("• **Lifetime Total:** %s (Net Savings: ~%.1f%%)\n", formatNumber(grossSessionTotal), sessionCostSaved))
	}

	sb.WriteString("\n💡 _Tip: Type `/tokens stats` or `/stats [agent_name]` for full temporal analytics & compaction efficiency report._")
	return sb.String()
}

func (e *Engine) handleTokenEfficiencyReportCommand(ctx context.Context, session *domain.Session, agentFilter string) string {
	report, err := e.storage.GetTokenEfficiencyReport(ctx, session.SessionKey, agentFilter)
	if err != nil {
		return fmt.Sprintf("⚠️ Failed to generate token analytics report: %v", err)
	}

	var sb strings.Builder
	sb.WriteString("📈 **agyent Token Analytics & Efficiency Report**\n")
	if agentFilter != "" {
		sb.WriteString(fmt.Sprintf("• **Agent Filter:** `%s`\n", agentFilter))
	} else {
		sb.WriteString(fmt.Sprintf("• **Active Agent:** `%s` (All Agents in Session)\n", session.ActiveAgent))
	}
	sb.WriteString(fmt.Sprintf("• **Report Generated:** `%s`\n\n", report.GeneratedAt.Format("2006-01-02 15:04:05")))

	// 1. Today Usage
	sb.WriteString("📅 **Today's Consumption:**\n")
	if report.TodayTurns > 0 {
		todayHit := report.TodayUsage.CacheHitRatio()
		todaySaved := report.TodayUsage.EffectiveCostSavingsRatio()
		sb.WriteString(fmt.Sprintf("• **Turns:** %d | **Total Billed:** %s tokens\n", report.TodayTurns, formatNumber(report.TodayUsage.EffectiveTotalTokens())))
		sb.WriteString(fmt.Sprintf("• **⚡ Cache Read:** %s (%.1f%% Hit Rate)\n", formatNumber(report.TodayUsage.CacheReadTokens), todayHit))
		sb.WriteString(fmt.Sprintf("• **Fresh Input:** %s | **Output:** %s\n", formatNumber(report.TodayUsage.UncachedInputTokens()), formatNumber(report.TodayUsage.OutputTokens)))
		sb.WriteString(fmt.Sprintf("• **💰 Estimated Cost Saved:** ~%.1f%%\n\n", todaySaved))
	} else {
		sb.WriteString("• _No turns recorded today yet._\n\n")
	}

	// 2. Past 7 Days
	sb.WriteString("🗓️ **Past 7 Days Consumption:**\n")
	if report.Past7DaysTurns > 0 {
		p7Hit := report.Past7DaysUsage.CacheHitRatio()
		p7Saved := report.Past7DaysUsage.EffectiveCostSavingsRatio()
		sb.WriteString(fmt.Sprintf("• **Turns:** %d | **Total Billed:** %s tokens\n", report.Past7DaysTurns, formatNumber(report.Past7DaysUsage.EffectiveTotalTokens())))
		sb.WriteString(fmt.Sprintf("• **⚡ Cache Read:** %s (%.1f%% Hit Rate)\n", formatNumber(report.Past7DaysUsage.CacheReadTokens), p7Hit))
		sb.WriteString(fmt.Sprintf("• **Fresh Input:** %s | **Output:** %s\n", formatNumber(report.Past7DaysUsage.UncachedInputTokens()), formatNumber(report.Past7DaysUsage.OutputTokens)))
		sb.WriteString(fmt.Sprintf("• **💰 Estimated Cost Saved:** ~%.1f%%\n\n", p7Saved))
	} else {
		sb.WriteString("• _No turns recorded in the past 7 days._\n\n")
	}

	// 3. All-Time Lifetime
	sb.WriteString("🌐 **All-Time Lifetime Totals:**\n")
	if report.AllTimeTurns > 0 {
		sb.WriteString(fmt.Sprintf("• **Total Executed Turns:** %d\n", report.AllTimeTurns))
		sb.WriteString(fmt.Sprintf("• **Total Input Evaluated:** %s tokens\n", formatNumber(report.AllTimeUsage.GrossInputTokens())))
		sb.WriteString(fmt.Sprintf("• **⚡ Total Cached Input:** %s (%.1f%% Lifetime Cache Hit)\n", formatNumber(report.AllTimeUsage.CacheReadTokens), report.AvgCacheHitRatio))
		sb.WriteString(fmt.Sprintf("• **Total Output Produced:** %s (Thinking: %s)\n", formatNumber(report.AllTimeUsage.OutputTokens), formatNumber(report.AllTimeUsage.ThinkingTokens)))
		sb.WriteString(fmt.Sprintf("• **💎 Net Resource Savings:** ~%.1f%% (Prefix KV-Cache Discount)\n\n", report.TotalCostSavedPct))
	} else {
		sb.WriteString("• _No lifetime turns recorded._\n\n")
	}

	// 4. Compaction Efficiency
	sb.WriteString("🧹 **Context Compactor Efficiency:**\n")
	if report.TotalCompactions > 0 {
		sb.WriteString(fmt.Sprintf("• **Successful Compactions:** %d runs\n", report.TotalCompactions))
		sb.WriteString(fmt.Sprintf("• **Estimated Bloat Prevented:** ~%s tokens\n", formatNumber(int(report.EstTokensSaved))))
		sb.WriteString("• **Average Context Reduction:** ~99.6% per compaction\n\n")
	} else {
		sb.WriteString("• **Successful Compactions:** 0 runs (Context has not needed compaction yet)\n\n")
	}

	// 5. Agent Breakdown
	if len(report.AgentBreakdown) > 0 {
		sb.WriteString("👥 **Breakdown by Agent Persona:**\n")
		for _, ab := range report.AgentBreakdown {
			hitRatio := ab.Usage.CacheHitRatio()
			sb.WriteString(fmt.Sprintf("• **%s:** %d turns | %s tokens | ⚡ %.1f%% cached\n", ab.AgentName, ab.TurnCount, formatNumber(ab.Usage.TotalTokens), hitRatio))
		}
		sb.WriteString("\n")
	}

	// 6. Model Breakdown
	if len(report.ModelBreakdown) > 0 {
		sb.WriteString("🤖 **Breakdown by Model:**\n")
		for _, m := range report.ModelBreakdown {
			hitRatio := m.Usage.CacheHitRatio()
			sb.WriteString(fmt.Sprintf("• **%s:** %d turns | %s tokens | ⚡ %.1f%% cached\n", m.DisplayName, m.TurnCount, formatNumber(m.Usage.TotalTokens), hitRatio))
		}
	}

	return sb.String()
}

func (e *Engine) handleCompactCommand(ctx context.Context, sender domain.SenderUser, session *domain.Session, args []string) string {
	if e.HasActiveTurn(session.SessionKey) {
		return "⚠️ A turn is currently executing in this conversation. Please wait for completion or send `/force_unlock` before compacting."
	}

	activeConvID := session.GetActiveConversationID()
	if activeConvID == "" {
		return "⚠️ No active conversation to compact in current scope. Start a conversation with a message first."
	}

	agent, err := e.storage.GetAgent(ctx, session.ActiveAgent)
	if err != nil {
		agent = &domain.Agent{Name: session.ActiveAgent}
	}

	// RBAC Authorization check
	if session.ActiveAgent != "" && sender.ID != "" {
		allowed, role, err := e.CheckAccess(ctx, agent, sender.ID)
		if err != nil || !allowed {
			return fmt.Sprintf("⛔ **Access Denied:** You do not have permission to operate agent **%s**.", session.ActiveAgent)
		}
		if role == "viewer" {
			return fmt.Sprintf("⛔ **Permission Denied:** Users with **viewer** role cannot archive or compact context for agent **%s**.", session.ActiveAgent)
		}
	}

	customNote := strings.Join(args, " ")

	res, err := e.CompactSessionContext(ctx, session, agent, "manual", customNote)
	if err != nil {
		return fmt.Sprintf("⚠️ Failed to compact conversation: %v", err)
	}

	scope := "Global Mode"
	if session.ActiveProject != "" {
		scope = fmt.Sprintf("Project: %s", session.ActiveProject)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🧹 **Conversation Context Compacted Successfully** for [%s • %s]!\n\n", session.ActiveAgent, scope))
	sb.WriteString(fmt.Sprintf("• **Archived Conversation:** `%s`\n", res.OldConversationID))
	if res.OriginalTokens > 0 {
		sb.WriteString(fmt.Sprintf("• **Original Context Size:** %s tokens\n", formatNumber(res.OriginalTokens)))
		sb.WriteString(fmt.Sprintf("• **Continuity Digest Size:** ~%s tokens\n", formatNumber(res.CompactedTokens)))
		sb.WriteString(fmt.Sprintf("• **💰 Token Reduction:** ~%.1f%%\n", res.ReductionPercent))
	} else {
		sb.WriteString(fmt.Sprintf("• **Continuity Digest Size:** ~%s tokens\n", formatNumber(res.CompactedTokens)))
	}
	sb.WriteString("\n🌱 **Fresh Context Ready:** Your next message will seamlessly continue with the structured continuity digest in Level 4.")

	return sb.String()
}

func formatNumber(n int) string {
	in := strconv.Itoa(n)
	out := make([]byte, 0, len(in)+(len(in)-1)/3)
	for i, c := range in {
		if i > 0 && (len(in)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, byte(c))
	}
	return string(out)
}

func (e *Engine) handleContextCommand(ctx context.Context, session *domain.Session) string {
	agentPath := config.ResolveAgentWorkspace(e.cfg.Storage.AgentsDir, session.ActiveAgent)
	wsDir := agentPath
	scopeLabel := "Global Scope"

	if session.ActiveProject != "" {
		projID := domain.FormatProjectID(session.ActiveAgent, session.ActiveProject)
		if proj, err := e.storage.GetProject(ctx, projID); err == nil {
			wsDir = proj.ProjectPath
			scopeLabel = fmt.Sprintf("Project Scope (%s)", proj.ProjectName)
		}
	}

	var resolved *domain.ResolvedContext
	if e.contextResolver != nil {
		resolved, _ = e.contextResolver.Resolve(ctx, agentPath, wsDir)
	}

	directivesLen := 0
	numSkills := 0
	if resolved != nil {
		directivesLen = len(resolved.CombinedDirectives)
		numSkills = len(resolved.SkillHeaders)
	}

	numPlugins := 0
	numActiveMCP := 0
	if e.pluginManager != nil {
		plugins, _ := e.pluginManager.ListPlugins(ctx, agentPath, wsDir)
		for _, p := range plugins {
			if p.Manifest.Enabled {
				numPlugins++
				numActiveMCP += len(p.MCPServers)
			}
		}
	}

	return fmt.Sprintf(`🧠 **agyent Context Status & Token Budget**
• **Active Agent:** %s
• **Active Scope:** %s
• **Working Directory:** %s
• **Directives Size:** %d bytes
• **Active Skills (Progressive Index):** %d skills
• **Active Plugins:** %d plugins
• **Active MCP Servers:** %d servers
• **Pruning Policy:** Head-Tail Sandwich (Max: 2000 chars)
• **Pre-Compaction Flush:** Enabled (Threshold: 75%%)`,
		session.ActiveAgent,
		scopeLabel,
		wsDir,
		directivesLen,
		numSkills,
		numPlugins,
		numActiveMCP,
	)
}

func (e *Engine) handleSkillsCommand(ctx context.Context, session *domain.Session, args []string) string {
	agentPath := config.ResolveAgentWorkspace(e.cfg.Storage.AgentsDir, session.ActiveAgent)
	wsDir := agentPath
	if session.ActiveProject != "" {
		projID := domain.FormatProjectID(session.ActiveAgent, session.ActiveProject)
		if proj, err := e.storage.GetProject(ctx, projID); err == nil {
			wsDir = proj.ProjectPath
		}
	}

	if e.contextResolver == nil {
		return "⚠️ ContextResolver is not configured on this engine."
	}

	skills, err := e.contextResolver.DiscoverSkills(ctx, agentPath, wsDir)
	if err != nil {
		return fmt.Sprintf("⚠️ Failed to discover skills: %v", err)
	}

	if len(skills) == 0 {
		return fmt.Sprintf("📚 No custom skills found for agent **%s**.\nPlace skills in `<workspace>/.agents/skills/<name>/SKILL.md`.", session.ActiveAgent)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📚 **Available Skills for [%s]:**\n\n", session.ActiveAgent))
	for _, s := range skills {
		sb.WriteString(fmt.Sprintf("• **`%s`** _[%s]_\n  _%s_\n", s.Name, s.Scope, s.Description))
	}
	sb.WriteString("\n_Skills are loaded on-demand via Progressive Disclosure._")
	return sb.String()
}

func (e *Engine) handlePluginsCommand(ctx context.Context, session *domain.Session, args []string) string {
	agentPath := config.ResolveAgentWorkspace(e.cfg.Storage.AgentsDir, session.ActiveAgent)
	wsDir := agentPath
	if session.ActiveProject != "" {
		projID := domain.FormatProjectID(session.ActiveAgent, session.ActiveProject)
		if proj, err := e.storage.GetProject(ctx, projID); err == nil {
			wsDir = proj.ProjectPath
		}
	}

	if e.pluginManager == nil {
		return "⚠️ PluginManager is not configured on this engine."
	}

	targetScope := domain.ScopeGlobal
	if session.ActiveProject != "" {
		targetScope = domain.ScopeWorkspace
	}

	if len(args) == 0 || args[0] == "list" {
		plugins, err := e.pluginManager.ListPlugins(ctx, agentPath, wsDir)
		if err != nil {
			return fmt.Sprintf("⚠️ Failed to list plugins: %v", err)
		}

		if len(plugins) == 0 {
			return "🔌 No plugins installed. Use `/plugin install <name>` from built-in repository."
		}

		var sb strings.Builder
		sb.WriteString("🔌 **Installed Plugins:**\n\n")
		for _, p := range plugins {
			status := "⚪ [DISABLED]"
			if p.Manifest.Enabled {
				status = "🟢 [ENABLED]"
			}
			sb.WriteString(fmt.Sprintf("• %s **%s** (v%s) _[%s]_\n  _%s_\n", status, p.Manifest.Name, p.Manifest.Version, p.Scope, p.Manifest.Description))
		}
		sb.WriteString("\nUse `/plugin enable <name>` or `/plugin disable <name>` to toggle.")
		return sb.String()
	}

	subCmd := strings.ToLower(args[0])
	switch subCmd {
	case "enable", "on", "1":
		if len(args) < 2 {
			return "⚠️ Usage: `/plugin enable <plugin_name>`"
		}
		name := args[1]
		err := e.pluginManager.TogglePlugin(ctx, name, true, targetScope, wsDir)
		if err != nil {
			return fmt.Sprintf("⚠️ Failed to enable plugin %q: %v", name, err)
		}
		return fmt.Sprintf("🟢 Plugin **%s** is now **ENABLED**. Capabilities are active for your next turn.", name)

	case "disable", "off", "0":
		if len(args) < 2 {
			return "⚠️ Usage: `/plugin disable <plugin_name>`"
		}
		name := args[1]
		err := e.pluginManager.TogglePlugin(ctx, name, false, targetScope, wsDir)
		if err != nil {
			return fmt.Sprintf("⚠️ Failed to disable plugin %q: %v", name, err)
		}
		return fmt.Sprintf("⚪ Plugin **%s** is now **DISABLED**. Associated tools and skills have been unmounted.", name)

	case "install":
		if len(args) < 2 {
			return "⚠️ Usage: `/plugin install <builtin_plugin_name>`\nAvailable builtins: `browser-camoufox`, `system-diagnostics`, `database-sqlite`"
		}
		name := args[1]
		err := e.pluginManager.InstallBuiltinPlugin(ctx, name, targetScope, wsDir)
		if err != nil {
			return fmt.Sprintf("⚠️ Failed to install plugin %q: %v", name, err)
		}
		return fmt.Sprintf("🎉 Built-in plugin **%s** successfully installed to workspace `.agents/plugins/%s`!", name, name)

	default:
		return "⚠️ Unknown plugin subcommand. Use `/plugins`, `/plugin enable <name>`, `/plugin disable <name>`, or `/plugin install <name>`."
	}
}

func (e *Engine) handleStreamCommand(args []string) string {
	if len(args) == 0 {
		status := "OFF"
		if e.IsStreamingEnabled() {
			status = "ON"
		}
		return fmt.Sprintf("⚡ **Streaming Mode** is currently **%s**.\nUse `/stream on` or `/stream off` to switch modes.", status)
	}

	switch strings.ToLower(args[0]) {
	case "on", "true", "1", "enable":
		e.SetStreamingEnabled(true)
		return "⚡ **Streaming Mode ENABLED** (`--output-format stream-json`).\nResponses will stream live with sub-second typing and progressive token edits."
	case "off", "false", "0", "disable":
		e.SetStreamingEnabled(false)
		return "📦 **Streaming Mode DISABLED** (`--output-format json`).\nResponses will run in classic batch mode with periodic heartbeat typing."
	default:
		return "⚠️ Invalid argument. Use `/stream on` or `/stream off`."
	}
}

func (e *Engine) handleModeCommand(args []string) string {
	if len(args) == 0 {
		currentMode := "fifo"
		if e.cfg != nil && e.cfg.AGY.QueueMode != "" {
			currentMode = strings.ToLower(e.cfg.AGY.QueueMode)
		}
		return fmt.Sprintf("⚙️ **Queue Mode:** `%s`\n\nOptions:\n• `/mode fifo`: Sequential turn queuing (waits for current turn to complete).\n• `/mode append`: Real-time steering (smart aborts active turn at checkpoint and runs new prompt).", currentMode)
	}

	mode := strings.ToLower(args[0])
	if mode != "fifo" && mode != "append" {
		return "⚠️ Invalid mode. Please specify `/mode fifo` or `/mode append`."
	}

	if e.cfg != nil {
		e.cfg.AGY.QueueMode = mode
	}
	return fmt.Sprintf("✅ **Queue mode updated to:** `%s`", mode)
}

func (e *Engine) handleAgentsCommand(ctx context.Context, sender domain.SenderUser, session *domain.Session, args []string) string {
	if len(args) == 0 || args[0] == "list" {
		var agents []domain.Agent
		var err error
		if e.isSenderSuperAdmin(sender) {
			agents, err = e.storage.ListAgents(ctx)
		} else {
			agents, err = e.storage.ListAgentsForUser(ctx, sender.ID)
		}
		if err != nil {
			return fmt.Sprintf("⚠️ Failed to list agents: %v", err)
		}
		if len(agents) == 0 {
			return "No agents available for your user. Use `/a new <name>` to create one."
		}

		var sb strings.Builder
		sb.WriteString("🤖 *Registered Agents:*\n")
		for _, a := range agents {
			marker := "  "
			if a.Name == session.ActiveAgent {
				marker = "👉 "
			}
			statusTag := "initialized"
			if !a.IsInitialized() {
				if e.workspaceManager != nil && e.workspaceManager.HasDirectives(a.WorkspacePath) {
					a.Status = domain.StatusInitialized
					_ = e.storage.SaveAgent(ctx, &a)
				} else {
					statusTag = "uninitialized (bootstrap on next turn)"
				}
			}

			badge := " [Public]"
			if a.OwnerID != "" {
				if a.OwnerID == sender.ID {
					badge = " [Owner]"
				} else {
					_, role, _ := e.storage.CheckAgentAccess(ctx, a.Name, sender.ID)
					if role != "" {
						badge = fmt.Sprintf(" [Shared: %s]", role)
					} else {
						badge = " [Private]"
					}
				}
			}

			sb.WriteString(fmt.Sprintf("%s• *%s*%s — %s _[%s]_\n", marker, a.Name, badge, a.Description, statusTag))
		}
		sb.WriteString("\n💡 Mỗi Agent được gắn với một Bot chuyên trách riêng biệt.")
		return sb.String()
	}

	subCmd := strings.ToLower(args[0])

	switch subCmd {
	case "bootstrap":
		return e.handleBootstrapCommand(ctx, sender, session, args[1:])

	case "new", "create":
		if len(args) < 2 {
			return "⚠️ Usage: `/a new <agent_name> [description]`\nExample: `/a new dev_architect Cloud & Go Systems Architect`"
		}
		if err := e.authorizeAgentAction(ctx, sender, domain.ActionAgentCreate, domain.ResourceKindAgent, "", session); err != nil {
			return "⛔ Access Denied: Only an agent owner, agent admin, or SuperAdmin can create a new agent."
		}
		name := strings.TrimSpace(args[1])
		if name == "" || strings.Contains(name, "..") || strings.Contains(name, "/") || strings.Contains(name, "\\") {
			return "⚠️ Invalid agent name. Agent name cannot contain path separators ('/', '\\') or '..'."
		}
		desc := "AI Assistant"
		if len(args) > 2 {
			desc = strings.Join(args[2:], " ")
		}

		agentPath := config.ResolveAgentWorkspace(e.cfg.Storage.AgentsDir, name)
		agent := &domain.Agent{
			Name:          name,
			Description:   desc,
			Status:        domain.StatusUninitialized,
			WorkspacePath: agentPath,
			OwnerID:       sender.ID,
			IsPublic:      false,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		}

		// Creating a profile must be insert-only. SaveAgent is intentionally an
		// upsert and would let an attacker who guesses an existing name take over
		// its owner/workspace fields.
		if err := e.storage.CreateAgent(ctx, agent); err != nil {
			if errors.Is(err, ports.ErrAlreadyExists) {
				return fmt.Sprintf("⚠️ Agent `%s` already exists. Choose a different name.", name)
			}
			return fmt.Sprintf("⚠️ Failed to register agent: %v", err)
		}
		if err := os.MkdirAll(agentPath, 0755); err != nil {
			// The profile has no usable workspace without this directory; roll back
			// this newly-created row instead of leaving a half-created agent behind.
			if rollbackErr := e.storage.DeleteAgent(ctx, name); rollbackErr != nil {
				return fmt.Sprintf("⚠️ Failed to create agent directory: %v (rollback also failed: %v)", err, rollbackErr)
			}
			return fmt.Sprintf("⚠️ Failed to create agent directory: %v", err)
		}

		session.ActiveAgent = name
		session.ActiveProject = ""
		_ = e.storage.SaveSession(ctx, session)

		return fmt.Sprintf("🎉 **Agent `%s` created** (Owner: `%s`) and set as active!\nWorkspace: `%s`\nGenesis bootstrap protocol will activate on your first message.", name, sender.ID, agentPath)

	case "preset", "set-preset":
		if len(args) < 3 {
			return "⚠️ Usage: `/a preset <agent_name> <unrestricted|developer|balanced|strict|read_only>`\nExample: `/a preset content_weaver unrestricted`"
		}
		targetAgentName := strings.TrimSpace(args[1])
		presetMode := domain.SecurityPreset(strings.ToLower(strings.TrimSpace(args[2])))
		switch presetMode {
		case domain.PresetUnrestricted, domain.PresetDeveloper, domain.PresetBalanced, domain.PresetStrict, domain.PresetReadOnly:
			// valid preset name
		default:
			return fmt.Sprintf("⚠️ Invalid preset `%s`. Options: `unrestricted`, `developer`, `balanced`, `strict`, `read_only`", args[2])
		}

		targetAgent, err := e.storage.GetAgent(ctx, targetAgentName)
		if err != nil || targetAgent == nil {
			return fmt.Sprintf("⚠️ Agent `%s` not found.", targetAgentName)
		}

		isOwner := targetAgent.OwnerID != "" && targetAgent.OwnerID == sender.ID
		if !isOwner && !e.isSenderSuperAdmin(sender) {
			return fmt.Sprintf("⛔ Access Denied: Only the owner of agent `@%s` or a SuperAdmin can modify its security preset.", targetAgentName)
		}

		oldPreset := targetAgent.SecurityPreset
		if oldPreset == "" {
			oldPreset = domain.PresetBalanced
		}

		// Enforce monotonic upgrade rule via chat: cannot downgrade security level in chat
		if !domain.CanSwitchPreset(oldPreset, presetMode) {
			allowedPresets := domain.GetAllowedPresets(oldPreset)
			var allowedStrs []string
			for _, p := range allowedPresets {
				allowedStrs = append(allowedStrs, fmt.Sprintf("`%s`", p))
			}
			return fmt.Sprintf("⛔ **Cannot downgrade security preset via chat:** Agent `%s` current baseline security level is `%s`. You can only switch to equal or more secure presets (allowed: %s).\n💡 To downgrade presets, run `agyent agent preset %s %s` directly on host CLI.",
				targetAgentName, oldPreset, strings.Join(allowedStrs, ", "), targetAgentName, presetMode)
		}

		targetAgent.SecurityPreset = presetMode
		targetAgent.UpdatedAt = time.Now()
		if err := e.storage.SaveAgent(ctx, targetAgent); err != nil {
			return fmt.Sprintf("⚠️ Failed to update security preset: %v", err)
		}
		if e.securityManager != nil && session.ActiveAgent == targetAgentName {
			e.securityManager.SetPreset(presetMode)
		}
		return fmt.Sprintf("🛡️ **Security preset for agent `%s` updated:** `%s` ➔ `%s` (Level %d)",
			targetAgentName, oldPreset, presetMode, domain.PresetLevel(presetMode))

	case "claim":
		if len(args) < 2 {
			return "⚠️ Usage: `/a claim <agent_name>`\nExample: `/a claim agyent`"
		}
		agentName := strings.TrimSpace(args[1])
		if !e.isSenderSuperAdmin(sender) {
			return "⛔ Access Denied: Only SuperAdmin can claim unowned agents."
		}
		audit := domain.AuditLog{
			SessionKey:   session.SessionKey,
			AgentName:    agentName,
			Status:       "SUCCESS",
			ErrorMessage: fmt.Sprintf("SuperAdmin %s claimed ownership of agent %s", sender.ID, agentName),
			CreatedAt:    time.Now(),
		}
		claimed, err := e.storage.ClaimAgentWithAudit(ctx, agentName, sender.ID, audit)
		if err != nil {
			return fmt.Sprintf("⚠️ Failed to claim agent: %v", err)
		}
		if !claimed {
			return fmt.Sprintf("⚠️ Agent `%s` cannot be claimed (it may already have an owner or does not exist).", agentName)
		}
		var senderUID int64
		_, _ = fmt.Sscanf(sender.ID, "%d", &senderUID)
		_ = e.storage.LogSecurityEvent(ctx, &domain.AuditSecurityEvent{
			EventID:        fmt.Sprintf("sec-claim-%d", time.Now().UnixNano()),
			Timestamp:      time.Now(),
			SessionKey:     session.SessionKey,
			UserID:         senderUID,
			AgentName:      agentName,
			Checkpoint:     "RBACClaim",
			ToolName:       "claim",
			TargetResource: agentName,
			Decision:       domain.DecisionAllow,
			Reason:         fmt.Sprintf("SuperAdmin %s claimed ownership of agent %s", sender.ID, agentName),
		})
		return fmt.Sprintf("✅ Successfully claimed agent `@%s`! You are now the official owner (User ID: `%s`).", agentName, sender.ID)

	case "share":
		if len(args) < 3 {
			return "⚠️ Usage: `/a share <agent_name> <user_id> [role]`\nExample: `/a share dev_architect 123456789 operator`\nAllowed roles: `admin`, `operator`, `viewer`"
		}
		agentName := strings.TrimSpace(args[1])
		targetUserID := strings.TrimSpace(args[2])
		role := "operator"
		if len(args) > 3 {
			role = strings.ToLower(strings.TrimSpace(args[3]))
		}
		if role != "admin" && role != "operator" && role != "viewer" {
			return "⚠️ Invalid role. Allowed roles: `admin`, `operator`, `viewer`."
		}

		agent, err := e.storage.GetAgent(ctx, agentName)
		if err != nil {
			return fmt.Sprintf("⚠️ Agent `%s` not found.", agentName)
		}

		if agent.OwnerID == "" {
			return fmt.Sprintf("⛔ **Access Denied:** Unowned agent `@%s` cannot be shared until officially claimed via `/a claim` by a SuperAdmin.", agentName)
		}
		if agent.OwnerID != sender.ID && !e.isSenderSuperAdmin(sender) {
			return fmt.Sprintf("⛔ **Access Denied:** Only the owner of agent `@%s` or a superadmin can share access.", agentName)
		}
		if role == "admin" && !e.isSenderSuperAdmin(sender) && agent.OwnerID != sender.ID {
			return "⛔ **Access Denied:** Only the owner or a SuperAdmin can grant the `admin` role."
		}

		perm := &domain.AgentPermission{
			AgentName: agentName,
			UserID:    targetUserID,
			Role:      role,
			GrantedBy: sender.ID,
			GrantedAt: time.Now(),
		}
		if err := e.storage.ShareAgent(ctx, perm); err != nil {
			return fmt.Sprintf("⚠️ Failed to share agent: %v", err)
		}

		return fmt.Sprintf("✅ **Access granted!** User `%s` now has `%s` access to agent `@%s`.", targetUserID, role, agentName)

	case "revoke":
		if len(args) < 3 {
			return "⚠️ Usage: `/a revoke <agent_name> <user_id>`\nExample: `/a revoke dev_architect 123456789`"
		}
		agentName := strings.TrimSpace(args[1])
		targetUserID := strings.TrimSpace(args[2])

		agent, err := e.storage.GetAgent(ctx, agentName)
		if err != nil {
			return fmt.Sprintf("⚠️ Agent `%s` not found.", agentName)
		}

		if agent.OwnerID == "" {
			return fmt.Sprintf("⛔ **Access Denied:** Unowned agent `@%s` cannot be modified until officially claimed via `/a claim` by a SuperAdmin.", agentName)
		}
		if agent.OwnerID != sender.ID && !e.isSenderSuperAdmin(sender) {
			return fmt.Sprintf("⛔ **Access Denied:** Only the owner of agent `@%s` or a superadmin can revoke access.", agentName)
		}

		if err := e.storage.RevokeAgentAccess(ctx, agentName, targetUserID); err != nil {
			return fmt.Sprintf("⚠️ Failed to revoke access: %v", err)
		}

		return fmt.Sprintf("🚫 **Access revoked!** User `%s` no longer has access to agent `@%s`.", targetUserID, agentName)

	case "info":
		agentName := session.ActiveAgent
		if len(args) > 1 && strings.TrimSpace(args[1]) != "" {
			agentName = strings.TrimSpace(args[1])
		}

		agent, err := e.storage.GetAgent(ctx, agentName)
		if err != nil {
			return fmt.Sprintf("⚠️ Agent `%s` not found.", agentName)
		}

		if err := e.authorizeTargetAgentAction(ctx, sender, domain.ActionAgentInspect, agentName); err != nil {
			return fmt.Sprintf("⛔ **Access Denied:** You do not have permission to view info for agent `@%s`.", agentName)
		}
		_, role, _ := e.CheckAccess(ctx, agent, sender.ID)

		perms, _ := e.storage.ListAgentPermissions(ctx, agentName)

		visibility := "🔒 Private"
		if agent.IsPublic {
			visibility = "🌐 Public"
		}
		ownerDisplay := agent.OwnerID
		if ownerDisplay == "" {
			ownerDisplay = "(system / admin)"
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("🤖 **Agent Information: `@%s`**\n", agent.Name))
		sb.WriteString(fmt.Sprintf("• **Description:** %s\n", agent.Description))
		sb.WriteString(fmt.Sprintf("• **Status:** `%s`\n", agent.Status))
		sb.WriteString(fmt.Sprintf("• **Visibility:** %s\n", visibility))
		sb.WriteString(fmt.Sprintf("• **Owner ID:** `%s`\n", ownerDisplay))
		sb.WriteString(fmt.Sprintf("• **Your Effective Role:** `%s`\n", role))
		sb.WriteString(fmt.Sprintf("• **Workspace Path:** `%s`\n", agent.WorkspacePath))
		if agent.DefaultModel != "" {
			sb.WriteString(fmt.Sprintf("• **Default Model:** `%s`\n", agent.DefaultModel))
		}
		if agent.DefaultEffort != "" {
			sb.WriteString(fmt.Sprintf("• **Default Effort:** `%s`\n", agent.DefaultEffort))
		}

		sb.WriteString(fmt.Sprintf("\n👥 **Collaborators (%d):**\n", len(perms)))
		if len(perms) == 0 {
			sb.WriteString("  _No external collaborators shared._\n")
		} else {
			for _, p := range perms {
				sb.WriteString(fmt.Sprintf("  • User `%s` — Role: `%s` (Granted by: `%s`)\n", p.UserID, p.Role, p.GrantedBy))
			}
		}

		return sb.String()

	default:
		return fmt.Sprintf("⚠️ Unknown agent subcommand `%s`. Use `/agents` to list, or `/agents new <name>` to create.", args[0])
	}
}

func (e *Engine) handleBootstrapCommand(ctx context.Context, sender domain.SenderUser, session *domain.Session, args []string) string {
	agentName := session.ActiveAgent
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		agentName = strings.TrimSpace(args[0])
	}

	agent, err := e.storage.GetAgent(ctx, agentName)
	if err != nil {
		return fmt.Sprintf("⚠️ Agent `%s` not found.", agentName)
	}

	if agent.OwnerID == "" && !e.isSenderSuperAdmin(sender) {
		return "⛔ **Access Denied:** An unowned agent can only be bootstrapped by a SuperAdmin after it is claimed."
	}
	if agent.OwnerID != "" && agent.OwnerID != sender.ID && !e.isSenderSuperAdmin(sender) {
		return fmt.Sprintf("⛔ **Access Denied:** Only the agent owner (User ID: `%s`) or an administrator can re-trigger Genesis Bootstrap.", agent.OwnerID)
	}

	agent.Status = domain.StatusUninitialized
	agent.UpdatedAt = time.Now()
	if err := e.storage.SaveAgent(ctx, agent); err != nil {
		return fmt.Sprintf("⚠️ Failed to update agent status: %v", err)
	}

	session.ActiveAgent = agentName
	session.ResetActiveConversationID()
	_ = e.storage.SaveSession(ctx, session)

	return fmt.Sprintf("🔄 **Agent `%s` reset for Genesis Bootstrap.**\nWorkspace: `%s`\nYour next message will initiate the bootstrap protocol and generate identity files (`AGENTS.md`, `IDENTITY.md`, `SOUL.md`, `USER.md`, `MEMORY.md`).", agent.Name, agent.WorkspacePath)
}

func (e *Engine) handleProjectsCommand(ctx context.Context, sender domain.SenderUser, session *domain.Session, args []string) string {
	if len(args) == 0 || args[0] == "list" {
		projects, err := e.storage.ListProjects(ctx, session.ActiveAgent)
		if err != nil {
			return fmt.Sprintf("⚠️ Failed to list projects: %v", err)
		}
		if len(projects) == 0 {
			return fmt.Sprintf("📁 No projects registered for agent **%s**.\nUse `/p new <name> [path]` to register one.", session.ActiveAgent)
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("📁 *Projects for Agent [%s]:*\n", session.ActiveAgent))
		for _, p := range projects {
			marker := "  "
			if p.ProjectName == session.ActiveProject {
				marker = "👉 "
			}
			sb.WriteString(fmt.Sprintf("%s• *%s* — `%s`\n", marker, p.ProjectName, p.ProjectPath))
		}
		sb.WriteString("\nUse `/p <name>` to enter project mode or `/p exit` to return to Global mode.")
		return sb.String()
	}

	subCmd := strings.ToLower(args[0])

	switch subCmd {
	case "exit", "~":
		if e.HasActiveTurn(session.SessionKey) {
			return "⚠️ A turn is currently executing in this conversation. Please wait for completion or send `/force_unlock` before exiting project."
		}
		session.ActiveProject = ""
		if err := e.storage.SaveSession(ctx, session); err != nil {
			return fmt.Sprintf("⚠️ Failed to exit project: %v", err)
		}
		return fmt.Sprintf("🌐 Exited project mode. Returned to **Global Chat Mode** for agent **%s**.", session.ActiveAgent)

	case "info":
		if session.ActiveProject == "" {
			return "🌐 You are currently in **Global Chat Mode** (no active project)."
		}
		projID := domain.FormatProjectID(session.ActiveAgent, session.ActiveProject)
		proj, err := e.storage.GetProject(ctx, projID)
		if err != nil {
			return fmt.Sprintf("⚠️ Failed to get project info: %v", err)
		}
		return fmt.Sprintf(`📁 *Active Project Details:*
• *Agent:* %s
• *Project Name:* %s
• *Filesystem Path:* %s
• *Conversation ID:* %s`,
			proj.AgentName, proj.ProjectName, proj.ProjectPath, session.ProjectConversationID)

	case "reset":
		if e.HasActiveTurn(session.SessionKey) {
			return "⚠️ A turn is currently executing in this conversation. Please wait for completion or send `/force_unlock` before resetting project."
		}
		if session.ActiveProject == "" {
			return "⚠️ No active project to reset. Use `/reset` for global mode."
		}
		session.ProjectConversationID = ""
		_ = e.storage.SaveSession(ctx, session)
		return fmt.Sprintf("🔄 **Project conversation reset** for project **%s**.\nSource code remains untouched.", session.ActiveProject)

	case "new", "create":
		if e.HasActiveTurn(session.SessionKey) {
			return "⚠️ A turn is currently executing in this conversation. Please wait for completion or send `/force_unlock` before creating project."
		}
		if len(args) < 2 {
			return "⚠️ Usage: `/p new <project_name> [path]`\nExample: `/p new ecommerce /home/ubuntu/projects/ecommerce`"
		}

		// RBAC: Verify sender has edit permissions on the active agent
		agent, err := e.storage.GetAgent(ctx, session.ActiveAgent)
		if err != nil {
			return fmt.Sprintf("⚠️ Active agent `%s` not found: %v", session.ActiveAgent, err)
		}
		allowed, role, err := e.CheckAccess(ctx, agent, sender.ID)
		if err != nil || !allowed || (role == "viewer" || role == "public") {
			return fmt.Sprintf("⛔ Access Denied: You do not have permission to create projects for agent `@%s`.", session.ActiveAgent)
		}

		projName := strings.TrimSpace(args[1])
		if projName == "" || strings.Contains(projName, "..") || strings.Contains(projName, "/") || strings.Contains(projName, "\\") {
			return "⚠️ Invalid project name. Project name cannot contain path separators ('/', '\\') or '..'."
		}

		agentPath := config.ResolveAgentWorkspace(e.cfg.Storage.AgentsDir, session.ActiveAgent)
		if agent.WorkspacePath != "" {
			agentPath = agent.WorkspacePath
		}
		defaultProjectsDir := filepath.Join(agentPath, "projects")

		var projPath string
		if len(args) > 2 {
			rawPath := strings.TrimSpace(args[2])
			var allowedRoots []string
			if e.cfg != nil {
				allowedRoots = e.cfg.Security.AllowedProjectRoots
			}
			validatedPath, valErr := validateProjectPath(rawPath, defaultProjectsDir, allowedRoots)
			if valErr != nil {
				return fmt.Sprintf("⛔ Access Denied: Path `%s` is outside allowed project directories.\nReason: %v\nTo allow external directories, add the path to `security.allowed_project_roots` in config.yaml.", rawPath, valErr)
			}
			projPath = validatedPath
		} else {
			projPath = filepath.Join(defaultProjectsDir, projName)
		}

		if err := os.MkdirAll(projPath, 0700); err != nil {
			return fmt.Sprintf("⚠️ Failed to create project directory: %v", err)
		}

		projID := domain.FormatProjectID(session.ActiveAgent, projName)
		proj := &domain.Project{
			ID:          projID,
			AgentName:   session.ActiveAgent,
			ProjectName: projName,
			ProjectPath: projPath,
			CreatedAt:   time.Now(),
		}

		if err := e.storage.SaveProject(ctx, proj); err != nil {
			return fmt.Sprintf("⚠️ Failed to save project: %v", err)
		}

		session.ActiveProject = projName
		session.ProjectConversationID = ""
		_ = e.storage.SaveSession(ctx, session)

		return fmt.Sprintf("🎉 **Project `%s` registered and activated!**\nPath: `%s`\nContext scoped to this codebase.", projName, projPath)

	case "use":
		if len(args) < 2 {
			return "⚠️ Usage: `/p use <project_name>`"
		}
		return e.switchProject(ctx, session, args[1])

	default:
		return e.switchProject(ctx, session, args[0])
	}
}

func (e *Engine) switchProject(ctx context.Context, session *domain.Session, projectName string) string {
	if e.HasActiveTurn(session.SessionKey) {
		return "⚠️ A turn is currently executing in this conversation. Please wait for completion or send `/force_unlock` before switching project."
	}

	projID := domain.FormatProjectID(session.ActiveAgent, projectName)
	proj, err := e.storage.GetProject(ctx, projID)
	if err != nil {
		return fmt.Sprintf("⚠️ Project `%s` not found for agent **%s**.\nUse `/projects` to list or `/p new %s` to create it.", projectName, session.ActiveAgent, projectName)
	}

	session.ActiveProject = proj.ProjectName

	// Restore latest conversation for the target project
	latest, _, err := e.storage.ListRecentConversations(ctx, session.SessionKey, session.ActiveAgent, proj.ProjectName, 1, 0)
	if err == nil && len(latest) > 0 {
		session.ProjectConversationID = latest[0].ID
	} else {
		session.ProjectConversationID = ""
	}

	if err := e.storage.SaveSession(ctx, session); err != nil {
		return fmt.Sprintf("⚠️ Failed to switch project: %v", err)
	}

	return fmt.Sprintf("📁 Switched into project **%s**\nPath: `%s`\nCodebase context loaded.", proj.ProjectName, proj.ProjectPath)
}

func evalSymlinksSafe(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	if realPath, err := filepath.EvalSymlinks(abs); err == nil {
		return realPath
	}
	parent := filepath.Dir(abs)
	if realParent, err := filepath.EvalSymlinks(parent); err == nil {
		return filepath.Join(realParent, filepath.Base(abs))
	}
	return abs
}

func validateProjectPath(requestedPath, defaultProjectsDir string, allowedRoots []string) (string, error) {
	cleanPath := filepath.Clean(requestedPath)
	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		return "", fmt.Errorf("invalid path: %w", err)
	}

	targetPath := evalSymlinksSafe(absPath)

	// Check if within agent projects directory
	cleanAgentProjectsDir := evalSymlinksSafe(defaultProjectsDir)
	relToAgent, err := filepath.Rel(cleanAgentProjectsDir, targetPath)
	if err == nil && !strings.HasPrefix(relToAgent, "..") && !strings.HasPrefix(relToAgent, "/") && relToAgent != "." {
		return targetPath, nil
	}

	// Check against allowed project roots
	for _, root := range allowedRoots {
		if strings.TrimSpace(root) == "" {
			continue
		}
		cleanRoot := evalSymlinksSafe(root)
		rel, err := filepath.Rel(cleanRoot, targetPath)
		if err == nil && !strings.HasPrefix(rel, "..") && !strings.HasPrefix(rel, "/") {
			return targetPath, nil
		}
	}

	return "", fmt.Errorf("path '%s' is not within agent projects directory or allowed_project_roots", requestedPath)
}

func (e *Engine) handleConversationsDispatcher(ctx context.Context, session *domain.Session, args []string) (string, domain.InlineKeyboard) {
	if len(args) > 0 {
		subCmd := strings.ToLower(args[0])
		switch subCmd {
		case "new", "create":
			return e.handleNewConversationCommand(ctx, session, args[1:]), nil
		case "switch", "use":
			return e.handleSwitchConversationCommand(ctx, session, args[1:]), nil
		case "pin":
			return e.handlePinCommand(ctx, session, args[1:], true), nil
		case "unpin":
			return e.handlePinCommand(ctx, session, args[1:], false), nil
		case "rename", "title":
			return e.handleRenameConversationCommand(ctx, session, args[1:]), nil
		case "archive", "close":
			return e.handleArchiveConversationCommand(ctx, session, args[1:]), nil
		case "clean", "purge":
			return e.handleCleanConversationsCommand(ctx), nil
		default:
			// Check if integer index, e.g. "/c 2"
			if idx, err := strconv.Atoi(subCmd); err == nil && idx > 0 {
				return e.handleSwitchConversationCommand(ctx, session, []string{subCmd}), nil
			}
			// Check if UUID
			if len(subCmd) == 36 && uuidRegex.MatchString(subCmd) {
				return e.handleSwitchConversationCommand(ctx, session, []string{subCmd}), nil
			}
		}
	}

	return e.renderConversationsList(ctx, session, 0)
}

func (e *Engine) renderConversationsList(ctx context.Context, session *domain.Session, page int) (string, domain.InlineKeyboard) {
	if page < 0 {
		page = 0
	}
	pageSize := 5
	offset := page * pageSize

	convs, totalCount, err := e.storage.ListRecentConversations(ctx, session.SessionKey, session.ActiveAgent, session.ActiveProject, pageSize, offset)
	if err != nil {
		return fmt.Sprintf("⚠️ Failed to retrieve conversations list: %v", err), nil
	}

	scopeLabel := "Global Mode"
	if session.ActiveProject != "" {
		scopeLabel = fmt.Sprintf("Project: %s", session.ActiveProject)
	}

	activeConvID := session.GetActiveConversationID()

	var sb strings.Builder
	totalPages := (totalCount + pageSize - 1) / pageSize
	if totalPages == 0 {
		totalPages = 1
	}

	sb.WriteString(fmt.Sprintf("🧵 **Conversations List** `[%s • %s]` _(Page %d/%d)_\n\n", session.ActiveAgent, scopeLabel, page+1, totalPages))

	if len(convs) == 0 {
		sb.WriteString("No conversations found in this scope.\nSend a message or use `/new` to start a fresh conversation.\n")
	}

	var keyboard domain.InlineKeyboard

	for _, c := range convs {
		prefix := "⚪ "
		activeTag := ""
		if c.ID == activeConvID {
			prefix = "👉 "
			activeTag = " 🟢 [Active]"
		}

		pinIcon := ""
		if c.IsPinned {
			pinIcon = " 📌"
		}

		timeAgo := formatTimeAgo(c.UpdatedAt)
		sb.WriteString(fmt.Sprintf("%s**#%d:**%s %s%s\n   _🕒 %s • %d turns_\n", prefix, c.AliasIndex, pinIcon, c.Title, activeTag, timeAgo, c.TurnCount))

		// Row buttons
		var row domain.InlineKeyboardRow
		if c.ID == activeConvID {
			row = append(row, domain.InlineButton{
				Text:         fmt.Sprintf("👉 #%d (Active)", c.AliasIndex),
				CallbackData: fmt.Sprintf("c:sw:%s", c.ID),
			})
		} else {
			row = append(row, domain.InlineButton{
				Text:         fmt.Sprintf("🔄 Switch #%d", c.AliasIndex),
				CallbackData: fmt.Sprintf("c:sw:%s", c.ID),
			})
		}

		if c.IsPinned {
			row = append(row, domain.InlineButton{
				Text:         "❌ Unpin",
				CallbackData: fmt.Sprintf("c:unpin:%s", c.ID),
			})
		} else {
			row = append(row, domain.InlineButton{
				Text:         "📌 Pin",
				CallbackData: fmt.Sprintf("c:pin:%s", c.ID),
			})
		}

		keyboard = append(keyboard, row)
	}

	// Action row
	actionRow := domain.InlineKeyboardRow{
		domain.InlineButton{
			Text:         "➕ New Conversation",
			CallbackData: "c:new",
		},
		domain.InlineButton{
			Text:         "🔄 Refresh",
			CallbackData: fmt.Sprintf("c:page:%d", page+1),
		},
	}
	keyboard = append(keyboard, actionRow)

	// Pagination row if multiple pages
	if totalPages > 1 {
		var navRow domain.InlineKeyboardRow
		if page > 0 {
			navRow = append(navRow, domain.InlineButton{
				Text:         "⬅️ Prev Page",
				CallbackData: fmt.Sprintf("c:page:%d", page),
			})
		}
		if page+1 < totalPages {
			navRow = append(navRow, domain.InlineButton{
				Text:         "Next Page ➡️",
				CallbackData: fmt.Sprintf("c:page:%d", page+2),
			})
		}
		if len(navRow) > 0 {
			keyboard = append(keyboard, navRow)
		}
	}

	sb.WriteString("\n_Use `/c <#>` to fast switch, `/pin` to pin, or click the buttons below._")
	return sb.String(), keyboard
}

func (e *Engine) handleNewConversationCommand(ctx context.Context, session *domain.Session, args []string) string {
	if e.HasActiveTurn(session.SessionKey) {
		return "⚠️ A turn is currently executing in this conversation. Please wait for completion or send `/force_unlock` before creating a new conversation."
	}

	oldConvID := session.GetActiveConversationID()
	if oldConvID != "" && e.evolution != nil {
		bgCtx := context.WithoutCancel(ctx)
		concurrency.SafeGo(func() {
			_ = e.evolution.TriggerConversationEvolution(bgCtx, oldConvID, domain.TriggerExplicitSwitch)
		})
	}

	session.ResetActiveConversationID()
	if err := e.storage.SaveSession(ctx, session); err != nil {
		return fmt.Sprintf("⚠️ Failed to initialize new conversation: %v", err)
	}

	scope := "Global Mode"
	if session.ActiveProject != "" {
		scope = fmt.Sprintf("Project: %s", session.ActiveProject)
	}

	titleNote := ""
	if len(args) > 0 {
		titleNote = fmt.Sprintf("\nIntended Topic: _%s_", strings.Join(args, " "))
	}

	return fmt.Sprintf("🎉 **New conversation created** for [%s • %s].%s\nYour next message will begin in a fresh, clean context.", session.ActiveAgent, scope, titleNote)
}

func (e *Engine) handleSwitchConversationCommand(ctx context.Context, session *domain.Session, args []string) string {
	if e.HasActiveTurn(session.SessionKey) {
		return "⚠️ A turn is currently executing in this conversation. Please wait for completion or send `/force_unlock` before switching conversation."
	}

	if len(args) == 0 {
		return "⚠️ Usage: `/c <#>` or `/c switch <id|#>`\nExample: `/c 2`"
	}

	target := strings.TrimSpace(args[0])
	var targetConv *domain.Conversation

	scope := domain.ConversationScope{
		SessionKey:  session.SessionKey,
		AgentName:   session.ActiveAgent,
		ProjectName: session.ActiveProject,
	}

	if idx, err := strconv.Atoi(target); err == nil && idx > 0 {
		conv, err := e.storage.GetConversationByAlias(ctx, scope.SessionKey, scope.AgentName, scope.ProjectName, idx)
		if err != nil {
			return fmt.Sprintf("⚠️ Conversation #%d not found in active scope.", idx)
		}
		targetConv = conv
	} else {
		conv, err := e.storage.GetConversationScoped(ctx, scope, target)
		if err != nil {
			return fmt.Sprintf("⚠️ Conversation `%s` not found in active scope.", target)
		}
		targetConv = conv
	}

	oldConvID := session.GetActiveConversationID()
	if oldConvID != "" && oldConvID != targetConv.ID && e.evolution != nil {
		bgCtx := context.WithoutCancel(ctx)
		concurrency.SafeGo(func() {
			_ = e.evolution.TriggerConversationEvolution(bgCtx, oldConvID, domain.TriggerExplicitSwitch)
		})
	}

	session.SetActiveConversationID(targetConv.ID)
	if err := e.storage.SaveSession(ctx, session); err != nil {
		return fmt.Sprintf("⚠️ Failed to switch conversation: %v", err)
	}

	pinTag := ""
	if targetConv.IsPinned {
		pinTag = " 📌"
	}

	shortID := targetConv.ID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}

	aliasPrefix := ""
	if targetConv.AliasIndex > 0 {
		aliasPrefix = fmt.Sprintf("**#%d:** ", targetConv.AliasIndex)
	}

	return fmt.Sprintf("🔄 Switched to conversation %s%s**%s** `[%s]`.\n_Historical context loaded and ready._", aliasPrefix, pinTag, targetConv.Title, shortID)
}

func (e *Engine) handlePinCommand(ctx context.Context, session *domain.Session, args []string, isPinned bool) string {
	var targetID string
	var title string

	scope := domain.ConversationScope{
		SessionKey:  session.SessionKey,
		AgentName:   session.ActiveAgent,
		ProjectName: session.ActiveProject,
	}

	if len(args) > 0 {
		target := strings.TrimSpace(args[0])
		if idx, err := strconv.Atoi(target); err == nil && idx > 0 {
			conv, err := e.storage.GetConversationByAlias(ctx, scope.SessionKey, scope.AgentName, scope.ProjectName, idx)
			if err != nil {
				return fmt.Sprintf("⚠️ Conversation #%d not found in active scope.", idx)
			}
			targetID = conv.ID
			title = conv.Title
		} else {
			conv, err := e.storage.GetConversationScoped(ctx, scope, target)
			if err != nil {
				return fmt.Sprintf("⚠️ Conversation `%s` not found in active scope.", target)
			}
			targetID = conv.ID
			title = conv.Title
		}
	} else {
		targetID = session.GetActiveConversationID()
		if targetID == "" {
			return "⚠️ No active conversation to pin/unpin. Send a message first."
		}
		if conv, err := e.storage.GetConversationScoped(ctx, scope, targetID); err == nil {
			title = conv.Title
		}
	}

	if err := e.storage.SetConversationPinnedScoped(ctx, scope, targetID, isPinned); err != nil {
		return fmt.Sprintf("⚠️ Failed to update pin status: %v", err)
	}

	if title == "" {
		title = "current conversation"
	} else {
		title = fmt.Sprintf("%q", title)
	}

	if isPinned {
		return fmt.Sprintf("📌 **Pinned** conversation %s. This session is permanently protected from automated garbage collection.", title)
	}
	return fmt.Sprintf("📍 **Unpinned** conversation %s.", title)
}

func (e *Engine) handleRenameConversationCommand(ctx context.Context, session *domain.Session, args []string) string {
	if len(args) == 0 {
		return "⚠️ Usage: `/c rename <new_title>`\nExample: `/c rename SQLite Database Design`"
	}

	newTitle := strings.TrimSpace(strings.Join(args, " "))
	targetID := session.GetActiveConversationID()
	if targetID == "" {
		return "⚠️ No active conversation to rename."
	}

	scope := domain.ConversationScope{
		SessionKey:  session.SessionKey,
		AgentName:   session.ActiveAgent,
		ProjectName: session.ActiveProject,
	}

	if err := e.storage.SetConversationTitleScoped(ctx, scope, targetID, newTitle); err != nil {
		return fmt.Sprintf("⚠️ Failed to rename conversation: %v", err)
	}

	return fmt.Sprintf("✏️ Renamed conversation to: **%s**.", newTitle)
}

func (e *Engine) handleArchiveConversationCommand(ctx context.Context, session *domain.Session, args []string) string {
	scope := domain.ConversationScope{
		SessionKey:  session.SessionKey,
		AgentName:   session.ActiveAgent,
		ProjectName: session.ActiveProject,
	}

	targetID := session.GetActiveConversationID()
	if len(args) > 0 {
		target := strings.TrimSpace(args[0])
		if idx, err := strconv.Atoi(target); err == nil && idx > 0 {
			if conv, err := e.storage.GetConversationByAlias(ctx, scope.SessionKey, scope.AgentName, scope.ProjectName, idx); err == nil {
				targetID = conv.ID
			} else {
				return fmt.Sprintf("⚠️ Conversation #%d not found in active scope.", idx)
			}
		} else {
			if conv, err := e.storage.GetConversationScoped(ctx, scope, target); err == nil {
				targetID = conv.ID
			} else {
				return fmt.Sprintf("⚠️ Conversation `%s` not found in active scope.", target)
			}
		}
	}

	if targetID == "" {
		return "⚠️ No active conversation to archive."
	}

	if err := e.storage.SetConversationArchivedScoped(ctx, scope, targetID, true); err != nil {
		return fmt.Sprintf("⚠️ Failed to archive conversation: %v", err)
	}

	if targetID == session.GetActiveConversationID() {
		session.ResetActiveConversationID()
		_ = e.storage.SaveSession(ctx, session)
	}

	return "📦 Conversation **archived** (hidden from main list).\nNext message will create a fresh conversation."
}

func (e *Engine) handleCleanConversationsCommand(ctx context.Context) string {
	purged, err := e.RunConversationGC(ctx, 30)
	if err != nil {
		return fmt.Sprintf("⚠️ Garbage collection error: %v", err)
	}
	return fmt.Sprintf("🧹 **Garbage collection complete!** Purged %d old archived sessions (> 30 days) and reclaimed disk space.", purged)
}

func formatModelButtonLabel(cap domain.ModelCapability) string {
	name := cap.DisplayName
	if name == "" {
		name = cap.ID
	}
	if idx := strings.Index(name, "("); idx > 0 {
		name = strings.TrimSpace(name[:idx])
	}
	idLower := strings.ToLower(cap.ID)
	emoji := "🔹"
	switch {
	case strings.Contains(idLower, "flash"):
		emoji = "⚡"
	case strings.Contains(idLower, "pro"):
		emoji = "🚀"
	case strings.Contains(idLower, "opus"):
		emoji = "🧠"
	case strings.Contains(idLower, "sonnet"):
		emoji = " "
	case strings.Contains(idLower, "gpt") || strings.Contains(idLower, "oss"):
		emoji = "🤖"
	}
	return emoji + " " + name
}

func (e *Engine) handleModelCommand(ctx context.Context, session *domain.Session, args []string) (string, domain.InlineKeyboard) {
	var agentObj *domain.Agent
	if session.ActiveAgent != "" {
		agentObj, _ = e.storage.GetAgent(ctx, session.ActiveAgent)
	}

	if len(args) == 0 {
		resolvedModel, resolvedEffort, source := e.ResolveExecutionParams("", "", session, agentObj)
		if resolvedModel == "" {
			resolvedModel = "default (agy CLI)"
		}

		availableModels := domain.ListAvailableModels()

		var sb strings.Builder
		sb.WriteString("⚡ **AI Model Selection**\n")
		sb.WriteString(fmt.Sprintf("• **Active Model:** `%s` (%s)\n", resolvedModel, source))
		if resolvedEffort != "" {
			sb.WriteString(fmt.Sprintf("• **Reasoning Effort:** `%s`\n", resolvedEffort))
		}
		sb.WriteString("\n**Available Model Tiers:**\n")
		for _, cap := range availableModels {
			effDesc := "No thinking"
			if len(cap.SupportedEfforts) > 0 {
				effDesc = strings.Join(cap.SupportedEfforts, ", ")
			}
			sb.WriteString(fmt.Sprintf("• **%s** (`%s`) — Effort: [%s]\n", cap.DisplayName, cap.ID, effDesc))
		}
		sb.WriteString("\n_💡 Click a button below or type `/model <name>` to switch model._")

		var inlineKb domain.InlineKeyboard
		var row []domain.InlineButton
		for _, cap := range availableModels {
			row = append(row, domain.InlineButton{
				Text:         formatModelButtonLabel(cap),
				CallbackData: "m:set:" + cap.ID,
			})
			if len(row) == 2 {
				inlineKb = append(inlineKb, row)
				row = nil
			}
		}
		if len(row) > 0 {
			inlineKb = append(inlineKb, row)
		}
		inlineKb = append(inlineKb, domain.InlineKeyboardRow{
			{Text: "🔄 Reset to Default", CallbackData: "m:reset"},
		})

		return sb.String(), inlineKb
	}

	target := strings.TrimSpace(args[0])
	if strings.EqualFold(target, "reset") || strings.EqualFold(target, "default") {
		session.ActiveModel = ""
		if err := e.storage.SaveSession(ctx, session); err != nil {
			return fmt.Sprintf("⚠️ Failed to reset model: %v", err), nil
		}
		resolvedModel, _, source := e.ResolveExecutionParams("", "", session, agentObj)
		return fmt.Sprintf("🔄 **Model override reset.**\nNow using `%s` (%s).", resolvedModel, source), nil
	}

	var customAliases map[string]string
	if e.cfg != nil {
		customAliases = e.cfg.AGY.ModelAliases
	}
	canonicalModel, _, isCustom := domain.NormalizeModelAndEffort(target, "", customAliases)
	if canonicalModel == "" {
		canonicalModel = target
	}

	session.ActiveModel = canonicalModel
	if err := e.storage.SaveSession(ctx, session); err != nil {
		return fmt.Sprintf("⚠️ Failed to update session model: %v", err), nil
	}

	customNote := ""
	if isCustom {
		customNote = "\n_(Custom model pass-through to agy CLI)_"
	}

	return fmt.Sprintf("⚡ **Active model switched to:** `%s` for this session.%s", canonicalModel, customNote), nil
}

func (e *Engine) handleEffortCommand(ctx context.Context, session *domain.Session, args []string) (string, domain.InlineKeyboard) {
	var agentObj *domain.Agent
	if session.ActiveAgent != "" {
		agentObj, _ = e.storage.GetAgent(ctx, session.ActiveAgent)
	}

	if len(args) == 0 {
		resolvedModel, resolvedEffort, _ := e.ResolveExecutionParams("", "", session, agentObj)
		if resolvedEffort == "" {
			resolvedEffort = "none"
		}

		var sb strings.Builder
		sb.WriteString("🧠 **Reasoning Effort Selection**\n")
		sb.WriteString(fmt.Sprintf("• **Current Effort:** `%s`\n", resolvedEffort))
		if resolvedModel != "" {
			sb.WriteString(fmt.Sprintf("• **Active Model:** `%s`\n", resolvedModel))
		}
		sb.WriteString("\n**Effort Levels:**\n")
		sb.WriteString("• **low** 🟢 — Quick thinking budget, low latency\n")
		sb.WriteString("• **medium** 🟡 — Balanced reasoning budget\n")
		sb.WriteString("• **high** 🔴 — Maximum thinking depth & verification\n")
		sb.WriteString("• **none** ⚪ — Disable thinking tokens (direct output)\n")
		sb.WriteString("\n_💡 Click a button below or type `/effort <level>` to switch._")

		inlineKb := domain.InlineKeyboard{
			{
				{Text: "🟢 Low", CallbackData: "eff:set:low"},
				{Text: "🟡 Medium", CallbackData: "eff:set:medium"},
			},
			{
				{Text: "🔴 High", CallbackData: "eff:set:high"},
				{Text: "⚪ None", CallbackData: "eff:set:none"},
			},
			{
				{Text: "🔄 Reset to Default", CallbackData: "eff:reset"},
			},
		}

		return sb.String(), inlineKb
	}

	target := strings.ToLower(strings.TrimSpace(args[0]))
	if target == "reset" || target == "default" {
		session.ActiveEffort = ""
		if err := e.storage.SaveSession(ctx, session); err != nil {
			return fmt.Sprintf("⚠️ Failed to reset effort: %v", err), nil
		}
		_, resolvedEffort, source := e.ResolveExecutionParams("", "", session, agentObj)
		return fmt.Sprintf("🔄 **Reasoning effort reset.**\nNow using `%s` (%s).", resolvedEffort, source), nil
	}

	if target != "low" && target != "medium" && target != "high" && target != "none" && target != "off" {
		return "⚠️ Invalid effort level. Supported values: `low`, `medium`, `high`, `none` (or `reset`).", nil
	}

	session.ActiveEffort = target
	if err := e.storage.SaveSession(ctx, session); err != nil {
		return fmt.Sprintf("⚠️ Failed to update session effort: %v", err), nil
	}

	// Validate against active model capabilities
	var customAliases map[string]string
	if e.cfg != nil {
		customAliases = e.cfg.AGY.ModelAliases
	}
	resolvedModel, _, _ := e.ResolveExecutionParams("", target, session, agentObj)
	cap, _, exists := domain.LookupModelCapability(resolvedModel, customAliases)
	var warningNote string
	if exists {
		if len(cap.SupportedEfforts) == 0 {
			warningNote = fmt.Sprintf("\n⚠️ _Note: Model `%s` does not support reasoning effort. The `--effort` flag will be automatically omitted during execution._", resolvedModel)
		} else {
			supported := false
			for _, se := range cap.SupportedEfforts {
				if se == target {
					supported = true
					break
				}
			}
			if !supported {
				warningNote = fmt.Sprintf("\n⚠️ _Note: Model `%s` only supports [%s]. Requested effort `%s` will be clamped to `%s` during execution._",
					resolvedModel, strings.Join(cap.SupportedEfforts, ", "), target, cap.DefaultEffort)
			}
		}
	}

	return fmt.Sprintf("🧠 **Reasoning effort set to:** `%s` for this session.%s", target, warningNote), nil
}

func formatTimeAgo(t time.Time) string {
	diff := time.Since(t)
	if diff < time.Minute {
		return "just now"
	}
	if diff < time.Hour {
		return fmt.Sprintf("%d mins ago", int(diff.Minutes()))
	}
	if diff < 24*time.Hour {
		return fmt.Sprintf("%d hours ago", int(diff.Hours()))
	}
	days := int(diff.Hours() / 24)
	if days == 1 {
		return "yesterday"
	}
	return fmt.Sprintf("%d days ago", days)
}

func (e *Engine) handleTasksCommand(ctx context.Context, session *domain.Session, args []string) (string, domain.InlineKeyboard) {
	if e.subagentDispatcher == nil {
		return "⚠️ Subagent subsystem is not configured.", nil
	}

	tasks, total, err := e.subagentDispatcher.ListTasks(ctx, session.SessionKey, 10, 0)
	if err != nil {
		return fmt.Sprintf("⚠️ Failed to list tasks: %v", err), nil
	}
	// A session can switch active agents. Do not disclose or offer controls for
	// a task created while a different agent was active in that same session.
	visibleTasks := tasks[:0]
	for _, task := range tasks {
		if task.AgentName == session.ActiveAgent {
			visibleTasks = append(visibleTasks, task)
		}
	}
	tasks = visibleTasks
	total = len(tasks)
	if total == 0 {
		return "📋 **No background sub-agent tasks found for this session.**\n\nMain Agent automatically delegates long-running tasks via `dispatch_subagent`.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📋 **Sub-Agent Tasks (%d total):**\n\n", total))

	var keyboard domain.InlineKeyboard
	for _, t := range tasks {
		badge := "⏳"
		switch t.Status {
		case domain.TaskStatusCompleted:
			badge = "✅"
		case domain.TaskStatusWaitingInput:
			badge = "⏸️"
		case domain.TaskStatusFailed:
			badge = "❌"
		case domain.TaskStatusCancelled:
			badge = "🛑"
		case domain.TaskStatusRunning:
			badge = "⚡"
		}

		sb.WriteString(fmt.Sprintf("%s **`%s`** | @%s\n", badge, t.ID, t.AgentName))
		sb.WriteString(fmt.Sprintf("   • **Title:** %s\n", t.Title))
		sb.WriteString(fmt.Sprintf("   • **Status:** `%s`", t.Status))
		if t.DurationSeconds > 0 {
			sb.WriteString(fmt.Sprintf(" (%.1fs)", t.DurationSeconds))
		}
		if t.Status == domain.TaskStatusWaitingInput && t.PendingQuestion != "" {
			sb.WriteString(fmt.Sprintf("\n   • ❓ **Question:** _%s_", t.PendingQuestion))
		}
		sb.WriteString("\n\n")

		if t.IsActive() {
			keyboard = append(keyboard, domain.InlineKeyboardRow{
				{
					Text:         fmt.Sprintf("ℹ️ Info %s", t.ID),
					CallbackData: fmt.Sprintf("task:info:%s", t.ID),
				},
				{
					Text:         fmt.Sprintf("🛑 Cancel %s", t.ID),
					CallbackData: fmt.Sprintf("task:cancel:%s", t.ID),
				},
			})
		}
	}

	sb.WriteString("💡 _Use `/task <id>` to inspect details or `/task reply <id> <input>` to respond._")
	return sb.String(), keyboard
}

func (e *Engine) handleTaskSubcommand(ctx context.Context, session *domain.Session, args []string) (string, domain.InlineKeyboard) {
	if e.subagentDispatcher == nil {
		return "⚠️ Subagent subsystem is not configured.", nil
	}
	if len(args) == 0 {
		return e.handleTasksCommand(ctx, session, nil)
	}

	subcmd := strings.ToLower(args[0])

	switch subcmd {
	case "cancel":
		if len(args) < 2 {
			return "⚠️ Usage: `/task cancel <task_id>`", nil
		}
		taskID := args[1]
		if _, err := e.getTaskInActiveScope(ctx, session, taskID); err != nil {
			return fmt.Sprintf("⚠️ Task `%s` is not available in this agent/session: %v", taskID, err), nil
		}
		if err := e.subagentDispatcher.CancelTaskScoped(ctx, session.SessionKey, taskID); err != nil {
			return fmt.Sprintf("⚠️ Failed to cancel task `%s`: %v", taskID, err), nil
		}
		return fmt.Sprintf("🛑 **Task `%s` has been cancelled** and its process tree terminated.", taskID), nil

	case "reply":
		if len(args) < 3 {
			return "⚠️ Usage: `/task reply <task_id> <your response>`", nil
		}
		taskID := args[1]
		replyText := strings.Join(args[2:], " ")
		if _, err := e.getTaskInActiveScope(ctx, session, taskID); err != nil {
			return fmt.Sprintf("⚠️ Task `%s` is not available in this agent/session: %v", taskID, err), nil
		}
		if err := e.subagentDispatcher.SendTaskInputScoped(ctx, session.SessionKey, taskID, replyText); err != nil {
			return fmt.Sprintf("⚠️ Failed to send reply to task `%s`: %v", taskID, err), nil
		}
		return fmt.Sprintf("✅ **Reply injected into Task `%s`!** Sub-Agent has resumed background execution.", taskID), nil

	case "clean", "purge":
		purged, err := e.storage.PurgeSubagentTasks(ctx, 0)
		if err != nil {
			return fmt.Sprintf("⚠️ Failed to purge tasks: %v", err), nil
		}
		return fmt.Sprintf("🧹 **Cleaned up %d completed/failed/cancelled subagent tasks.**", purged), nil

	default:
		// Assume args[0] is task_id
		taskID := args[0]
		task, err := e.getTaskInActiveScope(ctx, session, taskID)
		if err != nil {
			return fmt.Sprintf("⚠️ Task `%s` not found: %v", taskID, err), nil
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("📋 **Sub-Agent Task Details: `%s`**\n\n", task.ID))
		sb.WriteString(fmt.Sprintf("• **Agent:** `@%s`\n", task.AgentName))
		sb.WriteString(fmt.Sprintf("• **Title:** %s\n", task.Title))
		sb.WriteString(fmt.Sprintf("• **Status:** `%s`\n", task.Status))
		sb.WriteString(fmt.Sprintf("• **Model / Effort:** `%s` (`%s`)\n", task.Model, task.Effort))
		sb.WriteString(fmt.Sprintf("• **Workspace Mode:** `%s`\n", task.WorkspaceMode))
		sb.WriteString(fmt.Sprintf("• **Callback Mode:** `%s`\n", task.CallbackMode))
		sb.WriteString(fmt.Sprintf("• **Duration:** `%.2fs`\n", task.DurationSeconds))
		sb.WriteString(fmt.Sprintf("• **Tokens Used:** `%d`\n", task.Usage.TotalTokens))

		if task.Status == domain.TaskStatusRunning {
			sb.WriteString(fmt.Sprintf("\n⚡ **Live Execution:** Step %d | Tool: `%s`\n%s\n", task.CurrentStep, task.CurrentTool, task.ProgressMessage))
		}

		if task.Status == domain.TaskStatusWaitingInput {
			sb.WriteString(fmt.Sprintf("\n⏸️ **Waiting for Input:**\n❓ _%s_\n\n👉 **To reply:** `/task reply %s <your response>`\n", task.PendingQuestion, task.ID))
		}

		if task.Status == domain.TaskStatusCompleted && task.ResultSummary != "" {
			sb.WriteString("\n📝 **Result Summary:**\n" + task.ResultSummary + "\n")
		}

		if task.Status == domain.TaskStatusFailed && task.ErrorMessage != "" {
			sb.WriteString("\n❌ **Error:** " + task.ErrorMessage + "\n")
		}

		var keyboard domain.InlineKeyboard
		if task.IsActive() {
			keyboard = append(keyboard, domain.InlineKeyboardRow{
				{
					Text:         "🛑 Cancel Task",
					CallbackData: fmt.Sprintf("task:cancel:%s", task.ID),
				},
			})
		}

		return sb.String(), keyboard
	}
}

func (e *Engine) getTaskInActiveScope(ctx context.Context, session *domain.Session, taskID string) (*domain.SubagentTask, error) {
	if session == nil || session.SessionKey == "" || session.ActiveAgent == "" {
		return nil, fmt.Errorf("%w: missing task scope", ports.ErrAccessDenied)
	}
	task, err := e.subagentDispatcher.GetTaskScoped(ctx, session.SessionKey, taskID)
	if err != nil {
		return nil, err
	}
	if task.AgentName != session.ActiveAgent {
		return nil, fmt.Errorf("%w: task belongs to another agent", ports.ErrAccessDenied)
	}
	return task, nil
}

func (e *Engine) handleSecurityCommand(sender domain.SenderUser, sessionKey string, args []string) (string, domain.InlineKeyboard) {
	if e.securityManager == nil {
		return "⚠️ **Security Gateway is not active.**", nil
	}

	// Resolve active agent and baseline security preset for current session
	activeAgentName := "agyent"
	var agent *domain.Agent
	if e.storage != nil && sessionKey != "" {
		if session, err := e.storage.GetSession(context.Background(), sessionKey); err == nil && session != nil && session.ActiveAgent != "" {
			activeAgentName = session.ActiveAgent
		}
		if a, err := e.storage.GetAgent(context.Background(), activeAgentName); err == nil && a != nil {
			agent = a
		}
	}

	baselinePreset := domain.PresetBalanced
	if agent != nil && agent.SecurityPreset != "" {
		baselinePreset = agent.SecurityPreset
	}

	if len(args) > 0 {
		if !e.isSenderSuperAdmin(sender) {
			return "⛔ **Unauthorized: Only administrators can modify security gateway settings.**", nil
		}

		subcmd := strings.ToLower(args[0])
		switch subcmd {
		case "preset":
			if len(args) < 2 {
				return "⚠️ Usage: `/security preset <unrestricted|developer|balanced|strict|read_only>`", nil
			}
			preset := domain.SecurityPreset(strings.ToLower(args[1]))
			switch preset {
			case domain.PresetUnrestricted, domain.PresetDeveloper, domain.PresetBalanced, domain.PresetStrict, domain.PresetReadOnly:
				// Valid preset name
			default:
				return fmt.Sprintf("⚠️ Invalid security preset: `%s`. Valid options: `unrestricted`, `developer`, `balanced`, `strict`, `read_only`", args[1]), nil
			}

			// Validate monotonic upgrade rule: cannot switch to less secure level than baseline
			if !domain.CanSwitchPreset(baselinePreset, preset) {
				allowedPresets := domain.GetAllowedPresets(baselinePreset)
				var allowedStrs []string
				for _, p := range allowedPresets {
					allowedStrs = append(allowedStrs, fmt.Sprintf("`%s`", p))
				}
				return fmt.Sprintf("⛔ **Cannot downgrade security preset:** Agent `%s` current baseline security level is `%s`. You can only switch to equal or more secure presets (allowed: %s).",
					activeAgentName, baselinePreset, strings.Join(allowedStrs, ", ")), nil
			}

			e.securityManager.SetPreset(preset)
			if agent != nil && e.storage != nil {
				agent.SecurityPreset = preset
				agent.UpdatedAt = time.Now()
				_ = e.storage.SaveAgent(context.Background(), agent)
			}
			return fmt.Sprintf("🛡️ **Security preset successfully switched to:** `%s` for agent `%s`", preset, activeAgentName), nil

		case "grant":
			if len(args) < 2 {
				return "⚠️ Usage: `/security grant <pattern|scope>`", nil
			}
			pattern := args[1]
			e.securityManager.GrantSessionPermission(sessionKey, pattern)
			return fmt.Sprintf("🛡️ **Temporary permission granted for session:** `%s` (valid for 15 minutes)", pattern), nil

		case "redact":
			if len(args) < 2 {
				return "⚠️ Usage: `/security redact <strict|permissive|audit_only>`", nil
			}
			mode := domain.RedactionMode(strings.ToLower(args[1]))
			e.securityManager.SetRedactionMode(mode)
			return fmt.Sprintf("🎭 **Secret Redaction mode switched to:** `%s`", mode), nil
		}
	}

	// Default: Show Dashboard with Interactive Preset Switcher Buttons (filtered to >= baseline)
	summary := e.securityManager.GetDashboardSummary(sessionKey)
	summary.Preset = baselinePreset

	var sb strings.Builder
	sb.WriteString("🛡️ **[Agyent Security Gateway Dashboard]**\n")
	sb.WriteString("━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	sb.WriteString(fmt.Sprintf("🤖 **Active Agent**      : `%s`\n", activeAgentName))
	sb.WriteString(fmt.Sprintf("📍 **Active Preset**     : `%s`\n", summary.Preset))
	sb.WriteString(fmt.Sprintf("📂 **Workspace Jail**    : `%s`\n", summary.ActiveJail))
	sb.WriteString(fmt.Sprintf("🎭 **Redaction Mode**    : `%s`\n", summary.RedactionMode))
	sb.WriteString(fmt.Sprintf("⚙️ **Delegated Config**  : `%t`\n", summary.ConfigDelegated))
	sb.WriteString(fmt.Sprintf("🛑 **Blocked Today**     : `%d` events\n", summary.BlockedToday))
	sb.WriteString(fmt.Sprintf("✅ **Approved Today**    : `%d` events\n", summary.ApprovedToday))
	sb.WriteString(fmt.Sprintf("⚡ **Total Evaluations** : `%d` checks\n", summary.TotalEvaluations))
	sb.WriteString("━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	sb.WriteString("💡 _Use buttons below to switch profiles or toggle redaction._")

	presetButtons := map[domain.SecurityPreset]domain.InlineButton{
		domain.PresetUnrestricted: {
			Text:         "🔓 Unrestricted (Full)",
			CallbackData: "sec:preset:unrestricted",
		},
		domain.PresetDeveloper: {
			Text:         "🛠️ Developer",
			CallbackData: "sec:preset:developer",
		},
		domain.PresetBalanced: {
			Text:         "🛡️ Balanced",
			CallbackData: "sec:preset:balanced",
		},
		domain.PresetStrict: {
			Text:         "🔒 Strict",
			CallbackData: "sec:preset:strict",
		},
		domain.PresetReadOnly: {
			Text:         "📖 Read Only",
			CallbackData: "sec:preset:read_only",
		},
	}

	var keyboard domain.InlineKeyboard
	var currentRow []domain.InlineButton

	for _, p := range domain.AllSecurityPresets {
		if domain.CanSwitchPreset(baselinePreset, p) {
			if btn, ok := presetButtons[p]; ok {
				currentRow = append(currentRow, btn)
				if len(currentRow) == 2 {
					keyboard = append(keyboard, currentRow)
					currentRow = nil
				}
			}
		}
	}
	if len(currentRow) > 0 {
		keyboard = append(keyboard, currentRow)
	}

	// Redaction mode options
	keyboard = append(keyboard, []domain.InlineButton{
		{
			Text:         "🎭 Redact: Strict",
			CallbackData: "sec:redact:strict",
		},
		{
			Text:         "🎭 Redact: Permissive",
			CallbackData: "sec:redact:permissive",
		},
	})

	return sb.String(), keyboard
}

func (e *Engine) handleWhitelistCommand(sender domain.SenderUser, sessionKey string, args []string) string {
	if e.securityManager == nil {
		return "⚠️ **Security Gateway is not active.**"
	}

	if len(args) >= 2 && strings.ToLower(args[0]) == "add" {
		if !e.isSenderSuperAdmin(sender) {
			return "⛔ **Unauthorized: Only administrators can modify security whitelist rules.**"
		}
		entry := strings.Join(args[1:], " ")
		e.securityManager.AddWhitelistEntry(entry)
		return fmt.Sprintf("✅ **Added custom whitelist rule:** `%s`", entry)
	}

	return "⚠️ Usage: `/whitelist add <command_or_path>`\nExample: `/whitelist add \"npm run build\"`"
}

func (e *Engine) handleScheduleCommand(ctx context.Context, sender domain.SenderUser, session *domain.Session, args []string) (string, domain.InlineKeyboard) {
	if e.scheduler == nil {
		return "⚠️ **Scheduler engine is not initialized.**", nil
	}

	agent, err := e.storage.GetAgent(ctx, session.ActiveAgent)
	if err == nil && agent != nil {
		allowed, _, err := e.CheckAccess(ctx, agent, sender.ID)
		if err != nil || !allowed {
			return fmt.Sprintf("⛔ **Access Denied:** You do not have permission to view or manage schedules for agent `@%s`.", agent.Name), nil
		}
	}

	// /schedule cancel <id>
	if len(args) > 0 && (strings.ToLower(args[0]) == "cancel" || strings.ToLower(args[0]) == "delete" || strings.ToLower(args[0]) == "del") {
		if len(args) < 2 || strings.TrimSpace(args[1]) == "" {
			return "⚠️ Usage: `/schedule cancel <task_id>`", nil
		}
		taskID := strings.TrimSpace(args[1])
		task, err := e.storage.GetSchedule(ctx, taskID)
		if err != nil {
			return fmt.Sprintf("⚠️ Schedule `%s` not found: %v", taskID, err), nil
		}
		if task.AgentName != session.ActiveAgent {
			return "⛔ Access Denied: That schedule belongs to a different agent.", nil
		}
		if err := e.scheduler.CancelSchedule(ctx, taskID); err != nil {
			return fmt.Sprintf("⚠️ Failed to cancel schedule `%s`: %v", taskID, err), nil
		}
		return fmt.Sprintf("🗑️ **Schedule cancelled:** `%s` has been removed.", taskID), nil
	}

	// List schedules
	tasks, err := e.scheduler.ListSchedules(ctx, session.ActiveAgent, "")
	if err != nil {
		return fmt.Sprintf("⚠️ Failed to query schedules: %v", err), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("⏰ **Scheduled & Cron Tasks (@%s)**\n\n", session.ActiveAgent))

	if len(tasks) == 0 {
		sb.WriteString("No scheduled tasks found.\n\n")
		sb.WriteString("_💡 To schedule a task naturally, prompt your agent:_\n*\"Remind me in 30 minutes to check deployment\"* or *\"Every day at 9am summarize hacker news\"*.")
		return sb.String(), nil
	}

	var keyboard domain.InlineKeyboard
	for _, t := range tasks {
		statusIcon := "🟢"
		if t.Status == domain.ScheduleStatusRunning {
			statusIcon = "⚡"
		} else if t.Status == domain.ScheduleStatusPaused {
			statusIcon = "⏸️"
		} else if t.Status == domain.ScheduleStatusFailed {
			statusIcon = "❌"
		}

		sb.WriteString(fmt.Sprintf("%s **%s** (`%s`)\n", statusIcon, t.Title, t.ID))
		sb.WriteString(fmt.Sprintf("   • **Type:** `%s` (%s)\n", t.ScheduleType, t.ScheduleExpr))
		sb.WriteString(fmt.Sprintf("   • **Next Run:** `%s`\n", t.NextRunAt.Format("2006-01-02 15:04:05 MST")))
		if t.LastError != "" {
			sb.WriteString(fmt.Sprintf("   • **Last Error:** _%s_\n", t.LastError))
		}
		sb.WriteString("\n")

		keyboard = append(keyboard, []domain.InlineButton{
			{
				Text:         fmt.Sprintf("❌ Cancel %s", t.ID),
				CallbackData: fmt.Sprintf("sched:cancel:%s", t.ID),
			},
		})
	}

	return sb.String(), keyboard
}

func (e *Engine) handleHeartbeatCommand(ctx context.Context, sender domain.SenderUser, session *domain.Session, args []string) (string, domain.InlineKeyboard) {
	if e.scheduler == nil {
		return "⚠️ **Scheduler engine is not initialized.**", nil
	}

	agent, err := e.storage.GetAgent(ctx, session.ActiveAgent)
	if err == nil && agent != nil {
		allowed, _, err := e.CheckAccess(ctx, agent, sender.ID)
		if err != nil || !allowed {
			return fmt.Sprintf("⛔ **Access Denied:** You do not have permission to view or manage heartbeat for agent `@%s`.", agent.Name), nil
		}
	}

	cfg, prompt, err := e.scheduler.GetHeartbeat(ctx, session.ActiveAgent)
	if err != nil {
		return fmt.Sprintf("⚠️ Failed to load heartbeat configuration: %v", err), nil
	}

	subCmd := ""
	if len(args) > 0 {
		subCmd = strings.ToLower(args[0])
	}

	switch subCmd {
	case "on", "enable":
		cfg.Enabled = true
		cfg.TargetSessionKey = session.SessionKey
		parsedKey, _ := domain.ParseSessionKey(session.SessionKey)
		cfg.ChatID = parsedKey.ChatID
		if parsedKey.ThreadID > 0 {
			cfg.ThreadID = strconv.FormatInt(parsedKey.ThreadID, 10)
		}
		cfg.Channel = parsedKey.Channel
		if cfg.Channel == "" {
			cfg.Channel = "telegram"
		}
		if err := e.scheduler.ConfigureHeartbeat(ctx, *cfg, prompt); err != nil {
			return fmt.Sprintf("⚠️ Failed to enable heartbeat: %v", err), nil
		}
		return fmt.Sprintf("💓 **Heartbeat Enabled for @%s**\n• Interval: `%s`\n• Next Wakeup: `%s`",
			session.ActiveAgent, formatIntervalDuration(cfg.IntervalSeconds), cfg.NextRunAt.Format("15:04:05 MST")), nil

	case "off", "disable":
		cfg.Enabled = false
		if err := e.scheduler.ConfigureHeartbeat(ctx, *cfg, prompt); err != nil {
			return fmt.Sprintf("⚠️ Failed to disable heartbeat: %v", err), nil
		}
		return fmt.Sprintf("💔 **Heartbeat Disabled for @%s**", session.ActiveAgent), nil

	case "interval":
		if len(args) < 2 {
			return "⚠️ Usage: `/heartbeat interval <duration>` (e.g. `/heartbeat interval 30m`, `/heartbeat interval 2h`)", nil
		}
		d, err := time.ParseDuration(strings.ToLower(args[1]))
		if err != nil || d <= 0 {
			return fmt.Sprintf("⚠️ Invalid duration %q. Example: `15m`, `1h`, `30m`", args[1]), nil
		}
		cfg.IntervalSeconds = int(d.Seconds())
		if err := e.scheduler.ConfigureHeartbeat(ctx, *cfg, prompt); err != nil {
			return fmt.Sprintf("⚠️ Failed to update heartbeat interval: %v", err), nil
		}
		return fmt.Sprintf("⏱️ **Heartbeat Interval Updated (@%s):** `%s`", session.ActiveAgent, args[1]), nil

	case "trigger", "run", "now":
		if err := e.scheduler.TriggerHeartbeatNow(ctx, session.ActiveAgent); err != nil {
			return fmt.Sprintf("⚠️ Failed to trigger heartbeat: %v", err), nil
		}
		return fmt.Sprintf("⚡ **Heartbeat Wakeup Triggered:** Agent `@%s` is executing directives in background...", session.ActiveAgent), nil

	default:
		statusStr := "🔴 **Disabled**"
		if cfg.Enabled {
			statusStr = "🟢 **Enabled**"
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("💓 **Agent Heartbeat Status (@%s)**\n\n", session.ActiveAgent))
		sb.WriteString(fmt.Sprintf("• **Status:** %s\n", statusStr))
		sb.WriteString(fmt.Sprintf("• **Interval:** `%s`\n", formatIntervalDuration(cfg.IntervalSeconds)))
		if cfg.Enabled && !cfg.NextRunAt.IsZero() {
			sb.WriteString(fmt.Sprintf("• **Next Wakeup:** `%s`\n", cfg.NextRunAt.Format("2006-01-02 15:04:05 MST")))
		}
		if !cfg.LastRunAt.IsZero() {
			sb.WriteString(fmt.Sprintf("• **Last Run:** `%s`\n", cfg.LastRunAt.Format("2006-01-02 15:04:05 MST")))
		}
		if cfg.LastError != "" {
			sb.WriteString(fmt.Sprintf("• **Last Error:** _%s_\n", cfg.LastError))
		}

		sb.WriteString("\n📄 **Prompt Directives (`HEARTBEAT.md`):**\n")
		promptSnippet := prompt
		if len(promptSnippet) > 300 {
			promptSnippet = promptSnippet[:300] + "..."
		}
		sb.WriteString(fmt.Sprintf("```markdown\n%s\n```\n", promptSnippet))
		sb.WriteString("\n_Commands: `/heartbeat [on|off]` • `/heartbeat interval <duration>` • `/heartbeat trigger`_")

		var keyboard domain.InlineKeyboard
		toggleBtnText := "🟢 Enable Heartbeat"
		toggleAction := "hb:on"
		if cfg.Enabled {
			toggleBtnText = "🔴 Disable Heartbeat"
			toggleAction = "hb:off"
		}
		keyboard = append(keyboard, []domain.InlineButton{
			{Text: toggleBtnText, CallbackData: toggleAction},
			{Text: "⚡ Trigger Now", CallbackData: "hb:trigger"},
		})

		return sb.String(), keyboard
	}
}

func formatIntervalDuration(seconds int) string {
	if seconds <= 0 {
		return "1h"
	}
	if seconds%3600 == 0 {
		return fmt.Sprintf("%dh", seconds/3600)
	}
	if seconds%60 == 0 {
		return fmt.Sprintf("%dm", seconds/60)
	}
	return fmt.Sprintf("%ds", seconds)
}
