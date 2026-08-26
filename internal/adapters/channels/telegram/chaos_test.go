package telegram

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"agyent/internal/config"
	"agyent/internal/core/domain"
	"agyent/internal/core/eventbus"
)

// TC-MKD-04: Fallback to PlainText on 400 Bad Request
func TestChaos_FallbackToPlainTextOn400(t *testing.T) {
	mockServer := NewMockTelegramServer("token_chaos_400")
	defer mockServer.Close()

	// Simulate Telegram returning 400 Bad Request on MarkdownV2
	mockServer.Simulate400Once = true

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	throttler := NewDeliveryThrottler(bot, nil, 0.05, true)
	defer throttler.Stop()

	sessionKey := "telegram:123456:0"
	ctx := context.Background()

	_ = throttler.OnStreamInit(ctx, domain.NewEvent(domain.EventStreamInit, domain.StreamInitPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_400",
	}))

	// Initial message
	_ = throttler.OnStreamDelta(ctx, domain.NewEvent(domain.EventStreamDelta, domain.StreamDeltaPayload{
		SessionKey: sessionKey,
		TextDelta:  "Initial text",
	}))

	require.Eventually(t, func() bool {
		mockServer.mu.Lock()
		defer mockServer.mu.Unlock()
		return len(mockServer.SentMessages) == 1
	}, 3*time.Second, 20*time.Millisecond)

	// Simulate Telegram returning 400 Bad Request on EditMessageText
	mockServer.mu.Lock()
	mockServer.Simulate400Once = true
	mockServer.mu.Unlock()

	// Stream text that would trigger 400
	_ = throttler.OnStreamDelta(ctx, domain.NewEvent(domain.EventStreamDelta, domain.StreamDeltaPayload{
		SessionKey: sessionKey,
		TextDelta:  " with special characters _not_closed",
	}))

	// Wait for edit
	require.Eventually(t, func() bool {
		mockServer.mu.Lock()
		defer mockServer.mu.Unlock()
		return len(mockServer.EditMessages) >= 1
	}, 3*time.Second, 20*time.Millisecond)

	mockServer.mu.Lock()
	lastEdit := mockServer.EditMessages[len(mockServer.EditMessages)-1]
	mockServer.mu.Unlock()

	// ParseMode should have fallen back to empty (PlainText)
	assert.Equal(t, "", lastEdit.ParseMode)
}

// TC-LFC-03: 50 Concurrent Streaming Sessions Stress
func TestChaos_ConcurrentSessionsStress(t *testing.T) {
	mockServer := NewMockTelegramServer("token_chaos_stress")
	defer mockServer.Close()

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	cfg := config.DefaultConfig()
	cfg.Telegram.BotToken = "token_chaos_stress"
	cfg.Telegram.AdminUserIDs = []int64{123456}

	bus := eventbus.NewEventBus(1000, 4)
	defer bus.Close()

	adapter := NewAdapter(cfg, bus, WithBot(bot))
	inbound := make(chan domain.CanonicalMessage, 100)
	err = adapter.Start(context.Background(), inbound)
	require.NoError(t, err)
	defer adapter.Stop()

	numSessions := 50
	var wg sync.WaitGroup
	wg.Add(numSessions)

	for i := 0; i < numSessions; i++ {
		go func(idx int) {
			defer wg.Done()
			sessionKey := fmt.Sprintf("telegram:%d:0", 1000+idx)
			convID := fmt.Sprintf("conv_%d", idx)

			// stream.init
			_ = bus.SyncEmit(context.Background(), domain.NewEvent(domain.EventStreamInit, domain.StreamInitPayload{
				SessionKey:     sessionKey,
				ConversationID: convID,
			}))

			// multiple deltas
			for d := 0; d < 5; d++ {
				_ = bus.SyncEmit(context.Background(), domain.NewEvent(domain.EventStreamDelta, domain.StreamDeltaPayload{
					SessionKey: sessionKey,
					TextDelta:  fmt.Sprintf(" delta_%d", d),
				}))
				time.Sleep(2 * time.Millisecond)
			}

			// stream.result
			_ = bus.SyncEmit(context.Background(), domain.NewEvent(domain.EventStreamResult, domain.StreamResultPayload{
				SessionKey: sessionKey,
				Status:     "SUCCESS",
				Response:   "Completed answer for session",
			}))
		}(i)
	}

	wg.Wait()

	// Verify all 50 sessions completed cleanly
	assert.Equal(t, 0, adapter.throttler.ActiveSessionsCount(), "All 50 sessions must be cleaned up")
}

// TC-LFC-04: Goroutine Leak Verification with goleak
func TestChaos_GoleakZeroLeaks(t *testing.T) {
	// Verify no goroutines leaked before test
	goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("internal/poll.runtime_pollWait"),
		goleak.IgnoreTopFunction("net/http.(*persistConn).readLoop"),
		goleak.IgnoreTopFunction("net/http.(*persistConn).writeLoop"),
	)

	mockServer := NewMockTelegramServer("token_chaos_goleak")

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	cfg := config.DefaultConfig()
	cfg.Telegram.BotToken = "token_chaos_goleak"
	cfg.Telegram.AdminUserIDs = []int64{123456}

	bus := eventbus.NewEventBus(100, 2)

	adapter := NewAdapter(cfg, bus, WithBot(bot))
	inbound := make(chan domain.CanonicalMessage, 10)

	err = adapter.Start(context.Background(), inbound)
	require.NoError(t, err)

	// Run a quick streaming cycle
	sessionKey := "telegram:999000:0"
	_ = bus.SyncEmit(context.Background(), domain.NewEvent(domain.EventStreamInit, domain.StreamInitPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_leak",
	}))
	_ = bus.SyncEmit(context.Background(), domain.NewEvent(domain.EventStreamDelta, domain.StreamDeltaPayload{
		SessionKey: sessionKey,
		TextDelta:  "Checking leak prevention",
	}))
	_ = bus.SyncEmit(context.Background(), domain.NewEvent(domain.EventStreamResult, domain.StreamResultPayload{
		SessionKey: sessionKey,
		Status:     "SUCCESS",
		Response:   "Result text",
	}))

	// Stop everything cleanly
	_ = adapter.Stop()
	_ = bus.Close()
	mockServer.Close()

	// Verify 0 goroutines leaked after shutdown
	goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("internal/poll.runtime_pollWait"),
		goleak.IgnoreTopFunction("net/http.(*persistConn).readLoop"),
		goleak.IgnoreTopFunction("net/http.(*persistConn).writeLoop"),
	)
}
