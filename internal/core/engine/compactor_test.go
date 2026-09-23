package engine_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"agyent/internal/core/domain"
	"agyent/internal/core/engine"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompactSessionContext_Lifecycle(t *testing.T) {
	eng, runner, _, store, _, cleanup := setupTestEngine(t)
	defer cleanup()

	ctx := context.Background()

	sessionKey := "telegram:999"
	session, err := store.GetOrCreateSession(ctx, sessionKey, "agyent")
	require.NoError(t, err)

	agent := &domain.Agent{
		Name:          "agyent",
		Status:        domain.StatusInitialized,
		WorkspacePath: t.TempDir(),
	}
	require.NoError(t, store.SaveAgent(ctx, agent))

	// Setup active conversation
	convID := "conv-bloated-101"
	session.SetActiveConversationID(convID)
	require.NoError(t, store.SaveSession(ctx, session))

	conv := &domain.Conversation{
		ID:         convID,
		SessionKey: sessionKey,
		AgentName:  "agyent",
		Title:      "Heavy Refactoring Session",
		AliasIndex: 1,
		TurnCount:  15,
		IsArchived: false,
		CreatedAt:  time.Now().Add(-1 * time.Hour),
		UpdatedAt:  time.Now(),
	}
	require.NoError(t, store.SaveConversation(ctx, conv))

	// Log audit record with high token count
	audit := &domain.AuditLog{
		SessionKey:     sessionKey,
		AgentName:      "agyent",
		ConversationID: convID,
		Model:          "gemini-3.7-flash",
		Status:         "SUCCESS",
		Usage: domain.TokenUsage{
			InputTokens:  800000,
			OutputTokens: 2500,
			TotalTokens:  802500,
		},
		CreatedAt: time.Now(),
	}
	require.NoError(t, store.LogAudit(ctx, audit))

	// Mock semantic synthesis response
	runner.executeFunc = func(ctx context.Context, req domain.ExecutionRequest) (*domain.ExecutionResult, error) {
		return &domain.ExecutionResult{
			Success:        true,
			ConversationID: convID,
			ResponseText: `### Executive Continuity Digest
1. 🎯 **Original Goals & Intent**: Build microkernel event hooks.
2. 💡 **Key Decisions & Technical Constraints**: Use pure Go, strict Level 0-4 KV-cache order.
3. 📁 **Workspace State & Modified Files**: internal/core/engine/compactor.go created.
4. ⏳ **Pending Tasks & Next Steps**: Implement tests and verify.`,
			DurationSec: 0.2,
		}, nil
	}

	// Execute Compaction
	result, err := eng.CompactSessionContext(ctx, session, agent, "manual", "User requested compaction")
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, convID, result.OldConversationID)
	assert.Greater(t, result.OriginalTokens, 0)
	assert.Greater(t, result.ReductionPercent, 90.0)
	assert.Contains(t, result.Digest, "Executive Continuity Digest")
	assert.Contains(t, result.Digest, "Build microkernel event hooks")

	// Verify old conversation is archived and title updated
	archivedConv, err := store.GetConversation(ctx, convID)
	require.NoError(t, err)
	assert.True(t, archivedConv.IsArchived)
	assert.True(t, strings.HasPrefix(archivedConv.Title, "[Compacted]"))

	// Verify active conversation on session is reset
	updatedSession, err := store.GetSession(ctx, sessionKey)
	require.NoError(t, err)
	assert.Empty(t, updatedSession.GetActiveConversationID())

	// Verify continuity digest is staged in engine
	stagedDigest := eng.GetAndClearPendingCompactionDigest(sessionKey)
	assert.NotEmpty(t, stagedDigest)
	assert.Contains(t, stagedDigest, "Prior Session ID: `conv-bloated-101`")
}

