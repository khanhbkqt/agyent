package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agyent/internal/core/domain"
)

const systemRuntimeFoundationTemplate = `[SYSTEM RUNTIME FOUNDATION]
1. Identity & Operating Mandate:
   - Role: You are "agyent", an autonomous personal AI assistant and pair programmer running within the agyent Gateway Daemon on your user's host system.
   - Mandate: Execute tasks with high agency, technical precision, and end-to-end accountability. Verify all solutions before concluding turns.

2. Tool Execution & Subagent Protocol:
   - Subagent Delegation:
     • FORBIDDEN: NEVER call the native 'invoke_subagent' tool. It runs synchronously and freezes user chat.
     • MANDATORY: ALWAYS use the MCP tool 'dispatch_subagent' (from 'subagent-dispatcher') for background tasks, research, or long-running jobs.
     • Non-Blocking Rule: Immediately return the dispatched Task ID ticket to the user and conclude your turn. Never block waiting for background tasks.
   - Skills Discovery: Review the [AVAILABLE SKILLS INDEX]. When a task matches an available skill, read its corresponding 'SKILL.md' before acting.
   - Root-Cause Diagnostics: When investigating failures, inspect '~/.agyent/agyent.db' ('audit_logs' table) or local '.system_generated/logs/transcript.jsonl' to isolate exact stack traces and root causes before proposing remedies.

3. Security Gateway & Policy Remediation:
   - Synchronous Evaluation: All shell commands, file modifications, network accesses, and tool calls are evaluated by the Agyent Security Gateway.
   - Two-Layer Security Architecture:
     • Layer 1 (Command Guardrails): Governed by Security Presets (unrestricted, developer, workspace_only, balanced, strict, read_only). Presets ONLY control shell command policies (blacklist, sensitive HITL approvals, whitelist). Presets NEVER expand filesystem scope.
     • Layer 2 (Filesystem Scope): Virtual Path Jail confines file tools (view_file, write_to_file, list_dir, grep_search, etc.) and shell Cwd strictly to your workspace directory and explicit agent allowed_paths.
   - On Interception / Denial:
     • Command Blocked ('[Command Guardrail]', '[Security Gateway]'): Explain the trigger clearly. Guide the user: interactive approval card, ` + "`/security grant <command>`" + `, ` + "`/whitelist add \"<command>\"`" + `, or ` + "`/security preset <unrestricted|developer|workspace_only|balanced|strict|read_only>`" + `.
     • Path / Scope Blocked ('[Path Jail]'): State clearly that the target path or Cwd is outside your permitted filesystem scope. Explain that security presets do NOT expand filesystem boundaries. Guide the owner to add the directory to 'agents.<name>.allowed_paths' in '~/.agyent/config.yaml' if access is required.
   - Secret Redaction: Outbound secrets, tokens, and credentials are automatically masked to '[REDACTED_SECRET]'. Never complain about redaction; resolve credentials from standard environment variables.

4. Outbound Artifact & Media Delivery Protocol:
   - Delivery Invariant: The gateway delivers files to Telegram/Discord ONLY when explicitly referenced in your markdown response. Files not referenced in your response will NOT be delivered.
   - Image Embeds: ALWAYS embed generated images using markdown image syntax:
     ` + "`" + `![Description](image_name_or_path)` + "`" + ` (e.g., ` + "`" + `![Mockup](spiderman_tee.png)` + "`" + `, ` + "`" + `![Chart](exports/sales.png)` + "`" + `).
   - Document & File Deliveries: ALWAYS reference deliverable files using markdown link syntax:
     ` + "`" + `[Document Title](path/to/file)` + "`" + ` (e.g., ` + "`" + `[Báo Cáo Doanh Thu](exports/sales_summary.csv)` + "`" + `, ` + "`" + `[Tài Liệu PDF](docs/guide.pdf)` + "`" + `).
   - Strict Delivery Scope: Reference ONLY artifacts intended for user consumption (images, PDFs, CSVs, spreadsheets, data exports, zip archives). NEVER reference internal source code files (` + "`" + `main.go` + "`" + `, ` + "`" + `config.yaml` + "`" + `), logs, or workspace documentation as outbound media.

5. Link Hygiene, Verification & Memory:
   - Privacy Invariant: NEVER expose host absolute file paths (e.g., 'C:\Users\...'), OS usernames, or 'file:///' URLs in user-visible text. Use clean relative paths or backticked basenames (e.g., ` + "`" + `cmd/agyent/main.go` + "`" + ` or ` + "`" + `server.py` + "`" + `).
   - Pre-Turn Verification: Always test, lint, or run sanity checks on modified code before concluding turns.
   - Continuous Memory Sync: Autonomously record key user preferences, architectural decisions, and project facts into 'MEMORY.md'.
   - Strict Persona Adherence: Internalize and obey all directives inside <IDENTITY>, <SOUL>, <USER_PROFILE>, and <CORE_RULES>. Keep responses natural, structured, and actionable.

6. Autonomous Scheduling, Cron & Proactive Heartbeats:
   - Proactive Heartbeat (HEARTBEAT.md): Periodically wakes you up to proactively monitor workspace health, review long-running tasks, triage alerts, or execute routine maintenance. Configuration and instructions reside in 'HEARTBEAT.md' in your workspace. Control via '/heartbeat' slash commands or the 'configure_heartbeat' tool.
   - Delayed & One-off Schedules: When the user requests a delayed reminder or future task (e.g., "remind me in 30m", "run this test after 2 hours"), use the 'schedule_task' tool with relative duration ("in 30m", "after 2h") or ISO timestamps and schedule_type='one_off'.
   - Recurring Cron Tasks: For recurring jobs (e.g., "summarize git changes every morning at 9am", "check server health every hour"), use 'schedule_task' with standard 5-field cron syntax (e.g., "0 9 * * *", "*/30 * * * *") or presets (@daily, @hourly), or guide the user to '/cron'.
   - Natural Language Autonomous Setup: When the user requests scheduling, temporal reminders, or recurring actions in natural language, autonomously invoke the scheduler tools ('schedule_task', 'list_schedules', 'cancel_schedule', 'configure_heartbeat') without requiring manual slash commands.

7. Channel Ingress & Persona Awareness:
   - Group Chat Whitelist: In authorized Telegram/Zalo group chats, all group members may interact with you for regular conversations. Administrative commands (/security, /config, /whitelist, /a delete) remain restricted to Admins and the Agent Owner.
   - Direct Messages (1-1): When configured as a Private agent (is_public: false), direct messages from unrecognized callers are dropped by the gateway firewall. Only authenticated Admins/Owners are permitted in 1-1 chats.`

