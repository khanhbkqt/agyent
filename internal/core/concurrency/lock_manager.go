package concurrency

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"agyent/internal/core/ports"
)

var (
	// ErrLockTimeout is returned when waiting for a session lock exceeds the timeout duration.
	ErrLockTimeout = errors.New("concurrency: timed out waiting for session lock")
	// ErrLockCanceled is returned when the context is canceled while waiting for a session lock.
	ErrLockCanceled = errors.New("concurrency: context canceled while waiting for session lock")
)

type lockEntry struct {
	sem      chan struct{}
	cancelCh chan struct{}
	refCount int
	isClosed bool
}

// SessionLockManager coordinates per-session mutual exclusion with automatic reference counting
// to eliminate memory leaks and ensure serial execution of turns for the same session.
type SessionLockManager struct {
	mu    sync.Mutex
	locks map[string]*lockEntry
}

var _ ports.LockManager = (*SessionLockManager)(nil)

// NewSessionLockManager creates a new SessionLockManager.
func NewSessionLockManager() *SessionLockManager {
	return &SessionLockManager{
		locks: make(map[string]*lockEntry),
	}
}

// Acquire gets an exclusive lock for sessionKey. It returns a ports.UnlockFunc and nil error on success.
// If the timeout expires or context is cancelled before acquiring the lock, an appropriate error is returned.
func (m *SessionLockManager) Acquire(ctx context.Context, sessionKey string, timeout time.Duration) (ports.UnlockFunc, error) {
	m.mu.Lock()
	entry, exists := m.locks[sessionKey]
	if !exists {
		entry = &lockEntry{
			sem:      make(chan struct{}, 1),
			cancelCh: make(chan struct{}),
			refCount: 0,
		}
		m.locks[sessionKey] = entry
	}
	entry.refCount++
	m.mu.Unlock()

	var timer *time.Timer
	var timeoutChan <-chan time.Time
	if timeout > 0 {
		timer = time.NewTimer(timeout)
		defer timer.Stop()
		timeoutChan = timer.C
	}

	cleanupRef := func() {
		m.mu.Lock()
		entry.refCount--
		if entry.refCount <= 0 && m.locks[sessionKey] == entry {
			delete(m.locks, sessionKey)
		}
		m.mu.Unlock()
	}

	if err := ctx.Err(); err != nil {
		cleanupRef()
		return nil, fmt.Errorf("%w: %w", ErrLockCanceled, err)
	}

	select {
	case entry.sem <- struct{}{}:
		// Non-blocking check to guard against select pseudo-randomness if cancelCh closed simultaneously
		select {
		case <-entry.cancelCh:
			select {
			case <-entry.sem:
			default:
			}
			cleanupRef()
			return nil, ErrLockCanceled
		default:
		}

		var once sync.Once
		unlock := func() {
			once.Do(func() {
				select {
				case <-entry.sem:
				default:
				}
				cleanupRef()
			})
		}
		return unlock, nil

	case <-entry.cancelCh:
		cleanupRef()
		return nil, ErrLockCanceled

	case <-ctx.Done():
		cleanupRef()
		return nil, fmt.Errorf("%w: %w", ErrLockCanceled, ctx.Err())

	case <-timeoutChan:
		cleanupRef()
		return nil, fmt.Errorf("%w after %v for session %s", ErrLockTimeout, timeout, sessionKey)
	}
}

// ActiveLockCount returns the current number of tracked session keys (used for memory leak QA assertions).
func (m *SessionLockManager) ActiveLockCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.locks)
}

// ForceUnlock unconditionally resets the lock state for sessionKey.
// Any in-flight goroutines waiting to acquire the lock for this key are immediately aborted with ErrLockCanceled.
// Returns true if a lock entry existed.
func (m *SessionLockManager) ForceUnlock(sessionKey string) bool {
	m.mu.Lock()
	entry, exists := m.locks[sessionKey]
	if !exists {
		m.mu.Unlock()
		return false
	}
	delete(m.locks, sessionKey)

	// Close cancelCh to immediately unblock and abort all waiting goroutines on this entry
	if !entry.isClosed {
		entry.isClosed = true
		close(entry.cancelCh)
	}
	m.mu.Unlock()

	return true
}
