package engine_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agyent/internal/core/domain"
	"agyent/internal/core/engine"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBootstrapPromptTokenEstimate(t *testing.T) {
	agent := &domain.Agent{
		Name:          "agyent",
		Description:   "Agyent - Trợ lý AI cá nhân đa năng",
		WorkspacePath: "C:\\Users\\khanh\\.agyent\\workspace",
	}
	sender := domain.SenderUser{
		ID:       "8544450322",
		Username: "khanhbkqt",
		FullName: "Khánh Nguyễn",
	}
	userMsg := "chào em"

	prompt := engine.BuildBootstrapPrompt(agent, sender, userMsg)

	charCount := len([]rune(prompt))
	words := strings.Fields(prompt)
	wordCount := len(words)

	require.NotEmpty(t, prompt)
	assert.Contains(t, prompt, "[SYSTEM BOOTSTRAP PROTOCOL - MANDATORY INITIALIZATION]")
	assert.Contains(t, prompt, "8544450322")
	assert.Contains(t, prompt, "@khanhbkqt")
	assert.Contains(t, prompt, "Khánh Nguyễn")
	assert.Contains(t, prompt, "chào em")
	assert.Contains(t, prompt, "System Architecture & Self-Diagnostics Knowledge")
	assert.Contains(t, prompt, "audit_logs")
	assert.Contains(t, prompt, "log/slog")

	// Verify token budget stays lightweight (< 1200 tokens)
	assert.Less(t, wordCount, 600)
	assert.Less(t, charCount, 4500)
}

func TestComposeResolvedTurnPrompt_Level0StaticPrefix(t *testing.T) {
	resolved := &domain.ResolvedContext{
		CombinedDirectives: "[GLOBAL CORE DIRECTIVES]\n<IDENTITY>\nName: agyent\n</IDENTITY>",
		SkillHeaders: []domain.SkillHeader{
			{
				Name:        "web-browse",
				Description: "Browse web pages",
				FilePath:    "/skills/web-browse/SKILL.md",
				Scope:       domain.ScopeGlobal,
			},
		},
	}
	msg := domain.CanonicalMessage{
		Text: "Kiểm tra hệ thống",
	}

	prompt := engine.ComposeResolvedTurnPrompt(resolved, msg)

	// Verify Level 0 is at Index 0 (Prefix Cache Hit)
	assert.True(t, strings.HasPrefix(prompt, "[SYSTEM RUNTIME FOUNDATION]"), "Prompt MUST start with [SYSTEM RUNTIME FOUNDATION] at Index 0")

	// Verify pillars of System Foundation
	assert.Contains(t, prompt, "1. Identity & Operating Mandate:")
	assert.Contains(t, prompt, "2. Tool Execution & Subagent Protocol:")
	assert.Contains(t, prompt, "3. Security Gateway & Policy Remediation:")
	assert.Contains(t, prompt, "4. Outbound Artifact & Media Delivery Protocol:")
	assert.Contains(t, prompt, "5. Link Hygiene, Verification & Memory:")
	assert.Contains(t, prompt, "6. Autonomous Scheduling, Cron & Proactive Heartbeats:")
	assert.Contains(t, prompt, "Root-Cause Diagnostics:")
	assert.Contains(t, prompt, "audit_logs")
	assert.Contains(t, prompt, "Skills Discovery:")
	assert.Contains(t, prompt, "/security grant")
	assert.Contains(t, prompt, "/whitelist add")
	assert.Contains(t, prompt, "/security preset")

	// Verify 5-level hierarchy order
	idxLevel0 := strings.Index(prompt, "[SYSTEM RUNTIME FOUNDATION]")
	idxLevel1 := strings.Index(prompt, "[GLOBAL CORE DIRECTIVES]")
	idxLevel3 := strings.Index(prompt, "[AVAILABLE SKILLS INDEX - PROGRESSIVE DISCLOSURE]")
	idxLevel4 := strings.Index(prompt, "[USER MESSAGE]")

	assert.NotEqual(t, -1, idxLevel0)
	assert.NotEqual(t, -1, idxLevel1)
	assert.NotEqual(t, -1, idxLevel3)
	assert.NotEqual(t, -1, idxLevel4)

	assert.True(t, idxLevel0 < idxLevel1, "Level 0 must precede Level 1")
	assert.True(t, idxLevel1 < idxLevel3, "Level 1 must precede Level 3")
	assert.True(t, idxLevel3 < idxLevel4, "Level 3 must precede Level 4")
	assert.Contains(t, prompt, "Kiểm tra hệ thống")
}

