package concurrency

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
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
	mu          sync.Mutex
	sem         chan struct{}
	refCount    int
	holderToken uint64
}

// SessionLockManager coordinates per-session mutual exclusion with automatic reference counting
// to eliminate memory leaks and ensure serial execution of turns for the same session.
type SessionLockManager struct {
	mu       sync.Mutex
	locks    map[string]*lockEntry
	tokenSeq atomic.Uint64
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
		token := m.tokenSeq.Add(1)
		entry.mu.Lock()
		entry.holderToken = token
		entry.mu.Unlock()

		var once sync.Once
		unlock := func() {
			once.Do(func() {
				entry.mu.Lock()
				if entry.holderToken == token {
					entry.holderToken = 0
					select {
					case <-entry.sem:
					default:
					}
				}
				entry.mu.Unlock()
				cleanupRef()
			})
		}
		return unlock, nil

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
// If an active turn holds the lock, its token is revoked and the semaphore is released
// so that any in-flight or subsequent turns can acquire the session lock immediately.
// Returns true if a lock entry existed.
func (m *SessionLockManager) ForceUnlock(sessionKey string) bool {
	m.mu.Lock()
	entry, exists := m.locks[sessionKey]
	if !exists {
		m.mu.Unlock()
		return false
	}

	entry.mu.Lock()
	if entry.holderToken != 0 {
		entry.holderToken = 0
		select {
		case <-entry.sem:
		default:
		}
	} else if entry.refCount <= 0 {
		delete(m.locks, sessionKey)
	}
	entry.mu.Unlock()
	m.mu.Unlock()

	return true
}
