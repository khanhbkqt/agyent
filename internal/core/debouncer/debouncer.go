package debouncer

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

var (
	// ErrDebouncerClosed is returned when operations are attempted on a closed debouncer.
	ErrDebouncerClosed = errors.New("debouncer: engine is closed")
	// ErrTooManySessions is returned when the active sessions capacity is exceeded.
	ErrTooManySessions = errors.New("debouncer: max active sessions limit exceeded")
)

// Config defines the tuning parameters for the message debouncer.
type Config struct {
	WindowDuration    time.Duration
	MaxWaitDuration   time.Duration
	MaxMessageCount   int
	MaxActiveSessions int
	Clock             Clock
}

// DefaultConfig returns standard production defaults for the debouncer.
func DefaultConfig() Config {
	return Config{
		WindowDuration:    2 * time.Second,
		MaxWaitDuration:   10 * time.Second,
		MaxMessageCount:   20,
		MaxActiveSessions: 5000,
		Clock:             RealClock{},
	}
}

type sessionState struct {
	mu            sync.Mutex
	sessionKey    string
	messages      []domain.CanonicalMessage
	timer         Timer
	firstReceived time.Time
	isFlushing    atomic.Bool
}

// Debouncer implements ports.DebouncerPort with sliding window coalescing and starvation prevention.
type Debouncer struct {
	mu       sync.RWMutex
	config   Config
	sessions map[string]*sessionState
	handler  ports.DebounceHandler

	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	isClosed bool
}

var _ ports.DebouncerPort = (*Debouncer)(nil)