func TestHandleCompactCommand(t *testing.T) {
	eng, _, _, store, _, cleanup := setupTestEngine(t)
	defer cleanup()

	ctx := context.Background()

	// Case 1: No active conversation
	msgNoConv := domain.CanonicalMessage{
		ID:        "msg-1",
		Channel:   "telegram",
		Sender:    domain.SenderUser{ID: "123456", Username: "stevan"},
		Chat:      domain.ChatContext{ID: "777", Type: "private"},
		Text:      "/compact",
		Timestamp: time.Now(),
	}
	outNoConv, err := eng.HandleCommand(ctx, msgNoConv)
	require.NoError(t, err)
	assert.Contains(t, outNoConv.Text, "No active conversation to compact")

	// Case 2: Active conversation with tokens
	sessionKey := msgNoConv.SessionKey()
	session, err := store.GetOrCreateSession(ctx, sessionKey, "agyent")
	require.NoError(t, err)

	convID := "conv-active-777"
	session.SetActiveConversationID(convID)
	require.NoError(t, store.SaveSession(ctx, session))

	conv := &domain.Conversation{
		ID:         convID,
		SessionKey: sessionKey,
		AgentName:  "agyent",
		Title:      "Active Feature Development",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	require.NoError(t, store.SaveConversation(ctx, conv))

	msgCompact := domain.CanonicalMessage{
		ID:        "msg-2",
		Channel:   "telegram",
		Sender:    domain.SenderUser{ID: "123456", Username: "stevan"},
		Chat:      domain.ChatContext{ID: "777", Type: "private"},
		Text:      "/compact milestone completed",
		Timestamp: time.Now(),
	}

	outCompact, err := eng.HandleCommand(ctx, msgCompact)
	require.NoError(t, err)
	assert.Contains(t, outCompact.Text, "Conversation Context Compacted Successfully")
	assert.Contains(t, outCompact.Text, convID)
	assert.Contains(t, outCompact.Text, "Fresh Context Ready")
}

func TestAutoCompactWatchdog(t *testing.T) {
	eng, runner, channel, store, cfg, cleanup := setupTestEngine(t)
	defer cleanup()

	cfg.AGY.AutoCompact = true
	cfg.AGY.CompactThresholdRatio = 0.90

	ctx := context.Background()
	require.NoError(t, eng.Start(ctx))

	// Setup mock runner to return usage exceeding compact threshold (threshold is 90% of 2M = 1,887,436 tokens)
	runner.executeFunc = func(ctx context.Context, req domain.ExecutionRequest) (*domain.ExecutionResult, error) {
		// If request is synthesis prompt, return summary
		if strings.Contains(req.Prompt, "Executive Continuity Digest") || strings.Contains(req.Prompt, "Continuity Digest") {
			return &domain.ExecutionResult{
				Success:        true,
				ConversationID: req.ConversationID,
				ResponseText:   "Auto-compacted continuity digest summary",
			}, nil
		}

		return &domain.ExecutionResult{
			Success:        true,
			ConversationID: "conv-overload-888",
			ResponseText:   "Finished heavy task with high tokens",
			DurationSec:    0.2,
			Usage: domain.TokenUsage{
				InputTokens:  1950000, // Exceeds 1,887,436 threshold
				OutputTokens: 1500,
				TotalTokens:  1951500,
			},
		}, nil
	}

	// Send message to execute turn
	msg := domain.CanonicalMessage{
		ID:        "msg-overload",
		Channel:   "telegram",
		Sender:    domain.SenderUser{ID: "123456", Username: "stevan"},
		Chat:      domain.ChatContext{ID: "888", Type: "private"},
		Text:      "Please run heavy analysis across the repo",
		Timestamp: time.Now(),
	}

	err := eng.HandleDebouncedMessage(ctx, msg)
	require.NoError(t, err)

	// Verify channel received Auto-Compact notification
	time.Sleep(50 * time.Millisecond)
	sentMessages := channel.GetSentMessages()
	var autoCompactSent bool
	for _, m := range sentMessages {
		if strings.Contains(m.Text, "Auto-Compact Triggered") {
			autoCompactSent = true
			break
		}
	}

	assert.True(t, autoCompactSent, "Expected Auto-Compact Triggered message to be sent to channel")

	// Verify that active conversation on session was reset
	session, err := store.GetSession(ctx, msg.SessionKey())
	require.NoError(t, err)
	assert.Empty(t, session.GetActiveConversationID())

	// Verify that continuity digest is staged for next turn
	stagedDigest := eng.GetAndClearPendingCompactionDigest(msg.SessionKey())
	assert.NotEmpty(t, stagedDigest)
}

func TestCompactAndContinueTurn(t *testing.T) {
	eng, runner, channel, store, cfg, cleanup := setupTestEngine(t)
	defer cleanup()
	cfg.Telegram.AdminUserIDs = append(cfg.Telegram.AdminUserIDs, 888001)

	ctx := context.Background()
	require.NoError(t, eng.Start(ctx))

	sessionKey := "telegram:999001:888001"
	session, err := store.GetOrCreateSession(ctx, sessionKey, "agyent")
	require.NoError(t, err)

	bloatedConvID := "conv-bloated-fixture-2.9m"
	session.SetActiveConversationID(bloatedConvID)
	require.NoError(t, store.SaveSession(ctx, session))

	conv := &domain.Conversation{
		ID:         bloatedConvID,
		SessionKey: sessionKey,
		AgentName:  "agyent",
		Title:      "Microkernel Refactor & SQLite Migration",
		TurnCount:  27,
		IsArchived: false,
		CreatedAt:  time.Now().Add(-2 * time.Hour),
		UpdatedAt:  time.Now(),
	}
	require.NoError(t, store.SaveConversation(ctx, conv))

	// Add huge audit log record (2,902,665 tokens)
	audit := &domain.AuditLog{
		SessionKey:     sessionKey,
		AgentName:      "agyent",
		ConversationID: bloatedConvID,
		Model:          "gemini-3.7-flash",
		Status:         "SUCCESS",
		Usage: domain.TokenUsage{
			InputTokens:     2902665,
			OutputTokens:    99095,
			TotalTokens:     3001760,
			CacheReadTokens: 2500000,
		},
		CreatedAt: time.Now(),
	}
	require.NoError(t, store.LogAudit(ctx, audit))

	var lastPromptSent string
	runner.executeFunc = func(ctx context.Context, req domain.ExecutionRequest) (*domain.ExecutionResult, error) {
		lastPromptSent = req.Prompt
		if strings.Contains(req.Prompt, "[CONTEXT COMPACTION REQUEST]") {
			return &domain.ExecutionResult{
				Success:        true,
				ConversationID: req.ConversationID,
				ResponseText: `### Executive Continuity Digest
1. 🎯 **Original Goals & Intent**: Refactor microkernel architecture and implement Pure-Go SQLite dual-pool engine.
2. 💡 **Key Decisions & Technical Constraints**: Strict Level 0-4 KV-cache preservation, WAL mode with MaxOpenConns=1 for writer.
3. 📁 **Workspace State & Modified Files**: internal/core/engine/compactor.go, internal/core/domain/model.go, internal/config/config.go.
4. ⏳ **Pending Tasks & Next Steps**: Verify live test execution and document architecture guides.`,
				DurationSec: 0.15,
			}, nil
		}

		return &domain.ExecutionResult{
			Success:        true,
			ConversationID: "conv-fresh-seed-999",
			ResponseText:   "Dạ anh, em đã nắm được toàn bộ ngữ cảnh từ phiên làm việc trước: mục tiêu refactor microkernel và Pure-Go SQLite đã hoàn thành. Bây giờ em sẵn sàng bắt đầu viết tài liệu kiến trúc và hướng dẫn tiếp theo ạ!",
			DurationSec:    0.25,
			Usage: domain.TokenUsage{
				InputTokens:  3200,
				OutputTokens: 150,
				TotalTokens:  3350,
			},
		}, nil
	}

	// 1. Check /tokens before compaction
	msgTokens := domain.CanonicalMessage{
		ID:        "msg-tokens",
		Channel:   "telegram",
		BotID:     999001,
		Sender:    domain.SenderUser{ID: "888001", Username: "tester"},
		Chat:      domain.ChatContext{ID: "888001", Type: "private"},
		Text:      "/tokens",
		Timestamp: time.Now(),
	}
	outTokens, err := eng.HandleCommand(ctx, msgTokens)
	require.NoError(t, err)
	assert.Contains(t, outTokens.Text, "Gemini 3.8 Flash")
	assert.Contains(t, outTokens.Text, "2,902,665")

	// 2. Run /compact command
	msgCompact := domain.CanonicalMessage{
		ID:        "msg-compact",
		Channel:   "telegram",
		BotID:     999001,
		Sender:    domain.SenderUser{ID: "888001", Username: "tester"},
		Chat:      domain.ChatContext{ID: "888001", Type: "private"},
		Text:      "/compact Đã hoàn thành refactor microkernel, chuẩn bị viết docs",
		Timestamp: time.Now(),
	}
	outCompact, err := eng.HandleCommand(ctx, msgCompact)
	require.NoError(t, err)
	assert.Contains(t, outCompact.Text, "Conversation Context Compacted Successfully")
	assert.Contains(t, outCompact.Text, "conv-bloated-fixture-2.9m")
	assert.Contains(t, outCompact.Text, "Token Reduction")

	// 3. User sends a follow-up message in the same chat
	msgFollowUp := domain.CanonicalMessage{
		ID:        "msg-followup",
		Channel:   "telegram",
		BotID:     999001,
		Sender:    domain.SenderUser{ID: "888001", Username: "tester"},
		Chat:      domain.ChatContext{ID: "888001", Type: "private"},
		Text:      "Tiến hành bước tiếp theo đi em",
		Timestamp: time.Now(),
	}

	err = eng.HandleDebouncedMessage(ctx, msgFollowUp)
	require.NoError(t, err)

	// 4. Verify that Level 4 in the prompt contained the Continuity Digest
	assert.Contains(t, lastPromptSent, "[CONVERSATION CONTINUITY & CONTEXT SNAPSHOT]")
	assert.Contains(t, lastPromptSent, "Prior Session ID: `conv-bloated-fixture-2.9m`")
	assert.Contains(t, lastPromptSent, "Đã hoàn thành refactor microkernel")
	assert.Contains(t, lastPromptSent, "Tiến hành bước tiếp theo đi em")

	// 5. Verify that the channel received the response
	time.Sleep(50 * time.Millisecond)
	sentMessages := channel.GetSentMessages()
	var agentReplied bool
	for _, m := range sentMessages {
		if strings.Contains(m.Text, "Dạ anh, em đã nắm được toàn bộ ngữ cảnh") {
			agentReplied = true
			break
		}
	}
	assert.True(t, agentReplied, "Expected agent response to follow-up turn after compaction")

	// 6. Verify that new conversation ID is active on session
	refreshedSession, err := store.GetSession(ctx, sessionKey)
	require.NoError(t, err)
	assert.Equal(t, "conv-fresh-seed-999", refreshedSession.GetActiveConversationID())

	// 7. Test /tokens stats command
	msgStats := domain.CanonicalMessage{
		ID:        "msg-stats",
		Channel:   "telegram",
		BotID:     999001,
		Sender:    domain.SenderUser{ID: "888001", Username: "tester"},
		Chat:      domain.ChatContext{ID: "888001", Type: "private"},
		Text:      "/tokens stats",
		Timestamp: time.Now(),
	}
	outStats, err := eng.HandleCommand(ctx, msgStats)
	require.NoError(t, err)
	assert.Contains(t, outStats.Text, "Token Analytics & Efficiency Report")
	assert.Contains(t, outStats.Text, "Today's Consumption")
	assert.Contains(t, outStats.Text, "Context Compactor Efficiency")
	assert.Contains(t, outStats.Text, "Successful Compactions")
	assert.Contains(t, outStats.Text, "Breakdown by Model")
}

func TestCompactor_CleanUserPromptText(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Plain user text",
			input:    "em check nginx tren may giup a",
			expected: "em check nginx tren may giup a",
		},
		{
			name: "XML wrapped user request",
			input: `<USER_REQUEST>
em check nginx trên máy giúp a
</USER_REQUEST>
<ADDITIONAL_METADATA>
The current local time is: 2026-09-06T22:08:39+07:00.
</ADDITIONAL_METADATA>`,
			expected: "em check nginx trên máy giúp a",
		},
		{
			name: "Bootstrap template with [USER MESSAGE]",
			input: `<USER_REQUEST>
[SYSTEM RUNTIME FOUNDATION]
Rule 1: Identity
[USER MESSAGE]
xin chao agent, hom nay lam gi
</USER_REQUEST>`,
			expected: "xin chao agent, hom nay lam gi",
		},
		{
			name: "Continuation with User Prompt marker",
			input: `[ATTACHED FILES RECEIVED]
- File: test.png (Type: image, Size: 100 bytes)

User Prompt: phan tich anh nay giup minh`,
			expected: "phan tich anh nay giup minh",
		},
		{
			name:     "Empty input",
			input:    "   \n\t  ",
			expected: "",
		},
		{
			name:     "Preserve legitimate user XML and uppercase HTML tags (CORR-02)",
			input:    "Cần cấu hình <CONFIG><PORT>8080</PORT></CONFIG> trong <DIV class='main'><BUTTON>Click</BUTTON></DIV>",
			expected: "Cần cấu hình <CONFIG><PORT>8080</PORT></CONFIG> trong <DIV class='main'><BUTTON>Click</BUTTON></DIV>",
		},
		{
			name:     "Strip temporal context tags (SEC-04)",
			input:    "[TEMPORAL CONTEXT: 15m elapsed since previous turn]\n[TEMPORAL GAP: 2 hours]\nSửa lỗi logic giùm anh",
			expected: "Sửa lỗi logic giùm anh",
		},
		{
			name:     "Strip known system envelope metadata tags",
			input:    "<CONTEXT_SUMMARY>Old historical context</CONTEXT_SUMMARY>\n<SYSTEM_DIRECTIVES>Be helpful</SYSTEM_DIRECTIVES>\nLàm tiếp tính năng mới",
			expected: "Làm tiếp tính năng mới",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := engine.CleanUserPromptText(tt.input)
			assert.Equal(t, tt.expected, actual)
		})
	}
}