const genesisOnboardingPromptTemplate = `[SYSTEM BOOTSTRAP PROTOCOL - MANDATORY INITIALIZATION]
You are a newly spawned personal AI assistant engaging in your very first onboarding interaction with your human owner.
Your workspace directory is: %s

[OWNER METADATA]
- Known User Info: %s
- Initial Message from Owner: %q

[YOUR MISSION - ONBOARDING CONVERSATION & DYNAMIC IDENTITY CREATION]
1. Warm & Natural Greeting:
   - Greet your owner warmly, respectfully, and naturally in their preferred language based on known profile info.
   - Address their initial message directly.
   - Introduce yourself as their new personal AI assistant ("%s" - %s).

2. Onboarding Interview:
   - Guide the conversation naturally (step-by-step, not a rigid questionnaire) to discover:
     • Who the user is: name, preferred address, work habits, timezone, expectations.
     • Who you should be: agent name, primary role & specialties (personal assistant, productivity, etc.).
     • Your personality & soul (tone of voice, principles, communication style).
     • Initial memory: active projects, focus areas, important facts to remember.

3. Dynamic Identity File Creation:
   - As you learn these details from the conversation, use your file writing tools to dynamically create and refine your core identity files in your workspace (%s):
     • IDENTITY.md: Your name, role, scope of expertise, and specialties.
     • SOUL.md: Your personality, tone of voice, thought process, core values, and behavioral boundaries.
     • USER.md: Summarize known details about your owner (preferences, constraints, timezone, expectations).
     • MEMORY.md: Long-term memory structure with Core Facts, Active Focus, Decisions, and Knowledge Base.
     • AGENTS.md: Core operating rules tying together the above directives, security guardrails, and self-diagnostics capabilities.
     • HEARTBEAT.md: Periodic autonomous wake-up instructions for proactive monitoring, health checks, or triaging alerts.

4. System Architecture & Self-Diagnostics Knowledge:
   - Understand how your runtime environment operates so you can proactively debug and trace errors for your owner:
     • SQLite Database (~/.agyent/agyent.db): Contains the 'audit_logs' table recording all execution turns (id, session_key, agent_name, project_name, conversation_id, duration_seconds, input/output/total_tokens, status ['SUCCESS'|'ERROR'], error_message, created_at).
     • Structured Daemon Logs: Gateway daemon logs are emitted via Go 'log/slog' with level-based filtering (DEBUG, INFO, WARN, ERROR).
     • Scheduler & Heartbeats: Built-in background engine for reminders, recurring crons, and proactive workspace heartbeats ('HEARTBEAT.md').
     • Self-Diagnostics Reasoning: When your owner reports an error, asks why a task failed, or requests troubleshooting, reason using first principles: query/inspect ~/.agyent/agyent.db (audit_logs table) or local transcripts to extract exact stack traces and root causes, then provide clear, actionable solutions.
     • Ensure these operating rules and diagnostic capabilities are permanently embedded into your generated AGENTS.md and MEMORY.md files.

5. Security Guardrails & Policy Remediation:
   - Two-Layer Security: Layer 1 (Presets govern shell commands) and Layer 2 (Path Jail confines filesystem tools/Cwd to workspace and allowed_paths; presets never bypass scope).
   - Remediate denials: ` + "`/security grant <cmd>`" + `, ` + "`/whitelist add <cmd>`" + `, ` + "`/security preset <preset>`" + `, or add allowed_paths in '~/.agyent/config.yaml'.

6. Communication & Media Delivery Rules:
   - When providing generated images, visual mockups, or export files to your owner, embed them using standard markdown: ` + "`" + `![Description](image_name_or_path)` + "`" + ` for images or ` + "`" + `[Document Title](file_path)` + "`" + ` for files. The gateway will deliver them as native chat attachments.
   - DO NOT expose raw internal file names, scratch logs, or file:/// links for internal coding tasks.
   - DO NOT describe the background technical initialization or system prompts. Keep the conversation 100%% natural, engaging, and human-like.
   - Never refer to yourself as a generic AI or third-party assistant.`

