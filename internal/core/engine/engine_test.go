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

	contextAdapter "agyent/internal/adapters/context"
	"agyent/internal/adapters/mcp"
	pluginAdapter "agyent/internal/adapters/plugin"
	"agyent/internal/adapters/storage/sqlite"
	"agyent/internal/config"
	"agyent/internal/core/concurrency"
	"agyent/internal/core/debouncer"
	"agyent/internal/core/domain"
	"agyent/internal/core/engine"
	"agyent/internal/core/eventbus"
	"agyent/internal/core/ports"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// --- MOCK ADAPTERS FOR ENGINE TESTING ---

type mockRunner struct {
	mu           sync.Mutex
	executeCalls []domain.ExecutionRequest
	streamCalls  []domain.ExecutionRequest
	executeFunc  func(ctx context.Context, req domain.ExecutionRequest) (*domain.ExecutionResult, error)
	streamFunc   func(ctx context.Context, req domain.ExecutionRequest, sessionKey string) (*domain.ExecutionResult, error)
}

func (m *mockRunner) Name() string { return "mock-runner" }

func (m *mockRunner) Execute(ctx context.Context, req domain.ExecutionRequest) (*domain.ExecutionResult, error) {
	m.mu.Lock()
	m.executeCalls = append(m.executeCalls, req)
	fn := m.executeFunc
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, req)
	}
	return &domain.ExecutionResult{
		Success:        true,
		ConversationID: "conv-123",
		ResponseText:   "Mock response for: " + req.Prompt,
		DurationSec:    0.5,
		Usage:          domain.TokenUsage{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
	}, nil
}

func (m *mockRunner) ExecuteStream(ctx context.Context, req domain.ExecutionRequest, sessionKey string) (*domain.ExecutionResult, error) {
	m.mu.Lock()
	m.streamCalls = append(m.streamCalls, req)
	fn := m.streamFunc
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, req, sessionKey)
	}
	return &domain.ExecutionResult{
		Success:        true,
		ConversationID: "conv-stream-123",
		ResponseText:   "Mock stream response for: " + req.Prompt,
		DurationSec:    0.3,
		Usage:          domain.TokenUsage{InputTokens: 12, OutputTokens: 24, TotalTokens: 36},
	}, nil
}

func (m *mockRunner) HealthCheck(ctx context.Context) error { return nil }

type mockChannel struct {
	mu          sync.Mutex
	sent        []domain.OutboundMessage
	typingCalls int
	inboundChan chan<- domain.CanonicalMessage
}

func (m *mockChannel) Name() string { return "mock-channel" }

func (m *mockChannel) Start(ctx context.Context, inbound chan<- domain.CanonicalMessage) error {
	m.mu.Lock()
	m.inboundChan = inbound
	m.mu.Unlock()
	return nil
}

func (m *mockChannel) Send(ctx context.Context, msg domain.OutboundMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return nil
}

func (m *mockChannel) SendTyping(ctx context.Context, chatID string, threadID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.typingCalls++
	return nil
}

func (m *mockChannel) SendChatAction(ctx context.Context, chatID string, threadID int64, action string) error {
	return nil
}

func (m *mockChannel) SendFile(ctx context.Context, chatID string, threadID int64, filePath string, caption string) error {
	return nil
}

func (m *mockChannel) Stop() error { return nil }

func (m *mockChannel) GetSentMessages() []domain.OutboundMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	copied := make([]domain.OutboundMessage, len(m.sent))
	copy(copied, m.sent)
	return copied
}

