package debouncer_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agyent/internal/core/debouncer"
	"agyent/internal/core/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestDebouncer_TC_DEB_01_SingleMessageFiring_VirtualClock(t *testing.T) {
	defer goleak.VerifyNone(t)

	clock := debouncer.NewMockClock()
	var received domain.CanonicalMessage
	var fired atomic.Bool

	deb := debouncer.NewDebouncer(debouncer.Config{
		WindowDuration:  2 * time.Second,
		MaxWaitDuration: 10 * time.Second,
		Clock:           clock,
	}, func(ctx context.Context, msg domain.CanonicalMessage) error {
		received = msg
		fired.Store(true)
		return nil
	})
	defer deb.Close(context.Background())

	msg := domain.CanonicalMessage{
		ID:        "m1",
		Channel:   "telegram",
		Chat:      domain.ChatContext{ID: "chat1"},
		Text:      "Hello world",
		Timestamp: clock.Now(),
	}

	err := deb.Ingest(context.Background(), msg)
	require.NoError(t, err)
	assert.Equal(t, 1, deb.ActiveSessions())

	// Advance 1s - should NOT fire yet
	clock.Advance(1 * time.Second)
	assert.False(t, fired.Load())
	assert.Equal(t, 1, deb.ActiveSessions())

	// Advance 1s more (total 2s) - must fire
	clock.Advance(1 * time.Second)

	require.Eventually(t, func() bool {
		return fired.Load()
	}, 1*time.Second, 10*time.Millisecond)

	assert.Equal(t, "Hello world", received.Text)
	assert.Equal(t, 0, deb.ActiveSessions(), "session should be removed after flush (Zero-Leak)")
}

func TestDebouncer_TC_DEB_02_RapidFire_Coalescing(t *testing.T) {
	defer goleak.VerifyNone(t)

	clock := debouncer.NewMockClock()
	var received domain.CanonicalMessage
	var fired atomic.Bool

	deb := debouncer.NewDebouncer(debouncer.Config{
		WindowDuration:  2 * time.Second,
		MaxWaitDuration: 10 * time.Second,
		Clock:           clock,
	}, func(ctx context.Context, msg domain.CanonicalMessage) error {
		received = msg
		fired.Store(true)
		return nil
	})
	defer deb.Close(context.Background())

	sessionKey := "telegram:chat2"

	// Ingest msg 1 at t=0
	_ = deb.Ingest(context.Background(), domain.CanonicalMessage{
		ID:        "m1",
		Channel:   "telegram",
		Chat:      domain.ChatContext{ID: "chat2"},
		Text:      "Line 1",
		Timestamp: clock.Now(),
	})

	// Advance 0.8s, Ingest msg 2
	clock.Advance(800 * time.Millisecond)
	_ = deb.Ingest(context.Background(), domain.CanonicalMessage{
		ID:        "m2",
		Channel:   "telegram",
		Chat:      domain.ChatContext{ID: "chat2"},
		Text:      "Line 2",
		Timestamp: clock.Now(),
	})

	// Advance 0.8s, Ingest msg 3
	clock.Advance(800 * time.Millisecond)
	_ = deb.Ingest(context.Background(), domain.CanonicalMessage{
		ID:        "m3",
		Channel:   "telegram",
		Chat:      domain.ChatContext{ID: "chat2"},
		Text:      "Line 3",
		Timestamp: clock.Now(),
	})

	_ = sessionKey
	assert.False(t, fired.Load(), "should not fire yet during sliding window")

	// Advance 2s silence
	clock.Advance(2 * time.Second)

	require.Eventually(t, func() bool {
		return fired.Load()
	}, 1*time.Second, 10*time.Millisecond)

	assert.Equal(t, "Line 1\nLine 2\nLine 3", received.Text)
	assert.Equal(t, 0, deb.ActiveSessions())
}

func TestDebouncer_TC_DEB_04_MaxWaitDurationCap_StarvationGuard(t *testing.T) {
	defer goleak.VerifyNone(t)

	clock := debouncer.NewMockClock()
	var fireCount atomic.Int32
	var lastReceived domain.CanonicalMessage

	deb := debouncer.NewDebouncer(debouncer.Config{
		WindowDuration:  2 * time.Second,
		MaxWaitDuration: 5 * time.Second, // Cap at 5.0s
		MaxMessageCount: 100,
		Clock:           clock,
	}, func(ctx context.Context, msg domain.CanonicalMessage) error {
		lastReceived = msg
		fireCount.Add(1)
		return nil
	})
	defer deb.Close(context.Background())

	// Send message every 1.5s (less than 2s window)
	// t=0s, t=1.5s, t=3.0s, t=4.5s, t=6.0s
	for i := 0; i < 5; i++ {
		_ = deb.Ingest(context.Background(), domain.CanonicalMessage{
			ID:        fmt.Sprintf("m-%d", i),
			Channel:   "telegram",
			Chat:      domain.ChatContext{ID: "chat-starve"},
			Text:      fmt.Sprintf("Item %d", i),
			Timestamp: clock.Now(),
		})
		clock.Advance(1500 * time.Millisecond)
	}

	// At t=6.0s (exceeded 5.0s max wait), debouncer MUST have flushed the accumulated batch!
	require.Eventually(t, func() bool {
		return fireCount.Load() >= 1
	}, 1*time.Second, 10*time.Millisecond)

	assert.Contains(t, lastReceived.Text, "Item 0")
}