// NewDebouncer creates and initializes a new Debouncer instance.
func NewDebouncer(cfg Config, handler ports.DebounceHandler) *Debouncer {
	if cfg.WindowDuration <= 0 {
		cfg.WindowDuration = 2 * time.Second
	}
	if cfg.MaxWaitDuration <= 0 {
		cfg.MaxWaitDuration = 10 * time.Second
	}
	if cfg.MaxMessageCount <= 0 {
		cfg.MaxMessageCount = 20
	}
	if cfg.MaxActiveSessions <= 0 {
		cfg.MaxActiveSessions = 5000
	}
	if cfg.Clock == nil {
		cfg.Clock = RealClock{}
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &Debouncer{
		config:   cfg,
		sessions: make(map[string]*sessionState),
		handler:  handler,
		ctx:      ctx,
		cancel:   cancel,
	}
}

// Ingest receives an inbound message, resets the sliding window, or executes fast-path pre-emption.
func (d *Debouncer) Ingest(ctx context.Context, msg domain.CanonicalMessage) error {
	sessionKey := msg.SessionKey()

	// 1. FAST-PATH PRE-EMPTION FOR SLASH COMMANDS
	if msg.IsCommand() {
		d.mu.Lock()
		if d.isClosed {
			d.mu.Unlock()
			return ErrDebouncerClosed
		}

		// Flush pending regular buffer BEFORE executing command to prevent lost messages
		var pendingMsgs []domain.CanonicalMessage
		if state, exists := d.sessions[sessionKey]; exists {
			state.mu.Lock()
			if state.timer != nil {
				state.timer.Stop()
				state.timer = nil
			}
			pendingMsgs = state.messages
			state.messages = nil
			state.isFlushing.Store(true)
			state.mu.Unlock()
			delete(d.sessions, sessionKey)
		}
		d.mu.Unlock()

		if len(pendingMsgs) > 0 {
			_ = d.dispatchAsync(ctx, pendingMsgs)
		}

		// Dispatch command immediately with 0ms delay outside mutex
		if d.handler != nil {
			return d.handler(ctx, msg)
		}
		return nil
	}

	// 2. REGULAR MESSAGE DEBOUNCING (ADMISSION BARRIER PATTERN)
	for {
		d.mu.Lock()
		if d.isClosed {
			d.mu.Unlock()
			return ErrDebouncerClosed
		}

		state, exists := d.sessions[sessionKey]
		if !exists || state.isFlushing.Load() {
			if len(d.sessions) >= d.config.MaxActiveSessions {
				d.mu.Unlock()
				return ErrTooManySessions
			}
			state = &sessionState{
				sessionKey:    sessionKey,
				messages:      make([]domain.CanonicalMessage, 0, 4),
				firstReceived: d.config.Clock.Now(),
			}
			d.sessions[sessionKey] = state
		}

		state.mu.Lock()
		if state.isFlushing.Load() {
			// State began flushing between check and lock acquisition, retry barrier admission
			state.mu.Unlock()
			d.mu.Unlock()
			continue
		}
		d.mu.Unlock()

		state.messages = append(state.messages, msg)
		msgCount := len(state.messages)
		elapsed := d.config.Clock.Now().Sub(state.firstReceived)
		remainingMax := d.config.MaxWaitDuration - elapsed

		// 3. CHECK FLUSH CONDITIONS (MAX COUNT OR MAX WAIT DURATION)
		if msgCount >= d.config.MaxMessageCount || remainingMax <= 0 {
			msgsToFlush := state.messages
			state.messages = nil
			if state.timer != nil {
				state.timer.Stop()
				state.timer = nil
			}
			state.isFlushing.Store(true)
			state.mu.Unlock()

			d.mu.Lock()
			if cur, ok := d.sessions[sessionKey]; ok && cur == state {
				delete(d.sessions, sessionKey)
			}
			d.mu.Unlock()

			return d.dispatchAsync(ctx, msgsToFlush)
		}

		// 4. DYNAMIC SLIDING WINDOW TIMER RESET (CLAMPED BY REMAINING MAX)
		nextDelay := d.config.WindowDuration
		if nextDelay > remainingMax {
			nextDelay = remainingMax
		}

		if state.timer != nil {
			state.timer.Stop()
		}

		state.timer = d.config.Clock.AfterFunc(nextDelay, func() {
			d.onTimerFired(sessionKey, state)
		})
		state.mu.Unlock()
		return nil
	}
}

func (d *Debouncer) onTimerFired(sessionKey string, state *sessionState) {
	state.mu.Lock()
	if len(state.messages) == 0 || state.isFlushing.Load() {
		state.mu.Unlock()
		return
	}
	msgsToFlush := state.messages
	state.messages = nil
	state.timer = nil
	state.isFlushing.Store(true)
	state.mu.Unlock()

	// Zero-Leak Map: Remove session from registry
	d.mu.Lock()
	if cur, ok := d.sessions[sessionKey]; ok && cur == state {
		delete(d.sessions, sessionKey)
	}
	d.mu.Unlock()

	_ = d.dispatchAsync(d.ctx, msgsToFlush)
}

func (d *Debouncer) dispatchAsync(ctx context.Context, msgs []domain.CanonicalMessage) error {
	if len(msgs) == 0 || d.handler == nil {
		return nil
	}

	coalesced := CoalesceMessages(msgs)

	d.mu.Lock()
	if d.isClosed {
		d.mu.Unlock()
		return d.handler(ctx, coalesced)
	}
	d.wg.Add(1)
	d.mu.Unlock()

	go func() {
		defer d.wg.Done()
		_ = d.handler(ctx, coalesced)
	}()

	return nil
}

// Flush immediately flushes pending messages for a specific session.
func (d *Debouncer) Flush(ctx context.Context, sessionKey string) error {
	d.mu.Lock()
	state, exists := d.sessions[sessionKey]
	if !exists {
		d.mu.Unlock()
		return nil
	}
	delete(d.sessions, sessionKey)
	d.mu.Unlock()

	state.mu.Lock()
	if len(state.messages) == 0 || state.isFlushing.Load() {
		state.mu.Unlock()
		return nil
	}
	msgsToFlush := state.messages
	state.messages = nil
	if state.timer != nil {
		state.timer.Stop()
		state.timer = nil
	}
	state.isFlushing.Store(true)
	state.mu.Unlock()

	return d.dispatchAsync(ctx, msgsToFlush)
}

// FlushAll flushes all pending in-flight session buffers.
func (d *Debouncer) FlushAll(ctx context.Context) error {
	d.mu.Lock()
	active := make(map[string]*sessionState, len(d.sessions))
	for k, v := range d.sessions {
		active[k] = v
	}
	d.sessions = make(map[string]*sessionState)
	d.mu.Unlock()

	for k, s := range active {
		s.mu.Lock()
		if len(s.messages) > 0 && !s.isFlushing.Load() {
			msgs := s.messages
			s.messages = nil
			if s.timer != nil {
				s.timer.Stop()
				s.timer = nil
			}
			s.isFlushing.Store(true)
			s.mu.Unlock()
			_ = d.dispatchAsync(ctx, msgs)
		} else {
			s.mu.Unlock()
		}
		_ = k
	}

	return nil
}

// ActiveSessions returns the current count of active tracked sessions.
func (d *Debouncer) ActiveSessions() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.sessions)
}

// Close gracefully flushes pending buffers and waits for handlers to complete.
func (d *Debouncer) Close(ctx context.Context) error {
	d.mu.Lock()
	if d.isClosed {
		d.mu.Unlock()
		return nil
	}
	d.isClosed = true
	d.mu.Unlock()

	if ctx == nil {
		ctx = context.Background()
	}

	_ = d.FlushAll(ctx)
	d.cancel()

	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
