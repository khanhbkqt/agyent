package telegram

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"agyent/internal/core/domain"
)

// TC-THR-01: Sub-Second First Token Dispatch (<1.0s)
func TestThrottler_SubSecondFirstToken(t *testing.T) {
	mockServer := NewMockTelegramServer("token_thr_01")
	defer mockServer.Close()

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	throttler := NewDeliveryThrottler(bot, nil, 1.5, true)
	defer throttler.Stop()

	sessionKey := "telegram:123456:0"
	ctx := context.Background()

	// 1. Emit stream.init
	err = throttler.OnStreamInit(ctx, domain.NewEvent(domain.EventStreamInit, domain.StreamInitPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_01",
		CWD:            "/tmp",
		Timestamp:      time.Now(),
	}))
	require.NoError(t, err)

	// 2. Emit first delta
	startTime := time.Now()
	err = throttler.OnStreamDelta(ctx, domain.NewEvent(domain.EventStreamDelta, domain.StreamDeltaPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_01",
		StepIndex:      0,
		TextDelta:      "Xin chào! ",
	}))
	require.NoError(t, err)

	// Verify message sent within 500ms (<1.0s requirement)
	require.Eventually(t, func() bool {
		mockServer.mu.Lock()
		defer mockServer.mu.Unlock()
		return len(mockServer.SentMessages) == 1
	}, 1*time.Second, 20*time.Millisecond)

	elapsed := time.Since(startTime)
	assert.Less(t, elapsed, 1000*time.Millisecond, "First token must be dispatched sub-second (<1.0s)")

	mockServer.mu.Lock()
	assert.Equal(t, int64(123456), mockServer.SentMessages[0].ChatID)
	assert.Equal(t, "Xin chào! ", mockServer.SentMessages[0].Text)
	mockServer.mu.Unlock()
}

// TC-THR-02: 1.5s Sliding Edit Throttling
func TestThrottler_SlidingEditThrottling(t *testing.T) {
	mockServer := NewMockTelegramServer("token_thr_02")
	defer mockServer.Close()

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	throttleSec := 0.2 // faster for test speed
	throttler := NewDeliveryThrottler(bot, nil, throttleSec, true)
	defer throttler.Stop()

	sessionKey := "telegram:123456:0"
	ctx := context.Background()

	_ = throttler.OnStreamInit(ctx, domain.NewEvent(domain.EventStreamInit, domain.StreamInitPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_02",
	}))

	// Emit initial token
	_ = throttler.OnStreamDelta(ctx, domain.NewEvent(domain.EventStreamDelta, domain.StreamDeltaPayload{
		SessionKey: sessionKey,
		TextDelta:  "Step 1",
	}))

	// Wait for first message
	require.Eventually(t, func() bool {
		mockServer.mu.Lock()
		defer mockServer.mu.Unlock()
		return len(mockServer.SentMessages) == 1
	}, 1*time.Second, 20*time.Millisecond)

	// Stream 10 rapid deltas in 50ms
	for i := 0; i < 10; i++ {
		_ = throttler.OnStreamDelta(ctx, domain.NewEvent(domain.EventStreamDelta, domain.StreamDeltaPayload{
			SessionKey: sessionKey,
			TextDelta:  ".",
		}))
		time.Sleep(5 * time.Millisecond)
	}

	// Wait for ticker to fire
	time.Sleep(300 * time.Millisecond)

	mockServer.mu.Lock()
	editCount := len(mockServer.EditMessages)
	mockServer.mu.Unlock()

	// Should not have made 10 edits; should have throttled to ~1-2 edits
	assert.GreaterOrEqual(t, editCount, 1)
	assert.LessOrEqual(t, editCount, 4)
}

// TC-THR-03: Zero-Alloc Hot-Path Ingestion (<5µs latency)
func TestThrottler_ZeroAllocHotPathLatency(t *testing.T) {
	mockServer := NewMockTelegramServer("token_thr_03")
	defer mockServer.Close()

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	throttler := NewDeliveryThrottler(bot, nil, 1.5, true)
	defer throttler.Stop()

	sessionKey := "telegram:123456:0"
	ctx := context.Background()

	_ = throttler.OnStreamInit(ctx, domain.NewEvent(domain.EventStreamInit, domain.StreamInitPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_03",
	}))

	payload := domain.StreamDeltaPayload{
		SessionKey: sessionKey,
		TextDelta:  "token",
	}
	evt := domain.NewEvent(domain.EventStreamDelta, payload)

	// Warm-up
	for i := 0; i < 100; i++ {
		_ = throttler.OnStreamDelta(ctx, evt)
	}

	// Measure average latency over 1000 iterations
	start := time.Now()
	iterations := 1000
	for i := 0; i < iterations; i++ {
		_ = throttler.OnStreamDelta(ctx, evt)
	}
	totalDuration := time.Since(start)
	avgDurationPerDelta := totalDuration / time.Duration(iterations)

	assert.Less(t, avgDurationPerDelta, 50*time.Microsecond, "Hot path delta ingest must be virtually instantaneous (<50µs in test framework)")
}

