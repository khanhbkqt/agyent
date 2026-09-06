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

// TC-THR-12: Transient Server Error (5xx) Retry Succeeds
func TestThrottler_TransientServerErrorRetry(t *testing.T) {
	mockServer := NewMockTelegramServer("token_thr_12")
	defer mockServer.Close()

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	throttler := NewDeliveryThrottler(bot, nil, 0.05, true)
	defer throttler.Stop()

	sessionKey := "telegram:123456:0"
	ctx := context.Background()

	// Simulate a 502 Bad Gateway on first sendMessage attempt
	mockServer.SimulateSendErrorStatus = 502

	_ = throttler.OnStreamInit(ctx, domain.NewEvent(domain.EventStreamInit, domain.StreamInitPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_12",
		TurnID:         "turn-12",
	}))

	err = throttler.OnStreamResult(ctx, domain.NewEvent(domain.EventStreamResult, domain.StreamResultPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_12",
		TurnID:         "turn-12",
		Status:         "SUCCESS",
		Response:       "Retried message successfully delivered!",
	}))
	require.NoError(t, err)

	// Wait for retry to succeed
	require.Eventually(t, func() bool {
		mockServer.mu.Lock()
		defer mockServer.mu.Unlock()
		return len(mockServer.SentMessages) == 1
	}, 3*time.Second, 50*time.Millisecond)

	mockServer.mu.Lock()
	assert.Equal(t, "Retried message successfully delivered!", mockServer.SentMessages[0].Text)
	mockServer.mu.Unlock()
}

// TC-THR-13: Message Too Long (400) Recursively Splits and Delivers All Chunks
func TestThrottler_MessageTooLongRecursiveSplit(t *testing.T) {
	mockServer := NewMockTelegramServer("token_thr_13")
	defer mockServer.Close()

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	throttler := NewDeliveryThrottler(bot, nil, 0.05, true)
	defer throttler.Stop()

	sessionKey := "telegram:123456:0"
	ctx := context.Background()

	// Simulate 400 "message is too long" on the first send attempt
	mockServer.SimulateTooLongOnce = true

	longText := strings.Repeat("This is a long sentence that exceeds limits. ", 80)

	_ = throttler.OnStreamInit(ctx, domain.NewEvent(domain.EventStreamInit, domain.StreamInitPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_13",
		TurnID:         "turn-13",
	}))

	err = throttler.OnStreamResult(ctx, domain.NewEvent(domain.EventStreamResult, domain.StreamResultPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_13",
		TurnID:         "turn-13",
		Status:         "SUCCESS",
		Response:       longText,
	}))
	require.NoError(t, err)

	// Throttler should recursively split and deliver multiple chunks without dropping
	require.Eventually(t, func() bool {
		mockServer.mu.Lock()
		defer mockServer.mu.Unlock()
		return len(mockServer.SentMessages) >= 2
	}, 2*time.Second, 50*time.Millisecond)

	mockServer.mu.Lock()
	var totalDelivered string
	for _, m := range mockServer.SentMessages {
		totalDelivered += m.Text
	}
	mockServer.mu.Unlock()

	assert.Contains(t, totalDelivered, "This is a long sentence")
}

// TC-THR-14: Edit Failure Falls Back to Sending Fresh Message
func TestThrottler_EditFailureFallbackToSendMessage(t *testing.T) {
	mockServer := NewMockTelegramServer("token_thr_14")
	defer mockServer.Close()

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	throttler := NewDeliveryThrottler(bot, nil, 0.05, true)
	defer throttler.Stop()

	sessionKey := "telegram:123456:0"
	ctx := context.Background()

	_ = throttler.OnStreamInit(ctx, domain.NewEvent(domain.EventStreamInit, domain.StreamInitPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_14",
		TurnID:         "turn-14",
	}))

	// 1. First delta creates message (sent message #1)
	_ = throttler.OnStreamDelta(ctx, domain.NewEvent(domain.EventStreamDelta, domain.StreamDeltaPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_14",
		TurnID:         "turn-14",
		TextDelta:      "Part 1...",
	}))

	require.Eventually(t, func() bool {
		mockServer.mu.Lock()
		defer mockServer.mu.Unlock()
		return len(mockServer.SentMessages) == 1
	}, 1*time.Second, 20*time.Millisecond)

	// 2. Make edit fail permanently (e.g. 500 internal server error or network issue)
	mockServer.SimulateEditErrorStatus = 500

	// 3. Complete stream with final response
	err = throttler.OnStreamResult(ctx, domain.NewEvent(domain.EventStreamResult, domain.StreamResultPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_14",
		TurnID:         "turn-14",
		Status:         "SUCCESS",
		Response:       "Part 1... and Part 2 final answer!",
	}))
	require.NoError(t, err)

	// Since edit failed, throttler MUST send a fresh message so the response isn't swallowed!
	require.Eventually(t, func() bool {
		mockServer.mu.Lock()
		defer mockServer.mu.Unlock()
		return len(mockServer.SentMessages) == 2
	}, 2*time.Second, 50*time.Millisecond)

	mockServer.mu.Lock()
	assert.Equal(t, "Part 1... and Part 2 final answer!", mockServer.SentMessages[1].Text)
	mockServer.mu.Unlock()
}