func TestComposeResolvedTurnPrompt_WithAttachments(t *testing.T) {
	resolved := &domain.ResolvedContext{
		CombinedDirectives: "[GLOBAL CORE DIRECTIVES]\n<IDENTITY>\nName: agyent\n</IDENTITY>",
	}
	msg := domain.CanonicalMessage{
		Text: "Hãy phân tích log",
		Attachments: []domain.Attachment{
			{
				FilePath: "/tmp/app.log",
				Type:     "text/plain",
				Size:     2048,
			},
		},
	}

	prompt := engine.ComposeResolvedTurnPrompt(resolved, msg)

	assert.True(t, strings.HasPrefix(prompt, "[SYSTEM RUNTIME FOUNDATION]"))
	assert.Contains(t, prompt, "[ATTACHED FILES RECEIVED]")
	assert.Contains(t, prompt, "- File: /tmp/app.log (Type: text/plain, Size: 2048 bytes)")
	assert.Contains(t, prompt, "User Prompt: Hãy phân tích log")
}

func TestComposeTurnPrompt_Level0Prefix(t *testing.T) {
	knowledge := "[AGENT CONTEXT & CORE DIRECTIVES]\n<IDENTITY>\nName: agyent\n</IDENTITY>"
	msg := domain.CanonicalMessage{Text: "Xin chào"}

	prompt := engine.ComposeTurnPrompt(knowledge, msg)

	assert.True(t, strings.HasPrefix(prompt, "[SYSTEM RUNTIME FOUNDATION]"))
	assert.Contains(t, prompt, knowledge)
	assert.Contains(t, prompt, "[USER MESSAGE]\nXin chào")
}

func TestLoadAgentKnowledgeDirectives_TwoTierMemory(t *testing.T) {
	dir := t.TempDir()

	// Write USER.md with timezone
	require.NoError(t, os.WriteFile(filepath.Join(dir, "USER.md"), []byte("Múi giờ: Asia/Ho_Chi_Minh\nName: Khánh"), 0644))
	// Write MEMORY.md
	require.NoError(t, os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("# Long Term\nCore rule: Clean Architecture"), 0644))

	// Write today's episodic memory in memory/YYYY-MM-DD.md
	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	require.NoError(t, err)
	todayStr := time.Now().In(loc).Format("2006-01-02")

	memDir := filepath.Join(dir, "memory")
	require.NoError(t, os.MkdirAll(memDir, 0755))
	todayFile := filepath.Join(memDir, fmt.Sprintf("%s.md", todayStr))
	require.NoError(t, os.WriteFile(todayFile, []byte("- 14:00 Thảo luận Milestone 9"), 0644))

	directives := engine.LoadAgentKnowledgeDirectives(dir)

	assert.Contains(t, directives, "<USER_PROFILE>\nMúi giờ: Asia/Ho_Chi_Minh\nName: Khánh\n</USER_PROFILE>")
	assert.Contains(t, directives, "<LONG_TERM_MEMORY>\n# Long Term\nCore rule: Clean Architecture\n</LONG_TERM_MEMORY>")
	assert.Contains(t, directives, "<TODAY_MEMORY>\n- 14:00 Thảo luận Milestone 9\n</TODAY_MEMORY>")
}

func TestLoadAgentKnowledgeDirectives_WhitespaceFiles_Graceful(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "IDENTITY.md"), []byte("   \n\t  "), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte(""), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "USER.md"), []byte(" \n\r\n "), 0644))

	directives := engine.LoadAgentKnowledgeDirectives(dir)
	assert.Empty(t, directives, "Whitespace-only files must not generate empty XML blocks")
}