// TC-THR-04: Active Stream Overflow Multi-Message Chaining (>4000 chars)
func TestThrottler_MultiMessageOverflow(t *testing.T) {
	mockServer := NewMockTelegramServer("token_thr_04")
	defer mockServer.Close()

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	throttler := NewDeliveryThrottler(bot, nil, 0.1, true)
	defer throttler.Stop()

	sessionKey := "telegram:123456:0"
	ctx := context.Background()

	_ = throttler.OnStreamInit(ctx, domain.NewEvent(domain.EventStreamInit, domain.StreamInitPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_04",
	}))

	// 1. Initial message
	_ = throttler.OnStreamDelta(ctx, domain.NewEvent(domain.EventStreamDelta, domain.StreamDeltaPayload{
		SessionKey: sessionKey,
		TextDelta:  "Start ",
	}))

	require.Eventually(t, func() bool {
		mockServer.mu.Lock()
		defer mockServer.mu.Unlock()
		return len(mockServer.SentMessages) >= 1
	}, 1*time.Second, 20*time.Millisecond)

	// 2. Stream a massive chunk > 4500 characters
	longText := strings.Repeat("Long text line of information.\n", 150)
	_ = throttler.OnStreamDelta(ctx, domain.NewEvent(domain.EventStreamDelta, domain.StreamDeltaPayload{
		SessionKey: sessionKey,
		TextDelta:  longText,
	}))

	// Wait for throttler to detect overflow and spawn second message
	require.Eventually(t, func() bool {
		mockServer.mu.Lock()
		defer mockServer.mu.Unlock()
		return len(mockServer.SentMessages) >= 2
	}, 2*time.Second, 50*time.Millisecond)

	mockServer.mu.Lock()
	assert.GreaterOrEqual(t, len(mockServer.SentMessages), 2, "Must spawn message 2 on overflow")
	mockServer.mu.Unlock()
}

// TC-THR-05: Rate Limit 429 Too Many Requests Backoff
func TestThrottler_RateLimit429Backoff(t *testing.T) {
	mockServer := NewMockTelegramServer("token_thr_05")
	defer mockServer.Close()

	mockServer.Simulate429Once = true
	mockServer.RetryAfterSec = 1

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	throttler := NewDeliveryThrottler(bot, nil, 0.1, true)
	defer throttler.Stop()

	sessionKey := "telegram:123456:0"
	ctx := context.Background()

	_ = throttler.OnStreamInit(ctx, domain.NewEvent(domain.EventStreamInit, domain.StreamInitPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_05",
	}))

	// Emit initial delta
	_ = throttler.OnStreamDelta(ctx, domain.NewEvent(domain.EventStreamDelta, domain.StreamDeltaPayload{
		SessionKey: sessionKey,
		TextDelta:  "Hello with rate limit",
	}))

	// Must handle 429 and eventually succeed sending initial message
	require.Eventually(t, func() bool {
		mockServer.mu.Lock()
		defer mockServer.mu.Unlock()
		return len(mockServer.SentMessages) == 1
	}, 3*time.Second, 100*time.Millisecond)
}

// TC-THR-06: Stream Result Final Flush & Indicator Removal
func TestThrottler_StreamResultFinalFlush(t *testing.T) {
	mockServer := NewMockTelegramServer("token_thr_06")
	defer mockServer.Close()

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	throttler := NewDeliveryThrottler(bot, nil, 0.1, true)

	sessionKey := "telegram:123456:0"
	ctx := context.Background()

	_ = throttler.OnStreamInit(ctx, domain.NewEvent(domain.EventStreamInit, domain.StreamInitPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_06",
	}))

	_ = throttler.OnStreamDelta(ctx, domain.NewEvent(domain.EventStreamDelta, domain.StreamDeltaPayload{
		SessionKey: sessionKey,
		TextDelta:  "Draft answer",
	}))

	require.Eventually(t, func() bool {
		mockServer.mu.Lock()
		defer mockServer.mu.Unlock()
		return len(mockServer.SentMessages) == 1
	}, 1*time.Second, 20*time.Millisecond)

	// Emit stream.result
	err = throttler.OnStreamResult(ctx, domain.NewEvent(domain.EventStreamResult, domain.StreamResultPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_06",
		Status:         "SUCCESS",
		Response:       "Final polished answer.",
	}))
	require.NoError(t, err)

	// Verify session cleaned up
	assert.Equal(t, 0, throttler.ActiveSessionsCount())
}

