package ports

import (
	"context"
	"io"

	"agyent/internal/core/domain"
)

// UnsubscribeFunc cancels an event subscription.
type UnsubscribeFunc func()

// SyncEventHandler is a handler for synchronous event delivery. Returning an error can abort execution.
type SyncEventHandler func(ctx context.Context, evt domain.Event) error

// AsyncEventHandler is a handler for asynchronous event delivery in background workers.
type AsyncEventHandler func(ctx context.Context, evt domain.Event)

// EventBusPort defines pub/sub contracts for internal system lifecycle and stream events.
type EventBusPort interface {
	io.Closer

	// SyncEmit dispatches an event synchronously to all registered sync handlers with panic isolation.
	SyncEmit(ctx context.Context, evt domain.Event) error

	// AsyncEmit queues an event non-blockingly for asynchronous processing.
	AsyncEmit(ctx context.Context, evt domain.Event)

	// SubscribeSync registers a handler for synchronous event processing and returns an unsubscribe callback.
	SubscribeSync(eventType domain.EventType, handler SyncEventHandler) UnsubscribeFunc

	// SubscribeAsync registers a handler for asynchronous event processing and returns an unsubscribe callback.
	SubscribeAsync(eventType domain.EventType, handler AsyncEventHandler) UnsubscribeFunc

	// DroppedEventsCount returns the count of dropped async events due to buffer saturation.
	DroppedEventsCount() uint64

	// CloseWithTimeout gracefully drains the async queue within the given context deadline.
	CloseWithTimeout(ctx context.Context) error
}
