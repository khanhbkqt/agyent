package composite

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

var (
	_ ports.ChannelPort      = (*Mux)(nil)
	_ ports.HITLApprovalPort = (*Mux)(nil)
)

// Mux aggregates multiple messaging channel adapters (e.g. Telegram, Zalo) and implements
// both ports.ChannelPort and ports.HITLApprovalPort as a unified multiplexer.
type Mux struct {
	mu           sync.RWMutex
	adapters     map[string]ports.ChannelPort
	hitlAdapters map[string]ports.HITLApprovalPort
	primary      ports.ChannelPort
	running      bool
}

// ChannelMux is an alias for Mux for semantic clarity and backwards compatibility.
type ChannelMux = Mux

// NewMux constructs an empty composite channel multiplexer.
func NewMux() *Mux {
	return &Mux{
		adapters:     make(map[string]ports.ChannelPort),
		hitlAdapters: make(map[string]ports.HITLApprovalPort),
	}
}

// NewChannelMux creates an empty channel multiplexer.
func NewChannelMux() *Mux {
	return NewMux()
}

// Register adds a channel adapter to the multiplexer under its identifier (e.g. "telegram", "zalo").
func (m *Mux) Register(adapter ports.ChannelPort) {
	if adapter == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	name := adapter.Name()
	m.adapters[name] = adapter
	if m.primary == nil {
		m.primary = adapter
	}

	// Check if adapter implements ports.HITLApprovalPort directly or via getter
	if hitl, ok := adapter.(ports.HITLApprovalPort); ok {
		m.hitlAdapters[name] = hitl
	} else if hitlGetter, ok := adapter.(interface{ HITLCoordinator() any }); ok {
		if c, ok := hitlGetter.HITLCoordinator().(ports.HITLApprovalPort); ok && c != nil {
			m.hitlAdapters[name] = c
		}
	}
}

// RegisterAdapter is an alias for Register.
func (m *Mux) RegisterAdapter(adapter ports.ChannelPort) {
	m.Register(adapter)
}

// Get retrieves a registered adapter by channel name.
func (m *Mux) Get(name string) (ports.ChannelPort, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ch, ok := m.adapters[name]
	return ch, ok
}

// Name returns the composite channel identifier.
func (m *Mux) Name() string {
	return "composite"
}

// resolveAdapter resolves the appropriate channel adapter using explicit channel, session key, or fallback.
func (m *Mux) resolveAdapter(channel, sessionKey string) (ports.ChannelPort, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.adapters) == 0 {
		return nil, errors.New("no channel adapters registered in composite mux")
	}

	if channel != "" {
		if ch, exists := m.adapters[channel]; exists {
			return ch, nil
		}
	}

	if sessionKey != "" {
		if parsed, err := domain.ParseSessionKey(sessionKey); err == nil && parsed.Channel != "" {
			if ch, exists := m.adapters[parsed.Channel]; exists {
				return ch, nil
			}
		}
	}

	if m.primary != nil {
		return m.primary, nil
	}

	for _, ch := range m.adapters {
		return ch, nil
	}

	return nil, errors.New("no available channel adapter")
}

// Start launches all registered channel adapters concurrently using the shared inbound message channel.
func (m *Mux) Start(ctx context.Context, inbound chan<- domain.CanonicalMessage) error {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return errors.New("composite mux already running")
	}
	if len(m.adapters) == 0 {
		m.mu.Unlock()
		return errors.New("no channel adapters registered in composite mux")
	}
	m.running = true

	adapters := make(map[string]ports.ChannelPort, len(m.adapters))
	for k, v := range m.adapters {
		adapters[k] = v
	}
	m.mu.Unlock()

	var startErr error
	startedCount := 0

	for name, ch := range adapters {
		if err := ch.Start(ctx, inbound); err != nil {
			slog.ErrorContext(ctx, "failed to start channel adapter", "channel", name, "error", err)
			if startErr == nil {
				startErr = fmt.Errorf("failed to start channel adapter %q: %w", name, err)
			}
		} else {
			startedCount++
		}
	}

	if startedCount == 0 && startErr != nil {
		return startErr
	}
	return nil
}

