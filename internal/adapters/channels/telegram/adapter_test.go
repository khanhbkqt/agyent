package telegram

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// TC-LFC-04: Multi-Bot Outbound Routing via AgentName (Scheduled Tasks / Crons)
func TestAdapter_MultiBot_RoutingByAgentName(t *testing.T) {
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
			Name:      "main_bot",
			BotToken:  "token_bot_1",
			BindAgent: "agyent",
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

	// 1. Send scheduled task output for wife_assistant (BotID is 0, pure AgentName routing)
	msgWife := domain.OutboundMessage{
		AgentName: "wife_assistant",
		ChatID:    "8544450322",
		Text:      "⏰ Scheduled report for wife!",
	}
	err = adapter.Send(context.Background(), msgWife)
	require.NoError(t, err)

	// 2. Send scheduled task output for agyent (BotID is 0, pure AgentName routing)
	msgMain := domain.OutboundMessage{
		AgentName: "agyent",
		ChatID:    "8544450322",
		Text:      "⏰ Scheduled report for agyent!",
	}
	err = adapter.Send(context.Background(), msgMain)
	require.NoError(t, err)

	// Verify that mockServer2 (wife_bot) received the wife report
	mockServer2.mu.Lock()
	require.Equal(t, 1, len(mockServer2.SentMessages))
	assert.Contains(t, mockServer2.SentMessages[0].Text, "report for wife")
	assert.Equal(t, int64(8544450322), mockServer2.SentMessages[0].ChatID)
	mockServer2.mu.Unlock()

	// Verify that mockServer1 (main_bot) received the main report
	mockServer1.mu.Lock()
	require.Equal(t, 1, len(mockServer1.SentMessages))
	assert.Contains(t, mockServer1.SentMessages[0].Text, "report for agyent")
	assert.Equal(t, int64(8544450322), mockServer1.SentMessages[0].ChatID)
	mockServer1.mu.Unlock()
}

// TC-LFC-05: Multi-Bot Fallback to Primary Bot when Agent Bot Fails (e.g. 403 Forbidden)
func TestAdapter_MultiBot_FallbackToPrimaryBotWhenAgentBotFails(t *testing.T) {
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
			Name:      "main_bot",
			BotToken:  "token_bot_1",
			BindAgent: "agyent",
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

	// Simulate wife_bot failing with 403 Forbidden on sendMessage (e.g. user hasn't /started wife_bot yet)
	mockServer2.SimulateSendErrorStatus = 403
	mockServer2.SimulateSendErrorBody = `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`

	// Send message for wife_assistant
	msgWife := domain.OutboundMessage{
		AgentName: "wife_assistant",
		ChatID:    "8544450322",
		Text:      "⏰ Urgent notification for wife!",
	}
	err = adapter.Send(context.Background(), msgWife)
	require.NoError(t, err, "Send must succeed via fallback to primary bot")

	// Verify that mockServer1 (main_bot) received the message due to fallback
	mockServer1.mu.Lock()
	require.Equal(t, 1, len(mockServer1.SentMessages))
	assert.Contains(t, mockServer1.SentMessages[0].Text, "Urgent notification for wife")
	assert.Equal(t, int64(8544450322), mockServer1.SentMessages[0].ChatID)
	mockServer1.mu.Unlock()
}

// TC-ACT-18: Smart Split Delivery for Outbound Messages with Media
func TestAdapter_Send_SmartSplitDelivery(t *testing.T) {
	mockServer := NewMockTelegramServer("token_smart_split")
	defer mockServer.Close()

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	cfg := config.DefaultConfig()
	cfg.Telegram.BotToken = "token_smart_split"
	cfg.Telegram.AdminUserIDs = []int64{123456}

	bus := eventbus.NewEventBus(10, 1)
	defer bus.Close()

	adapter := NewAdapter(cfg, bus, WithBot(bot))
	inbound := make(chan domain.CanonicalMessage, 10)
	_ = adapter.Start(context.Background(), inbound)
	defer adapter.Stop()

	tmpDir := t.TempDir()
	photoPath := filepath.Join(tmpDir, "be_na.jpg")
	require.NoError(t, os.WriteFile(photoPath, []byte("fake-photo-binary"), 0644))

	t.Run("Text under 1024 chars attaches to photo caption without separate text message", func(t *testing.T) {
		mockServer.mu.Lock()
		mockServer.SentMessages = nil
		mockServer.SentMedia = nil
		mockServer.mu.Unlock()

		greeting := "Em chào anh Khánh buổi sáng! Chúc anh một ngày tốt lành!"
		text := fmt.Sprintf("%s\n\n![Bé Na thức dậy](%s)", greeting, photoPath)

		err := adapter.Send(context.Background(), domain.OutboundMessage{
			ChatID: "123456",
			Text:   text,
		})
		require.NoError(t, err)

		mockServer.mu.Lock()
		defer mockServer.mu.Unlock()

		require.Len(t, mockServer.SentMedia, 1, "Photo must be sent")
		assert.Equal(t, "photo", mockServer.SentMedia[0].Type)
		assert.Contains(t, mockServer.SentMedia[0].Caption, greeting, "Caption must contain greeting")
		assert.Equal(t, "HTML", mockServer.SentMedia[0].ParseMode, "Caption should use HTML parse mode")
		assert.Empty(t, mockServer.SentMessages, "No separate text message should be sent when caption <= 1024 chars")
	})

	t.Run("Text over 1024 chars sends photo first then sends full text message", func(t *testing.T) {
		mockServer.mu.Lock()
		mockServer.SentMessages = nil
		mockServer.SentMedia = nil
		mockServer.mu.Unlock()

		longGreeting := strings.Repeat("Chào buổi sáng anh Khánh! ", 50) // ~1300 chars
		require.Greater(t, len(longGreeting), 1024)
		text := fmt.Sprintf("%s\n\n![Bé Na thức dậy](%s)", longGreeting, photoPath)

		err := adapter.Send(context.Background(), domain.OutboundMessage{
			ChatID: "123456",
			Text:   text,
		})
		require.NoError(t, err)

		mockServer.mu.Lock()
		defer mockServer.mu.Unlock()

		require.Len(t, mockServer.SentMedia, 1, "Photo must be sent")
		assert.Equal(t, "photo", mockServer.SentMedia[0].Type)
		assert.Equal(t, "Bé Na thức dậy", mockServer.SentMedia[0].Caption, "Photo keeps alt-text when text > 1024")
		require.Len(t, mockServer.SentMessages, 1, "Full text message must be sent separately")
		assert.Contains(t, mockServer.SentMessages[0].Text, "Chào buổi sáng anh Khánh!")
	})
}