func TestDebouncer_TC_DEB_05_MaxMessageCountCap(t *testing.T) {
	defer goleak.VerifyNone(t)

	clock := debouncer.NewMockClock()
	var fireCount atomic.Int32
	var lastReceived domain.CanonicalMessage

	deb := debouncer.NewDebouncer(debouncer.Config{
		WindowDuration:  2 * time.Second,
		MaxWaitDuration: 10 * time.Second,
		MaxMessageCount: 5, // Cap at 5 messages
		Clock:           clock,
	}, func(ctx context.Context, msg domain.CanonicalMessage) error {
		lastReceived = msg
		fireCount.Add(1)
		return nil
	})
	defer deb.Close(context.Background())

	// Ingest 5 messages rapidly without clock advance
	for i := 0; i < 5; i++ {
		_ = deb.Ingest(context.Background(), domain.CanonicalMessage{
			ID:        fmt.Sprintf("m-%d", i),
			Channel:   "telegram",
			Chat:      domain.ChatContext{ID: "chat-count"},
			Text:      fmt.Sprintf("Burst %d", i),
			Timestamp: clock.Now(),
		})
	}

	// Message 5 should trigger immediate flush!
	require.Eventually(t, func() bool {
		return fireCount.Load() == 1
	}, 1*time.Second, 10*time.Millisecond)

	assert.Equal(t, "Burst 0\nBurst 1\nBurst 2\nBurst 3\nBurst 4", lastReceived.Text)
}

func TestDebouncer_TC_DEB_06_SlashCommandFastPathPreemption(t *testing.T) {
	defer goleak.VerifyNone(t)

	clock := debouncer.NewMockClock()
	var received []domain.CanonicalMessage
	var mu sync.Mutex

	deb := debouncer.NewDebouncer(debouncer.Config{
		WindowDuration:  2 * time.Second,
		MaxWaitDuration: 10 * time.Second,
		Clock:           clock,
	}, func(ctx context.Context, msg domain.CanonicalMessage) error {
		mu.Lock()
		received = append(received, msg)
		mu.Unlock()
		return nil
	})
	defer deb.Close(context.Background())

	// Send normal message (buffered)
	_ = deb.Ingest(context.Background(), domain.CanonicalMessage{
		ID:        "msg-pending",
		Channel:   "telegram",
		Chat:      domain.ChatContext{ID: "chat-cmd"},
		Text:      "Please delete old database",
		Timestamp: clock.Now(),
	})

	assert.Equal(t, 1, deb.ActiveSessions())

	// Send slash command /reset - should discard pending and fire immediately with 0 delay!
	cmdMsg := domain.CanonicalMessage{
		ID:        "msg-cmd",
		Channel:   "telegram",
		Chat:      domain.ChatContext{ID: "chat-cmd"},
		Text:      "/reset",
		Timestamp: clock.Now(),
	}

	err := deb.Ingest(context.Background(), cmdMsg)
	require.NoError(t, err)

	mu.Lock()
	assert.Len(t, received, 1, "only the slash command should be dispatched, pending was pre-empted")
	assert.Equal(t, "/reset", received[0].Text)
	mu.Unlock()

	assert.Equal(t, 0, deb.ActiveSessions(), "session map should be clean")
}

func TestDebouncer_TC_DEB_09_MultiSessionIsolation(t *testing.T) {
	defer goleak.VerifyNone(t)

	clock := debouncer.NewMockClock()
	receivedSessions := make(map[string]string)
	var mu sync.Mutex

	deb := debouncer.NewDebouncer(debouncer.Config{
		WindowDuration:  2 * time.Second,
		MaxWaitDuration: 10 * time.Second,
		Clock:           clock,
	}, func(ctx context.Context, msg domain.CanonicalMessage) error {
		mu.Lock()
		receivedSessions[msg.SessionKey()] = msg.Text
		mu.Unlock()
		return nil
	})
	defer deb.Close(context.Background())

	_ = deb.Ingest(context.Background(), domain.CanonicalMessage{
		ID:      "m1",
		Channel: "telegram",
		Chat:    domain.ChatContext{ID: "chatA"},
		Text:    "Hello from Chat A",
	})

	_ = deb.Ingest(context.Background(), domain.CanonicalMessage{
		ID:      "m2",
		Channel: "telegram",
		Chat:    domain.ChatContext{ID: "chatB"},
		Text:    "Hello from Chat B",
	})

	assert.Equal(t, 2, deb.ActiveSessions())

	// Advance 2s
	clock.Advance(2 * time.Second)

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(receivedSessions) == 2
	}, 1*time.Second, 10*time.Millisecond)

	mu.Lock()
	assert.Equal(t, "Hello from Chat A", receivedSessions["telegram:chatA"])
	assert.Equal(t, "Hello from Chat B", receivedSessions["telegram:chatB"])
	mu.Unlock()

	assert.Equal(t, 0, deb.ActiveSessions())
}