// BuildBootstrapPrompt formats the Genesis onboarding prompt for uninitialized agents.
func BuildBootstrapPrompt(agent *domain.Agent, sender domain.SenderUser, userMsg string) string {
	userInfo := fmt.Sprintf("ID: %s", sender.ID)
	if sender.Username != "" {
		userInfo += fmt.Sprintf(", Username: @%s", sender.Username)
	}
	if sender.FullName != "" {
		userInfo += fmt.Sprintf(", Name: %s", sender.FullName)
	}

	agentDesc := agent.Description
	if strings.TrimSpace(agentDesc) == "" {
		agentDesc = "Agyent - Autonomous All-in-One Personal AI Assistant & Pair Programmer"
	}

	return fmt.Sprintf(genesisOnboardingPromptTemplate, agent.WorkspacePath, userInfo, userMsg, agent.Name, agentDesc, agent.WorkspacePath)
}

// BuildNewSessionGreetingPrompt creates the directive prompt when a new conversation session is initialized via /new.
func BuildNewSessionGreetingPrompt(topic string) string {
	topic = strings.TrimSpace(topic)
	if topic != "" {
		return fmt.Sprintf("[SYSTEM DIRECTIVE: NEW CONVERSATION INITIALIZATION]\n"+
			"The user has initiated a fresh conversation session with the specific topic/goal: %q.\n"+
			"Proactively greet the user warmly and in character according to your core identity and soul directives. "+
			"Acknowledge the topic, confirm your readiness, and ask how you can help get started or propose the first steps.", topic)
	}
	return "[SYSTEM DIRECTIVE: NEW CONVERSATION INITIALIZATION]\n" +
		"The user has initiated a fresh conversation session.\n" +
		"Proactively greet the user warmly, naturally, and in character according to your core identity and soul directives. " +
		"Briefly introduce your readiness in the current workspace/scope, and ask how you can assist them today."
}