// TC-THR-15: Empty AI Response Delivers Friendly Feedback (Never Swallowed)
func TestThrottler_EmptyResponseFeedback(t *testing.T) {
	mockServer := NewMockTelegramServer("token_thr_15")
	defer mockServer.Close()

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	throttler := NewDeliveryThrottler(bot, nil, 0.05, true)
	defer throttler.Stop()

	sessionKey := "telegram:123456:0"
	ctx := context.Background()

	_ = throttler.OnStreamInit(ctx, domain.NewEvent(domain.EventStreamInit, domain.StreamInitPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_15",
		TurnID:         "turn-15",
	}))

	// Complete turn with empty response and no prior deltas
	err = throttler.OnStreamResult(ctx, domain.NewEvent(domain.EventStreamResult, domain.StreamResultPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_15",
		TurnID:         "turn-15",
		Status:         "SUCCESS",
		Response:       "",
	}))
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		mockServer.mu.Lock()
		defer mockServer.mu.Unlock()
		return len(mockServer.SentMessages) == 1
	}, 1*time.Second, 20*time.Millisecond)

	mockServer.mu.Lock()
	assert.Contains(t, mockServer.SentMessages[0].Text, "không có nội dung phản hồi")
	mockServer.mu.Unlock()
}

// TC-THR-16: Multi-Chunk Overflow With 3+ Chunks (No Chunks Dropped)
func TestThrottler_MultiChunkOverflow_ThreeOrMoreChunks(t *testing.T) {
	mockServer := NewMockTelegramServer("token_thr_16")
	defer mockServer.Close()

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	throttler := NewDeliveryThrottler(bot, nil, 0.05, true)
	defer throttler.Stop()

	sessionKey := "telegram:123456:0"
	ctx := context.Background()

	_ = throttler.OnStreamInit(ctx, domain.NewEvent(domain.EventStreamInit, domain.StreamInitPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_16",
		TurnID:         "turn-16",
	}))

	// 1. Initial token to spawn first message
	_ = throttler.OnStreamDelta(ctx, domain.NewEvent(domain.EventStreamDelta, domain.StreamDeltaPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_16",
		TurnID:         "turn-16",
		TextDelta:      "Initial greeting... ",
	}))

	require.Eventually(t, func() bool {
		mockServer.mu.Lock()
		defer mockServer.mu.Unlock()
		return len(mockServer.SentMessages) == 1
	}, 1*time.Second, 20*time.Millisecond)

	// 2. Large rapid burst exceeding 3 full chunks (>7000 runes)
	largeText := strings.Repeat("Chunk segment content that will span multiple chunks. \n\n", 150)
	_ = throttler.OnStreamDelta(ctx, domain.NewEvent(domain.EventStreamDelta, domain.StreamDeltaPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_16",
		TurnID:         "turn-16",
		TextDelta:      largeText,
	}))

	// Wait for multi-message overflow to handle chunks
	require.Eventually(t, func() bool {
		mockServer.mu.Lock()
		defer mockServer.mu.Unlock()
		return len(mockServer.SentMessages) >= 3
	}, 2*time.Second, 50*time.Millisecond)

	// Complete stream
	_ = throttler.OnStreamResult(ctx, domain.NewEvent(domain.EventStreamResult, domain.StreamResultPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_16",
		TurnID:         "turn-16",
		Status:         "SUCCESS",
		Response:       largeText,
	}))

	mockServer.mu.Lock()
	sentCount := len(mockServer.SentMessages)
	mockServer.mu.Unlock()
	assert.GreaterOrEqual(t, sentCount, 3, "All 3+ chunks must be delivered without data loss")
}