// Send routes an outbound message to the target channel adapter.
func (m *Mux) Send(ctx context.Context, msg domain.OutboundMessage) error {
	adapter, err := m.resolveAdapter(msg.Channel, msg.SessionKey)
	if err != nil {
		return err
	}
	return adapter.Send(ctx, msg)
}

// SendTyping routes typing indicators to the matching channel adapter.
func (m *Mux) SendTyping(ctx context.Context, target domain.TargetContext) error {
	adapter, err := m.resolveAdapter(target.Channel, "")
	if err != nil {
		return err
	}
	return adapter.SendTyping(ctx, target)
}

// SendChatAction routes chat action indicators to the matching channel adapter.
func (m *Mux) SendChatAction(ctx context.Context, target domain.TargetContext, action string) error {
	adapter, err := m.resolveAdapter(target.Channel, "")
	if err != nil {
		return err
	}
	return adapter.SendChatAction(ctx, target, action)
}

// SendFile routes outbound file attachments to the matching channel adapter.
func (m *Mux) SendFile(ctx context.Context, target domain.TargetContext, filePath string, caption string) error {
	adapter, err := m.resolveAdapter(target.Channel, "")
	if err != nil {
		return err
	}
	return adapter.SendFile(ctx, target, filePath, caption)
}

// Stop gracefully shuts down all registered channel adapters concurrently with a timeout.
func (m *Mux) Stop() error {
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return nil
	}
	m.running = false

	adapters := make(map[string]ports.ChannelPort, len(m.adapters))
	for k, v := range m.adapters {
		adapters[k] = v
	}
	m.mu.Unlock()

	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex

	for name, ch := range adapters {
		wg.Add(1)
		go func(n string, a ports.ChannelPort) {
			defer wg.Done()
			if err := a.Stop(); err != nil {
				slog.Warn("channel adapter failed to stop cleanly", "channel", n, "error", err)
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errMu.Unlock()
			}
		}(name, ch)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		slog.Info("all channel adapters stopped cleanly")
	case <-time.After(2000 * time.Millisecond):
		slog.Warn("channel multiplexer stop timed out after 2s, forcing exit")
	}

	return firstErr
}

// RequestApproval routes an interactive HITL approval card to the appropriate channel.
func (m *Mux) RequestApproval(ctx context.Context, req domain.ApprovalRequest) (domain.ApprovalDecision, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	channel := ""
	if parsed, err := domain.ParseSessionKey(req.SessionKey); err == nil {
		channel = parsed.Channel
	}

	if channel != "" {
		if hitl, ok := m.hitlAdapters[channel]; ok {
			return hitl.RequestApproval(ctx, req)
		}
	}

	// Fallback to primary or first available HITL coordinator
	for _, hitl := range m.hitlAdapters {
		return hitl.RequestApproval(ctx, req)
	}

	return domain.ApprovalDecision{
		RequestID: req.RequestID,
		Action:    "denied_no_channel",
		Approved:  false,
		Timestamp: time.Now(),
	}, fmt.Errorf("no HITL approval coordinator available for channel %q", channel)
}

// HandleCallback processes inline keyboard clicks on the target channel if supported.
func (m *Mux) HandleCallback(ctx context.Context, callbackID string, userID int64, action string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, hitl := range m.hitlAdapters {
		if handler, ok := hitl.(interface {
			HandleCallback(ctx context.Context, callbackID string, userID int64, action string) error
		}); ok {
			if err := handler.HandleCallback(ctx, callbackID, userID, action); err == nil {
				return nil
			}
		}
	}
	return nil
}

// CancelPendingRequest terminates pending approval requests across all registered channel adapters.
func (m *Mux) CancelPendingRequest(requestID string) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, hitl := range m.hitlAdapters {
		hitl.CancelPendingRequest(requestID)
	}
}

// CancelPendingRequestsForSession terminates all pending requests for a session.
func (m *Mux) CancelPendingRequestsForSession(sessionKey string) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	channel := ""
	if parsed, err := domain.ParseSessionKey(sessionKey); err == nil {
		channel = parsed.Channel
	}

	if channel != "" {
		if hitl, ok := m.hitlAdapters[channel]; ok {
			hitl.CancelPendingRequestsForSession(sessionKey)
			return
		}
	}

	for _, hitl := range m.hitlAdapters {
		hitl.CancelPendingRequestsForSession(sessionKey)
	}
}
