package debouncer

import (
	"sync"
	"time"
)

// Timer abstracts the Go time.Timer contract.
type Timer interface {
	Stop() bool
	Reset(d time.Duration) bool
}

// Clock abstracts system time functions for deterministic testing.
type Clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, f func()) Timer
}

// RealClock uses the standard Go time package.
type RealClock struct{}

var _ Clock = RealClock{}

func (RealClock) Now() time.Time {
	return time.Now()
}

type realTimer struct {
	t *time.Timer
}

func (r *realTimer) Stop() bool {
	return r.t.Stop()
}

func (r *realTimer) Reset(d time.Duration) bool {
	return r.t.Reset(d)
}

func (RealClock) AfterFunc(d time.Duration, f func()) Timer {
	return &realTimer{t: time.AfterFunc(d, f)}
}

// MockClock provides a deterministic virtual clock for flake-free instant unit testing.
type MockClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*mockTimer
}

// NewMockClock creates a MockClock initialized at a fixed base timestamp.
func NewMockClock() *MockClock {
	return &MockClock{
		now: time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC),
	}
}

func (m *MockClock) Now() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.now
}

func (m *MockClock) AfterFunc(d time.Duration, f func()) Timer {
	m.mu.Lock()
	defer m.mu.Unlock()

	t := &mockTimer{
		clock:    m,
		callback: f,
		fireAt:   m.now.Add(d),
		active:   true,
	}
	m.timers = append(m.timers, t)
	return t
}

// Advance advances the virtual clock time by d and triggers any expired timers synchronously.
func (m *MockClock) Advance(d time.Duration) {
	m.mu.Lock()
	m.now = m.now.Add(d)
	current := m.now

	var toFire []func()
	for _, t := range m.timers {
		if t.active && !current.Before(t.fireAt) {
			t.active = false
			toFire = append(toFire, t.callback)
		}
	}
	m.mu.Unlock()

	for _, cb := range toFire {
		cb()
	}
}

type mockTimer struct {
	clock    *MockClock
	callback func()
	fireAt   time.Time
	active   bool
}

func (t *mockTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	wasActive := t.active
	t.active = false
	return wasActive
}

func (t *mockTimer) Reset(d time.Duration) bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	wasActive := t.active
	t.fireAt = t.clock.now.Add(d)
	t.active = true
	return wasActive
}
