package ports

import (
	"context"

	"agyent/internal/core/domain"
)

// DebounceHandler is invoked when a debounced message window closes and emits a coalesced message.
type DebounceHandler func(ctx context.Context, msg domain.CanonicalMessage) error

// DebouncerPort defines the sliding window buffering and coalescing contract.
type DebouncerPort interface {
	// Ingest accepts an inbound message, resets the sliding window, or triggers fast-path pre-emption.
	Ingest(ctx context.Context, msg domain.CanonicalMessage) error

	// Flush forces immediate coalescing and emission for a specific session.
	Flush(ctx context.Context, sessionKey string) error

	// FlushAll flushes all pending in-flight session buffers.
	FlushAll(ctx context.Context) error

	// ActiveSessions returns the current count of active buffered sessions (zero-idle check).
	ActiveSessions() int

	// Close gracefully flushes pending buffers and stops all timers.
	Close(ctx context.Context) error
}
