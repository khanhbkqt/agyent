package eventbus_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agyent/internal/core/domain"
	"agyent/internal/core/eventbus"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestEventBus_TC_EB_01_SyncEmit_SequentialFIFO(t *testing.T) {
	defer goleak.VerifyNone(t)

	bus := eventbus.NewEventBus(100, 2)
	defer bus.Close()

	var sequence []int
	var mu sync.Mutex

	bus.SubscribeSync(domain.EventMessageReceived, func(ctx context.Context, evt domain.Event) error {
		mu.Lock()
		sequence = append(sequence, 1)
		mu.Unlock()
		return nil
	})

	bus.SubscribeSync(domain.EventMessageReceived, func(ctx context.Context, evt domain.Event) error {
		mu.Lock()
		sequence = append(sequence, 2)
		mu.Unlock()
		return nil
	})

	bus.SubscribeSync(domain.EventMessageReceived, func(ctx context.Context, evt domain.Event) error {
		mu.Lock()
		sequence = append(sequence, 3)
		mu.Unlock()
		return nil
	})

	err := bus.SyncEmit(context.Background(), domain.NewEvent(domain.EventMessageReceived, "test"))
	require.NoError(t, err)

	assert.Equal(t, []int{1, 2, 3}, sequence, "handlers must execute in registered FIFO order")
}

func TestEventBus_TC_EB_02_SyncEmit_PanicIsolation(t *testing.T) {
	defer goleak.VerifyNone(t)

	bus := eventbus.NewEventBus(100, 2)
	defer bus.Close()

	var h1Called, h3Called bool

	bus.SubscribeSync(domain.EventStreamDelta, func(ctx context.Context, evt domain.Event) error {
		h1Called = true
		return nil
	})

	// Panicking handler
	bus.SubscribeSync(domain.EventStreamDelta, func(ctx context.Context, evt domain.Event) error {
		panic("nil pointer dereference inside sync handler")
	})

	bus.SubscribeSync(domain.EventStreamDelta, func(ctx context.Context, evt domain.Event) error {
		h3Called = true
		return nil
	})

	err := bus.SyncEmit(context.Background(), domain.NewEvent(domain.EventStreamDelta, "token"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sync handler panic: nil pointer dereference inside sync handler")
	assert.Contains(t, err.Error(), "stack:")
	assert.True(t, h1Called, "handler 1 should execute")
	assert.True(t, h3Called, "handler 3 should execute despite handler 2 panic")
}

func TestEventBus_TC_EB_03_SyncEmit_MultierrAggregation(t *testing.T) {
	defer goleak.VerifyNone(t)

	bus := eventbus.NewEventBus(100, 2)
	defer bus.Close()

	errA := errors.New("error Alpha")
	errB := errors.New("error Beta")

	bus.SubscribeSync(domain.EventPreExecution, func(ctx context.Context, evt domain.Event) error {
		return errA
	})
	bus.SubscribeSync(domain.EventPreExecution, func(ctx context.Context, evt domain.Event) error {
		return errB
	})

	err := bus.SyncEmit(context.Background(), domain.NewEvent(domain.EventPreExecution, "payload"))
	require.Error(t, err)
	assert.True(t, errors.Is(err, errA))
	assert.True(t, errors.Is(err, errB))
}

func TestEventBus_TC_EB_04_AsyncEmit_NonBlockingDispatch(t *testing.T) {
	defer goleak.VerifyNone(t)

	bus := eventbus.NewEventBus(100, 2)
	defer bus.Close()

	var received atomic.Int32
	bus.SubscribeAsync(domain.EventArtifactDetected, func(ctx context.Context, evt domain.Event) {
		time.Sleep(20 * time.Millisecond) // Simulate slow audit / index operation
		received.Add(1)
	})

	start := time.Now()
	bus.AsyncEmit(context.Background(), domain.NewEvent(domain.EventArtifactDetected, "file.png"))
	elapsed := time.Since(start)

	assert.Less(t, elapsed, 5*time.Millisecond, "AsyncEmit must return immediately without waiting for handler")

	// Wait for async worker to complete
	require.Eventually(t, func() bool {
		return received.Load() == 1
	}, 1*time.Second, 10*time.Millisecond)
}

func TestEventBus_TC_EB_05_AsyncQueue_OverflowPolicyAndDropCounter(t *testing.T) {
	defer goleak.VerifyNone(t)

	queueCap := 5
	bus := eventbus.NewEventBus(queueCap, 1)

	// Block the single worker
	blocker := make(chan struct{})
	bus.SubscribeAsync(domain.EventErrorOccurred, func(ctx context.Context, evt domain.Event) {
		<-blocker
	})

	// Fill queue
	for i := 0; i < queueCap+1; i++ {
		bus.AsyncEmit(context.Background(), domain.NewEvent(domain.EventErrorOccurred, fmt.Sprintf("err-%d", i)))
	}

	// Next 10 emits should be dropped non-blockingly
	for i := 0; i < 10; i++ {
		bus.AsyncEmit(context.Background(), domain.NewEvent(domain.EventErrorOccurred, fmt.Sprintf("overflow-%d", i)))
	}

	assert.GreaterOrEqual(t, bus.DroppedEventsCount(), uint64(10), "dropped count should track dropped events")

	close(blocker)
	bus.Close()
}

func TestEventBus_TC_EB_06_GracefulShutdown_DrainQueue(t *testing.T) {
	defer goleak.VerifyNone(t)

	bus := eventbus.NewEventBus(100, 2)

	var processedCount atomic.Int32
	bus.SubscribeAsync(domain.EventPostExecution, func(ctx context.Context, evt domain.Event) {
		time.Sleep(5 * time.Millisecond)
		processedCount.Add(1)
	})

	totalEvents := 30
	for i := 0; i < totalEvents; i++ {
		bus.AsyncEmit(context.Background(), domain.NewEvent(domain.EventPostExecution, i))
	}

	// Close with timeout should wait for all 30 events to be drained
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := bus.CloseWithTimeout(ctx)
	require.NoError(t, err)

	assert.Equal(t, int32(totalEvents), processedCount.Load(), "graceful close must drain all pending events")
}

func TestEventBus_TC_EB_07_ConcurrentPubSubStress(t *testing.T) {
	defer goleak.VerifyNone(t)

	bus := eventbus.NewEventBus(1000, 8)
	defer bus.Close()

	workers := 50
	var wg sync.WaitGroup
	var syncProcessed, asyncProcessed atomic.Int64

	bus.SubscribeSync(domain.EventStreamDelta, func(ctx context.Context, evt domain.Event) error {
		syncProcessed.Add(1)
		return nil
	})

	bus.SubscribeAsync(domain.EventMessageReceived, func(ctx context.Context, evt domain.Event) {
		asyncProcessed.Add(1)
	})

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = bus.SyncEmit(context.Background(), domain.NewEvent(domain.EventStreamDelta, j))
				bus.AsyncEmit(context.Background(), domain.NewEvent(domain.EventMessageReceived, j))
			}
		}(i)
	}

	wg.Wait()

	assert.Equal(t, int64(workers*50), syncProcessed.Load())
	require.Eventually(t, func() bool {
		return asyncProcessed.Load() == int64(workers*50)
	}, 3*time.Second, 20*time.Millisecond)
}