func TestCompactor_ScrubSensitiveDialogue(t *testing.T) {
	raw := "My OpenAI key is sk-1234567890abcdef1234567890, anthropic: sk-ant-api03-abcdef12345678901234567890, token: ghpat_1234567890abcdef1234567890, bearer: Bearer token12345678901234567890. password: 'supersecretpassword123'"
	scrubbed := engine.ScrubSensitiveDialogue(raw)
	assert.NotContains(t, scrubbed, "sk-1234567890abcdef1234567890")
	assert.NotContains(t, scrubbed, "sk-ant-api03-abcdef12345678901234567890")
	assert.NotContains(t, scrubbed, "supersecretpassword123")
	assert.Contains(t, scrubbed, "[REDACTED")
}

func TestCompactor_FindBrainTranscript_PathTraversal(t *testing.T) {
	// SEC-01 & CORR-03: Ensure malicious or traversed convIDs are rejected immediately
	assert.Empty(t, engine.FindBrainTranscript("../../etc/passwd"))
	assert.Empty(t, engine.FindBrainTranscript("conv/with/slash"))
	assert.Empty(t, engine.FindBrainTranscript("conv\\with\\backslash"))
	assert.Empty(t, engine.FindBrainTranscript("../.."))
	assert.Empty(t, engine.FindBrainTranscript("valid_id; rm -rf /"))
	assert.Empty(t, engine.FindBrainTranscript(""))
}