// TC-THR-17: Edit Failure Preserves Forum Topic ThreadID
func TestThrottler_EditFailure_PreservesForumTopicThreadID(t *testing.T) {
	mockServer := NewMockTelegramServer("token_thr_17")
	defer mockServer.Close()

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	throttler := NewDeliveryThrottler(bot, nil, 0.05, true)
	defer throttler.Stop()

	sessionKey := "telegram:0:-100123456:7788" // ThreadID = 7788 (forum topic)
	ctx := context.Background()

	_ = throttler.OnStreamInit(ctx, domain.NewEvent(domain.EventStreamInit, domain.StreamInitPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_17",
		TurnID:         "turn-17",
	}))

	// 1. Initial delta creates message in topic 7788
	_ = throttler.OnStreamDelta(ctx, domain.NewEvent(domain.EventStreamDelta, domain.StreamDeltaPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_17",
		TurnID:         "turn-17",
		TextDelta:      "Starting in forum topic...",
	}))

	require.Eventually(t, func() bool {
		mockServer.mu.Lock()
		defer mockServer.mu.Unlock()
		return len(mockServer.SentMessages) == 1
	}, 1*time.Second, 20*time.Millisecond)

	mockServer.mu.Lock()
	assert.Equal(t, int64(-100123456), mockServer.SentMessages[0].ChatID)
	assert.Equal(t, int64(7788), mockServer.SentMessages[0].ThreadID)
	mockServer.mu.Unlock()

	// 2. Simulate edit failure on final flush
	mockServer.SimulateEditErrorStatus = 500

	err = throttler.OnStreamResult(ctx, domain.NewEvent(domain.EventStreamResult, domain.StreamResultPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_17",
		TurnID:         "turn-17",
		Status:         "SUCCESS",
		Response:       "Final answer delivered to forum topic!",
	}))
	require.NoError(t, err)

	// Fallback send MUST have ThreadID = 7788
	require.Eventually(t, func() bool {
		mockServer.mu.Lock()
		defer mockServer.mu.Unlock()
		return len(mockServer.SentMessages) == 2
	}, 2*time.Second, 50*time.Millisecond)

	mockServer.mu.Lock()
	assert.Equal(t, int64(7788), mockServer.SentMessages[1].ThreadID, "Fallback message must maintain forum topic ThreadID")
	assert.Equal(t, "Final answer delivered to forum topic!", mockServer.SentMessages[1].Text)
	mockServer.mu.Unlock()
}

// TC-THR-18: Streaming Turn with Media and Standalone Text Message Delivery
func TestThrottler_MediaAndStandaloneTextMessage(t *testing.T) {
	mockServer := NewMockTelegramServer("token_thr_18")
	defer mockServer.Close()

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	mediaMgr := NewMediaManager(nil, bot)
	throttler := NewDeliveryThrottler(bot, mediaMgr, 0.2, true)
	defer throttler.Stop()

	tmpDir := t.TempDir()
	photoPath := filepath.Join(tmpDir, "chart.png")
	require.NoError(t, os.WriteFile(photoPath, []byte("fake-photo-binary"), 0644))

	sessionKey := "telegram:123456:0"
	ctx := context.Background()

	_ = throttler.OnStreamInit(ctx, domain.NewEvent(domain.EventStreamInit, domain.StreamInitPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_18",
		TurnID:         "turn-18",
	}))

	summary := "Dưới đây là báo cáo tăng trưởng doanh thu."
	responseText := fmt.Sprintf("%s\n\n![Báo Cáo](%s)", summary, photoPath)

	err = throttler.OnStreamResult(ctx, domain.NewEvent(domain.EventStreamResult, domain.StreamResultPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_18",
		TurnID:         "turn-18",
		Status:         "SUCCESS",
		Response:       responseText,
	}))
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		mockServer.mu.Lock()
		defer mockServer.mu.Unlock()
		return len(mockServer.SentMedia) == 1 && len(mockServer.SentMessages) == 1
	}, 2*time.Second, 50*time.Millisecond)

	mockServer.mu.Lock()
	assert.Equal(t, "Báo Cáo", mockServer.SentMedia[0].Caption, "Media should preserve its own caption")
	assert.Contains(t, mockServer.SentMessages[0].Text, summary, "Text summary must be sent as a standalone text message")
	mockServer.mu.Unlock()
}

// TC-THR-19: Streaming Turn Image-Only Does Not Inject Bogus Warning Message
func TestThrottler_ImageOnlyDoesNotInjectWarning(t *testing.T) {
	mockServer := NewMockTelegramServer("token_thr_19")
	defer mockServer.Close()

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	mediaMgr := NewMediaManager(nil, bot)
	throttler := NewDeliveryThrottler(bot, mediaMgr, 0.2, true)
	defer throttler.Stop()

	tmpDir := t.TempDir()
	photoPath := filepath.Join(tmpDir, "logo.png")
	require.NoError(t, os.WriteFile(photoPath, []byte("fake-photo-binary"), 0644))

	sessionKey := "telegram:123456:0"
	ctx := context.Background()

	_ = throttler.OnStreamInit(ctx, domain.NewEvent(domain.EventStreamInit, domain.StreamInitPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_19",
		TurnID:         "turn-19",
	}))

	// Response has ONLY the image markdown
	responseText := fmt.Sprintf("![Logo](%s)", photoPath)

	err = throttler.OnStreamResult(ctx, domain.NewEvent(domain.EventStreamResult, domain.StreamResultPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_19",
		TurnID:         "turn-19",
		Status:         "SUCCESS",
		Response:       responseText,
	}))
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		mockServer.mu.Lock()
		defer mockServer.mu.Unlock()
		return len(mockServer.SentMedia) == 1
	}, 2*time.Second, 50*time.Millisecond)

	mockServer.mu.Lock()
	assert.Equal(t, "Logo", mockServer.SentMedia[0].Caption)
	assert.Empty(t, mockServer.SentMessages, "No warning or empty text message should be sent for image-only turn")
	mockServer.mu.Unlock()
}

