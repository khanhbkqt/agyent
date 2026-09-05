package engine_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"agyent/internal/core/domain"

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
	cfg.AGY.CompactThresholdRatio = 0.70

	ctx := context.Background()
	require.NoError(t, eng.Start(ctx))

	// Setup mock runner to return usage exceeding compact threshold (threshold is 70% of 1M = 734k tokens)
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
				InputTokens:  780000, // Exceeds 734,003 threshold
				OutputTokens: 1500,
				TotalTokens:  781500,
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
	assert.Contains(t, outTokens.Text, "Gemini 3.7 Flash")
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
