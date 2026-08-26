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
	outMsg := domain.OutboundMessage{
		ChatID:    "123456",
		Text:      "Hello from agyent core!",
		ParseMode: "MarkdownV2",
	}
	err = adapter.Send(context.Background(), outMsg)
	require.NoError(t, err)

	mockServer.mu.Lock()
	require.Equal(t, 1, len(mockServer.SentMessages))
	assert.Equal(t, int64(123456), mockServer.SentMessages[0].ChatID)
	assert.Equal(t, "Hello from agyent core!", mockServer.SentMessages[0].Text)
	mockServer.mu.Unlock()

	// 2. SendTyping
	err = adapter.SendTyping(context.Background(), "123456", 0)
	require.NoError(t, err)

	// 3. SendFile (photo)
	tmpDir := t.TempDir()
	imgFile := filepath.Join(tmpDir, "photo.jpg")
	_ = os.WriteFile(imgFile, []byte("fake image data"), 0644)

	err = adapter.SendFile(context.Background(), "123456", 0, imgFile, "Preview")
	require.NoError(t, err)

	// 4. SendFile (document)
	docFile := filepath.Join(tmpDir, "report.pdf")
	_ = os.WriteFile(docFile, []byte("fake pdf data"), 0644)

	err = adapter.SendFile(context.Background(), "123456", 0, docFile, "Specification")
	require.NoError(t, err)

	mockServer.mu.Lock()
	assert.GreaterOrEqual(t, len(mockServer.SentMedia), 2)
	mockServer.mu.Unlock()
}
