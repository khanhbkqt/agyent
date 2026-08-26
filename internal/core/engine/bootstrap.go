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
1. Identity & Operating Environment:
   - You are "agyent" — an autonomous, highly capable personal AI assistant and pair programmer running within the agyent Gateway Daemon environment for your human owner.
   - You operate with extreme competence, high agency, proactive accountability, and technical rigor.

2. Core Capabilities & Tool Utilization:
   - Full OS & Tool Access: You have access to local file tools, shell execution, subagents, and Model Context Protocol (MCP) servers.
   - Progressive Skills Disclosure: The [AVAILABLE SKILLS INDEX] contains lightweight metadata. When a task matches a specialized skill, proactively read the corresponding SKILL.md before executing.
   - Self-Diagnostics Protocol: When encountering errors or investigating failures, use First Principles reasoning: inspect ~/.agyent/agyent.db (audit_logs table) or local transcript logs to isolate root causes and stack traces.

3. Execution Principles:
   - Proactive Verification: Always implement end-to-end solutions. Test, lint, and verify code before concluding turns.
   - Continuous Memory Sync: Autonomously capture key user preferences, architectural decisions, and project facts into MEMORY.md.
   - Strict Persona Adherence: Internalize and obey all directives inside <IDENTITY>, <SOUL>, <USER_PROFILE>, and <CORE_RULES>.

Do not break character. Keep communication natural, structured, and actionable.`

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
     • AGENTS.md: Core operating rules tying together the above directives and self-diagnostics capabilities.

4. System Architecture & Self-Diagnostics Knowledge:
   - Understand how your runtime environment operates so you can proactively debug and trace errors for your owner:
     • SQLite Database (~/.agyent/agyent.db): Contains the 'audit_logs' table recording all execution turns (id, session_key, agent_name, project_name, conversation_id, duration_seconds, input/output/total_tokens, status ['SUCCESS'|'ERROR'], error_message, created_at).
     • Structured Daemon Logs: Gateway daemon logs are emitted via Go 'log/slog' with level-based filtering (DEBUG, INFO, WARN, ERROR).
     • Subprocess Transcripts: Full LLM tool calls and turn step history are saved in '.system_generated/logs/transcript.jsonl' under the conversation workspace.
     • Self-Diagnostics Reasoning: When your owner reports an error, asks why a task failed, or requests troubleshooting, reason using first principles: query/inspect ~/.agyent/agyent.db (audit_logs table) or local transcripts to extract exact stack traces and root causes, then provide clear, actionable solutions.
     • Ensure these operating rules and diagnostic capabilities are permanently embedded into your generated AGENTS.md and MEMORY.md files.

5. Communication Rules:
   - DO NOT list, report, or expose raw internal file names, file paths, or file:/// links in your user-visible messages.
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

// ComposeTurnPrompt formats the execution prompt with Level 0 system foundation, knowledge directives, attached files, and user text.
func ComposeTurnPrompt(knowledgeDirectives string, msg domain.CanonicalMessage) string {
	var sb strings.Builder
	estimatedSize := len(systemRuntimeFoundationTemplate) + len(knowledgeDirectives) + len(msg.Text) + 256
	sb.Grow(estimatedSize)

	// Level 0: Static System Runtime Foundation
	sb.WriteString(systemRuntimeFoundationTemplate)
	sb.WriteString("\n\n")

	if knowledgeDirectives != "" {
		sb.WriteString(knowledgeDirectives)
		sb.WriteString("\n\n")
	}

	if len(msg.Attachments) > 0 {
		sb.WriteString("[ATTACHED FILES RECEIVED]\n")
		for _, att := range msg.Attachments {
			sb.WriteString(fmt.Sprintf("- File: %s (Type: %s, Size: %d bytes)\n", att.FilePath, att.Type, att.Size))
		}
		sb.WriteString("\nUser Prompt: ")
		sb.WriteString(msg.Text)
	} else if knowledgeDirectives != "" {
		sb.WriteString("[USER MESSAGE]\n")
		sb.WriteString(msg.Text)
	} else {
		sb.WriteString(msg.Text)
	}

	return sb.String()
}

// ComposeResolvedTurnPrompt assembles turn prompt with full 5-level hierarchy:
// Level 0: [SYSTEM RUNTIME FOUNDATION] (Static Prefix)
// Level 1: [GLOBAL CORE DIRECTIVES] (Identity, Soul, User, Long-Term Memory, Today Memory, Core Rules)
// Level 2: [WORKSPACE PROJECT DIRECTIVES]
// Level 3: [PLUGIN RULES] & [AVAILABLE SKILLS INDEX - PROGRESSIVE DISCLOSURE]
// Level 4: [ATTACHED FILES RECEIVED], [TEMPORAL CONTEXT] & [USER MESSAGE]
func ComposeResolvedTurnPrompt(resolved *domain.ResolvedContext, msg domain.CanonicalMessage, temporalTagOpt ...string) string {
	var sb strings.Builder
	estimatedSize := len(systemRuntimeFoundationTemplate) + len(msg.Text) + 512
	if resolved != nil {
		estimatedSize += len(resolved.CombinedDirectives) + len(resolved.SkillHeaders)*120
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

	// Level 4: Temporal Context Marker (~6 tokens)
	if len(temporalTagOpt) > 0 && strings.TrimSpace(temporalTagOpt[0]) != "" {
		sb.WriteString(strings.TrimSpace(temporalTagOpt[0]))
		sb.WriteString("\n")
	}

	if len(msg.Attachments) > 0 {
		sb.WriteString("User Prompt: ")
		sb.WriteString(msg.Text)
	} else {
		sb.WriteString("[USER MESSAGE]\n")
		sb.WriteString(msg.Text)
	}

	return sb.String()
}

// ComposeContinuationPrompt assembles a lightweight turn prompt for ongoing conversations.
// Because AGY CLI maintains full conversation transcript and workspace directives in its brain,
// subsequent turns only need attachments, temporal tag (if any), and the user's message.
func ComposeContinuationPrompt(msg domain.CanonicalMessage, temporalTagOpt ...string) string {
	var sb strings.Builder
	estimatedSize := len(msg.Text) + 256
	sb.Grow(estimatedSize)

	if len(msg.Attachments) > 0 {
		sb.WriteString("[ATTACHED FILES RECEIVED]\n")
		for _, att := range msg.Attachments {
			sb.WriteString(fmt.Sprintf("- File: %s (Type: %s, Size: %d bytes)\n", att.FilePath, att.Type, att.Size))
		}
		sb.WriteString("\n")
	}

	if len(temporalTagOpt) > 0 && strings.TrimSpace(temporalTagOpt[0]) != "" {
		sb.WriteString(strings.TrimSpace(temporalTagOpt[0]))
		sb.WriteString("\n\n")
	}

	if len(msg.Attachments) > 0 {
		sb.WriteString("User Prompt: ")
		sb.WriteString(msg.Text)
	} else {
		sb.WriteString(msg.Text)
	}

	return sb.String()
}