func TestEventBus_TC_EB_08_ReentrantSyncEmitSafety(t *testing.T) {
	defer goleak.VerifyNone(t)

	bus := eventbus.NewEventBus(100, 2)
	defer bus.Close()

	var innerCalled bool

	bus.SubscribeSync(domain.EventStreamResult, func(ctx context.Context, evt domain.Event) error {
		innerCalled = true
		return nil
	})

	bus.SubscribeSync(domain.EventPostExecution, func(ctx context.Context, evt domain.Event) error {
		// Re-entrant emit inside handler
		return bus.SyncEmit(ctx, domain.NewEvent(domain.EventStreamResult, "inner result"))
	})

	err := bus.SyncEmit(context.Background(), domain.NewEvent(domain.EventPostExecution, "outer post"))
	require.NoError(t, err)
	assert.True(t, innerCalled, "inner event emission must succeed without deadlock")
}

func TestEventBus_TC_EB_09_DynamicSubscribeUnsubscribeDuringHotEmit(t *testing.T) {
	defer goleak.VerifyNone(t)

	bus := eventbus.NewEventBus(500, 4)
	defer bus.Close()

	done := make(chan struct{})
	var wg sync.WaitGroup

	// Publisher goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				_ = bus.SyncEmit(context.Background(), domain.NewEvent(domain.EventStreamDelta, "token"))
				bus.AsyncEmit(context.Background(), domain.NewEvent(domain.EventArtifactDetected, "img"))
				time.Sleep(100 * time.Microsecond)
			}
		}
	}()

	// Mutator goroutines subscribing & unsubscribing dynamically
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 30; j++ {
				unsubSync := bus.SubscribeSync(domain.EventStreamDelta, func(ctx context.Context, evt domain.Event) error {
					return nil
				})
				unsubAsync := bus.SubscribeAsync(domain.EventArtifactDetected, func(ctx context.Context, evt domain.Event) {})

				time.Sleep(500 * time.Microsecond)
				unsubSync()
				unsubAsync()
			}
		}(i)
	}

	time.Sleep(100 * time.Millisecond)
	close(done)
	wg.Wait()
}

func TestEventBus_TC_EB_10_ContextPropagationAndCancellation(t *testing.T) {
	defer goleak.VerifyNone(t)

	bus := eventbus.NewEventBus(100, 2)
	defer bus.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Pre-canceled context

	var hCalled bool
	bus.SubscribeSync(domain.EventStreamDelta, func(c context.Context, evt domain.Event) error {
		hCalled = true
		return nil
	})

	err := bus.SyncEmit(ctx, domain.NewEvent(domain.EventStreamDelta, "token"))
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled))
	assert.False(t, hCalled, "handler should not be called if context was already canceled")
}

func BenchmarkEventBus_SyncEmitHotPath(b *testing.B) {
	bus := eventbus.NewEventBus(100, 2)
	defer bus.Close()

	bus.SubscribeSync(domain.EventStreamDelta, func(ctx context.Context, evt domain.Event) error {
		return nil
	})

	ctx := context.Background()
	evt := domain.NewEvent(domain.EventStreamDelta, &domain.StreamDeltaPayload{
		SessionKey: "telegram:123",
		TextDelta:  "token",
	})

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = bus.SyncEmit(ctx, evt)
	}
}