func TestCompactor_PruneMessageContent(t *testing.T) {
	short := "Đây là tin nhắn ngắn không bị cắt."
	assert.Equal(t, short, engine.PruneMessageContent(short, 200))

	long := "Bắt đầu kiểm tra kiến trúc: " + strings.Repeat("hệ thống hoạt động ổn định và chính xác. ", 50) + "Kết thúc kiểm tra."
	pruned := engine.PruneMessageContent(long, 300)
	assert.Less(t, len(pruned), len(long))
	assert.Contains(t, pruned, "TRUNCATED")
	assert.True(t, strings.HasPrefix(pruned, "Bắt đầu kiểm tra kiến trúc:"))
	assert.True(t, strings.HasSuffix(pruned, "Kết thúc kiểm tra."))

	// Short limit test
	tiny := engine.PruneMessageContent("ngắn", 2)
	assert.NotEmpty(t, tiny)
}

func TestCompactor_ExtractLastDialogueFromTranscript(t *testing.T) {
	tempDir := t.TempDir()
	transcriptPath := filepath.Join(tempDir, "transcript.jsonl")

	lines := []string{
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","content":"<USER_REQUEST>\nTurn 1: xin chao\n</USER_REQUEST>"}`,
		`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","content":"Chao ban! Toi la Agyent."}`,
		`{"step_index":2,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","content":"<USER_REQUEST>\nTurn 2: em check race condition giup anh\n</USER_REQUEST>"}`,
		`{"step_index":3,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","tool_calls":[{"name":"run_command"}]}`,
		`{"step_index":4,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","content":"Da em da kiem tra xong, moi thu an toan a!"}`,
	}
	require.NoError(t, os.WriteFile(transcriptPath, []byte(strings.Join(lines, "\n")), 0600))

	messages, err := engine.ExtractLastDialogueFromTranscript(transcriptPath, 500)
	require.NoError(t, err)
	require.Len(t, messages, 2)

	assert.Equal(t, "user", messages[0].Role)
	assert.Equal(t, "Turn 2: em check race condition giup anh", messages[0].Content)

	assert.Equal(t, "agent", messages[1].Role)
	assert.Equal(t, "Da em da kiem tra xong, moi thu an toan a!", messages[1].Content)
}