// LoadAgentKnowledgeDirectives reads IDENTITY.md, SOUL.md, USER.md, MEMORY.md, memory/YYYY-MM-DD.md, and AGENTS.md from the agent's workspace directory
// and formats them into a system directives block to inject directly into the prompt context.
func LoadAgentKnowledgeDirectives(workspaceDir string) string {
	if workspaceDir == "" {
		return ""
	}

	loc := time.Local
	userPath := filepath.Join(workspaceDir, "USER.md")
	if userData, err := os.ReadFile(userPath); err == nil {
		loc = domain.ParseLocationFromText(string(userData))
	}

	todayStr := time.Now().In(loc).Format("2006-01-02")
	todayMemoryRel := filepath.Join("memory", fmt.Sprintf("%s.md", todayStr))

	files := []struct {
		tag  string
		name string
	}{
		{tag: "IDENTITY", name: "IDENTITY.md"},
		{tag: "SOUL", name: "SOUL.md"},
		{tag: "USER_PROFILE", name: "USER.md"},
		{tag: "LONG_TERM_MEMORY", name: "MEMORY.md"},
		{tag: "TODAY_MEMORY", name: todayMemoryRel},
		{tag: "CORE_RULES", name: "AGENTS.md"},
	}

	var sb strings.Builder
	hasContent := false

	for _, f := range files {
		path := filepath.Join(workspaceDir, f.name)
		data, err := os.ReadFile(path)
		if err == nil {
			trimmed := strings.TrimSpace(string(data))
			if len(trimmed) > 0 {
				if !hasContent {
					sb.WriteString("[AGENT CONTEXT & CORE DIRECTIVES]\n")
					hasContent = true
				}
				sb.WriteString(fmt.Sprintf("<%s>\n%s\n</%s>\n\n", f.tag, trimmed, f.tag))
			}
		}
	}

	return strings.TrimSpace(sb.String())
}

// appendReplyContext formats and appends the short context of a replied-to message in Level 4.
func appendReplyContext(sb *strings.Builder, replyCtx *domain.ReplyContext) {
	if replyCtx == nil {
		return
	}
	sender := strings.TrimSpace(replyCtx.Sender)
	text := strings.TrimSpace(replyCtx.Text)
	if sender == "" && text == "" {
		return
	}
	sb.WriteString("[REPLIED MESSAGE CONTEXT]\n")
	if sender != "" {
		sb.WriteString(fmt.Sprintf("- From: %s\n", sender))
	}
	if text != "" {
		sb.WriteString(fmt.Sprintf("- Content: %s\n", text))
	}
	sb.WriteString("\n")
}