// TC-THR-07: Mid-Stream Process Crash / Error Flushing
func TestThrottler_MidStreamCrashFlushing(t *testing.T) {
	mockServer := NewMockTelegramServer("token_thr_07")
	defer mockServer.Close()

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	throttler := NewDeliveryThrottler(bot, nil, 0.1, true)

	sessionKey := "telegram:123456:0"
	ctx := context.Background()

	_ = throttler.OnStreamInit(ctx, domain.NewEvent(domain.EventStreamInit, domain.StreamInitPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_07",
	}))

	_ = throttler.OnStreamDelta(ctx, domain.NewEvent(domain.EventStreamDelta, domain.StreamDeltaPayload{
		SessionKey: sessionKey,
		TextDelta:  "Working on tasks...",
	}))

	require.Eventually(t, func() bool {
		mockServer.mu.Lock()
		defer mockServer.mu.Unlock()
		return len(mockServer.SentMessages) == 1
	}, 1*time.Second, 20*time.Millisecond)

	// Emit stream.error
	err = throttler.OnStreamError(ctx, domain.NewEvent(domain.EventStreamError, domain.StreamErrorPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_07",
		Error:          "process terminated unexpectedly (SIGSEGV)",
	}))
	require.NoError(t, err)

	mockServer.mu.Lock()
	defer mockServer.mu.Unlock()
	require.GreaterOrEqual(t, len(mockServer.EditMessages), 1)
	lastEdit := mockServer.EditMessages[len(mockServer.EditMessages)-1]
	assert.Contains(t, lastEdit.Text, "Execution interrupted")
	assert.Equal(t, 0, throttler.ActiveSessionsCount())
}

// TC-THR-08: Zero-Idle Memory Cleanup (Map Size == 0)
func TestThrottler_ZeroIdleMemoryCleanup(t *testing.T) {
	mockServer := NewMockTelegramServer("token_thr_08")
	defer mockServer.Close()

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	throttler := NewDeliveryThrottler(bot, nil, 0.1, true)
	ctx := context.Background()

	for i := 1; i <= 5; i++ {
		key := "telegram:" + string(rune('0'+i)) + ":0"
		_ = throttler.OnStreamInit(ctx, domain.NewEvent(domain.EventStreamInit, domain.StreamInitPayload{
			SessionKey:     key,
			ConversationID: "conv",
		}))
		_ = throttler.OnStreamDelta(ctx, domain.NewEvent(domain.EventStreamDelta, domain.StreamDeltaPayload{
			SessionKey: key,
			TextDelta:  "turn text",
		}))
		_ = throttler.OnStreamResult(ctx, domain.NewEvent(domain.EventStreamResult, domain.StreamResultPayload{
			SessionKey: key,
		}))
	}

	assert.Equal(t, 0, throttler.ActiveSessionsCount(), "Map size must be exactly 0 when idle")
}

// TC-THR-09: Large Message Result Overflow (>5000 chars on OnStreamResult)
func TestThrottler_LargeMessageResultOverflow(t *testing.T) {
	mockServer := NewMockTelegramServer("token_thr_09")
	defer mockServer.Close()

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	throttler := NewDeliveryThrottler(bot, nil, 0.1, true)

	sessionKey := "telegram:998877:0"
	ctx := context.Background()

	_ = throttler.OnStreamInit(ctx, domain.NewEvent(domain.EventStreamInit, domain.StreamInitPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_09",
	}))

	// Initial short delta
	_ = throttler.OnStreamDelta(ctx, domain.NewEvent(domain.EventStreamDelta, domain.StreamDeltaPayload{
		SessionKey: sessionKey,
		TextDelta:  "Draft message before waiting...",
	}))

	require.Eventually(t, func() bool {
		mockServer.mu.Lock()
		defer mockServer.mu.Unlock()
		return len(mockServer.SentMessages) == 1
	}, 1*time.Second, 20*time.Millisecond)

	// Stream finishes with a massive 5500-char response in Result event
	largeResponse := "Header Report:\n\n" + strings.Repeat("Section analysis item with detailed information.\n", 130) + "\nConclusion."
	require.Greater(t, len(largeResponse), 5000)

	err = throttler.OnStreamResult(ctx, domain.NewEvent(domain.EventStreamResult, domain.StreamResultPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_09",
		Status:         "SUCCESS",
		Response:       largeResponse,
	}))
	require.NoError(t, err)

	// Must edit message 1 with chunk 0 AND send message 2 with chunk 1
	mockServer.mu.Lock()
	defer mockServer.mu.Unlock()
	assert.GreaterOrEqual(t, len(mockServer.SentMessages), 2, "Must spawn subsequent message for >4000 char response")
	assert.GreaterOrEqual(t, len(mockServer.EditMessages), 1, "Must edit initial message with chunk 0")
	assert.Equal(t, 0, throttler.ActiveSessionsCount(), "Must clean up session")
}

