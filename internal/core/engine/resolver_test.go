package engine_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"agyent/internal/adapters/storage/sqlite"
	"agyent/internal/config"
	"agyent/internal/core/concurrency"
	"agyent/internal/core/domain"
	"agyent/internal/core/engine"
	"agyent/internal/core/eventbus"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEngine_ResolveExecutionParams_5Tiers(t *testing.T) {
	cfg := &config.Config{
		AGY: config.AGYConfig{
			DefaultModel:  "gemini-3.7-flash",
			DefaultEffort: "high",
			ModelAliases: map[string]string{
				"custom-pro": "gemini-3.1-pro",
			},
		},
	}

	eng := engine.NewEngine(cfg, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	t.Run("Tier 1: Explicit per-turn parameters take top priority", func(t *testing.T) {
		sess := &domain.Session{ActiveModel: "gemini-3.6-flash", ActiveEffort: "low"}
		agent := &domain.Agent{DefaultModel: "claude-sonnet-4-6"}

		model, effort, source := eng.ResolveExecutionParams("pro", "high", sess, agent)
		assert.Equal(t, "gemini-3.1-pro", model)
		assert.Equal(t, "high", effort)
		assert.Equal(t, "Per-Turn Override", source)
	})

	t.Run("Tier 2: Session override applies when per-turn is empty", func(t *testing.T) {
		sess := &domain.Session{ActiveModel: "claude", ActiveEffort: "high"}
		agent := &domain.Agent{DefaultModel: "gemini-3.1-pro", DefaultEffort: "low"}

		model, effort, source := eng.ResolveExecutionParams("", "", sess, agent)
		assert.Equal(t, "claude-sonnet-4-6", model)
		assert.Equal(t, "", effort, "claude-sonnet-4-6 has no effort support, effort must be stripped")
		assert.Equal(t, "Session Override", source)
	})

	t.Run("Tier 3: Agent persona defaults apply when session has no override", func(t *testing.T) {
		sess := &domain.Session{}
		agent := &domain.Agent{DefaultModel: "gemini-3.1-pro", DefaultEffort: "low"}

		model, effort, source := eng.ResolveExecutionParams("", "", sess, agent)
		assert.Equal(t, "gemini-3.1-pro", model)
		assert.Equal(t, "low", effort)
		assert.Equal(t, "Agent Default", source)
	})

	t.Run("Tier 4: Global gateway config applies when agent and session are empty", func(t *testing.T) {
		sess := &domain.Session{}
		agent := &domain.Agent{}

		model, effort, source := eng.ResolveExecutionParams("", "", sess, agent)
		assert.Equal(t, "gemini-3.7-flash", model)
		assert.Equal(t, "high", effort)
		assert.Equal(t, "Global Config", source)
	})

	t.Run("Edge Case: Effort clamping on gemini-3.1-pro (medium -> high)", func(t *testing.T) {
		sess := &domain.Session{ActiveModel: "gemini-3.1-pro", ActiveEffort: "medium"}
		agent := &domain.Agent{}

		model, effort, _ := eng.ResolveExecutionParams("", "", sess, agent)
		assert.Equal(t, "gemini-3.1-pro", model)
		assert.Equal(t, "high", effort, "gemini-3.1-pro does not support medium, must clamp to high")
	})

	t.Run("Custom config alias resolution", func(t *testing.T) {
		sess := &domain.Session{ActiveModel: "custom-pro"}
		agent := &domain.Agent{}

		model, effort, _ := eng.ResolveExecutionParams("", "low", sess, agent)
		assert.Equal(t, "gemini-3.1-pro", model)
		assert.Equal(t, "low", effort)
	})
}

func TestEngine_ModelAndEffortSlashCommands(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_commands.db")
	store, err := sqlite.Open(dbPath)
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	cfg := &config.Config{
		AGY: config.AGYConfig{
			DefaultModel:  "gemini-3.7-flash",
			DefaultEffort: "high",
		},
		Storage: config.StorageConfig{
			AgentsDir: tmpDir,
		},
	}

	bus := eventbus.NewEventBus(16, 1)
	defer bus.Close()
	lockMgr := concurrency.NewSessionLockManager()

	eng := engine.NewEngine(cfg, store, nil, nil, bus, nil, lockMgr, nil, nil, nil)
	sessKey := "telegram:12345"

	// Create default agent to satisfy foreign key constraint
	err = store.SaveAgent(ctx, &domain.Agent{
		Name:          "agyent",
		Description:   "Default Agent",
		Status:        domain.StatusInitialized,
		WorkspacePath: filepath.Join(tmpDir, "workspace"),
	})
	require.NoError(t, err)

	_, err = store.GetOrCreateSession(ctx, sessKey, "agyent")
	require.NoError(t, err)

	// 1. Test /model query (menu)
	msgModel := domain.CanonicalMessage{
		ID:        "1",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Chat:      domain.ChatContext{ID: "12345", Type: "private"},
		Text:      "/model",
	}
	out, err := eng.HandleCommand(ctx, msgModel)
	assert.NoError(t, err)
	assert.NotNil(t, out)
	assert.Contains(t, out.Text, "AI Model Selection")
	assert.NotEmpty(t, out.InlineKeyboard)

	var callbackDataList []string
	var buttonTexts []string
	for _, row := range out.InlineKeyboard {
		for _, btn := range row {
			callbackDataList = append(callbackDataList, btn.CallbackData)
			buttonTexts = append(buttonTexts, btn.Text)
		}
	}
	assert.Contains(t, callbackDataList, "m:set:gemini-3.8-flash")
	assert.Contains(t, callbackDataList, "m:set:gemini-3.7-flash")
	assert.Contains(t, callbackDataList, "m:reset")

	// 2. Test /model pro switch
	msgSetModel := domain.CanonicalMessage{
		ID:        "2",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Chat:      domain.ChatContext{ID: "12345", Type: "private"},
		Text:      "/model pro",
	}
	out2, err := eng.HandleCommand(ctx, msgSetModel)
	assert.NoError(t, err)
	assert.Contains(t, out2.Text, "gemini-3.1-pro")

	sess, err := store.GetSession(ctx, sessKey)
	require.NoError(t, err)
	assert.Equal(t, "gemini-3.1-pro", sess.ActiveModel)

	// 3. Test /effort low switch
	msgSetEffort := domain.CanonicalMessage{
		ID:        "3",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Chat:      domain.ChatContext{ID: "12345", Type: "private"},
		Text:      "/effort low",
	}
	out3, err := eng.HandleCommand(ctx, msgSetEffort)
	assert.NoError(t, err)
	assert.Contains(t, out3.Text, "low")

	sess, err = store.GetSession(ctx, sessKey)
	require.NoError(t, err)
	assert.Equal(t, "low", sess.ActiveEffort)

	// 4. Test /status includes Model and Effort
	msgStatus := domain.CanonicalMessage{
		ID:        "4",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Chat:      domain.ChatContext{ID: "12345", Type: "private"},
		Text:      "/status",
	}
	outStatus, err := eng.HandleCommand(ctx, msgStatus)
	assert.NoError(t, err)
	assert.Contains(t, outStatus.Text, "**Active Model:** gemini-3.1-pro")
	assert.Contains(t, outStatus.Text, "**Reasoning Effort:** low")

	// 5. Test /model reset
	msgResetModel := domain.CanonicalMessage{
		ID:        "5",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Chat:      domain.ChatContext{ID: "12345", Type: "private"},
		Text:      "/model reset",
	}
	outReset, err := eng.HandleCommand(ctx, msgResetModel)
	assert.NoError(t, err)
	assert.Contains(t, outReset.Text, "Model override reset")

	sess, err = store.GetSession(ctx, sessKey)
	require.NoError(t, err)
	assert.Equal(t, "", sess.ActiveModel)
}