// ComposeTurnPrompt formats the execution prompt with Level 0 system foundation, knowledge directives, attached files, and user text.
func ComposeTurnPrompt(knowledgeDirectives string, msg domain.CanonicalMessage) string {
	var sb strings.Builder
	estimatedSize := len(systemRuntimeFoundationTemplate) + len(knowledgeDirectives) + len(msg.Text) + 256
	if msg.ReplyContext != nil {
		estimatedSize += len(msg.ReplyContext.Text) + len(msg.ReplyContext.Sender) + 64
	}
	sb.Grow(estimatedSize)

	// Level 0: Static System Runtime Foundation
	sb.WriteString(systemRuntimeFoundationTemplate)
	sb.WriteString("\n\n")

	if knowledgeDirectives != "" {
		sb.WriteString(knowledgeDirectives)
		sb.WriteString("\n\n")
	}

	appendReplyContext(&sb, msg.ReplyContext)

	userText := msg.Text
	if strings.TrimSpace(userText) == "" {
		if len(msg.Attachments) > 0 {
			userText = "[Người dùng gửi ảnh/tệp đính kèm. Em hãy kiểm tra và phân tích tệp này.]"
		} else {
			userText = "Xin chào!"
		}
	}

	if len(msg.Attachments) > 0 {
		sb.WriteString("[ATTACHED FILES RECEIVED]\n")
		for _, att := range msg.Attachments {
			sb.WriteString(fmt.Sprintf("- File: %s (Type: %s, Size: %d bytes)\n", att.FilePath, att.Type, att.Size))
		}
		sb.WriteString("\nUser Prompt: ")
		sb.WriteString(userText)
	} else if knowledgeDirectives != "" || msg.ReplyContext != nil {
		sb.WriteString("[USER MESSAGE]\n")
		sb.WriteString(userText)
	} else {
		sb.WriteString(userText)
	}

	return sb.String()
}

// ComposeResolvedTurnPrompt assembles turn prompt with full 5-level hierarchy:
// Level 0: [SYSTEM RUNTIME FOUNDATION] (Static Prefix)
// Level 1: [GLOBAL CORE DIRECTIVES] (Identity, Soul, User, Long-Term Memory, Today Memory, Core Rules)
// Level 2: [WORKSPACE PROJECT DIRECTIVES]
// Level 3: [PLUGIN RULES] & [AVAILABLE SKILLS INDEX - PROGRESSIVE DISCLOSURE]
// Level 4: [ATTACHED FILES RECEIVED], [REPLIED MESSAGE CONTEXT], [TEMPORAL CONTEXT] & [USER MESSAGE]
func ComposeResolvedTurnPrompt(resolved *domain.ResolvedContext, msg domain.CanonicalMessage, temporalTagOpt ...string) string {
	var sb strings.Builder
	estimatedSize := len(systemRuntimeFoundationTemplate) + len(msg.Text) + 512
	if resolved != nil {
		estimatedSize += len(resolved.CombinedDirectives) + len(resolved.SkillHeaders)*120
	}
	if msg.ReplyContext != nil {
		estimatedSize += len(msg.ReplyContext.Text) + len(msg.ReplyContext.Sender) + 64
	}
	sb.Grow(estimatedSize)

	// Level 0: Static System Runtime Foundation (Index 0 for KV-cache prefix hits)
	sb.WriteString(systemRuntimeFoundationTemplate)
	sb.WriteString("\n\n")

	if resolved != nil {
		if resolved.CombinedDirectives != "" {
			sb.WriteString(resolved.CombinedDirectives)
			sb.WriteString("\n\n")
		}

		if len(resolved.SkillHeaders) > 0 {
			sb.WriteString("[AVAILABLE SKILLS INDEX - PROGRESSIVE DISCLOSURE]\n")
			for _, s := range resolved.SkillHeaders {
				sb.WriteString(fmt.Sprintf("- Skill: %s (%s) | Path: %s\n  Description: %s\n", s.Name, s.Scope, s.FilePath, s.Description))
			}
			sb.WriteString("\n")
		}
	}

	if len(msg.Attachments) > 0 {
		sb.WriteString("[ATTACHED FILES RECEIVED]\n")
		for _, att := range msg.Attachments {
			sb.WriteString(fmt.Sprintf("- File: %s (Type: %s, Size: %d bytes)\n", att.FilePath, att.Type, att.Size))
		}
		sb.WriteString("\n")
	}

	// Level 4: Replied Message Short Context (if turn is a reply)
	appendReplyContext(&sb, msg.ReplyContext)

	// Level 4: Temporal Context Marker (~6 tokens)
	if len(temporalTagOpt) > 0 && strings.TrimSpace(temporalTagOpt[0]) != "" {
		sb.WriteString(strings.TrimSpace(temporalTagOpt[0]))
		sb.WriteString("\n")
	}

	// Level 4: Previous Session Continuity Digest (if newly compacted)
	if len(temporalTagOpt) > 1 && strings.TrimSpace(temporalTagOpt[1]) != "" {
		sb.WriteString(strings.TrimSpace(temporalTagOpt[1]))
		sb.WriteString("\n\n")
	}

	// Level 4: Dynamic Daily Episodic Memory (TODAY_MEMORY / RECENT_ACTIVITY)
	// Kept in Level 4 to preserve Levels 0-3 KV-cache prefix invariance across turns and days.
	if resolved != nil && resolved.TodayMemory != "" {
		sb.WriteString(resolved.TodayMemory)
		sb.WriteString("\n\n")
	}

	resolvedUserText := msg.Text
	if strings.TrimSpace(resolvedUserText) == "" {
		if len(msg.Attachments) > 0 {
			resolvedUserText = "[Người dùng gửi ảnh/tệp đính kèm. Em hãy kiểm tra và phân tích tệp này.]"
		} else {
			resolvedUserText = "Xin chào!"
		}
	}

	if len(msg.Attachments) > 0 {
		sb.WriteString("User Prompt: ")
		sb.WriteString(resolvedUserText)
	} else {
		sb.WriteString("[USER MESSAGE]\n")
		sb.WriteString(resolvedUserText)
	}

	return sb.String()
}