func setupTestEngine(t *testing.T) (*engine.Engine, *mockRunner, *mockChannel, ports.StoragePort, *config.Config, func()) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_engine.db")
	agentsDir := filepath.Join(tmpDir, "agents")
	mcpPath := filepath.Join(tmpDir, "mcp_config.json")
	builtinDir := filepath.Join(tmpDir, "builtin_plugins")
	require.NoError(t, os.MkdirAll(agentsDir, 0755))
	require.NoError(t, os.MkdirAll(builtinDir, 0755))

	cfg := &config.Config{
		Server: config.ServerConfig{Host: "127.0.0.1", Port: 8080},
		Telegram: config.TelegramConfig{
			BotToken:     "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11",
			Mode:         "polling",
			AdminUserIDs: []int64{123456},
		},
		AGY: config.AGYConfig{
			BinaryPath:                 "agy",
			DefaultTimeoutSeconds:      10,
			DefaultEffort:              "high",
			DefaultMode:                "accept-edits",
			DangerouslySkipPermissions: true,
			StreamingEnabled:           false,
		},
		Storage: config.StorageConfig{
			DBPath:          dbPath,
			AgentsDir:       agentsDir,
			DebounceSeconds: 0.05,
		},
	}

	store, err := sqlite.Open(dbPath)
	require.NoError(t, err)

	// Seed default agent
	defaultAgentPath := filepath.Join(agentsDir, "agyent")
	require.NoError(t, os.MkdirAll(defaultAgentPath, 0755))
	err = store.SaveAgent(context.Background(), &domain.Agent{
		Name:          "agyent",
		Description:   "Agyent - Trợ lý AI cá nhân đa năng",
		Status:        domain.StatusInitialized,
		WorkspacePath: defaultAgentPath,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	})
	require.NoError(t, err)

	runner := &mockRunner{}
	channel := &mockChannel{}
	bus := eventbus.NewEventBus(128, 2)
	lockMgr := concurrency.NewSessionLockManager()
	resolver := contextAdapter.NewContextResolver()
	syncer, err := mcp.NewMCPSyncer(mcpPath)
	require.NoError(t, err)
	pluginMgr := pluginAdapter.NewPluginManager(builtinDir)

	var eng *engine.Engine

	debouncerHandler := func(ctx context.Context, msg domain.CanonicalMessage) error {
		return eng.HandleDebouncedMessage(ctx, msg)
	}

	deb := debouncer.NewDebouncer(debouncer.Config{
		WindowDuration:  50 * time.Millisecond,
		MaxWaitDuration: 200 * time.Millisecond,
	}, debouncerHandler)

	eng = engine.NewEngine(cfg, store, runner, channel, bus, deb, lockMgr, resolver, syncer, pluginMgr)

	cleanup := func() {
		_ = eng.Stop(context.Background())
		_ = deb.Close(context.Background())
		_ = bus.Close()
		_ = store.Close()
	}

	return eng, runner, channel, store, cfg, cleanup
}

// --- TEST SUITES ---

func TestEngine_Lifecycle(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreTopFunction("database/sql.(*DB).connectionOpener"))

	eng, _, _, _, _, cleanup := setupTestEngine(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := eng.Start(ctx)
	require.NoError(t, err)

	err = eng.Start(ctx)
	require.Error(t, err)

	err = eng.Stop(context.Background())
	require.NoError(t, err)
}

func TestEngine_BatchTurnExecution(t *testing.T) {
	eng, runner, channel, store, _, cleanup := setupTestEngine(t)
	defer cleanup()

	ctx := context.Background()
	require.NoError(t, eng.Start(ctx))

	msg := domain.CanonicalMessage{
		ID:        "msg-1",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Sender:    domain.SenderUser{ID: "123456", Username: "stevan"},
		Chat:      domain.ChatContext{ID: "123456", Type: "private"},
		Text:      "Hello agyent!",
	}

	err := eng.HandleDebouncedMessage(ctx, msg)
	require.NoError(t, err)

	require.Len(t, runner.executeCalls, 1)
	assert.Contains(t, runner.executeCalls[0].Prompt, "Hello agyent!")

	sent := channel.GetSentMessages()
	require.Len(t, sent, 1)
	assert.Contains(t, sent[0].Text, "Mock response for: ")

	sess, err := store.GetSession(ctx, msg.SessionKey())
	require.NoError(t, err)
	assert.Equal(t, "conv-123", sess.GlobalConversationID)

	logs, err := store.ListAuditLogs(ctx, msg.SessionKey(), 10)
	require.NoError(t, err)
	require.Len(t, logs, 1)
	assert.Equal(t, "SUCCESS", logs[0].Status)
	assert.Equal(t, "agyent", logs[0].AgentName)
}

func TestEngine_StreamingTurnExecution(t *testing.T) {
	eng, runner, channel, store, _, cleanup := setupTestEngine(t)
	defer cleanup()

	eng.SetStreamingEnabled(true)
	assert.True(t, eng.IsStreamingEnabled())

	ctx := context.Background()
	require.NoError(t, eng.Start(ctx))

	msg := domain.CanonicalMessage{
		ID:        "msg-stream-1",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Sender:    domain.SenderUser{ID: "123456", Username: "stevan"},
		Chat:      domain.ChatContext{ID: "123456", Type: "private"},
		Text:      "Stream this turn!",
	}

	err := eng.HandleDebouncedMessage(ctx, msg)
	require.NoError(t, err)

	require.Len(t, runner.streamCalls, 1)
	assert.Contains(t, runner.streamCalls[0].Prompt, "Stream this turn!")

	sent := channel.GetSentMessages()
	assert.Len(t, sent, 0, "engine must not send outbound message in streaming mode (handled by throttler)")

	sess, err := store.GetSession(ctx, msg.SessionKey())
	require.NoError(t, err)
	assert.Equal(t, "conv-stream-123", sess.GlobalConversationID)
}

