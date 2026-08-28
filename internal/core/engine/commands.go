package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"agyent/internal/config"
	"agyent/internal/core/domain"
)

// HandleCommand processes slash commands and returns an outbound response.
func (e *Engine) HandleCommand(ctx context.Context, msg domain.CanonicalMessage) (*domain.OutboundMessage, error) {
	cmd, args := msg.CommandArgs()
	cmd = strings.ToLower(cmd)
	sessionKey := msg.SessionKey()

	defaultAgent := "agyent"
	session, err := e.storage.GetOrCreateSession(ctx, sessionKey, defaultAgent)
	if err != nil {
		return &domain.OutboundMessage{
			BotID:            msg.BotID,
			ChatID:           msg.Chat.ID,
			ThreadID:         msg.Chat.ThreadID,
			Text:             fmt.Sprintf("⚠️ Failed to load session: %v", err),
			ReplyToMessageID: msg.ID,
		}, nil
	}

	var responseText string
	var inlineKeyboard domain.InlineKeyboard

	switch cmd {
	case "/help":
		responseText = e.handleHelpCommand()

	case "/status":
		responseText = e.handleStatusCommand(ctx, session)

	case "/tokens", "/token", "/metrics":
		responseText = e.handleTokensCommand(ctx, session)

	case "/context":
		responseText = e.handleContextCommand(ctx, session)

	case "/skills", "/skill":
		responseText = e.handleSkillsCommand(ctx, session, args)

	case "/plugins", "/plugin":
		responseText = e.handlePluginsCommand(ctx, session, args)

	case "/stream":
		responseText = e.handleStreamCommand(args)

	case "/model", "/m", "/models":
		responseText, inlineKeyboard = e.handleModelCommand(ctx, session, args)

	case "/effort", "/eff":
		responseText, inlineKeyboard = e.handleEffortCommand(ctx, session, args)

	case "/agents", "/agent", "/a":
		responseText = e.handleAgentsCommand(ctx, msg.Sender, session, args)

	case "/bootstrap":
		responseText = e.handleBootstrapCommand(ctx, msg.Sender, session, args)

	case "/use":
		if len(args) == 0 {
			responseText = "⚠️ Usage: `/use <agent_name>`\nExample: `/use agyent`"
		} else {
			responseText = e.switchAgent(ctx, msg.Sender, session, args[0])
		}

	case "/projects", "/project", "/p":
		responseText = e.handleProjectsCommand(ctx, session, args)

	case "/security", "/sec":
		responseText, inlineKeyboard = e.handleSecurityCommand(msg.Sender, sessionKey, args)

	case "/whitelist":
		responseText = e.handleWhitelistCommand(msg.Sender, sessionKey, args)

	case "/tasks", "/subagents":
		responseText, inlineKeyboard = e.handleTasksCommand(ctx, session, args)

	case "/task":
		responseText, inlineKeyboard = e.handleTaskSubcommand(ctx, session, args)

	case "/conversations", "/c":
		responseText, inlineKeyboard = e.handleConversationsDispatcher(ctx, session, args)

	case "/new":
		responseText = e.handleNewConversationCommand(ctx, session, args)

	case "/pin":
		responseText = e.handlePinCommand(ctx, session, args, true)

	case "/unpin":
		responseText = e.handlePinCommand(ctx, session, args, false)

	case "/reset":
		if e.HasActiveTurn(sessionKey) {
			responseText = "⚠️ A turn is currently executing in this conversation. Please wait for completion or send `/force_unlock` before resetting."
		} else {
			session.ResetActiveConversationID()
			if err := e.storage.SaveSession(ctx, session); err != nil {
				responseText = fmt.Sprintf("⚠️ Failed to reset conversation: %v", err)
			} else {
				scope := "Global Mode"
				if session.ActiveProject != "" {
					scope = fmt.Sprintf("Project: %s", session.ActiveProject)
				}
				responseText = fmt.Sprintf("🔄 **Conversation context reset** for [%s • %s].\nNext message will start a fresh conversation session.", session.ActiveAgent, scope)
			}
		}

	case "/force_unlock":
		e.cancelActiveTurn(sessionKey)
		unlocked := e.lockManager.ForceUnlock(sessionKey)
		if unlocked {
			responseText = "🔓 **Session lock forcefully released** and any active subprocess was cancelled."
		} else {
			responseText = "🔓 Session was not locked. Active state has been reset."
		}

	default:
		responseText = fmt.Sprintf("❓ Unknown command `%s`. Type `/help` for available commands.", cmd)
	}

	return &domain.OutboundMessage{
		BotID:            msg.BotID,
		ChatID:           msg.Chat.ID,
		ThreadID:         msg.Chat.ThreadID,
		Text:             responseText,
		ParseMode:        "HTML",
		ReplyToMessageID: msg.ID,
		InlineKeyboard:   inlineKeyboard,
	}, nil
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

**🧵 Conversation & Context:**
• ` + "`/ask <prompt>`" + ` — Ask an isolated ephemeral question without polluting active context.
• ` + "`/c`" + ` (or ` + "`/conversations`" + `) — View interactive conversation list with 1-touch buttons.
• ` + "`/c <#>`" + ` — Fast switch to conversation by number (e.g. ` + "`/c 2`" + `).
• ` + "`/new`" + ` — Start a fresh new conversation context.
• ` + "`/pin`" + ` / ` + "`/unpin`" + ` — Pin or unpin the current active conversation.
• ` + "`/c rename <title>`" + ` — Rename the current active conversation.
• ` + "`/c archive`" + ` — Archive the current conversation.
• ` + "`/c clean`" + ` — Trigger garbage collection for old archived sessions.

**🤖 Agent Management & RBAC:**
• ` + "`/agents`" + ` (or ` + "`/a list`" + `) — List all agents accessible to your user.
• ` + "`/use <name>`" + ` (or ` + "`/a <name>`" + `) — Switch active agent profile (validates permissions).
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

func (e *Engine) handleTokensCommand(ctx context.Context, session *domain.Session) string {
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
		return fmt.Sprintf("📊 **No token metrics recorded yet** for [%s • %s].\n\nSend a message to start a conversation session and track token metrics.", session.ActiveAgent, scopeLabel)
	}

	convTag := activeConvID
	if convTag == "" {
		convTag = "(uninitialized / new)"
	} else if len(convTag) > 8 {
		convTag = convTag[:8] + "..."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📊 **agyent Token Usage & Cache Metrics**\n"))
	sb.WriteString(fmt.Sprintf("• **Active Agent:** %s\n", session.ActiveAgent))
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

		sb.WriteString("📐 **Active Turn Context Window:**\n")
		sb.WriteString(fmt.Sprintf("• **Prompt Input Size:** %s\n", formatNumber(latestLog.Usage.InputTokens)))
		sb.WriteString(fmt.Sprintf("• **⚡ KV-Cache Hit:** %s (%.1f%% Cache Hit)\n", formatNumber(latestLog.Usage.CacheReadTokens), latestHitRatio))
		sb.WriteString(fmt.Sprintf("• **Fresh Uncached Input:** %s\n", formatNumber(latestLog.Usage.UncachedInputTokens())))
		sb.WriteString(fmt.Sprintf("• **Turn Output:** %s (Thinking: %s)\n", formatNumber(latestLog.Usage.OutputTokens), formatNumber(latestLog.Usage.ThinkingTokens)))
		sb.WriteString(fmt.Sprintf("• **Cache State:** %s\n\n", cacheState))
	}

	if convStats != nil && convStats.TotalTokens > 0 {
		hitRatio := convStats.CacheHitRatio()
		costSaved := convStats.EffectiveCostSavingsRatio()

		sb.WriteString("🧵 **Conversation Cumulative Billed:**\n")
		sb.WriteString(fmt.Sprintf("• **Total Input Billed:** %s\n", formatNumber(convStats.InputTokens)))
		sb.WriteString(fmt.Sprintf("• **⚡ Total Cache Read:** %s (%.1f%%)\n", formatNumber(convStats.CacheReadTokens), hitRatio))
		sb.WriteString(fmt.Sprintf("• **Total Fresh Input:** %s\n", formatNumber(convStats.UncachedInputTokens())))
		sb.WriteString(fmt.Sprintf("• **Total Output Billed:** %s (Thinking: %s)\n", formatNumber(convStats.OutputTokens), formatNumber(convStats.ThinkingTokens)))
		sb.WriteString(fmt.Sprintf("• **Total Billed Tokens:** %s\n", formatNumber(convStats.TotalTokens)))
		sb.WriteString(fmt.Sprintf("• **💰 Estimated Cost Savings:** ~%.1f%% (Gemini 0.25x Cache)\n\n", costSaved))
	}

	if sessionStats != nil && sessionStats.TotalTokens > 0 {
		sessionHitRatio := sessionStats.CacheHitRatio()
		sessionCostSaved := sessionStats.EffectiveCostSavingsRatio()

		sb.WriteString("🌐 **Session Lifetime:**\n")
		sb.WriteString(fmt.Sprintf("• **Lifetime Input:** %s | **Cached:** %s (%.1f%%)\n", formatNumber(sessionStats.InputTokens), formatNumber(sessionStats.CacheReadTokens), sessionHitRatio))
		sb.WriteString(fmt.Sprintf("• **Lifetime Output:** %s | **Thinking:** %s\n", formatNumber(sessionStats.OutputTokens), formatNumber(sessionStats.ThinkingTokens)))
		sb.WriteString(fmt.Sprintf("• **Lifetime Total:** %s (Net Savings: ~%.1f%%)\n", formatNumber(sessionStats.TotalTokens), sessionCostSaved))
	}

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
		err := e.pluginManager.TogglePlugin(ctx, name, true, domain.ScopeWorkspace, wsDir)
		if err != nil {
			return fmt.Sprintf("⚠️ Failed to enable plugin %q: %v", name, err)
		}
		return fmt.Sprintf("🟢 Plugin **%s** is now **ENABLED**. Capabilities are active for your next turn.", name)

	case "disable", "off", "0":
		if len(args) < 2 {
			return "⚠️ Usage: `/plugin disable <plugin_name>`"
		}
		name := args[1]
		err := e.pluginManager.TogglePlugin(ctx, name, false, domain.ScopeWorkspace, wsDir)
		if err != nil {
			return fmt.Sprintf("⚠️ Failed to disable plugin %q: %v", name, err)
		}
		return fmt.Sprintf("⚪ Plugin **%s** is now **DISABLED**. Associated tools and skills have been unmounted.", name)

	case "install":
		if len(args) < 2 {
			return "⚠️ Usage: `/plugin install <builtin_plugin_name>`\nAvailable builtins: `browser-camoufox`, `system-diagnostics`, `database-sqlite`"
		}
		name := args[1]
		err := e.pluginManager.InstallBuiltinPlugin(ctx, name, domain.ScopeWorkspace, wsDir)
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

func (e *Engine) handleAgentsCommand(ctx context.Context, sender domain.SenderUser, session *domain.Session, args []string) string {
	if len(args) == 0 || args[0] == "list" {
		var agents []domain.Agent
		var err error
		if e.IsSuperAdmin(sender.ID) {
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
				statusTag = "uninitialized (bootstrap on next turn)"
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
		sb.WriteString("\nUse `/use <name>` to switch active agent.")
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
		name := strings.TrimSpace(args[1])
		desc := "AI Assistant"
		if len(args) > 2 {
			desc = strings.Join(args[2:], " ")
		}

		agentPath := config.ResolveAgentWorkspace(e.cfg.Storage.AgentsDir, name)
		if err := os.MkdirAll(agentPath, 0755); err != nil {
			return fmt.Sprintf("⚠️ Failed to create agent directory: %v", err)
		}

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

		if err := e.storage.SaveAgent(ctx, agent); err != nil {
			return fmt.Sprintf("⚠️ Failed to register agent: %v", err)
		}

		session.ActiveAgent = name
		session.ActiveProject = ""
		_ = e.storage.SaveSession(ctx, session)

		return fmt.Sprintf("🎉 **Agent `%s` created** (Owner: `%s`) and set as active!\nWorkspace: `%s`\nGenesis bootstrap protocol will activate on your first message.", name, sender.ID, agentPath)

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

		if agent.OwnerID != "" && agent.OwnerID != sender.ID && !e.IsSuperAdmin(sender.ID) {
			return fmt.Sprintf("⛔ <b>Access Denied:</b> Only the owner of agent <code>@%s</code> or a superadmin can share access.", agentName)
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

		if agent.OwnerID != "" && agent.OwnerID != sender.ID && !e.IsSuperAdmin(sender.ID) {
			return fmt.Sprintf("⛔ <b>Access Denied:</b> Only the owner of agent <code>@%s</code> or a superadmin can revoke access.", agentName)
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

		allowed, role, _ := e.CheckAccess(ctx, agent, sender.ID)
		if !allowed {
			return fmt.Sprintf("⛔ <b>Access Denied:</b> You do not have permission to view info for agent <code>@%s</code>.", agentName)
		}

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
		sb.WriteString(fmt.Sprintf("🤖 <b>Agent Information: <code>@%s</code></b>\n", agent.Name))
		sb.WriteString(fmt.Sprintf("• <b>Description:</b> %s\n", agent.Description))
		sb.WriteString(fmt.Sprintf("• <b>Status:</b> <code>%s</code>\n", agent.Status))
		sb.WriteString(fmt.Sprintf("• <b>Visibility:</b> %s\n", visibility))
		sb.WriteString(fmt.Sprintf("• <b>Owner ID:</b> <code>%s</code>\n", ownerDisplay))
		sb.WriteString(fmt.Sprintf("• <b>Your Effective Role:</b> <code>%s</code>\n", role))
		sb.WriteString(fmt.Sprintf("• <b>Workspace Path:</b> <code>%s</code>\n", agent.WorkspacePath))
		if agent.DefaultModel != "" {
			sb.WriteString(fmt.Sprintf("• <b>Default Model:</b> <code>%s</code>\n", agent.DefaultModel))
		}
		if agent.DefaultEffort != "" {
			sb.WriteString(fmt.Sprintf("• <b>Default Effort:</b> <code>%s</code>\n", agent.DefaultEffort))
		}

		sb.WriteString(fmt.Sprintf("\n👥 <b>Collaborators (%d):</b>\n", len(perms)))
		if len(perms) == 0 {
			sb.WriteString("  <i>No external collaborators shared.</i>\n")
		} else {
			for _, p := range perms {
				sb.WriteString(fmt.Sprintf("  • User <code>%s</code> — Role: <code>%s</code> (Granted by: <code>%s</code>)\n", p.UserID, p.Role, p.GrantedBy))
			}
		}

		return sb.String()

	default:
		return e.switchAgent(ctx, sender, session, args[0])
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

	if agent.OwnerID != "" && agent.OwnerID != sender.ID && !e.IsSuperAdmin(sender.ID) {
		return fmt.Sprintf("⛔ <b>Access Denied:</b> Only the agent owner (User ID: <code>%s</code>) or an administrator can re-trigger Genesis Bootstrap.", agent.OwnerID)
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

func (e *Engine) switchAgent(ctx context.Context, sender domain.SenderUser, session *domain.Session, agentName string) string {
	if e.HasActiveTurn(session.SessionKey) {
		return "⚠️ A turn is currently executing in this conversation. Please wait for completion or send `/force_unlock` before switching agent."
	}

	agent, err := e.storage.GetAgent(ctx, agentName)
	if err != nil {
		return fmt.Sprintf("⚠️ Agent `%s` not found. Use `/agents` to view available agents.", agentName)
	}

	allowed, _, err := e.CheckAccess(ctx, agent, sender.ID)
	if err != nil || !allowed {
		ownerDesc := agent.OwnerID
		if ownerDesc == "" {
			ownerDesc = "admin"
		}
		return fmt.Sprintf("⛔ <b>Access Denied (403):</b> You do not have permission to switch to agent <code>@%s</code>.\nAsk the agent owner (User ID: <code>%s</code>) to grant you access via:\n<code>/a share %s %s [role]</code>", agent.Name, ownerDesc, agent.Name, sender.ID)
	}

	session.ActiveAgent = agent.Name
	session.ActiveProject = ""

	// Restore latest conversation for the target agent in global mode to prevent context bleed
	latest, _, err := e.storage.ListRecentConversations(ctx, session.SessionKey, agent.Name, "", 1, 0)
	if err == nil && len(latest) > 0 {
		session.GlobalConversationID = latest[0].ID
	} else {
		session.GlobalConversationID = ""
	}

	if err := e.storage.SaveSession(ctx, session); err != nil {
		return fmt.Sprintf("⚠️ Failed to update session: %v", err)
	}

	return fmt.Sprintf("🔄 Switched active agent to **%s** (%s).\nContext returned to Global Mode.", agent.Name, agent.Description)
}

func (e *Engine) handleProjectsCommand(ctx context.Context, session *domain.Session, args []string) string {
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
		projName := args[1]
		projPath := ""
		if len(args) > 2 {
			projPath = args[2]
		} else {
			agentPath := config.ResolveAgentWorkspace(e.cfg.Storage.AgentsDir, session.ActiveAgent)
			if agent, err := e.storage.GetAgent(ctx, session.ActiveAgent); err == nil && agent != nil && agent.WorkspacePath != "" {
				agentPath = agent.WorkspacePath
			}
			projPath = filepath.Join(agentPath, "projects", projName)
		}

		projPath, _ = filepath.Abs(projPath)
		if err := os.MkdirAll(projPath, 0755); err != nil {
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

	if idx, err := strconv.Atoi(target); err == nil && idx > 0 {
		conv, err := e.storage.GetConversationByAlias(ctx, session.SessionKey, session.ActiveAgent, session.ActiveProject, idx)
		if err != nil {
			return fmt.Sprintf("⚠️ Conversation #%d not found in active scope.", idx)
		}
		targetConv = conv
	} else {
		conv, err := e.storage.GetConversation(ctx, target)
		if err != nil {
			return fmt.Sprintf("⚠️ Conversation `%s` not found.", target)
		}
		targetConv = conv
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

	if len(args) > 0 {
		target := strings.TrimSpace(args[0])
		if idx, err := strconv.Atoi(target); err == nil && idx > 0 {
			conv, err := e.storage.GetConversationByAlias(ctx, session.SessionKey, session.ActiveAgent, session.ActiveProject, idx)
			if err != nil {
				return fmt.Sprintf("⚠️ Conversation #%d not found.", idx)
			}
			targetID = conv.ID
			title = conv.Title
		} else {
			conv, err := e.storage.GetConversation(ctx, target)
			if err != nil {
				return fmt.Sprintf("⚠️ Conversation `%s` not found.", target)
			}
			targetID = conv.ID
			title = conv.Title
		}
	} else {
		targetID = session.GetActiveConversationID()
		if targetID == "" {
			return "⚠️ No active conversation to pin/unpin. Send a message first."
		}
		if conv, err := e.storage.GetConversation(ctx, targetID); err == nil {
			title = conv.Title
		}
	}

	if err := e.storage.SetConversationPinned(ctx, targetID, isPinned); err != nil {
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

	if err := e.storage.SetConversationTitle(ctx, targetID, newTitle); err != nil {
		return fmt.Sprintf("⚠️ Failed to rename conversation: %v", err)
	}

	return fmt.Sprintf("✏️ Renamed conversation to: **%s**.", newTitle)
}

func (e *Engine) handleArchiveConversationCommand(ctx context.Context, session *domain.Session, args []string) string {
	targetID := session.GetActiveConversationID()
	if len(args) > 0 {
		target := strings.TrimSpace(args[0])
		if idx, err := strconv.Atoi(target); err == nil && idx > 0 {
			if conv, err := e.storage.GetConversationByAlias(ctx, session.SessionKey, session.ActiveAgent, session.ActiveProject, idx); err == nil {
				targetID = conv.ID
			}
		} else {
			targetID = target
		}
	}

	if targetID == "" {
		return "⚠️ No active conversation to archive."
	}

	if err := e.storage.SetConversationArchived(ctx, targetID, true); err != nil {
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

		var sb strings.Builder
		sb.WriteString("⚡ <b>AI Model Selection</b>\n")
		sb.WriteString(fmt.Sprintf("• <b>Active Model:</b> <code>%s</code> (%s)\n", resolvedModel, source))
		if resolvedEffort != "" {
			sb.WriteString(fmt.Sprintf("• <b>Reasoning Effort:</b> <code>%s</code>\n", resolvedEffort))
		}
		sb.WriteString("\n<b>Available Model Tiers:</b>\n")
		for _, cap := range domain.DefaultModelCapabilities {
			effDesc := "No thinking"
			if len(cap.SupportedEfforts) > 0 {
				effDesc = strings.Join(cap.SupportedEfforts, ", ")
			}
			sb.WriteString(fmt.Sprintf("• <b>%s</b> (<code>%s</code>) — Effort: [%s]\n", cap.DisplayName, cap.ID, effDesc))
		}
		sb.WriteString("\n<i>💡 Click a button below or type <code>/model &lt;name&gt;</code> to switch model.</i>")

		inlineKb := domain.InlineKeyboard{
			{
				{Text: "⚡ Gemini 3.7 Flash", CallbackData: "m:set:gemini-3.7-flash"},
				{Text: "🚀 Gemini 3.1 Pro", CallbackData: "m:set:gemini-3.1-pro"},
			},
			{
				{Text: " Claude Sonnet 4.6", CallbackData: "m:set:claude-sonnet-4-6"},
				{Text: "🧠 Claude Opus 4.6", CallbackData: "m:set:claude-opus-4-6-thinking"},
			},
			{
				{Text: "🔄 Reset to Default", CallbackData: "m:reset"},
			},
		}

		return sb.String(), inlineKb
	}

	target := strings.TrimSpace(args[0])
	if strings.EqualFold(target, "reset") || strings.EqualFold(target, "default") {
		session.ActiveModel = ""
		if err := e.storage.SaveSession(ctx, session); err != nil {
			return fmt.Sprintf("⚠️ Failed to reset model: %v", err), nil
		}
		resolvedModel, _, source := e.ResolveExecutionParams("", "", session, agentObj)
		return fmt.Sprintf("🔄 <b>Model override reset.</b>\nNow using <code>%s</code> (%s).", resolvedModel, source), nil
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
		customNote = "\n<i>(Custom model pass-through to agy CLI)</i>"
	}

	return fmt.Sprintf("⚡ <b>Active model switched to:</b> <code>%s</code> for this session.%s", canonicalModel, customNote), nil
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
		sb.WriteString("🧠 <b>Reasoning Effort Selection</b>\n")
		sb.WriteString(fmt.Sprintf("• <b>Current Effort:</b> <code>%s</code>\n", resolvedEffort))
		if resolvedModel != "" {
			sb.WriteString(fmt.Sprintf("• <b>Active Model:</b> <code>%s</code>\n", resolvedModel))
		}
		sb.WriteString("\n<b>Effort Levels:</b>\n")
		sb.WriteString("• <b>low</b> 🟢 — Quick thinking budget, low latency\n")
		sb.WriteString("• <b>medium</b> 🟡 — Balanced reasoning budget\n")
		sb.WriteString("• <b>high</b> 🔴 — Maximum thinking depth & verification\n")
		sb.WriteString("• <b>none</b> ⚪ — Disable thinking tokens (direct output)\n")
		sb.WriteString("\n<i>💡 Click a button below or type <code>/effort &lt;level&gt;</code> to switch.</i>")

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
		return fmt.Sprintf("🔄 <b>Reasoning effort reset.</b>\nNow using <code>%s</code> (%s).", resolvedEffort, source), nil
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
			warningNote = fmt.Sprintf("\n⚠️ <i>Note: Model <code>%s</code> does not support reasoning effort. The <code>--effort</code> flag will be automatically omitted during execution.</i>", resolvedModel)
		} else {
			supported := false
			for _, se := range cap.SupportedEfforts {
				if se == target {
					supported = true
					break
				}
			}
			if !supported {
				warningNote = fmt.Sprintf("\n⚠️ <i>Note: Model <code>%s</code> only supports [%s]. Requested effort <code>%s</code> will be clamped to <code>%s</code> during execution.</i>",
					resolvedModel, strings.Join(cap.SupportedEfforts, ", "), target, cap.DefaultEffort)
			}
		}
	}

	return fmt.Sprintf("🧠 <b>Reasoning effort set to:</b> <code>%s</code> for this session.%s", target, warningNote), nil
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
	if total == 0 {
		return "📋 <b>No background sub-agent tasks found for this session.</b>\n\nMain Agent automatically delegates long-running tasks via <code>dispatch_subagent</code>.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📋 <b>Sub-Agent Tasks (%d total):</b>\n\n", total))

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

		sb.WriteString(fmt.Sprintf("%s <b><code>%s</code></b> | @%s\n", badge, t.ID, t.AgentName))
		sb.WriteString(fmt.Sprintf("   • <b>Title:</b> %s\n", t.Title))
		sb.WriteString(fmt.Sprintf("   • <b>Status:</b> <code>%s</code>", t.Status))
		if t.DurationSeconds > 0 {
			sb.WriteString(fmt.Sprintf(" (%.1fs)", t.DurationSeconds))
		}
		if t.Status == domain.TaskStatusWaitingInput && t.PendingQuestion != "" {
			sb.WriteString(fmt.Sprintf("\n   • ❓ <b>Question:</b> <i>%s</i>", t.PendingQuestion))
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

	sb.WriteString("💡 <i>Use <code>/task &lt;id&gt;</code> to inspect details or <code>/task reply &lt;id&gt; &lt;input&gt;</code> to respond.</i>")
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
			return "⚠️ Usage: <code>/task cancel &lt;task_id&gt;</code>", nil
		}
		taskID := args[1]
		if err := e.subagentDispatcher.CancelTask(ctx, taskID); err != nil {
			return fmt.Sprintf("⚠️ Failed to cancel task <code>%s</code>: %v", taskID, err), nil
		}
		return fmt.Sprintf("🛑 <b>Task <code>%s</code> has been cancelled</b> and its process tree terminated.", taskID), nil

	case "reply":
		if len(args) < 3 {
			return "⚠️ Usage: <code>/task reply &lt;task_id&gt; &lt;your response&gt;</code>", nil
		}
		taskID := args[1]
		replyText := strings.Join(args[2:], " ")
		if err := e.subagentDispatcher.SendTaskInput(ctx, taskID, replyText); err != nil {
			return fmt.Sprintf("⚠️ Failed to send reply to task <code>%s</code>: %v", taskID, err), nil
		}
		return fmt.Sprintf("✅ <b>Reply injected into Task <code>%s</code>!</b> Sub-Agent has resumed background execution.", taskID), nil

	case "clean", "purge":
		purged, err := e.storage.PurgeSubagentTasks(ctx, 0)
		if err != nil {
			return fmt.Sprintf("⚠️ Failed to purge tasks: %v", err), nil
		}
		return fmt.Sprintf("🧹 <b>Cleaned up %d completed/failed/cancelled subagent tasks.</b>", purged), nil

	default:
		// Assume args[0] is task_id
		taskID := args[0]
		task, err := e.subagentDispatcher.GetTask(ctx, taskID)
		if err != nil {
			return fmt.Sprintf("⚠️ Task <code>%s</code> not found: %v", taskID, err), nil
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("📋 <b>Sub-Agent Task Details: <code>%s</code></b>\n\n", task.ID))
		sb.WriteString(fmt.Sprintf("• <b>Agent:</b> <code>@%s</code>\n", task.AgentName))
		sb.WriteString(fmt.Sprintf("• <b>Title:</b> %s\n", task.Title))
		sb.WriteString(fmt.Sprintf("• <b>Status:</b> <code>%s</code>\n", task.Status))
		sb.WriteString(fmt.Sprintf("• <b>Model / Effort:</b> <code>%s</code> (<code>%s</code>)\n", task.Model, task.Effort))
		sb.WriteString(fmt.Sprintf("• <b>Workspace Mode:</b> <code>%s</code>\n", task.WorkspaceMode))
		sb.WriteString(fmt.Sprintf("• <b>Callback Mode:</b> <code>%s</code>\n", task.CallbackMode))
		sb.WriteString(fmt.Sprintf("• <b>Duration:</b> <code>%.2fs</code>\n", task.DurationSeconds))
		sb.WriteString(fmt.Sprintf("• <b>Tokens Used:</b> <code>%d</code>\n", task.Usage.TotalTokens))

		if task.Status == domain.TaskStatusRunning {
			sb.WriteString(fmt.Sprintf("\n⚡ <b>Live Execution:</b> Step %d | Tool: <code>%s</code>\n%s\n", task.CurrentStep, task.CurrentTool, task.ProgressMessage))
		}

		if task.Status == domain.TaskStatusWaitingInput {
			sb.WriteString(fmt.Sprintf("\n⏸️ <b>Waiting for Input:</b>\n❓ <i>%s</i>\n\n👉 <b>To reply:</b> <code>/task reply %s &lt;your response&gt;</code>\n", task.PendingQuestion, task.ID))
		}

		if task.Status == domain.TaskStatusCompleted && task.ResultSummary != "" {
			sb.WriteString("\n📝 <b>Result Summary:</b>\n" + task.ResultSummary + "\n")
		}

		if task.Status == domain.TaskStatusFailed && task.ErrorMessage != "" {
			sb.WriteString("\n❌ <b>Error:</b> " + task.ErrorMessage + "\n")
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

func (e *Engine) isSenderAdmin(sender domain.SenderUser) bool {
	if e.cfg == nil {
		return true
	}
	if len(e.cfg.Telegram.AdminUserIDs) == 0 && len(e.cfg.Security.AdminUserIDs) == 0 {
		return true
	}
	id, err := strconv.ParseInt(sender.ID, 10, 64)
	if err != nil {
		return false
	}
	for _, admin := range e.cfg.Telegram.AdminUserIDs {
		if admin == id {
			return true
		}
	}
	for _, admin := range e.cfg.Security.AdminUserIDs {
		if admin == id {
			return true
		}
	}
	return false
}

func (e *Engine) handleSecurityCommand(sender domain.SenderUser, sessionKey string, args []string) (string, domain.InlineKeyboard) {
	if e.securityManager == nil {
		return "⚠️ <b>Security Gateway is not active.</b>", nil
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
		if !e.isSenderAdmin(sender) {
			return "⛔ <b>Unauthorized: Only administrators can modify security gateway settings.</b>", nil
		}

		subcmd := strings.ToLower(args[0])
		switch subcmd {
		case "preset":
			if len(args) < 2 {
				return "⚠️ Usage: <code>/security preset &lt;unrestricted|developer|balanced|strict|read_only&gt;</code>", nil
			}
			preset := domain.SecurityPreset(strings.ToLower(args[1]))
			switch preset {
			case domain.PresetUnrestricted, domain.PresetDeveloper, domain.PresetBalanced, domain.PresetStrict, domain.PresetReadOnly:
				// Valid preset name
			default:
				return fmt.Sprintf("⚠️ Invalid security preset: <code>%s</code>. Valid options: <code>unrestricted, developer, balanced, strict, read_only</code>", args[1]), nil
			}

			// Validate monotonic upgrade rule: cannot switch to less secure level than baseline
			if !domain.CanSwitchPreset(baselinePreset, preset) {
				allowedPresets := domain.GetAllowedPresets(baselinePreset)
				var allowedStrs []string
				for _, p := range allowedPresets {
					allowedStrs = append(allowedStrs, fmt.Sprintf("<code>%s</code>", p))
				}
				return fmt.Sprintf("⛔ <b>Cannot downgrade security preset:</b> Agent <code>%s</code> current baseline security level is <code>%s</code>. You can only switch to equal or more secure presets (allowed: %s).",
					activeAgentName, baselinePreset, strings.Join(allowedStrs, ", ")), nil
			}

			e.securityManager.SetPreset(preset)
			if agent != nil && e.storage != nil {
				agent.SecurityPreset = preset
				agent.UpdatedAt = time.Now()
				_ = e.storage.SaveAgent(context.Background(), agent)
			}
			return fmt.Sprintf("🛡️ <b>Security preset successfully switched to:</b> <code>%s</code> for agent <code>%s</code>", preset, activeAgentName), nil

		case "grant":
			if len(args) < 2 {
				return "⚠️ Usage: <code>/security grant &lt;pattern|scope&gt;</code>", nil
			}
			pattern := args[1]
			e.securityManager.GrantSessionPermission(sessionKey, pattern)
			return fmt.Sprintf("🛡️ <b>Temporary permission granted for session:</b> <code>%s</code> (valid for 15 minutes)", pattern), nil

		case "redact":
			if len(args) < 2 {
				return "⚠️ Usage: <code>/security redact &lt;strict|permissive|audit_only&gt;</code>", nil
			}
			mode := domain.RedactionMode(strings.ToLower(args[1]))
			e.securityManager.SetRedactionMode(mode)
			return fmt.Sprintf("🎭 <b>Secret Redaction mode switched to:</b> <code>%s</code>", mode), nil
		}
	}

	// Default: Show Dashboard with Interactive Preset Switcher Buttons (filtered to >= baseline)
	summary := e.securityManager.GetDashboardSummary(sessionKey)
	summary.Preset = baselinePreset

	var sb strings.Builder
	sb.WriteString("🛡️ <b>[Agyent Security Gateway Dashboard]</b>\n")
	sb.WriteString("━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	sb.WriteString(fmt.Sprintf("🤖 <b>Active Agent</b>      : <code>%s</code>\n", activeAgentName))
	sb.WriteString(fmt.Sprintf("📍 <b>Active Preset</b>     : <code>%s</code>\n", summary.Preset))
	sb.WriteString(fmt.Sprintf("📂 <b>Workspace Jail</b>    : <code>%s</code>\n", summary.ActiveJail))
	sb.WriteString(fmt.Sprintf("🎭 <b>Redaction Mode</b>    : <code>%s</code>\n", summary.RedactionMode))
	sb.WriteString(fmt.Sprintf("⚙️ <b>Delegated Config</b>  : <code>%t</code>\n", summary.ConfigDelegated))
	sb.WriteString(fmt.Sprintf("🛑 <b>Blocked Today</b>     : <code>%d</code> events\n", summary.BlockedToday))
	sb.WriteString(fmt.Sprintf("✅ <b>Approved Today</b>    : <code>%d</code> events\n", summary.ApprovedToday))
	sb.WriteString(fmt.Sprintf("⚡ <b>Total Evaluations</b> : <code>%d</code> checks\n", summary.TotalEvaluations))
	sb.WriteString("━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	sb.WriteString("💡 <i>Use buttons below to switch profiles or toggle redaction.</i>")

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
		return "⚠️ <b>Security Gateway is not active.</b>"
	}

	if len(args) >= 2 && strings.ToLower(args[0]) == "add" {
		if !e.isSenderAdmin(sender) {
			return "⛔ <b>Unauthorized: Only administrators can modify security whitelist rules.</b>"
		}
		entry := strings.Join(args[1:], " ")
		e.securityManager.AddWhitelistEntry(entry)
		return fmt.Sprintf("✅ <b>Added custom whitelist rule:</b> <code>%s</code>", entry)
	}

	return "⚠️ Usage: <code>/whitelist add &lt;command_or_path&gt;</code>\nExample: <code>/whitelist add \"npm run build\"</code>"
}
