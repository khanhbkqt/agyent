package engine_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	contextAdapter "agyent/internal/adapters/context"
	"agyent/internal/adapters/mcp"
	pluginAdapter "agyent/internal/adapters/plugin"
	"agyent/internal/adapters/storage/sqlite"
	"agyent/internal/config"
	"agyent/internal/core/auth"
	"agyent/internal/core/concurrency"
	"agyent/internal/core/debouncer"
	"agyent/internal/core/domain"
	"agyent/internal/core/engine"
	"agyent/internal/core/eventbus"
	"agyent/internal/core/execution"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestRecoveryEngine(t *testing.T, cfgOverrides ...func(*config.Config)) (*engine.Engine, *sqlite.SQLiteStore, *mockRunner, *mockChannel) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_recovery.db")

	store, err := sqlite.Open(dbPath)
	require.NoError(t, err)

	cfg := config.DefaultConfig()
	cfg.Storage.DBPath = dbPath
	cfg.Storage.AgentsDir = tempDir
	cfg.Recovery.Enabled = true
	cfg.Recovery.Mode = "auto"
	cfg.Recovery.MaxRetries = 1
	cfg.Recovery.MaxConcurrentRecoveries = 2

	for _, override := range cfgOverrides {
		override(cfg)
	}

	runner := &mockRunner{}
	channel := &mockChannel{}
	bus := eventbus.NewEventBus(100, 1)
	lockMgr := concurrency.NewSessionLockManager()
	ctxResolver := contextAdapter.NewContextResolver()
	mcpSyncer, _ := mcp.NewMCPSyncer(tempDir)
	pluginMgr := pluginAdapter.NewPluginManager("builtin/plugins", nil)

	deb := debouncer.NewDebouncer(debouncer.Config{
		WindowDuration:    100 * time.Millisecond,
		MaxWaitDuration:   500 * time.Millisecond,
		MaxMessageCount:   10,
		MaxActiveSessions: 100,
	}, func(ctx context.Context, msg domain.CanonicalMessage) error {
		return nil
	})

	eng := engine.NewEngine(cfg, store, runner, channel, bus, deb, lockMgr, ctxResolver, mcpSyncer, pluginMgr)
	policyEngine := auth.NewEngine(store, cfg)
	execSvc := execution.NewService(runner, policyEngine, nil, store, cfg, nil)
	eng.SetPolicyEngine(policyEngine)
	eng.SetExecutionService(execSvc)

	return eng, store, runner, channel
}

