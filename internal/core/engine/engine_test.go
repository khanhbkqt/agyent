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
	securityAdapter "agyent/internal/adapters/security"
	"agyent/internal/adapters/storage/sqlite"
	workspaceAdapter "agyent/internal/adapters/workspace"
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

func (m *mockRunner) ListAvailableModels(ctx context.Context) ([]domain.ModelCapability, error) {
	return domain.ListAvailableModels(), nil
}

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
	pluginMgr := pluginAdapter.NewPluginManager(builtinDir, nil)

	var eng *engine.Engine

	debouncerHandler := func(ctx context.Context, msg domain.CanonicalMessage) error {
		return eng.HandleDebouncedMessage(ctx, msg)
	}

	deb := debouncer.NewDebouncer(debouncer.Config{
		WindowDuration:  50 * time.Millisecond,
		MaxWaitDuration: 200 * time.Millisecond,
	}, debouncerHandler)

	cfg.Security = config.GetEffectiveSecurityPreset("balanced")
	secMgr := securityAdapter.NewManager(cfg.Security, nil, nil)

	eng = engine.NewEngine(cfg, store, runner, channel, bus, deb, lockMgr, resolver, syncer, pluginMgr)
	eng.SetSecurityManager(secMgr)

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
		OwnerID:       "8544450322",
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
			name:      "Stream Toggle Off",
			command:   "/stream off",
			expectSub: "Streaming Mode DISABLED",
			validateFn: func(t *testing.T) {
				assert.False(t, eng.IsStreamingEnabled())
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
			expectSub: "Mock response for:",
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
		{
			name:      "Security Dashboard Command",
			command:   "/security",
			expectSub: "Agyent Security Gateway Dashboard",
		},
		{
			name:      "Security Preset Switch",
			command:   "/security preset strict",
			expectSub: "Security preset successfully switched",
		},
		{
			name:      "Whitelist Add Command",
			command:   "/whitelist add npm run build",
			expectSub: "Added custom whitelist rule",
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

func TestEngine_WorkspaceHookAutoProvisioning(t *testing.T) {
	eng, _, _, _, cfg, cleanup := setupTestEngine(t)
	defer cleanup()

	secMgr := securityAdapter.NewManager(cfg.Security, nil, nil)
	eng.SetSecurityManager(secMgr)

	ctx := context.Background()
	msg := domain.CanonicalMessage{
		ID:        "msg-hook-test",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Sender:    domain.SenderUser{ID: "123456", Username: "admin"},
		Chat:      domain.ChatContext{ID: "123456", Type: "private"},
		Text:      "Hello world",
	}

	err := eng.HandleDebouncedMessage(ctx, msg)
	require.NoError(t, err)

	expectedHookPath := filepath.Join(cfg.Storage.AgentsDir, "agyent", ".agents", "hooks.json")
	assert.FileExists(t, expectedHookPath, "Workspace hook must be automatically provisioned before turn execution")
	data, err := os.ReadFile(expectedHookPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "agyent-security-gate")
	assert.Contains(t, string(data), "hook-bridge pre")
}

func TestEngine_AgentOwnershipAndRBAC(t *testing.T) {
	eng, _, channel, store, _, cleanup := setupTestEngine(t)
	defer cleanup()

	ctx := context.Background()
	require.NoError(t, eng.Start(ctx))

	user1 := domain.SenderUser{ID: "111", Username: "alice"}
	user2 := domain.SenderUser{ID: "222", Username: "bob"}
	chat1 := domain.ChatContext{ID: "111", Type: "private"}
	chat2 := domain.ChatContext{ID: "222", Type: "private"}

	// 1. User 1 creates private agent 'alice_sec'
	createMsg := domain.CanonicalMessage{
		ID:        "msg-create-1",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Sender:    user1,
		Chat:      chat1,
		Text:      "/a new alice_sec Alice Private Security Agent",
	}
	err := eng.HandleDebouncedMessage(ctx, createMsg)
	require.NoError(t, err)

	agent, err := store.GetAgent(ctx, "alice_sec")
	require.NoError(t, err)
	assert.Equal(t, "alice_sec", agent.Name)
	assert.Equal(t, "111", agent.OwnerID)
	assert.False(t, agent.IsPublic)

	// 2. User 2 (unauthorized) tries to switch to 'alice_sec' -> 403
	switchMsg := domain.CanonicalMessage{
		ID:        "msg-switch-2",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Sender:    user2,
		Chat:      chat2,
		Text:      "/use alice_sec",
	}
	err = eng.HandleDebouncedMessage(ctx, switchMsg)
	require.NoError(t, err)
	sent := channel.GetSentMessages()
	lastSent := sent[len(sent)-1]
	assert.Contains(t, lastSent.Text, "Access Denied")

	// 3. User 1 shares agent with User 2
	shareMsg := domain.CanonicalMessage{
		ID:        "msg-share-1",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Sender:    user1,
		Chat:      chat1,
		Text:      "/a share alice_sec 222 operator",
	}
	err = eng.HandleDebouncedMessage(ctx, shareMsg)
	require.NoError(t, err)
	sent = channel.GetSentMessages()
	lastSent = sent[len(sent)-1]
	assert.Contains(t, lastSent.Text, "Access granted")

	// 4. User 2 switches to 'alice_sec' -> Succeeded
	err = eng.HandleDebouncedMessage(ctx, switchMsg)
	require.NoError(t, err)
	sent = channel.GetSentMessages()
	lastSent = sent[len(sent)-1]
	assert.Contains(t, lastSent.Text, "Switched active agent to **alice_sec**")

	// 5. User 1 inspects agent info -> contains collaborator info
	infoMsg := domain.CanonicalMessage{
		ID:        "msg-info-1",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Sender:    user1,
		Chat:      chat1,
		Text:      "/a info alice_sec",
	}
	err = eng.HandleDebouncedMessage(ctx, infoMsg)
	require.NoError(t, err)
	sent = channel.GetSentMessages()
	lastSent = sent[len(sent)-1]
	assert.Contains(t, lastSent.Text, "Collaborators (1)")
	assert.Contains(t, lastSent.Text, "222")

	// 6. User 1 revokes User 2
	revokeMsg := domain.CanonicalMessage{
		ID:        "msg-revoke-1",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Sender:    user1,
		Chat:      chat1,
		Text:      "/a revoke alice_sec 222",
	}
	err = eng.HandleDebouncedMessage(ctx, revokeMsg)
	require.NoError(t, err)
	sent = channel.GetSentMessages()
	lastSent = sent[len(sent)-1]
	assert.Contains(t, lastSent.Text, "Access revoked")

	// 7. User 2 tries to switch again -> Access Denied
	err = eng.HandleDebouncedMessage(ctx, switchMsg)
	require.NoError(t, err)
	sent = channel.GetSentMessages()
	lastSent = sent[len(sent)-1]
	assert.Contains(t, lastSent.Text, "Access Denied")
}

func TestEngine_DedicatedAgentBinding(t *testing.T) {
	eng, runner, _, store, _, cleanup := setupTestEngine(t)
	defer cleanup()

	ctx := context.Background()
	require.NoError(t, eng.Start(ctx))

	// Create dedicated agent
	agent := &domain.Agent{
		Name:          "dev_bot",
		Description:   "Dedicated Dev Bot",
		Status:        domain.StatusInitialized,
		WorkspacePath: t.TempDir(),
		IsPublic:      true,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	require.NoError(t, store.SaveAgent(ctx, agent))

	msg := domain.CanonicalMessage{
		ID:          "msg-bot-bind-1",
		Timestamp:   time.Now(),
		Channel:     "telegram",
		BotID:       9999,
		BotUsername: "dev_agent_bot",
		BindAgent:   "dev_bot",
		Sender:      domain.SenderUser{ID: "555", Username: "developer"},
		Chat:        domain.ChatContext{ID: "555", Type: "private"},
		Text:        "Hello dedicated bot",
	}

	err := eng.HandleDebouncedMessage(ctx, msg)
	require.NoError(t, err)

	session, err := store.GetSession(ctx, "telegram:9999:555")
	require.NoError(t, err)
	assert.Equal(t, "dev_bot", session.ActiveAgent)

	require.NotEmpty(t, runner.executeCalls)
}

func TestEngine_SecurityPresetMonotonicUpgradeAndKeyboard(t *testing.T) {
	eng, _, channel, store, _, cleanup := setupTestEngine(t)
	defer cleanup()

	ctx := context.Background()
	require.NoError(t, eng.Start(ctx))

	adminUser := domain.SenderUser{ID: "123456", Username: "admin"}
	chat := domain.ChatContext{ID: "123456", Type: "private"}
	sessionKey := "telegram:123456"

	// Register admin in storage
	require.NoError(t, store.SaveUser(ctx, &domain.User{
		ID:        "123456",
		Username:  "admin",
		FullName:  "Admin User",
		Role:      "admin",
		CreatedAt: time.Now(),
	}))

	// Create test agent with baseline balanced
	agent := &domain.Agent{
		Name:           "sec_agent",
		Description:    "Security Test Agent",
		Status:         domain.StatusInitialized,
		WorkspacePath:  t.TempDir(),
		SecurityPreset: domain.PresetBalanced,
		OwnerID:        "123456",
		IsPublic:       true,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	require.NoError(t, store.SaveAgent(ctx, agent))

	// Bind session to sec_agent
	sess, err := store.GetOrCreateSession(ctx, sessionKey, "sec_agent")
	require.NoError(t, err)
	sess.ActiveAgent = "sec_agent"
	require.NoError(t, store.SaveSession(ctx, sess))

	// 1. Send /security dashboard command
	err = eng.HandleDebouncedMessage(ctx, domain.CanonicalMessage{
		ID:        "msg-sec-1",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Sender:    adminUser,
		Chat:      chat,
		Text:      "/security",
	})
	require.NoError(t, err)

	sent := channel.GetSentMessages()
	lastSent := sent[len(sent)-1]
	assert.Contains(t, lastSent.Text, "Active Agent")
	assert.Contains(t, lastSent.Text, "sec_agent")
	assert.NotNil(t, lastSent.InlineKeyboard)

	// Verify inline keyboard only contains valid presets (balanced, strict, read_only)
	var callbackDataList []string
	for _, row := range lastSent.InlineKeyboard {
		for _, btn := range row {
			callbackDataList = append(callbackDataList, btn.CallbackData)
		}
	}
	assert.Contains(t, callbackDataList, "sec:preset:balanced")
	assert.Contains(t, callbackDataList, "sec:preset:strict")
	assert.Contains(t, callbackDataList, "sec:preset:read_only")
	assert.NotContains(t, callbackDataList, "sec:preset:unrestricted")
	assert.NotContains(t, callbackDataList, "sec:preset:developer")

	// 2. Try illegal downgrade: /security preset developer
	err = eng.HandleDebouncedMessage(ctx, domain.CanonicalMessage{
		ID:        "msg-sec-2",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Sender:    adminUser,
		Chat:      chat,
		Text:      "/security preset developer",
	})
	require.NoError(t, err)
	sent = channel.GetSentMessages()
	lastSent = sent[len(sent)-1]
	assert.Contains(t, lastSent.Text, "Cannot downgrade security preset")

	// 3. Valid upgrade: /security preset strict
	err = eng.HandleDebouncedMessage(ctx, domain.CanonicalMessage{
		ID:        "msg-sec-3",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Sender:    adminUser,
		Chat:      chat,
		Text:      "/security preset strict",
	})
	require.NoError(t, err)
	sent = channel.GetSentMessages()
	lastSent = sent[len(sent)-1]
	assert.Contains(t, lastSent.Text, "Security preset successfully switched to")
	assert.Contains(t, lastSent.Text, "strict")

	// Verify agent updated in database
	dbAgent, err := store.GetAgent(ctx, "sec_agent")
	require.NoError(t, err)
	assert.Equal(t, domain.PresetStrict, dbAgent.SecurityPreset)

	// 4. Send /security again, keyboard now only has strict and read_only
	err = eng.HandleDebouncedMessage(ctx, domain.CanonicalMessage{
		ID:        "msg-sec-4",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Sender:    adminUser,
		Chat:      chat,
		Text:      "/security",
	})
	require.NoError(t, err)
	sent = channel.GetSentMessages()
	lastSent = sent[len(sent)-1]

	var callbackDataListStrict []string
	for _, row := range lastSent.InlineKeyboard {
		for _, btn := range row {
			callbackDataListStrict = append(callbackDataListStrict, btn.CallbackData)
		}
	}
	assert.NotContains(t, callbackDataListStrict, "sec:preset:balanced")
	assert.Contains(t, callbackDataListStrict, "sec:preset:strict")
	assert.Contains(t, callbackDataListStrict, "sec:preset:read_only")

	// 5. Try illegal downgrade: /security preset balanced
	err = eng.HandleDebouncedMessage(ctx, domain.CanonicalMessage{
		ID:        "msg-sec-5",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Sender:    adminUser,
		Chat:      chat,
		Text:      "/security preset balanced",
	})
	require.NoError(t, err)
	sent = channel.GetSentMessages()
	lastSent = sent[len(sent)-1]
	assert.Contains(t, lastSent.Text, "Cannot downgrade security preset")
}

func TestEngine_NewConversationBootstrapAndGreeting(t *testing.T) {
	eng, runner, channel, store, _, cleanup := setupTestEngine(t)
	defer cleanup()

	ctx := context.Background()
	require.NoError(t, eng.Start(ctx))

	sender := domain.SenderUser{ID: "user-123", Username: "stevan"}
	chat := domain.ChatContext{ID: "chat-123", Type: "private"}

	// Case 1: Initialized Agent sends /new (proactive greeting without topic)
	msgNew := domain.CanonicalMessage{
		ID:        "msg-new-1",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Sender:    sender,
		Chat:      chat,
		Text:      "/new",
	}
	sessionKey := msgNew.SessionKey()

	err := eng.HandleDebouncedMessage(ctx, msgNew)
	require.NoError(t, err)

	// Verify runner executed a turn
	require.NotEmpty(t, runner.executeCalls)
	lastCall := runner.executeCalls[len(runner.executeCalls)-1]
	assert.Empty(t, lastCall.ConversationID, "New conversation turn must start with empty ConversationID")
	assert.Contains(t, lastCall.Prompt, "[SYSTEM DIRECTIVE: NEW CONVERSATION INITIALIZATION]")
	assert.Contains(t, lastCall.Prompt, "The user has initiated a fresh conversation session.")

	// Verify session now has the new conversation ID
	session, err := store.GetSession(ctx, sessionKey)
	require.NoError(t, err)
	assert.Equal(t, "conv-123", session.GetActiveConversationID())

	// Verify greeting message sent to channel
	sent := channel.GetSentMessages()
	require.NotEmpty(t, sent)
	lastSent := sent[len(sent)-1]
	assert.Contains(t, lastSent.Text, "Mock response for:")

	// Case 2: Send /new with topic: /new Xây dựng API authentication
	msgNewWithTopic := domain.CanonicalMessage{
		ID:        "msg-new-2",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Sender:    sender,
		Chat:      chat,
		Text:      "/new Xây dựng API authentication",
	}

	err = eng.HandleDebouncedMessage(ctx, msgNewWithTopic)
	require.NoError(t, err)

	lastCall = runner.executeCalls[len(runner.executeCalls)-1]
	assert.Empty(t, lastCall.ConversationID)
	assert.Contains(t, lastCall.Prompt, "[SYSTEM DIRECTIVE: NEW CONVERSATION INITIALIZATION]")
	assert.Contains(t, lastCall.Prompt, "Xây dựng API authentication")

	// Case 3: Send /c new with topic
	msgCNew := domain.CanonicalMessage{
		ID:        "msg-new-3",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Sender:    sender,
		Chat:      chat,
		Text:      "/c new Refactor Database Layer",
	}

	err = eng.HandleDebouncedMessage(ctx, msgCNew)
	require.NoError(t, err)

	lastCall = runner.executeCalls[len(runner.executeCalls)-1]
	assert.Contains(t, lastCall.Prompt, "Refactor Database Layer")

	// Case 4: Uninitialized agent Genesis bootstrap on /new
	uninitAgent := &domain.Agent{
		Name:          "newbie_agent",
		Description:   "Newbie Assistant",
		Status:        domain.StatusUninitialized,
		WorkspacePath: t.TempDir(),
		OwnerID:       sender.ID,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	require.NoError(t, store.SaveAgent(ctx, uninitAgent))

	session.ActiveAgent = "newbie_agent"
	session.ResetActiveConversationID()
	require.NoError(t, store.SaveSession(ctx, session))

	msgGenesisNew := domain.CanonicalMessage{
		ID:        "msg-genesis-new",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Sender:    sender,
		Chat:      chat,
		Text:      "/new Khởi tạo trợ lý mới",
	}

	err = eng.HandleDebouncedMessage(ctx, msgGenesisNew)
	require.NoError(t, err)

	lastCall = runner.executeCalls[len(runner.executeCalls)-1]
	assert.Contains(t, lastCall.Prompt, "[SYSTEM BOOTSTRAP PROTOCOL - MANDATORY INITIALIZATION]")

	// Agent should now be initialized in database
	dbAgent, err := store.GetAgent(ctx, "newbie_agent")
	require.NoError(t, err)
	assert.Equal(t, domain.StatusInitialized, dbAgent.Status)
}

func TestEngine_PerAgentSecurityIsolationAndConfig(t *testing.T) {
	eng, runner, channel, store, cfg, cleanup := setupTestEngine(t)
	defer cleanup()

	// Configure per-agent security overrides in config
	cfg.Security.Preset = "balanced"
	cfg.Agents = map[string]config.AgentProfileConfig{
		"admin_agent": {
			SecurityPreset: "unrestricted",
			DefaultModel:   "gemini-2.5-pro",
		},
		"auditor_agent": {
			SecurityPreset: "strict",
			DefaultModel:   "gemini-2.5-flash",
		},
	}

	ctx := context.Background()
	require.NoError(t, eng.Start(ctx))

	adminUser := domain.SenderUser{ID: "123456", Username: "admin"}
	chat := domain.ChatContext{ID: "chat-sec-1", Type: "private"}

	// 1. Initialize admin_agent via message
	adminMsg := domain.CanonicalMessage{
		ID:        "msg-admin-1",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Sender:    adminUser,
		Chat:      chat,
		Text:      "/use admin_agent",
	}
	require.NoError(t, eng.HandleDebouncedMessage(ctx, adminMsg))

	// Send a turn to trigger agent creation and verification
	turnMsg1 := domain.CanonicalMessage{
		ID:        "msg-admin-turn",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Sender:    adminUser,
		Chat:      chat,
		Text:      "Run deployment tasks",
	}
	require.NoError(t, eng.HandleDebouncedMessage(ctx, turnMsg1))

	agent1, err := store.GetAgent(ctx, "admin_agent")
	require.NoError(t, err)
	assert.Equal(t, domain.PresetUnrestricted, agent1.SecurityPreset, "admin_agent must be initialized with unrestricted preset from config")

	// Check /security output for admin_agent
	secCmd1 := domain.CanonicalMessage{
		ID:        "msg-admin-sec",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Sender:    adminUser,
		Chat:      chat,
		Text:      "/security",
	}
	require.NoError(t, eng.HandleDebouncedMessage(ctx, secCmd1))
	sent := channel.GetSentMessages()
	require.NotEmpty(t, sent)
	lastSent := sent[len(sent)-1]
	assert.Contains(t, lastSent.Text, "Active Agent</b>      : <code>admin_agent</code>")
	assert.Contains(t, lastSent.Text, "Active Preset</b>     : <code>unrestricted</code>")

	// 2. Initialize auditor_agent via /use
	auditMsg := domain.CanonicalMessage{
		ID:        "msg-audit-1",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Sender:    adminUser,
		Chat:      chat,
		Text:      "/use auditor_agent",
	}
	require.NoError(t, eng.HandleDebouncedMessage(ctx, auditMsg))

	turnMsg2 := domain.CanonicalMessage{
		ID:        "msg-audit-turn",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Sender:    adminUser,
		Chat:      chat,
		Text:      "Inspect codebase vulnerabilities",
	}
	require.NoError(t, eng.HandleDebouncedMessage(ctx, turnMsg2))

	agent2, err := store.GetAgent(ctx, "auditor_agent")
	require.NoError(t, err)
	assert.Equal(t, domain.PresetStrict, agent2.SecurityPreset, "auditor_agent must be initialized with strict preset from config")

	// Check /security output for auditor_agent
	secCmd2 := domain.CanonicalMessage{
		ID:        "msg-audit-sec",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Sender:    adminUser,
		Chat:      chat,
		Text:      "/security",
	}
	require.NoError(t, eng.HandleDebouncedMessage(ctx, secCmd2))
	sent = channel.GetSentMessages()
	lastSent = sent[len(sent)-1]
	assert.Contains(t, lastSent.Text, "Active Agent</b>      : <code>auditor_agent</code>")
	assert.Contains(t, lastSent.Text, "Active Preset</b>     : <code>strict</code>")

	// 3. Initialize unlisted agent -> falls back to balanced
	fallbackMsg := domain.CanonicalMessage{
		ID:        "msg-fall-1",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Sender:    adminUser,
		Chat:      chat,
		Text:      "/use generic_assistant",
	}
	require.NoError(t, eng.HandleDebouncedMessage(ctx, fallbackMsg))

	turnMsg3 := domain.CanonicalMessage{
		ID:        "msg-fall-turn",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Sender:    adminUser,
		Chat:      chat,
		Text:      "Help me summarize notes",
	}
	require.NoError(t, eng.HandleDebouncedMessage(ctx, turnMsg3))

	agent3, err := store.GetAgent(ctx, "generic_assistant")
	require.NoError(t, err)
	assert.Equal(t, domain.PresetBalanced, agent3.SecurityPreset, "unlisted agent must fall back to balanced preset")

	_ = runner
}

func TestEngine_InboundAttachmentRelocation(t *testing.T) {
	ctx := context.Background()
	eng, runner, _, store, cfg, cleanup := setupTestEngine(t)
	defer cleanup()

	wsMgr := workspaceAdapter.NewManager(nil)
	eng.SetWorkspaceManager(wsMgr)

	// Create staging file
	stagingDir := filepath.Join(cfg.Storage.AgentsDir, "staging")
	require.NoError(t, os.MkdirAll(stagingDir, 0755))
	srcPath := filepath.Join(stagingDir, "sample_spec.pdf")
	require.NoError(t, os.WriteFile(srcPath, []byte("PDF specifications content 123"), 0644))

	msg := domain.CanonicalMessage{
		ID:        "msg-att-1",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Sender:    domain.SenderUser{ID: "111", Username: "stevan", FullName: "Stevan"},
		Chat:      domain.ChatContext{ID: "chat-att-1", Type: "private"},
		Text:      "Please read this uploaded spec",
		Attachments: []domain.Attachment{
			{
				ID:       "att-file-1",
				FileName: "sample_spec.pdf",
				FilePath: srcPath,
				Type:     "document",
				MIMEType: "application/pdf",
			},
		},
	}

	err := eng.HandleDebouncedMessage(ctx, msg)
	require.NoError(t, err)

	// Verify runner received the turn request with attachments inside workspace
	runner.mu.Lock()
	calls := runner.streamCalls
	if len(calls) == 0 {
		calls = runner.executeCalls
	}
	runner.mu.Unlock()

	require.NotEmpty(t, calls, "Runner must have received an execution turn")
	lastReq := calls[len(calls)-1]

	// Prompt must contain the in-workspace uploads path
	assert.Contains(t, lastReq.Prompt, "uploads")
	assert.Contains(t, lastReq.Prompt, "sample_spec.pdf")

	// Verify uploads dir in agent workspace contains the file and .gitignore
	agent, err := store.GetAgent(ctx, "agyent")
	require.NoError(t, err)
	uploadsDir := filepath.Join(agent.WorkspacePath, "uploads")
	assert.DirExists(t, uploadsDir)
	assert.FileExists(t, filepath.Join(uploadsDir, ".gitignore"))

	// Original staging file was relocated
	assert.NoFileExists(t, srcPath)
}

type mockEngineEvolutionOrchestrator struct {
	mu             sync.Mutex
	triggeredCount int
	lastConvID     string
	lastTrigger    domain.EvolutionTrigger
}

func (m *mockEngineEvolutionOrchestrator) Start(ctx context.Context) error { return nil }
func (m *mockEngineEvolutionOrchestrator) Stop(ctx context.Context) error  { return nil }
func (m *mockEngineEvolutionOrchestrator) TriggerConversationEvolution(ctx context.Context, convID string, trigger domain.EvolutionTrigger) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.triggeredCount++
	m.lastConvID = convID
	m.lastTrigger = trigger
	return nil
}
func (m *mockEngineEvolutionOrchestrator) NotifyUserActivity(sessionKey string) {}

func TestEngine_EvolutionExplicitSwitchHooks(t *testing.T) {
	eng, _, _, store, _, cleanup := setupTestEngine(t)
	defer cleanup()

	ctx := context.Background()
	mockEvo := &mockEngineEvolutionOrchestrator{}
	eng.SetEvolutionOrchestrator(mockEvo)

	sender := domain.SenderUser{ID: "998877", Username: "tester"}
	chat := domain.ChatContext{ID: "998877", Type: "private"}

	sessionKey := "telegram:998877"
	sess, err := store.GetOrCreateSession(ctx, sessionKey, "agyent")
	require.NoError(t, err)

	// 1. Set active conversation
	sess.SetActiveConversationID("conv-old-reset")
	require.NoError(t, store.SaveSession(ctx, sess))

	// 2. Test /reset command triggers evolution
	resetMsg := domain.CanonicalMessage{
		ID:        "msg-reset",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Text:      "/reset",
		Sender:    sender,
		Chat:      chat,
	}
	outbound, err := eng.HandleCommand(ctx, resetMsg)
	require.NoError(t, err)
	require.NotNil(t, outbound)
	assert.Contains(t, outbound.Text, "Short-term conversation context reset")

	// Wait for non-blocking SafeGo execution
	time.Sleep(50 * time.Millisecond)

	mockEvo.mu.Lock()
	assert.Equal(t, 1, mockEvo.triggeredCount)
	assert.Equal(t, "conv-old-reset", mockEvo.lastConvID)
	assert.Equal(t, domain.TriggerExplicitSwitch, mockEvo.lastTrigger)
	mockEvo.mu.Unlock()

	// 3. Set another active conversation and test /new command
	sess, err = store.GetOrCreateSession(ctx, sessionKey, "agyent")
	require.NoError(t, err)
	sess.SetActiveConversationID("conv-old-new")
	require.NoError(t, store.SaveSession(ctx, sess))

	newMsg := domain.CanonicalMessage{
		ID:        "msg-new",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Text:      "/new",
		Sender:    sender,
		Chat:      chat,
	}
	err = eng.HandleDebouncedMessage(ctx, newMsg)
	require.NoError(t, err)

	// Wait for non-blocking SafeGo execution
	time.Sleep(50 * time.Millisecond)

	mockEvo.mu.Lock()
	assert.Equal(t, 2, mockEvo.triggeredCount)
	assert.Equal(t, "conv-old-new", mockEvo.lastConvID)
	assert.Equal(t, domain.TriggerExplicitSwitch, mockEvo.lastTrigger)
	mockEvo.mu.Unlock()
}


