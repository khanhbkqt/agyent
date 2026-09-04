package telegram

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"agyent/internal/config"
	"agyent/internal/core/domain"
	"agyent/internal/core/eventbus"
)

// TC-LFC-01: Graceful Adapter Start & Stop (Polling Mode)
func TestAdapter_LifecyclePollingMode(t *testing.T) {
	mockServer := NewMockTelegramServer("token_lfc_01")
	defer mockServer.Close()

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	cfg := config.DefaultConfig()
	cfg.Telegram.BotToken = "token_lfc_01"
	cfg.Telegram.Mode = "polling"
	cfg.Telegram.AdminUserIDs = []int64{12345}

	bus := eventbus.NewEventBus(100, 2)
	defer bus.Close()

	adapter := NewAdapter(cfg, bus, WithBot(bot))
	assert.Equal(t, "telegram", adapter.Name())

	inbound := make(chan domain.CanonicalMessage, 10)
	ctx := context.Background()

	err = adapter.Start(ctx, inbound)
	require.NoError(t, err)

	// Verify double start returns error
	err = adapter.Start(ctx, inbound)
	require.Error(t, err)

	// Stop cleanly
	stopStart := time.Now()
	err = adapter.Stop()
	require.NoError(t, err)
	elapsed := time.Since(stopStart)

	assert.Less(t, elapsed, 1000*time.Millisecond, "Adapter must stop gracefully within 1.0s")
}

// TC-LFC-02: Graceful Adapter Start & Stop (Webhook Mode)
func TestAdapter_LifecycleWebhookMode(t *testing.T) {
	mockServer := NewMockTelegramServer("token_lfc_02")
	defer mockServer.Close()

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	cfg := config.DefaultConfig()
	cfg.Telegram.BotToken = "token_lfc_02"
	cfg.Telegram.Mode = "webhook"
	cfg.Telegram.WebhookURL = "https://example.com/telegram/webhook"
	cfg.Telegram.AdminUserIDs = []int64{12345}
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.Port = 18089 // Free test port

	bus := eventbus.NewEventBus(100, 2)
	defer bus.Close()

	adapter := NewAdapter(cfg, bus, WithBot(bot))

	inbound := make(chan domain.CanonicalMessage, 10)
	ctx := context.Background()

	err = adapter.Start(ctx, inbound)
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)

	err = adapter.Stop()
	require.NoError(t, err)
}

