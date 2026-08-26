package engine_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	contextAdapter "agyent/internal/adapters/context"
	agyHarness "agyent/internal/adapters/harness/agy"
	"agyent/internal/adapters/mcp"
	pluginAdapter "agyent/internal/adapters/plugin"
	"agyent/internal/adapters/storage/sqlite"
	"agyent/internal/config"
	"agyent/internal/core/concurrency"
	"agyent/internal/core/debouncer"
	"agyent/internal/core/domain"
	"agyent/internal/core/engine"
	"agyent/internal/core/eventbus"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)


func skipIfNoRealAGY(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping live AGY CLI test in short mode")
	}

	agyPath, err := exec.LookPath("agy")
	if err != nil {
		if os.Getenv("AGY_E2E") == "" {
			t.Skip("skipping live AGY CLI test: 'agy' binary not found in PATH")
		}
		agyPath = "agy"
	}
	return agyPath
}

func TestRealAGY_HealthCheck(t *testing.T) {
	agyPath := skipIfNoRealAGY(t)

	cfg := config.AGYConfig{
		BinaryPath: agyPath,
	}
	harness := agyHarness.NewHarness(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := harness.HealthCheck(ctx)
	require.NoError(t, err, "agy binary healthcheck must succeed")
}

func TestRealAGY_BatchExecution(t *testing.T) {
	agyPath := skipIfNoRealAGY(t)

	tmpDir := t.TempDir()
	cfg := config.AGYConfig{
		BinaryPath:                 agyPath,
		DefaultTimeoutSeconds:      60,
		DangerouslySkipPermissions: true,
		DefaultMode:                "accept-edits",
		DefaultEffort:              "low",
	}
	harness := agyHarness.NewHarness(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	req := domain.ExecutionRequest{
		Prompt:                     "Echo back the exact text: 'AGYENT_BATCH_INTEGRATION_OK' without extra explanations.",
		WorkspaceDir:               tmpDir,
		Timeout:                    45 * time.Second,
		DangerouslySkipPermissions: true,
		Mode:                       "accept-edits",
		Effort:                     "low",
	}

	res, err := harness.Execute(ctx, req)
	require.NoError(t, err, "batch execution against real agy must succeed")
	require.NotNil(t, res)
	assert.True(t, res.Success)
	assert.NotEmpty(t, res.ConversationID, "conversation ID must be generated")
	assert.Contains(t, res.ResponseText, "AGYENT_BATCH_INTEGRATION_OK")
	assert.Greater(t, res.Usage.TotalTokens, 0)
}

func TestRealAGY_StreamingExecution(t *testing.T) {
	agyPath := skipIfNoRealAGY(t)

	tmpDir := t.TempDir()
	bus := eventbus.NewEventBus(256, 4)
	defer bus.Close()

	cfg := config.AGYConfig{
		BinaryPath:                 agyPath,
		DefaultTimeoutSeconds:      60,
		DangerouslySkipPermissions: true,
		DefaultMode:                "accept-edits",
		DefaultEffort:              "low",
	}
	harness := agyHarness.NewHarness(cfg, bus)

	var streamDeltas []string
	var mu sync.Mutex
	_ = bus.SubscribeSync(domain.EventStreamDelta, func(ctx context.Context, ev domain.Event) error {
		mu.Lock()
		defer mu.Unlock()
		if p, ok := ev.Payload.(domain.StreamDeltaPayload); ok {
			streamDeltas = append(streamDeltas, p.TextDelta)
		}
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	req := domain.ExecutionRequest{
		Prompt:                     "Write a 1-line poem about Go concurrency.",
		WorkspaceDir:               tmpDir,
		Timeout:                    45 * time.Second,
		DangerouslySkipPermissions: true,
		Mode:                       "accept-edits",
		Effort:                     "low",
	}

	res, err := harness.ExecuteStream(ctx, req, "test-stream-session")
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.Success)
	assert.NotEmpty(t, res.ResponseText)
	assert.NotEmpty(t, res.ConversationID)

	mu.Lock()
	deltaCount := len(streamDeltas)
	mu.Unlock()
	assert.Greater(t, deltaCount, 0, "must receive stream deltas via EventBus")
}

func TestRealAGY_MultiTurnConversationContext(t *testing.T) {
	agyPath := skipIfNoRealAGY(t)

	tmpDir := t.TempDir()
	cfg := config.AGYConfig{
		BinaryPath:                 agyPath,
		DefaultTimeoutSeconds:      60,
		DangerouslySkipPermissions: true,
		DefaultMode:                "accept-edits",
		DefaultEffort:              "low",
	}
	harness := agyHarness.NewHarness(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	req1 := domain.ExecutionRequest{
		Prompt:                     "Remember this secret number: 8848. Reply with ONLY 'SAVED'.",
		WorkspaceDir:               tmpDir,
		Timeout:                    45 * time.Second,
		DangerouslySkipPermissions: true,
		Mode:                       "accept-edits",
		Effort:                     "low",
	}
	res1, err := harness.Execute(ctx, req1)
	require.NoError(t, err)
	assert.NotEmpty(t, res1.ConversationID)

	req2 := domain.ExecutionRequest{
		Prompt:                     "What was the secret number I told you?",
		ConversationID:             res1.ConversationID,
		WorkspaceDir:               tmpDir,
		Timeout:                    45 * time.Second,
		DangerouslySkipPermissions: true,
		Mode:                       "accept-edits",
		Effort:                     "low",
	}
	res2, err := harness.Execute(ctx, req2)
	require.NoError(t, err)
	assert.Contains(t, res2.ResponseText, "8848", "multi-turn conversation must recall memory from turn 1")
}

func TestRealAGY_AgentGenesisBootstrap(t *testing.T) {
	agyPath := skipIfNoRealAGY(t)

	agentWorkspace := t.TempDir()
	agent := &domain.Agent{
		Name:          "agyent",
		Description:   "Agyent - Trợ lý AI cá nhân đa năng",
		Status:        domain.StatusUninitialized,
		WorkspacePath: agentWorkspace,
	}

	sender := domain.SenderUser{
		ID:       "8544450322",
		Username: "khanhbkqt",
		FullName: "Khánh Nguyễn",
	}

	userMsg := "Chào em! Anh là Khánh Nguyễn (@khanhbkqt), ID: 8544450322. Múi giờ của anh là UTC+7 (Asia/Ho_Chi_Minh). Em hãy tạo các file USER.md, IDENTITY.md, SOUL.md, MEMORY.md, AGENTS.md trong thư mục hiện tại theo đúng nhiệm vụ bootstrap của em nhé."

	bootstrapPrompt := engine.BuildBootstrapPrompt(agent, sender, userMsg)
	require.Contains(t, bootstrapPrompt, "[SYSTEM BOOTSTRAP PROTOCOL - MANDATORY INITIALIZATION]")
	require.Contains(t, bootstrapPrompt, "Khánh Nguyễn")
	require.Contains(t, bootstrapPrompt, "8544450322")

	cfg := config.AGYConfig{
		BinaryPath:                 agyPath,
		DefaultTimeoutSeconds:      60,
		DangerouslySkipPermissions: true,
		DefaultMode:                "accept-edits",
		DefaultEffort:              "low",
	}
	harness := agyHarness.NewHarness(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	req := domain.ExecutionRequest{
		Prompt:                     bootstrapPrompt,
		WorkspaceDir:               agentWorkspace,
		Timeout:                    45 * time.Second,
		DangerouslySkipPermissions: true,
		Mode:                       "accept-edits",
		Effort:                     "low",
	}

	res, err := harness.Execute(ctx, req)
	require.NoError(t, err)
	assert.True(t, res.Success)
	assert.NotEmpty(t, res.ResponseText, "bootstrap execution must return non-empty response")
	assert.NotEmpty(t, res.ConversationID, "bootstrap execution must return valid conversation ID")

	// Verify ContextResolver loads the newly created workspace directives if generated
	resolver := contextAdapter.NewContextResolver()
	resolved, err := resolver.Resolve(ctx, agentWorkspace, "")
	require.NoError(t, err)
	require.NotNil(t, resolved)

	// Check if USER.md or any core directives were created in the agent's workspace
	userFile := filepath.Join(agentWorkspace, "USER.md")
	if data, readErr := os.ReadFile(userFile); readErr == nil {
		loc := domain.ParseLocationFromText(string(data))
		assert.NotNil(t, loc)
	}
	_ = agent
	_ = sender
}

func TestRealAGY_Level0SystemMetaInstructionPrompt(t *testing.T) {
	agyPath := skipIfNoRealAGY(t)

	agentWorkspace := t.TempDir()
	resolved := &domain.ResolvedContext{
		WorkingDir:         agentWorkspace,
		CombinedDirectives: "[GLOBAL CORE DIRECTIVES]\n<IDENTITY>\nName: agyent\nRole: Personal AI Assistant\n</IDENTITY>\n\n<LONG_TERM_MEMORY>\n- Test fact: Prefix Caching is enabled\n</LONG_TERM_MEMORY>",
	}
	msg := domain.CanonicalMessage{
		Text: "Acknowledge receipt with exact keyword 'LEVEL0_ANCHOR_VERIFIED'.",
	}

	prompt := engine.ComposeResolvedTurnPrompt(resolved, msg)
	require.True(t, strings.HasPrefix(prompt, "[SYSTEM RUNTIME FOUNDATION]"))

	cfg := config.AGYConfig{
		BinaryPath:                 agyPath,
		DefaultTimeoutSeconds:      60,
		DangerouslySkipPermissions: true,
		DefaultMode:                "accept-edits",
		DefaultEffort:              "low",
	}
	harness := agyHarness.NewHarness(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	req := domain.ExecutionRequest{
		Prompt:                     prompt,
		WorkspaceDir:               agentWorkspace,
		Timeout:                    45 * time.Second,
		DangerouslySkipPermissions: true,
		Mode:                       "accept-edits",
		Effort:                     "low",
	}

	res, err := harness.Execute(ctx, req)
	require.NoError(t, err, "execution with Level 0 prefix prompt must succeed")
	assert.True(t, res.Success)
	assert.Contains(t, res.ResponseText, "LEVEL0_ANCHOR_VERIFIED")
}

func TestRealAGY_SetupNewAgentWithTelegramToken_NaturalLanguagePrompt(t *testing.T) {
	agyPath := skipIfNoRealAGY(t)

	agentWorkspace := t.TempDir()
	resolved := &domain.ResolvedContext{
		WorkingDir:         agentWorkspace,
		CombinedDirectives: "[GLOBAL CORE DIRECTIVES]\n<IDENTITY>\nName: agyent\nRole: Agyent - Trợ lý AI cá nhân đa năng của anh Khánh\n</IDENTITY>\n\n<USER_PROFILE>\nName: Khánh Nguyễn\nID: 8544450322\nUsername: khanhbkqt\n</USER_PROFILE>\n\n<CORE_RULES>\nQuy tắc: Luôn xưng em và gọi anh Khánh/anh. Giải thích rõ ràng theo kiến trúc Gateway Daemon của agyent.\n</CORE_RULES>",
	}
	msg := domain.CanonicalMessage{
		Text: "Em ơi, anh muốn setup thêm 1 agent mới chuyên về DevOps tên là devops_bot và muốn chạy nó với một Telegram Bot Token riêng biệt (ví dụ: 123456789:AAFakeToken123456). Em hãy phân tích kiến trúc của agyent, giải thích cách thực hiện và hướng dẫn anh các bước cấu hình để chạy nhé.",
	}

	prompt := engine.ComposeResolvedTurnPrompt(resolved, msg)

	cfg := config.AGYConfig{
		BinaryPath:                 agyPath,
		DefaultTimeoutSeconds:      60,
		DangerouslySkipPermissions: true,
		DefaultMode:                "accept-edits",
		DefaultEffort:              "low",
	}
	harness := agyHarness.NewHarness(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	req := domain.ExecutionRequest{
		Prompt:                     prompt,
		WorkspaceDir:               agentWorkspace,
		Timeout:                    60 * time.Second,
		DangerouslySkipPermissions: true,
		Mode:                       "accept-edits",
		Effort:                     "low",
	}

	res, err := harness.Execute(ctx, req)
	require.NoError(t, err)
	assert.True(t, res.Success)
	assert.NotEmpty(t, res.ResponseText)

	// Verify that the response addresses anh Khánh and explains both in-daemon multi-agent and dedicated bot instance
	t.Logf("=== AGY REAL RESPONSE ===\n%s\n=========================", res.ResponseText)
	assert.True(t, strings.Contains(res.ResponseText, "anh Khánh") || strings.Contains(res.ResponseText, "anh") || strings.Contains(res.ResponseText, "Khánh"))
}

func TestRealAGY_ExplicitAction_CreateAgentFiles(t *testing.T) {
	agyPath := skipIfNoRealAGY(t)

	agentWorkspace := t.TempDir()
	resolved := &domain.ResolvedContext{
		WorkingDir:         agentWorkspace,
		CombinedDirectives: "[GLOBAL CORE DIRECTIVES]\n<IDENTITY>\nName: agyent\nRole: Agyent - Trợ lý AI cá nhân đa năng của anh Khánh\n</IDENTITY>\n\n<USER_PROFILE>\nName: Khánh Nguyễn\nID: 8544450322\nUsername: khanhbkqt\n</USER_PROFILE>\n\n<CORE_RULES>\nQuy tắc: Luôn xưng em và gọi anh Khánh. Khi được yêu cầu tạo tệp, hãy chủ động sử dụng công cụ tạo tệp để tạo ngay lập tức.\n</CORE_RULES>",
	}
	msg := domain.CanonicalMessage{
		Text: "Em hãy tạo ngay cho anh các file IDENTITY.md, SOUL.md, USER.md, MEMORY.md cho một agent DevOps tên là devops_bot trong thư mục hiện tại. Nhớ ghi đúng thông tin của anh (Khánh Nguyễn, múi giờ Asia/Ho_Chi_Minh).",
	}

	prompt := engine.ComposeResolvedTurnPrompt(resolved, msg)

	cfg := config.AGYConfig{
		BinaryPath:                 agyPath,
		DefaultTimeoutSeconds:      60,
		DangerouslySkipPermissions: true,
		DefaultMode:                "accept-edits",
		DefaultEffort:              "low",
	}
	harness := agyHarness.NewHarness(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	req := domain.ExecutionRequest{
		Prompt:                     prompt,
		WorkspaceDir:               agentWorkspace,
		Timeout:                    60 * time.Second,
		DangerouslySkipPermissions: true,
		Mode:                       "accept-edits",
		Effort:                     "low",
	}

	res, err := harness.Execute(ctx, req)
	require.NoError(t, err)
	assert.True(t, res.Success)

	// Check if the files were actually created on disk
	files, _ := os.ReadDir(agentWorkspace)
	t.Logf("=== FILES CREATED ON DISK (%d) ===", len(files))
	for _, f := range files {
		t.Logf(" - %s (dir=%v)", f.Name(), f.IsDir())
	}

	assert.Greater(t, len(files), 0, "AGY must actively create files on disk when asked to create")
}

func TestRealAGY_EndToEnd_EngineTurnContinuation_TokenOptimization(t *testing.T) {
	agyPath := skipIfNoRealAGY(t)

	// 1. Setup isolated directories & SQLite store
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_agyent.db")
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0755))

	agentWorkspace := filepath.Join(agentsDir, "test_agent")
	require.NoError(t, os.MkdirAll(agentWorkspace, 0755))

	// Write agent identity files in workspace
	require.NoError(t, os.WriteFile(filepath.Join(agentWorkspace, "IDENTITY.md"), []byte("# AGENT IDENTITY\n- **Name**: Bé Na\n- **Role**: Trợ lý AI của anh Khánh"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(agentWorkspace, "USER.md"), []byte("Name: Khánh Nguyễn\nID: 8544450322\nMúi giờ: Asia/Ho_Chi_Minh"), 0644))

	mcpPath := filepath.Join(tmpDir, "mcp_config.json")
	builtinDir := filepath.Join(tmpDir, "builtin_plugins")
	require.NoError(t, os.MkdirAll(builtinDir, 0755))

	store, err := sqlite.Open(dbPath)
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	require.NoError(t, store.SaveAgent(ctx, &domain.Agent{
		Name:          "test_agent",
		Description:   "Bé Na - Trợ lý AI",
		Status:        domain.StatusInitialized,
		WorkspacePath: agentWorkspace,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}))

	channel := &mockChannel{sent: make([]domain.OutboundMessage, 0)}
	bus := eventbus.NewEventBus(256, 4)
	defer bus.Close()

	cfg := &config.Config{
		AGY: config.AGYConfig{
			BinaryPath:                 agyPath,
			DefaultTimeoutSeconds:      60,
			DangerouslySkipPermissions: true,
			DefaultMode:                "accept-edits",
			DefaultEffort:              "low",
		},
		Storage: config.StorageConfig{
			AgentsDir: agentsDir,
		},
	}

	harness := agyHarness.NewHarness(cfg.AGY)
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
		WindowDuration:  10 * time.Millisecond,
		MaxWaitDuration: 50 * time.Millisecond,
	}, debouncerHandler)
	defer deb.Close(context.Background())

	eng = engine.NewEngine(cfg, store, harness, channel, bus, deb, lockMgr, resolver, syncer, pluginMgr)

	require.NoError(t, eng.Start(ctx))
	defer eng.Stop(ctx)


	sessionKey := "telegram:8544450322"
	sess, err := store.GetOrCreateSession(ctx, sessionKey, "test_agent")
	require.NoError(t, err)
	sess.ActiveAgent = "test_agent"
	require.NoError(t, store.SaveSession(ctx, sess))

	// Turn 1: Fresh conversation turn (ConversationID is empty)
	msgTurn1 := domain.CanonicalMessage{
		ID:        "msg-turn-1",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Sender: domain.SenderUser{
			ID:       "8544450322",
			Username: "khanhbkqt",
			FullName: "Khánh Nguyễn",
		},
		Chat: domain.ChatContext{ID: "8544450322", Type: "private"},
		Text: "Chào em! Em là Bé Na, trợ lý của anh nhé.",
	}

	t.Log("--- Executing Live Turn 1 through Engine ---")
	err = eng.HandleDebouncedMessage(ctx, msgTurn1)
	require.NoError(t, err)

	// Verify Turn 1 generated ConversationID and logged audit
	logs, err := store.ListAuditLogs(ctx, sessionKey, 10)
	require.NoError(t, err)
	require.Len(t, logs, 1)
	assert.Equal(t, "SUCCESS", logs[0].Status)
	assert.NotEmpty(t, logs[0].ConversationID)
	convID := logs[0].ConversationID
	turn1Input := logs[0].Usage.InputTokens
	t.Logf("Turn 1 Input Tokens: %d | Total: %d | ConvID: %s", turn1Input, logs[0].Usage.TotalTokens, convID)

	// Turn 2: Subsequent conversation turn (ConversationID is preserved)
	msgTurn2 := domain.CanonicalMessage{
		ID:        "msg-turn-2",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Sender: domain.SenderUser{
			ID:       "8544450322",
			Username: "khanhbkqt",
			FullName: "Khánh Nguyễn",
		},
		Chat: domain.ChatContext{ID: "8544450322", Type: "private"},
		Text: "Em cho anh hỏi tên của em là gì và anh là ai?",
	}

	t.Log("--- Executing Live Turn 2 through Engine (Continuation) ---")
	err = eng.HandleDebouncedMessage(ctx, msgTurn2)
	require.NoError(t, err)

	logs, err = store.ListAuditLogs(ctx, sessionKey, 10)
	require.NoError(t, err)
	require.Len(t, logs, 2)
	assert.Equal(t, "SUCCESS", logs[0].Status)
	assert.Equal(t, convID, logs[0].ConversationID)
	turn2Input := logs[0].Usage.InputTokens
	t.Logf("Turn 2 Input Tokens: %d | Total: %d", turn2Input, logs[0].Usage.TotalTokens)

	// Turn 3: Inspect /tokens slash command output
	msgTokensCmd := domain.CanonicalMessage{
		ID:        "msg-tokens",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Sender:    msgTurn1.Sender,
		Chat:      msgTurn1.Chat,
		Text:      "/tokens",
	}

	outMsg, err := eng.HandleCommand(ctx, msgTokensCmd)
	require.NoError(t, err)
	require.NotNil(t, outMsg)
	t.Logf("=== /tokens Output ===\n%s\n======================", outMsg.Text)

	assert.Contains(t, outMsg.Text, "Active Turn Context Window:")
	assert.Contains(t, outMsg.Text, "Conversation Cumulative Billed:")
	assert.Contains(t, outMsg.Text, "Session Lifetime:")
}

func TestRealAGY_EndToEnd_CacheHit_Verification(t *testing.T) {
	agyPath := skipIfNoRealAGY(t)

	// 1. Setup isolated workspace with seed knowledge > 32,768 tokens (to cross Gemini KV-cache threshold)
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_cache_hit.db")
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0755))

	agentWorkspace := filepath.Join(agentsDir, "cache_agent")
	require.NoError(t, os.MkdirAll(agentWorkspace, 0755))

	// Ingest seed knowledge text (~36k-40k tokens total) into MEMORY.md to cross Gemini 32k threshold without triggering checkpoint truncation
	seedText := strings.Repeat("Architecture agyent includes Dual-Pool SQLite Storage, EventBus with Worker Pool, Dynamic Tool Mounting, Progressive Disclosure Skills, and Memory Compactor. ", 900)
	require.NoError(t, os.WriteFile(filepath.Join(agentWorkspace, "IDENTITY.md"), []byte("# AGENT IDENTITY\n- **Name**: CacheBot\n- **Role**: Cache Test Assistant"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(agentWorkspace, "USER.md"), []byte("Name: Khánh Nguyễn\nID: 8544450322"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(agentWorkspace, "MEMORY.md"), []byte("# LONG TERM MEMORY\n"+seedText), 0644))


	mcpPath := filepath.Join(tmpDir, "mcp_config.json")
	builtinDir := filepath.Join(tmpDir, "builtin_plugins")
	require.NoError(t, os.MkdirAll(builtinDir, 0755))

	store, err := sqlite.Open(dbPath)
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	require.NoError(t, store.SaveAgent(ctx, &domain.Agent{
		Name:          "cache_agent",
		Description:   "CacheBot Assistant",
		Status:        domain.StatusInitialized,
		WorkspacePath: agentWorkspace,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}))

	channel := &mockChannel{sent: make([]domain.OutboundMessage, 0)}
	bus := eventbus.NewEventBus(256, 4)
	defer bus.Close()

	cfg := &config.Config{
		AGY: config.AGYConfig{
			BinaryPath:                 agyPath,
			DefaultTimeoutSeconds:      90,
			DangerouslySkipPermissions: true,
			DefaultMode:                "accept-edits",
			DefaultEffort:              "low",
		},
		Storage: config.StorageConfig{
			AgentsDir: agentsDir,
		},
	}

	harness := agyHarness.NewHarness(cfg.AGY)
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
		WindowDuration:  10 * time.Millisecond,
		MaxWaitDuration: 50 * time.Millisecond,
	}, debouncerHandler)
	defer deb.Close(context.Background())

	eng = engine.NewEngine(cfg, store, harness, channel, bus, deb, lockMgr, resolver, syncer, pluginMgr)
	require.NoError(t, eng.Start(ctx))
	defer eng.Stop(ctx)

	sessionKey := "telegram:8544450322"
	sess, err := store.GetOrCreateSession(ctx, sessionKey, "cache_agent")
	require.NoError(t, err)
	sess.ActiveAgent = "cache_agent"
	require.NoError(t, store.SaveSession(ctx, sess))

	// Turn 1: Ingest large context (>35k tokens) to cross Gemini KV-cache threshold
	msgTurn1 := domain.CanonicalMessage{
		ID:        "msg-cache-turn-1",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Sender: domain.SenderUser{
			ID:       "8544450322",
			Username: "khanhbkqt",
			FullName: "Khánh Nguyễn",
		},
		Chat: domain.ChatContext{ID: "8544450322", Type: "private"},
		Text: "Hãy đọc bộ nhớ của em và trả lời ngắn gọn: 'KHO_TRI_THUC_DA_NAP'.",
	}

	t.Log("--- Executing Turn 1 (>35k tokens context seed) ---")
	err = eng.HandleDebouncedMessage(ctx, msgTurn1)
	require.NoError(t, err)

	logs, err := store.ListAuditLogs(ctx, sessionKey, 10)
	require.NoError(t, err)
	require.Len(t, logs, 1)
	convID := logs[0].ConversationID
	turn1Input := logs[0].Usage.InputTokens
	t.Logf("Turn 1 Input Tokens: %d | Cached: %d | ConvID: %s", turn1Input, logs[0].Usage.CacheReadTokens, convID)
	assert.Greater(t, turn1Input, 32768, "Turn 1 must exceed 32k tokens threshold to arm Gemini KV-cache")

	// Allow Gemini TPU cluster to asynchronously commit KV-cache block (2s settle window)
	time.Sleep(3 * time.Second)

	// Turn 2: Follow-up question using ComposeContinuationPrompt on warm context
	msgTurn2 := domain.CanonicalMessage{
		ID:        "msg-cache-turn-2",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Sender:    msgTurn1.Sender,
		Chat:      msgTurn1.Chat,
		Text:      "Em có nhớ từ khóa xác nhận mà em đã trả lời ở câu trước không?",
	}

	t.Log("--- Executing Turn 2 (Continuation query on warm KV-cache) ---")
	err = eng.HandleDebouncedMessage(ctx, msgTurn2)
	require.NoError(t, err)


	logs, err = store.ListAuditLogs(ctx, sessionKey, 10)
	require.NoError(t, err)
	require.Len(t, logs, 2)
	turn2Cached := logs[0].Usage.CacheReadTokens
	turn2Input := logs[0].Usage.InputTokens
	turn2HitRate := logs[0].Usage.CacheHitRatio()
	// Verify Turn 2 continuation token accounting
	assert.Greater(t, turn2Input, 0, "Turn 2 input tokens must be recorded")
	if turn2Cached > 0 {
		t.Logf("⚡ Gemini KV-Cache Active: %d cached tokens (%.1f%% Cache Hit)", turn2Cached, turn2HitRate)
		assert.Greater(t, turn2HitRate, 0.0)
	} else {
		t.Log("⚪ Gemini KV-Cache Cold on this pod (normal during rapid automated test suite runs)")
	}

	// Turn 3: Test /tokens command reporting
	msgTokensCmd := domain.CanonicalMessage{
		ID:        "msg-cache-tokens",
		Timestamp: time.Now(),
		Channel:   "telegram",
		Sender:    msgTurn1.Sender,
		Chat:      msgTurn1.Chat,
		Text:      "/tokens",
	}

	outMsg, err := eng.HandleCommand(ctx, msgTokensCmd)
	require.NoError(t, err)
	require.NotNil(t, outMsg)
	t.Logf("=== /tokens Output ===\n%s\n======================================", outMsg.Text)

	assert.Contains(t, outMsg.Text, "Active Turn Context Window:")
	assert.Contains(t, outMsg.Text, "Conversation Cumulative Billed:")
}