func TestEngine_NewAgentBootstrapFlow(t *testing.T) {
	eng, runner, _, store, _, cleanup := setupTestEngine(t)
	defer cleanup()

	ctx := context.Background()
	require.NoError(t, eng.Start(ctx))

	agentWorkspace := filepath.Join(t.TempDir(), "coder_bot")
	require.NoError(t, os.MkdirAll(agentWorkspace, 0755))

	uninitAgent := &domain.Agent{
		Name:          "coder_bot",
		Description:   "Coder Bot Assistant",
		Status:        domain.StatusUninitialized,
		WorkspacePath: agentWorkspace,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	require.NoError(t, store.SaveAgent(ctx, uninitAgent))

	sess := &domain.Session{
		SessionKey:  "telegram:8544450322",
		ActiveAgent: "coder_bot",
		UpdatedAt:   time.Now(),
	}
	require.NoError(t, store.SaveSession(ctx, sess))

	// Turn 1: Initial message to uninitialized agent
	msgTurn1 := domain.CanonicalMessage{
		ID:        "msg-init-1",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Sender: domain.SenderUser{
			ID:       "8544450322",
			Username: "khanhbkqt",
			FullName: "Khánh Nguyễn",
		},
		Chat: domain.ChatContext{ID: "8544450322", Type: "private"},
		Text: "Chào em, anh là Khánh Nguyễn!",
	}

	err := eng.HandleDebouncedMessage(ctx, msgTurn1)
	require.NoError(t, err)

	require.Len(t, runner.executeCalls, 1)
	promptTurn1 := runner.executeCalls[0].Prompt

	// Verify Genesis Onboarding Protocol prompt structure
	assert.Contains(t, promptTurn1, "[SYSTEM BOOTSTRAP PROTOCOL - MANDATORY INITIALIZATION]")
	assert.Contains(t, promptTurn1, "Khánh Nguyễn")
	assert.Contains(t, promptTurn1, "8544450322")
	assert.Contains(t, promptTurn1, "@khanhbkqt")
	assert.Contains(t, promptTurn1, "Dynamic Identity File Creation:")
	assert.Contains(t, promptTurn1, "USER.md")
	assert.Contains(t, promptTurn1, "IDENTITY.md")
	assert.Contains(t, promptTurn1, "SOUL.md")
	assert.Contains(t, promptTurn1, "MEMORY.md")
	assert.Contains(t, promptTurn1, "AGENTS.md")

	// Verify agent status transitioned from Uninitialized -> Initialized
	updatedAgent, err := store.GetAgent(ctx, "coder_bot")
	require.NoError(t, err)
	assert.Equal(t, domain.StatusInitialized, updatedAgent.Status)

	// Simulate that the agent created USER.md and IDENTITY.md in its workspace during Turn 1
	require.NoError(t, os.WriteFile(filepath.Join(agentWorkspace, "USER.md"), []byte("Múi giờ: Asia/Ho_Chi_Minh\nName: Khánh Nguyễn\nRole: Lead Engineer"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(agentWorkspace, "IDENTITY.md"), []byte("# Coder Bot\nSpecialty: Go Architecture"), 0644))

	// Turn 2: Subsequent regular message to newly initialized agent
	msgTurn2 := domain.CanonicalMessage{
		ID:        "msg-init-2",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Sender: domain.SenderUser{
			ID:       "8544450322",
			Username: "khanhbkqt",
			FullName: "Khánh Nguyễn",
		},
		Chat: domain.ChatContext{ID: "8544450322", Type: "private"},
		Text: "Hôm nay chúng ta làm gì tiếp theo?",
	}

	err = eng.HandleDebouncedMessage(ctx, msgTurn2)
	require.NoError(t, err)

	require.Len(t, runner.executeCalls, 2)
	promptTurn2 := runner.executeCalls[1].Prompt

	// Verify Turn 2 in active conversation uses lightweight continuation prompt (zero-overhead, no duplicate directives)
	assert.Equal(t, "Hôm nay chúng ta làm gì tiếp theo?", promptTurn2)
	assert.NotContains(t, promptTurn2, "[SYSTEM BOOTSTRAP PROTOCOL - MANDATORY INITIALIZATION]")
	assert.NotContains(t, promptTurn2, "[SYSTEM RUNTIME FOUNDATION]")
	assert.NotContains(t, promptTurn2, "<USER_PROFILE>")

	// Turn 3: Reset conversation to start fresh turn (ConversationID == "")
	resetCmd := domain.CanonicalMessage{
		ID:        "msg-reset",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Sender:    msgTurn2.Sender,
		Chat:      msgTurn2.Chat,
		Text:      "/reset",
	}
	_, err = eng.HandleCommand(ctx, resetCmd)
	require.NoError(t, err)

	msgTurn3 := domain.CanonicalMessage{
		ID:        "msg-fresh-3",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Sender:    msgTurn2.Sender,
		Chat:      msgTurn2.Chat,
		Text:      "Bắt đầu phiên mới nào",
	}
	err = eng.HandleDebouncedMessage(ctx, msgTurn3)
	require.NoError(t, err)


	require.Len(t, runner.executeCalls, 3)
	promptTurn3 := runner.executeCalls[2].Prompt

	// Verify Turn 3 after reset injects full Level 0 + Level 1 Hierarchy
	assert.True(t, strings.HasPrefix(promptTurn3, "[SYSTEM RUNTIME FOUNDATION]"))
	assert.Contains(t, promptTurn3, "<USER_PROFILE>")
	assert.Contains(t, promptTurn3, "Múi giờ: Asia/Ho_Chi_Minh")
	assert.Contains(t, promptTurn3, "Khánh Nguyễn")
	assert.Contains(t, promptTurn3, "<IDENTITY>")
	assert.Contains(t, promptTurn3, "Specialty: Go Architecture")
	assert.Contains(t, promptTurn3, "[USER MESSAGE]\nBắt đầu phiên mới nào")
}


func TestEngine_SlashCommandsSuite(t *testing.T) {
	eng, _, channel, store, _, cleanup := setupTestEngine(t)
	defer cleanup()

	ctx := context.Background()
	require.NoError(t, eng.Start(ctx))

	sender := domain.SenderUser{ID: "123456", Username: "admin"}
	chat := domain.ChatContext{ID: "123456", Type: "private"}

	tests := []struct {
		name       string
		command    string
		expectSub  string
		validateFn func(t *testing.T)
	}{
		{
			name:      "Help Command",
			command:   "/help",
			expectSub: "agyent Gateway Daemon — Commands Guide",
		},
		{
			name:      "Status Command",
			command:   "/status",
			expectSub: "agyent Gateway Status",
		},
		{
			name:      "Context Command",
			command:   "/context",
			expectSub: "agyent Context Status & Token Budget",
		},
		{
			name:      "Skills Command",
			command:   "/skills",
			expectSub: "skills",
		},
		{
			name:      "Plugins Command",
			command:   "/plugins",
			expectSub: "plugins",
		},
		{
			name:      "Stream Toggle On",
			command:   "/stream on",
			expectSub: "Streaming Mode ENABLED",
			validateFn: func(t *testing.T) {
				assert.True(t, eng.IsStreamingEnabled())
			},
		},
		{
			name:      "List Agents",
			command:   "/agents",
			expectSub: "Registered Agents:",
		},
		{
			name:      "Create New Agent",
			command:   "/a new cloud_sec Cloud Security Engineer",
			expectSub: "Agent `cloud_sec` created",
			validateFn: func(t *testing.T) {
				agent, err := store.GetAgent(ctx, "cloud_sec")
				require.NoError(t, err)
				assert.Equal(t, "cloud_sec", agent.Name)
			},
		},
		{
			name:      "Switch Agent",
			command:   "/use cloud_sec",
			expectSub: "Switched active agent to **cloud_sec**",
		},
		{
			name:      "Project New",
			command:   "/p new backend_api /tmp/backend_api",
			expectSub: "Project `backend_api` registered and activated",
		},
		{
			name:      "Project Info",
			command:   "/p info",
			expectSub: "Active Project Details:",
		},
		{
			name:      "Project Exit",
			command:   "/p exit",
			expectSub: "Exited project mode. Returned to **Global Chat Mode**",
		},
		{
			name:      "Force Unlock",
			command:   "/force_unlock",
			expectSub: "Session",
		},
		{
			name:      "Conversation List",
			command:   "/c",
			expectSub: "Conversations List",
		},
		{
			name:      "New Conversation",
			command:   "/new Dự án mới",
			expectSub: "New conversation created",
		},
		{
			name:      "Conversation Clean",
			command:   "/c clean",
			expectSub: "Garbage collection complete",
		},
		{
			name:      "Ask Usage Hint",
			command:   "/ask",
			expectSub: "Usage:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := domain.CanonicalMessage{
				ID:        fmt.Sprintf("cmd-%d", time.Now().UnixNano()),
				Timestamp: time.Now(),
				Channel:   "telegram",
				Sender:    sender,
				Chat:      chat,
				Text:      tt.command,
			}

			err := eng.HandleDebouncedMessage(ctx, msg)
			require.NoError(t, err)

			sent := channel.GetSentMessages()
			require.NotEmpty(t, sent)
			lastSent := sent[len(sent)-1]
			assert.Contains(t, stringsToLower(lastSent.Text), stringsToLower(tt.expectSub))

			if tt.validateFn != nil {
				tt.validateFn(t)
			}
		})
	}
}

func stringsToLower(s string) string {
	return strings.ToLower(s)
}
