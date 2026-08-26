package eventbus_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agyent/internal/core/debouncer"
	"agyent/internal/core/domain"
	"agyent/internal/core/eventbus"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestChaos_TC_CHS_01_TimerFire_vs_SlashCommand_Race(t *testing.T) {
	defer goleak.VerifyNone(t)

	clock := debouncer.NewMockClock()
	var dispatched []domain.CanonicalMessage
	var mu sync.Mutex

	deb := debouncer.NewDebouncer(debouncer.Config{
		WindowDuration:  2 * time.Second,
		MaxWaitDuration: 10 * time.Second,
		Clock:           clock,
	}, func(ctx context.Context, msg domain.CanonicalMessage) error {
		mu.Lock()
		dispatched = append(dispatched, msg)
		mu.Unlock()
		return nil
	})
	defer deb.Close(context.Background())

	// Step 1: Ingest regular message
	_ = deb.Ingest(context.Background(), domain.CanonicalMessage{
		ID:        "m1",
		Channel:   "telegram",
		Chat:      domain.ChatContext{ID: "c1"},
		Text:      "Pending message",
		Timestamp: clock.Now(),
	})

	// Step 2: Advance to 1.99s (almost firing)
	clock.Advance(1990 * time.Millisecond)

	// Step 3: Slash command pre-empts
	_ = deb.Ingest(context.Background(), domain.CanonicalMessage{
		ID:        "m-cmd",
		Channel:   "telegram",
		Chat:      domain.ChatContext{ID: "c1"},
		Text:      "/reset",
		Timestamp: clock.Now(),
	})

	// Step 4: Advance further
	clock.Advance(1 * time.Second)

	mu.Lock()
	defer mu.Unlock()
	// Should have exactly 1 dispatched message (/reset), the pending one was cleared
	require.Len(t, dispatched, 1)
	assert.Equal(t, "/reset", dispatched[0].Text)
}

func TestChaos_TC_CHS_02_PoisonedAsyncSubscriber_Isolation(t *testing.T) {
	defer goleak.VerifyNone(t)

	// EventBus with 3 workers and queueCap 100
	bus := eventbus.NewEventBus(100, 3)
	defer bus.Close()

	var healthyProcessed atomic.Int32
	poisonBlocker := make(chan struct{})
	var closeOnce sync.Once
	defer closeOnce.Do(func() { close(poisonBlocker) })

	// Poisoned handler that hangs on 1 worker
	bus.SubscribeAsync(domain.EventErrorOccurred, func(ctx context.Context, evt domain.Event) {
		if evt.Payload == "poison" {
			<-poisonBlocker
		}
	})

	// Healthy handler on other events
	bus.SubscribeAsync(domain.EventMessageReceived, func(ctx context.Context, evt domain.Event) {
		healthyProcessed.Add(1)
	})

	// Send poison
	bus.AsyncEmit(context.Background(), domain.NewEvent(domain.EventErrorOccurred, "poison"))

	// Send 20 healthy events
	for i := 0; i < 20; i++ {
		bus.AsyncEmit(context.Background(), domain.NewEvent(domain.EventMessageReceived, i))
	}

	// Healthy events must still be processed by other workers in the pool
	require.Eventually(t, func() bool {
		return healthyProcessed.Load() >= 20
	}, 2*time.Second, 20*time.Millisecond)

	closeOnce.Do(func() { close(poisonBlocker) })
}

func TestChaos_TC_CHS_03_MicrosecondTimestamp_Ingestion(t *testing.T) {
	defer goleak.VerifyNone(t)

	baseTime := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	msgs := make([]domain.CanonicalMessage, 10)
	for i := 0; i < 10; i++ {
		msgs[i] = domain.CanonicalMessage{
			ID:        fmt.Sprintf("msg-%d", i),
			Text:      fmt.Sprintf("Segment %d", i),
			Timestamp: baseTime.Add(time.Duration(i) * time.Microsecond),
		}
	}

	coalesced := debouncer.CoalesceMessages(msgs)
	lines := ""
	for i := 0; i < 10; i++ {
		if i > 0 {
			lines += "\n"
		}
		lines += fmt.Sprintf("Segment %d", i)
	}

	assert.Equal(t, lines, coalesced.Text)
}

func TestChaos_TC_CHS_04_Goleak_ComprehensiveAudit(t *testing.T) {
	defer goleak.VerifyNone(t)

	bus := eventbus.NewEventBus(50, 4)
	deb := debouncer.NewDebouncer(debouncer.DefaultConfig(), func(ctx context.Context, msg domain.CanonicalMessage) error {
		return nil
	})

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = bus.SyncEmit(context.Background(), domain.NewEvent(domain.EventStreamDelta, "test"))
				bus.AsyncEmit(context.Background(), domain.NewEvent(domain.EventArtifactDetected, "file"))
				_ = deb.Ingest(context.Background(), domain.CanonicalMessage{
					ID:      fmt.Sprintf("%d-%d", id, j),
					Channel: "telegram",
					Chat:    domain.ChatContext{ID: fmt.Sprintf("chat-%d", id)},
					Text:    "burst text",
				})
			}
		}(i)
	}

	wg.Wait()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	require.NoError(t, bus.CloseWithTimeout(ctx))
	require.NoError(t, deb.Close(ctx))
}