// Test Adapter Send and SendFile methods
func TestAdapter_SendAndSendFile(t *testing.T) {
	mockServer := NewMockTelegramServer("token_adapter_send")
	defer mockServer.Close()

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	cfg := config.DefaultConfig()
	cfg.Telegram.BotToken = "token_adapter_send"
	cfg.Telegram.AdminUserIDs = []int64{123456}

	bus := eventbus.NewEventBus(100, 2)
	defer bus.Close()

	adapter := NewAdapter(cfg, bus, WithBot(bot))
	inbound := make(chan domain.CanonicalMessage, 10)
	_ = adapter.Start(context.Background(), inbound)
	defer adapter.Stop()

	// 1. Send OutboundMessage
	// 1. Send OutboundMessage with MarkdownV2
	outMsg := domain.OutboundMessage{
		ChatID:    "123456",
		Text:      "Hello from agyent core!",
		ParseMode: "MarkdownV2",
	}
	err = adapter.Send(context.Background(), outMsg)
	require.NoError(t, err)

	// 1b. Send OutboundMessage with channel-agnostic Markdown (should be converted to HTML)
	outMarkdownMsg := domain.OutboundMessage{
		ChatID:    "123456",
		Text:      "Here is **bold** text and `inline code`.",
		ParseMode: "Markdown",
	}
	err = adapter.Send(context.Background(), outMarkdownMsg)
	require.NoError(t, err)

	// 1c. Send OutboundMessage with empty ParseMode (defaults to HTML conversion)
	outDefaultMsg := domain.OutboundMessage{
		ChatID: "123456",
		Text:   "Default **message** formatting.",
	}
	err = adapter.Send(context.Background(), outDefaultMsg)
	require.NoError(t, err)

	mockServer.mu.Lock()
	require.Equal(t, 3, len(mockServer.SentMessages))
	assert.Equal(t, int64(123456), mockServer.SentMessages[0].ChatID)
	assert.Equal(t, "Hello from agyent core!", mockServer.SentMessages[0].Text)
	assert.Equal(t, "MarkdownV2", mockServer.SentMessages[0].ParseMode)

	assert.Equal(t, "HTML", mockServer.SentMessages[1].ParseMode)
	assert.Contains(t, mockServer.SentMessages[1].Text, "<b>bold</b>")
	assert.Contains(t, mockServer.SentMessages[1].Text, "<code>inline code</code>")

	assert.Equal(t, "HTML", mockServer.SentMessages[2].ParseMode)
	assert.Contains(t, mockServer.SentMessages[2].Text, "<b>message</b>")
	mockServer.mu.Unlock()

	target := domain.TargetContext{
		Channel: "telegram",
		ChatID:  "123456",
	}

	// 2. SendTyping
	err = adapter.SendTyping(context.Background(), target)
	require.NoError(t, err)

	// 3. SendFile (photo)
	tmpDir := t.TempDir()
	imgFile := filepath.Join(tmpDir, "photo.jpg")
	_ = os.WriteFile(imgFile, []byte("fake image data"), 0644)

	err = adapter.SendFile(context.Background(), target, imgFile, "Preview")
	require.NoError(t, err)

	// 4. SendFile (document)
	docFile := filepath.Join(tmpDir, "report.pdf")
	_ = os.WriteFile(docFile, []byte("fake pdf data"), 0644)

	err = adapter.SendFile(context.Background(), target, docFile, "Specification")
	require.NoError(t, err)

	mockServer.mu.Lock()
	assert.GreaterOrEqual(t, len(mockServer.SentMedia), 2)
	mockServer.mu.Unlock()
}

// TC-LFC-03: Multi-Bot Lifecycle Pool Initialization & Dedicated Binding Integrity
func TestAdapter_MultiBotPoolInitialization(t *testing.T) {
	mockServer1 := NewMockTelegramServer("token_bot_1", 1001)
	defer mockServer1.Close()

	mockServer2 := NewMockTelegramServer("token_bot_2", 2002)
	defer mockServer2.Close()

	bot1, err := mockServer1.NewBot()
	require.NoError(t, err)

	bot2, err := mockServer2.NewBot()
	require.NoError(t, err)

	cfg := config.DefaultConfig()
	cfg.Telegram.Bots = []config.BotConfig{
		{
			Name:      "dev_bot",
			BotToken:  "token_bot_1",
			BindAgent: "dev_architect",
		},
		{
			Name:      "wife_bot",
			BotToken:  "token_bot_2",
			BindAgent: "wife_assistant",
		},
	}
	cfg.Telegram.AdminUserIDs = []int64{12345}

	bus := eventbus.NewEventBus(100, 2)
	defer bus.Close()

	adapter := NewAdapter(cfg, bus, WithBots(bot1, bot2))

	inbound := make(chan domain.CanonicalMessage, 10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = adapter.Start(ctx, inbound)
	require.NoError(t, err)
	defer adapter.Stop()

	// Verify both bots exist and are mapped to distinct agents
	assert.Equal(t, 2, len(adapter.bots))
	assert.Equal(t, bot1, adapter.getBot(bot1.Id))
	assert.Equal(t, bot2, adapter.getBot(bot2.Id))
	assert.Equal(t, "dev_architect", adapter.bindAgents[bot1.Id])
	assert.Equal(t, "wife_assistant", adapter.bindAgents[bot2.Id])
}
