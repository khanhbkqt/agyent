package eventbus

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"sync/atomic"

	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

var (
	// ErrEventBusClosed is returned when operations are attempted on a closed EventBus.
	ErrEventBusClosed = errors.New("eventbus: bus is closed")
)

type subscriptionEntry[T any] struct {
	id      uint64
	handler T
}

// EventBus implements ports.EventBusPort with low-latency synchronous dispatch and buffered asynchronous workers.
type EventBus struct {
	mu            sync.RWMutex
	syncHandlers  map[domain.EventType][]subscriptionEntry[ports.SyncEventHandler]
	asyncHandlers map[domain.EventType][]subscriptionEntry[ports.AsyncEventHandler]
	nextSubID     atomic.Uint64

	asyncQueue chan domain.Event
	dropCount  atomic.Uint64
	isClosed   bool
	wg         sync.WaitGroup
}

var _ ports.EventBusPort = (*EventBus)(nil)

// NewEventBus creates a new EventBus with the specified async queue capacity and worker count.
func NewEventBus(queueCap, workerCount int) *EventBus {
	if queueCap <= 0 {
		queueCap = 1024
	}
	if workerCount <= 0 {
		workerCount = 4
	}

	b := &EventBus{
		syncHandlers:  make(map[domain.EventType][]subscriptionEntry[ports.SyncEventHandler]),
		asyncHandlers: make(map[domain.EventType][]subscriptionEntry[ports.AsyncEventHandler]),
		asyncQueue:    make(chan domain.Event, queueCap),
	}

	b.startWorkers(workerCount)
	return b
}

func (b *EventBus) startWorkers(count int) {
	for i := 0; i < count; i++ {
		b.wg.Add(1)
		go func() {
			defer b.wg.Done()
			for evt := range b.asyncQueue {
				b.dispatchAsync(evt)
			}
		}()
	}
}

// SubscribeSync registers a synchronous handler using Copy-On-Write slice semantics.
func (b *EventBus) SubscribeSync(t domain.EventType, h ports.SyncEventHandler) ports.UnsubscribeFunc {
	if h == nil {
		return func() {}
	}

	subID := b.nextSubID.Add(1)
	entry := subscriptionEntry[ports.SyncEventHandler]{
		id:      subID,
		handler: h,
	}

	b.mu.Lock()
	curr := b.syncHandlers[t]
	next := make([]subscriptionEntry[ports.SyncEventHandler], len(curr)+1)
	copy(next, curr)
	next[len(curr)] = entry
	b.syncHandlers[t] = next
	b.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			entries := b.syncHandlers[t]
			for i, e := range entries {
				if e.id == subID {
					newEntries := make([]subscriptionEntry[ports.SyncEventHandler], len(entries)-1)
					copy(newEntries, entries[:i])
					copy(newEntries[i:], entries[i+1:])
					b.syncHandlers[t] = newEntries
					break
				}
			}
		})
	}
}

// SubscribeAsync registers an asynchronous handler using Copy-On-Write slice semantics.
func (b *EventBus) SubscribeAsync(t domain.EventType, h ports.AsyncEventHandler) ports.UnsubscribeFunc {
	if h == nil {
		return func() {}
	}

	subID := b.nextSubID.Add(1)
	entry := subscriptionEntry[ports.AsyncEventHandler]{
		id:      subID,
		handler: h,
	}

	b.mu.Lock()
	curr := b.asyncHandlers[t]
	next := make([]subscriptionEntry[ports.AsyncEventHandler], len(curr)+1)
	copy(next, curr)
	next[len(curr)] = entry
	b.asyncHandlers[t] = next
	b.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			entries := b.asyncHandlers[t]
			for i, e := range entries {
				if e.id == subID {
					newEntries := make([]subscriptionEntry[ports.AsyncEventHandler], len(entries)-1)
					copy(newEntries, entries[:i])
					copy(newEntries[i:], entries[i+1:])
					b.asyncHandlers[t] = newEntries
					break
				}
			}
		})
	}
}

// SyncEmit dispatches an event synchronously to all registered sync handlers with panic isolation.
func (b *EventBus) SyncEmit(ctx context.Context, evt domain.Event) error {
	b.mu.RLock()
	entries := b.syncHandlers[evt.Type]
	b.mu.RUnlock()

	if len(entries) == 0 {
		return nil
	}

	var errs []error
	for _, entry := range entries {
		if ctx != nil && ctx.Err() != nil {
			errs = append(errs, ctx.Err())
			break
		}

		if err := b.invokeSyncHandler(ctx, entry.handler, evt); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func (b *EventBus) invokeSyncHandler(ctx context.Context, h ports.SyncEventHandler, evt domain.Event) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("sync handler panic: %v\nstack: %s", r, debug.Stack())
		}
	}()

	if ctx == nil {
		ctx = context.Background()
	}
	return h(ctx, evt)
}

// AsyncEmit queues an event non-blockingly for asynchronous processing.
func (b *EventBus) AsyncEmit(ctx context.Context, evt domain.Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.isClosed {
		b.dropCount.Add(1)
		return
	}

	select {
	case b.asyncQueue <- evt:
	default:
		b.dropCount.Add(1)
	}
}

func (b *EventBus) dispatchAsync(evt domain.Event) {
	b.mu.RLock()
	entries := b.asyncHandlers[evt.Type]
	b.mu.RUnlock()

	if len(entries) == 0 {
		return
	}

	ctx := context.Background()
	for _, entry := range entries {
		func(h ports.AsyncEventHandler) {
			defer func() {
				_ = recover() // Catch and isolate panic to keep worker goroutine alive
			}()
			h(ctx, evt)
		}(entry.handler)
	}
}

// DroppedEventsCount returns the total number of dropped async events.
func (b *EventBus) DroppedEventsCount() uint64 {
	return b.dropCount.Load()
}

// Close gracefully closes the eventbus and drains pending queue events with background context.
func (b *EventBus) Close() error {
	return b.CloseWithTimeout(context.Background())
}

// CloseWithTimeout gracefully closes the eventbus and drains pending queue events within the context deadline.
func (b *EventBus) CloseWithTimeout(ctx context.Context) error {
	b.mu.Lock()
	if b.isClosed {
		b.mu.Unlock()
		return nil
	}
	b.isClosed = true
	close(b.asyncQueue)
	b.mu.Unlock()

	done := make(chan struct{})
	go func() {
		b.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