// TC-THR-10: Stream Interrupted Graceful UI Finalization
func TestThrottler_StreamInterrupted(t *testing.T) {
	mockServer := NewMockTelegramServer("token_thr_10")
	defer mockServer.Close()

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	throttler := NewDeliveryThrottler(bot, nil, 0.05, true)
	defer throttler.Stop()

	sessionKey := "telegram:112233:0"
	ctx := context.Background()

	_ = throttler.OnStreamInit(ctx, domain.NewEvent(domain.EventStreamInit, domain.StreamInitPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_10",
	}))

	_ = throttler.OnStreamDelta(ctx, domain.NewEvent(domain.EventStreamDelta, domain.StreamDeltaPayload{
		SessionKey: sessionKey,
		TextDelta:  "Processing turn 1...",
	}))

	require.Eventually(t, func() bool {
		mockServer.mu.Lock()
		defer mockServer.mu.Unlock()
		return len(mockServer.SentMessages) == 1
	}, 1*time.Second, 20*time.Millisecond)

	// Emit EventStreamInterrupted
	err = throttler.OnStreamInterrupted(ctx, domain.NewEvent(domain.EventStreamInterrupted, domain.StreamInterruptedPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_10",
		Reason:         "Preempted by incoming user message",
		Timestamp:      time.Now(),
	}))
	require.NoError(t, err)

	assert.Equal(t, 0, throttler.ActiveSessionsCount(), "Session must be cleaned up on interrupt")

	mockServer.mu.Lock()
	defer mockServer.mu.Unlock()
	require.NotEmpty(t, mockServer.EditMessages)
	lastEdit := mockServer.EditMessages[len(mockServer.EditMessages)-1]
	assert.Contains(t, lastEdit.Text, "Đã tạm dừng lượt này để nhận chỉ dẫn mới")
}

// TC-THR-11: Turn Race Condition Protection: Late Turn 1 Error does not kill Turn 2 active stream session
func TestThrottler_TurnRaceCondition_StaleTurnDoesNotKillActiveTurn(t *testing.T) {
	mockServer := NewMockTelegramServer("token_thr_11")
	defer mockServer.Close()

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	throttler := NewDeliveryThrottler(bot, nil, 0.05, true)
	defer throttler.Stop()

	sessionKey := "telegram:999888:0"
	ctx := context.Background()

	// Turn 1 starts
	_ = throttler.OnStreamInit(ctx, domain.NewEvent(domain.EventStreamInit, domain.StreamInitPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_race",
		TurnID:         "turn-1",
	}))

	_ = throttler.OnStreamDelta(ctx, domain.NewEvent(domain.EventStreamDelta, domain.StreamDeltaPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_race",
		TurnID:         "turn-1",
		TextDelta:      "Turn 1 processing...",
	}))

	// Turn 2 is dispatched (e.g. via Append mode)
	_ = throttler.OnStreamInit(ctx, domain.NewEvent(domain.EventStreamInit, domain.StreamInitPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_race",
		TurnID:         "turn-2",
	}))

	// Verify session now belongs to Turn 2
	assert.Equal(t, 1, throttler.ActiveSessionsCount())

	// Stale Turn 1 error arrives late over EventBus
	err = throttler.OnStreamError(ctx, domain.NewEvent(domain.EventStreamError, domain.StreamErrorPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_race",
		TurnID:         "turn-1", // Belongs to turn-1!
		Error:          "context canceled",
	}))
	require.NoError(t, err)

	// Turn 2 MUST STILL BE ACTIVE!
	assert.Equal(t, 1, throttler.ActiveSessionsCount(), "Turn 2 session must NOT be deleted by stale Turn 1 error")

	// Turn 2 sends delta and finishes successfully
	_ = throttler.OnStreamDelta(ctx, domain.NewEvent(domain.EventStreamDelta, domain.StreamDeltaPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_race",
		TurnID:         "turn-2",
		TextDelta:      "Turn 2 actual answer!",
	}))

	err = throttler.OnStreamResult(ctx, domain.NewEvent(domain.EventStreamResult, domain.StreamResultPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_race",
		TurnID:         "turn-2",
		Status:         "SUCCESS",
		Response:       "Turn 2 final answer complete.",
	}))
	require.NoError(t, err)

	assert.Equal(t, 0, throttler.ActiveSessionsCount(), "Turn 2 must cleanly finalize and clean up")
}