func TestComposeResolvedTurnPrompt_Regression_M8Commands(t *testing.T) {
	resolved := &domain.ResolvedContext{
		CombinedDirectives: "[GLOBAL CORE DIRECTIVES]\n<IDENTITY>\nName: agyent\n</IDENTITY>",
	}

	// 1. /ask command
	msgAsk := domain.CanonicalMessage{Text: "/ask Hãy giải thích kiến trúc Clean Architecture"}
	promptAsk := engine.ComposeResolvedTurnPrompt(resolved, msgAsk)
	assert.True(t, strings.HasPrefix(promptAsk, "[SYSTEM RUNTIME FOUNDATION]"))
	assert.Contains(t, promptAsk, "[USER MESSAGE]\n/ask Hãy giải thích kiến trúc Clean Architecture")

	// 2. /c list command
	msgC := domain.CanonicalMessage{Text: "/c"}
	promptC := engine.ComposeResolvedTurnPrompt(resolved, msgC)
	assert.True(t, strings.HasPrefix(promptC, "[SYSTEM RUNTIME FOUNDATION]"))
	assert.Contains(t, promptC, "[USER MESSAGE]\n/c")

	// 3. /pin and /unpin
	msgPin := domain.CanonicalMessage{Text: "/pin"}
	promptPin := engine.ComposeResolvedTurnPrompt(resolved, msgPin)
	assert.Contains(t, promptPin, "[USER MESSAGE]\n/pin")
}

func TestComposeContinuationPrompt(t *testing.T) {
	// 1. Simple text message
	msg1 := domain.CanonicalMessage{Text: "Hôm nay thời tiết sao rồi em?"}
	p1 := engine.ComposeContinuationPrompt(msg1)
	assert.Equal(t, "Hôm nay thời tiết sao rồi em?", p1)
	assert.NotContains(t, p1, "[SYSTEM RUNTIME FOUNDATION]")
	assert.NotContains(t, p1, "[GLOBAL CORE DIRECTIVES]")

	// 2. Message with temporal gap tag
	temporalTag := "[GAP: Next morning, 07:11]"
	p2 := engine.ComposeContinuationPrompt(msg1, temporalTag)
	assert.Contains(t, p2, "[GAP: Next morning, 07:11]")
	assert.Contains(t, p2, "Hôm nay thời tiết sao rồi em?")
	assert.NotContains(t, p2, "[SYSTEM RUNTIME FOUNDATION]")

	// 3. Message with attachments
	msg3 := domain.CanonicalMessage{
		Text: "Xem file này giúp anh",
		Attachments: []domain.Attachment{
			{
				FilePath: "/tmp/data.csv",
				Type:     "text/csv",
				Size:     1024,
			},
		},
	}
	p3 := engine.ComposeContinuationPrompt(msg3)
	assert.Contains(t, p3, "[ATTACHED FILES RECEIVED]")
	assert.Contains(t, p3, "- File: /tmp/data.csv (Type: text/csv, Size: 1024 bytes)")
	assert.Contains(t, p3, "User Prompt: Xem file này giúp anh")
	assert.NotContains(t, p3, "[SYSTEM RUNTIME FOUNDATION]")
}

func TestBuildNewSessionGreetingPrompt(t *testing.T) {
	// Without topic
	promptWithoutTopic := engine.BuildNewSessionGreetingPrompt("")
	assert.Contains(t, promptWithoutTopic, "[SYSTEM DIRECTIVE: NEW CONVERSATION INITIALIZATION]")
	assert.Contains(t, promptWithoutTopic, "The user has initiated a fresh conversation session.")
	assert.Contains(t, promptWithoutTopic, "Proactively greet the user warmly")

	// With topic
	promptWithTopic := engine.BuildNewSessionGreetingPrompt("Thiết kế Database mới")
	assert.Contains(t, promptWithTopic, "[SYSTEM DIRECTIVE: NEW CONVERSATION INITIALIZATION]")
	assert.Contains(t, promptWithTopic, "Thiết kế Database mới")
	assert.Contains(t, promptWithTopic, "Acknowledge the topic")
}