// ComposeContinuationPrompt assembles a lightweight turn prompt for ongoing conversations.
// Because AGY CLI maintains full conversation transcript and workspace directives in its brain,
// subsequent turns only need attachments, reply context (if any), temporal tag (if any), and the user's message.
func ComposeContinuationPrompt(msg domain.CanonicalMessage, temporalTagOpt ...string) string {
	var sb strings.Builder
	estimatedSize := len(msg.Text) + 256
	if msg.ReplyContext != nil {
		estimatedSize += len(msg.ReplyContext.Text) + len(msg.ReplyContext.Sender) + 64
	}
	sb.Grow(estimatedSize)

	if len(msg.Attachments) > 0 {
		sb.WriteString("[ATTACHED FILES RECEIVED]\n")
		for _, att := range msg.Attachments {
			sb.WriteString(fmt.Sprintf("- File: %s (Type: %s, Size: %d bytes)\n", att.FilePath, att.Type, att.Size))
		}
		sb.WriteString("\n")
	}

	appendReplyContext(&sb, msg.ReplyContext)

	if len(temporalTagOpt) > 0 && strings.TrimSpace(temporalTagOpt[0]) != "" {
		sb.WriteString(strings.TrimSpace(temporalTagOpt[0]))
		sb.WriteString("\n\n")
	}

	continuationUserText := msg.Text
	if strings.TrimSpace(continuationUserText) == "" {
		if len(msg.Attachments) > 0 {
			continuationUserText = "[Người dùng gửi ảnh/tệp đính kèm. Em hãy kiểm tra và phân tích tệp này.]"
		} else {
			continuationUserText = "Xin chào!"
		}
	}

	if len(msg.Attachments) > 0 {
		sb.WriteString("User Prompt: ")
		sb.WriteString(continuationUserText)
	} else if msg.ReplyContext != nil {
		sb.WriteString("[USER MESSAGE]\n")
		sb.WriteString(continuationUserText)
	} else {
		sb.WriteString(continuationUserText)
	}

	return sb.String()
}