func TestDebouncer_TC_DEB_11_Close_FlushInFlight(t *testing.T) {
	defer goleak.VerifyNone(t)

	clock := debouncer.NewMockClock()
	var flushedCount atomic.Int32

	deb := debouncer.NewDebouncer(debouncer.Config{
		WindowDuration:  2 * time.Second,
		MaxWaitDuration: 10 * time.Second,
		Clock:           clock,
	}, func(ctx context.Context, msg domain.CanonicalMessage) error {
		flushedCount.Add(1)
		return nil
	})

	// Ingest 3 sessions
	for i := 0; i < 3; i++ {
		_ = deb.Ingest(context.Background(), domain.CanonicalMessage{
			ID:      fmt.Sprintf("m-%d", i),
			Channel: "telegram",
			Chat:    domain.ChatContext{ID: fmt.Sprintf("chat-%d", i)},
			Text:    fmt.Sprintf("Text %d", i),
		})
	}

	assert.Equal(t, 3, deb.ActiveSessions())

	// Close before timer expires - must flush all 3 in-flight sessions
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := deb.Close(ctx)
	require.NoError(t, err)

	assert.Equal(t, int32(3), flushedCount.Load())
	assert.Equal(t, 0, deb.ActiveSessions())
}

func TestDebouncer_TC_DEB_12_ConcurrentIngestDuringClose(t *testing.T) {
	defer goleak.VerifyNone(t)

	deb := debouncer.NewDebouncer(debouncer.DefaultConfig(), func(ctx context.Context, msg domain.CanonicalMessage) error {
		return nil
	})

	var wg sync.WaitGroup
	workers := 20

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = deb.Ingest(context.Background(), domain.CanonicalMessage{
					ID:      fmt.Sprintf("m-%d-%d", id, j),
					Channel: "telegram",
					Chat:    domain.ChatContext{ID: fmt.Sprintf("chat-%d", id)},
					Text:    "concurrency test",
				})
			}
		}(i)
	}

	time.Sleep(10 * time.Millisecond)
	_ = deb.Close(context.Background())
	wg.Wait()
}

func TestDebouncer_FlushAndFlushAll(t *testing.T) {
	defer goleak.VerifyNone(t)

	var flushedCount atomic.Int32
	deb := debouncer.NewDebouncer(debouncer.DefaultConfig(), func(ctx context.Context, msg domain.CanonicalMessage) error {
		flushedCount.Add(1)
		return nil
	})
	defer deb.Close(context.Background())

	_ = deb.Ingest(context.Background(), domain.CanonicalMessage{
		ID:      "m1",
		Channel: "telegram",
		Chat:    domain.ChatContext{ID: "flush-target"},
		Text:    "Hello",
	})
	_ = deb.Ingest(context.Background(), domain.CanonicalMessage{
		ID:      "m2",
		Channel: "telegram",
		Chat:    domain.ChatContext{ID: "other-target"},
		Text:    "World",
	})

	assert.Equal(t, 2, deb.ActiveSessions())

	// Flush single session
	err := deb.Flush(context.Background(), "telegram:flush-target")
	require.NoError(t, err)
	assert.Equal(t, 1, deb.ActiveSessions())

	// Flush non-existent session
	err = deb.Flush(context.Background(), "non-existent")
	require.NoError(t, err)

	// FlushAll remaining
	err = deb.FlushAll(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, deb.ActiveSessions())

	require.Eventually(t, func() bool {
		return flushedCount.Load() == 2
	}, 1*time.Second, 10*time.Millisecond)
}

func TestDebouncer_MaxActiveSessionsCapacityExceeded(t *testing.T) {
	defer goleak.VerifyNone(t)

	deb := debouncer.NewDebouncer(debouncer.Config{
		WindowDuration:    2 * time.Second,
		MaxActiveSessions: 2, // Max 2 sessions
	}, func(ctx context.Context, msg domain.CanonicalMessage) error {
		return nil
	})
	defer deb.Close(context.Background())

	_ = deb.Ingest(context.Background(), domain.CanonicalMessage{
		ID:      "m1",
		Channel: "telegram",
		Chat:    domain.ChatContext{ID: "chat1"},
		Text:    "1",
	})
	_ = deb.Ingest(context.Background(), domain.CanonicalMessage{
		ID:      "m2",
		Channel: "telegram",
		Chat:    domain.ChatContext{ID: "chat2"},
		Text:    "2",
	})

	// 3rd session should exceed limit
	err := deb.Ingest(context.Background(), domain.CanonicalMessage{
		ID:      "m3",
		Channel: "telegram",
		Chat:    domain.ChatContext{ID: "chat3"},
		Text:    "3",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, debouncer.ErrTooManySessions))
}