func TestCompactor_ExtractLastDialogueFromTranscript_ToolOnlyOrInterruptedTurn(t *testing.T) {
	// CORR-01: If the final turn has only tool calls without textual response (or was interrupted),
	// it must NOT pair Turn 1's agent response with Turn 2's prompt!
	tempDir := t.TempDir()
	transcriptPath := filepath.Join(tempDir, "transcript.jsonl")

	lines := []string{
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","content":"<USER_REQUEST>\nTurn 1: chào em\n</USER_REQUEST>"}`,
		`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","content":"Dạ em chào anh!"}`,
		`{"step_index":2,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","content":"<USER_REQUEST>\nTurn 2: chạy lệnh build cho anh\n</USER_REQUEST>"}`,
		`{"step_index":3,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","tool_calls":[{"name":"run_command"}]}`,
	}
	require.NoError(t, os.WriteFile(transcriptPath, []byte(strings.Join(lines, "\n")), 0600))

	messages, err := engine.ExtractLastDialogueFromTranscript(transcriptPath, 500)
	require.NoError(t, err)
	require.Len(t, messages, 1)

	assert.Equal(t, "user", messages[0].Role)
	assert.Equal(t, "Turn 2: chạy lệnh build cho anh", messages[0].Content)
}

func TestCompactor_RecentDialogueTracking_BoundedCapacity(t *testing.T) {
	eng, _, _, _, _, cleanup := setupTestEngine(t)
	defer cleanup()

	// Fill more than 500 session entries to verify eviction (SEC-02, CORR-05)
	for i := 0; i < 550; i++ {
		sessionKey := fmt.Sprintf("session:%d", i)
		convID := fmt.Sprintf("conv-%d", i)
		eng.RecordRecentTurn(sessionKey, convID, "hello", "hi")
	}

	// Verify it didn't panic and still operates correctly
	recent := eng.GetRecentDialogue("session:549", "conv-549")
	require.Len(t, recent, 2)
}