func TestEngine_TurnAutoRecovery_Continuation(t *testing.T) {
	eng, store, runner, channel := setupTestRecoveryEngine(t)
	defer store.Close()

	ctx := context.Background()
	sessionKey := "telegram:12345"

	// 1. Seed agent and session
	agent := &domain.Agent{
		Name:           "agyent",
		OwnerID:        "user-1",
		Status:         domain.StatusInitialized,
		WorkspacePath:  t.TempDir(),
		SecurityPreset: domain.PresetBalanced,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	require.NoError(t, store.SaveAgent(ctx, agent))

	session, err := store.GetOrCreateSession(ctx, sessionKey, "agyent")
	require.NoError(t, err)
	session.SetActiveConversationID("conv-recov-001")
	require.NoError(t, store.SaveSession(ctx, session))

	// 2. Insert interrupted in-flight turn
	turn := &domain.InFlightTurn{
		TurnID:           "turn-interrupted-001",
		SessionKey:       sessionKey,
		ConversationID:   "conv-recov-001",
		AgentName:        "agyent",
		Channel:          "telegram",
		ChatID:           "12345",
		InboundMessageID: 555,
		BotID:            100,
		UserID:           "user-1",
		UserName:         "bob",
		Prompt:           "Refactor authentication module",
		IsEphemeral:      false,
		Status:           domain.TurnStatusExecuting,
		RetryCount:       0,
		MaxRetries:       1,
		RecoveryMode:     "auto",
		CreatedAt:        time.Now().Add(-2 * time.Minute),
		UpdatedAt:        time.Now().Add(-2 * time.Minute),
	}
	require.NoError(t, store.SaveInFlightTurn(ctx, turn))

	// 3. Execute recovery
	err = eng.RecoverInterruptedTurns(ctx)
	require.NoError(t, err)

	// Wait briefly for background recovery goroutine
	time.Sleep(100 * time.Millisecond)

	// 4. Verify turn transitioned to COMPLETED
	recovTurn, err := store.GetInFlightTurn(ctx, "turn-interrupted-001")
	require.NoError(t, err)
	assert.Equal(t, domain.TurnStatusCompleted, recovTurn.Status)
	assert.Equal(t, 1, recovTurn.RetryCount)

	// 5. Verify channel received proactive notification and final result
	sent := channel.GetSentMessages()
	require.Len(t, sent, 2)
	assert.Contains(t, sent[0].Text, "Hệ thống vừa khởi động lại")
	assert.Equal(t, "555", sent[0].ReplyToMessageID)
	assert.Contains(t, sent[1].Text, "Mock response for:")

	// 6. Verify runner was called with guarded continuation prompt
	runner.mu.Lock()
	calls := runner.executeCalls
	runner.mu.Unlock()
	require.Len(t, calls, 1)
	assert.Contains(t, calls[0].Prompt, "[SYSTEM AUTO-RECOVERY NOTIFICATION]")
	assert.Contains(t, calls[0].Prompt, "Avoid re-executing non-idempotent side effects")
	assert.Equal(t, "conv-recov-001", calls[0].ConversationID)
}

func TestEngine_TurnAutoRecovery_CircuitBreaker(t *testing.T) {
	eng, store, _, channel := setupTestRecoveryEngine(t)
	defer store.Close()

	ctx := context.Background()
	sessionKey := "telegram:99999"

	// Insert in-flight turn that already exhausted max retries
	turn := &domain.InFlightTurn{
		TurnID:           "turn-fatal-001",
		SessionKey:       sessionKey,
		AgentName:        "agyent",
		Channel:          "telegram",
		ChatID:           "99999",
		InboundMessageID: 888,
		UserID:           "user-1",
		Prompt:           "Fatal prompt causing crash",
		IsEphemeral:      false,
		Status:           domain.TurnStatusExecuting,
		RetryCount:       1,
		MaxRetries:       1,
		RecoveryMode:     "auto",
		CreatedAt:        time.Now().Add(-5 * time.Minute),
		UpdatedAt:        time.Now().Add(-5 * time.Minute),
	}
	require.NoError(t, store.SaveInFlightTurn(ctx, turn))

	// Execute recovery
	err := eng.RecoverInterruptedTurns(ctx)
	require.NoError(t, err)

	// Verify turn transitioned directly to FAILED
	recovTurn, err := store.GetInFlightTurn(ctx, "turn-fatal-001")
	require.NoError(t, err)
	assert.Equal(t, domain.TurnStatusFailed, recovTurn.Status)
	assert.Contains(t, recovTurn.ErrorMessage, "exceeded maximum recovery attempts")

	// Verify user received failure warning
	sent := channel.GetSentMessages()
	require.Len(t, sent, 1)
	assert.Contains(t, sent[0].Text, "Yêu cầu không thể tự động khôi phục")
}

func TestEngine_TurnAutoRecovery_EphemeralDiscard(t *testing.T) {
	eng, store, runner, channel := setupTestRecoveryEngine(t)
	defer store.Close()

	ctx := context.Background()

	// Insert ephemeral turn (/ask)
	turn := &domain.InFlightTurn{
		TurnID:       "turn-ask-001",
		SessionKey:   "telegram:11111",
		AgentName:    "agyent",
		Channel:      "telegram",
		ChatID:       "11111",
		UserID:       "user-1",
		Prompt:       "Quick question",
		IsEphemeral:  true,
		Status:       domain.TurnStatusExecuting,
		CreatedAt:    time.Now().Add(-1 * time.Minute),
		UpdatedAt:    time.Now().Add(-1 * time.Minute),
	}
	require.NoError(t, store.SaveInFlightTurn(ctx, turn))

	// Execute recovery
	err := eng.RecoverInterruptedTurns(ctx)
	require.NoError(t, err)

	// Verify ephemeral turn is marked FAILED and discarded
	recovTurn, err := store.GetInFlightTurn(ctx, "turn-ask-001")
	require.NoError(t, err)
	assert.Equal(t, domain.TurnStatusFailed, recovTurn.Status)
	assert.Contains(t, recovTurn.ErrorMessage, "ephemeral turn discarded")

	// Verify no channel message and no execution
	assert.Empty(t, channel.GetSentMessages())
	runner.mu.Lock()
	assert.Empty(t, runner.executeCalls)
	runner.mu.Unlock()
}

func TestEngine_TurnAutoRecovery_NotifyOnlyMode(t *testing.T) {
	eng, store, runner, channel := setupTestRecoveryEngine(t, func(cfg *config.Config) {
		cfg.Recovery.Mode = "notify_only"
	})
	defer store.Close()

	ctx := context.Background()

	turn := &domain.InFlightTurn{
		TurnID:           "turn-notify-001",
		SessionKey:       "telegram:22222",
		AgentName:        "agyent",
		Channel:          "telegram",
		ChatID:           "22222",
		InboundMessageID: 102,
		UserID:           "user-1",
		Prompt:           "Do some work",
		IsEphemeral:      false,
		Status:           domain.TurnStatusExecuting,
		RetryCount:       0,
		MaxRetries:       1,
		CreatedAt:        time.Now().Add(-1 * time.Minute),
		UpdatedAt:        time.Now().Add(-1 * time.Minute),
	}
	require.NoError(t, store.SaveInFlightTurn(ctx, turn))

	err := eng.RecoverInterruptedTurns(ctx)
	require.NoError(t, err)

	// Wait for background worker
	time.Sleep(50 * time.Millisecond)

	recovTurn, err := store.GetInFlightTurn(ctx, "turn-notify-001")
	require.NoError(t, err)
	assert.Equal(t, domain.TurnStatusFailed, recovTurn.Status)
	assert.Contains(t, recovTurn.ErrorMessage, "recovery mode notify_only")

	// Verify user received notification to re-send
	sent := channel.GetSentMessages()
	require.Len(t, sent, 1)
	assert.True(t, strings.Contains(sent[0].Text, "Vui lòng gửi lại nếu bạn muốn tiếp tục"))

	runner.mu.Lock()
	assert.Empty(t, runner.executeCalls)
	runner.mu.Unlock()
}

func TestEngine_TurnAutoRecovery_SecurityHookFailure(t *testing.T) {
	eng, store, runner, _ := setupTestRecoveryEngine(t)
	defer store.Close()

	ctx := context.Background()
	sessionKey := "telegram:hookfail"

	// Create an invalid workspace path (a file instead of directory) so MkdirAll fails
	tempFile := filepath.Join(t.TempDir(), "not_a_dir")
	require.NoError(t, os.WriteFile(tempFile, []byte("file"), 0644))

	agent := &domain.Agent{
		Name:          "fail_agent",
		OwnerID:       "user-1",
		Status:        domain.StatusInitialized,
		WorkspacePath: filepath.Join(tempFile, "child"), // will fail MkdirAll
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	require.NoError(t, store.SaveAgent(ctx, agent))

	turn := &domain.InFlightTurn{
		TurnID:       "turn-hook-fail-001",
		SessionKey:   sessionKey,
		AgentName:    "fail_agent",
		Channel:      "telegram",
		ChatID:       "123",
		UserID:       "user-1",
		Prompt:       "Do something dangerous",
		IsEphemeral:  false,
		Status:       domain.TurnStatusExecuting,
		RetryCount:   0,
		MaxRetries:   1,
		CreatedAt:    time.Now().Add(-1 * time.Minute),
		UpdatedAt:    time.Now().Add(-1 * time.Minute),
	}
	require.NoError(t, store.SaveInFlightTurn(ctx, turn))

	err := eng.RecoverInterruptedTurns(ctx)
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	recovTurn, err := store.GetInFlightTurn(ctx, "turn-hook-fail-001")
	require.NoError(t, err)
	assert.Equal(t, domain.TurnStatusFailed, recovTurn.Status)
	assert.Contains(t, recovTurn.ErrorMessage, "workspace creation failed")

	runner.mu.Lock()
	assert.Empty(t, runner.executeCalls)
	runner.mu.Unlock()
}