func TestCompactor_RecentDialogueTracking_ThreadSafety(t *testing.T) {
	eng, _, _, _, _, cleanup := setupTestEngine(t)
	defer cleanup()

	sessionKey := "test:session:concurrency"
	convID := "conv-concurrent-101"

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			userMsg := fmt.Sprintf("User message iteration %d", idx)
			agentMsg := fmt.Sprintf("Agent response iteration %d", idx)
			eng.RecordRecentTurn(sessionKey, convID, userMsg, agentMsg)
			_ = eng.GetRecentDialogue(sessionKey, convID)
		}(i)
	}
	wg.Wait()

	recent := eng.GetRecentDialogue(sessionKey, convID)
	require.Len(t, recent, 2)
	assert.Equal(t, "user", recent[0].Role)
	assert.Equal(t, "agent", recent[1].Role)
}

func TestCompactSessionContext_WithRetainedMessages(t *testing.T) {
	eng, runner, _, store, _, cleanup := setupTestEngine(t)
	defer cleanup()

	ctx := context.Background()
	sessionKey := "telegram:user:retained_context"
	convID := "conv-retained-test-01"

	session, err := store.GetOrCreateSession(ctx, sessionKey, "agyent")
	require.NoError(t, err)
	session.SetActiveConversationID(convID)
	require.NoError(t, store.SaveSession(ctx, session))

	agent := &domain.Agent{
		Name:          "agyent",
		Status:        domain.StatusInitialized,
		WorkspacePath: t.TempDir(),
	}
	require.NoError(t, store.SaveAgent(ctx, agent))

	conv := &domain.Conversation{
		ID:         convID,
		SessionKey: sessionKey,
		AgentName:  "agyent",
		Title:      "Active Session With Dialogue History",
		TurnCount:  5,
		CreatedAt:  time.Now().Add(-30 * time.Minute),
		UpdatedAt:  time.Now(),
	}
	require.NoError(t, store.SaveConversation(ctx, conv))

	// Record the recent dialogue turn in engine
	userTurn := "em sửa bug timeout trong compactor.go nhé"
	agentTurn := "Dạ em đã sửa timeout thành 35s và chạy kiểm thử thành công rồi anh!"
	eng.RecordRecentTurn(sessionKey, convID, userTurn, agentTurn)

	// Mock runner for semantic synthesis
	runner.executeFunc = func(ctx context.Context, req domain.ExecutionRequest) (*domain.ExecutionResult, error) {
		assert.Contains(t, req.Prompt, "Context Compaction Checkpoint")
		return &domain.ExecutionResult{
			Success:        true,
			ConversationID: req.ConversationID,
			ResponseText: `### Executive Continuity Digest
1. 🎯 **Context & Task Trajectory**: Refactor compactor timeout and recent messages retention.
2. 💡 **Key Decisions, Invariants & Trade-offs**: Keep Level 0-3 KV-cache invariant, add recent dialogue tail.
3. 📁 **Modified Files & Working Tree**: internal/core/engine/compactor.go.
4. ⚠️ **Errors Encountered & Solutions**: None.
5. ⏳ **Immediate Next Action & Open Items**: Verify test coverage and synchronize architecture docs.`,
		}, nil
	}

	// Execute compaction
	result, err := eng.CompactSessionContext(ctx, session, agent, "manual", "User requested compaction")
	require.NoError(t, err)
	require.NotNil(t, result)

	// 1. Verify RetainedMessages in CompactionResult
	require.Len(t, result.RetainedMessages, 2)
	assert.Equal(t, "user", result.RetainedMessages[0].Role)
	assert.Equal(t, userTurn, result.RetainedMessages[0].Content)
	assert.Equal(t, "agent", result.RetainedMessages[1].Role)
	assert.Equal(t, agentTurn, result.RetainedMessages[1].Content)

	// 2. Verify Digest contains the Recent Interaction block
	assert.Contains(t, result.Digest, "### Recent Interaction (Last Messages Retained for Context)")
	assert.Contains(t, result.Digest, userTurn)
	assert.Contains(t, result.Digest, agentTurn)

	// 3. Verify staged digest in engine preserves recent dialogue for next turn
	stagedDigest := eng.GetAndClearPendingCompactionDigest(sessionKey)
	assert.Contains(t, stagedDigest, "Recent Interaction (Last Messages Retained for Context)")
	assert.Contains(t, stagedDigest, userTurn)
	assert.Contains(t, stagedDigest, agentTurn)
}
