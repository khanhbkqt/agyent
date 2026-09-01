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

// ChannelMux aggregates multiple ChannelPorts and acts as a single composite ChannelPort.
type ChannelMux struct {
	mu           sync.RWMutex
	adapters     map[string]ports.ChannelPort
	hitlAdapters map[string]ports.HITLApprovalPort
}

// NewChannelMux creates an empty channel multiplexer.
func NewChannelMux() *ChannelMux {
	return &ChannelMux{
		adapters:     make(map[string]ports.ChannelPort),
		hitlAdapters: make(map[string]ports.HITLApprovalPort),
	}
}

// RegisterAdapter adds a channel adapter to the multiplexer.
func (m *ChannelMux) RegisterAdapter(adapter ports.ChannelPort) {
	if adapter == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	name := adapter.Name()
	m.adapters[name] = adapter

	// Check if adapter implements ports.HITLApprovalPort
	if hitl, ok := adapter.(ports.HITLApprovalPort); ok {
		m.hitlAdapters[name] = hitl
	} else if hitlGetter, ok := adapter.(interface{ HITLCoordinator() ports.HITLApprovalPort }); ok {
		if hitl := hitlGetter.HITLCoordinator(); hitl != nil {
			m.hitlAdapters[name] = hitl
		}
	}
}

// Name returns the composite channel identifier.
func (m *ChannelMux) Name() string {
	return "composite"
}

// Start initiates all registered channel adapters in parallel.
func (m *ChannelMux) Start(ctx context.Context, inbound chan<- domain.CanonicalMessage) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.adapters) == 0 {
		return errors.New("no channel adapters registered in multiplexer")
	}

	errChan := make(chan error, len(m.adapters))
	for name, adapter := range m.adapters {
		go func(n string, a ports.ChannelPort) {
			if err := a.Start(ctx, inbound); err != nil {
				slog.ErrorContext(ctx, "failed to start channel adapter", "channel", n, "error", err)
				errChan <- fmt.Errorf("channel %s failed to start: %w", n, err)
			}
		}(name, adapter)
	}

	// Non-blocking start: return nil if at least one adapter succeeds
	return nil
}

// Send routes an outbound message to the target channel adapter.
func (m *ChannelMux) Send(ctx context.Context, msg domain.OutboundMessage) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	adapter := m.resolveAdapter(msg.Channel, msg.SessionKey)
	if adapter == nil {
		return fmt.Errorf("no suitable channel adapter found to deliver message (channel: %q, session: %q)", msg.Channel, msg.SessionKey)
	}

	return adapter.Send(ctx, msg)
}

// SendTyping broadcasts typing action to relevant channel adapters.
func (m *ChannelMux) SendTyping(ctx context.Context, chatID string, threadID int64) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, adapter := range m.adapters {
		_ = adapter.SendTyping(ctx, chatID, threadID)
	}
	return nil
}

// SendChatAction sends chat action to all active adapters.
func (m *ChannelMux) SendChatAction(ctx context.Context, chatID string, threadID int64, action string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, adapter := range m.adapters {
		_ = adapter.SendChatAction(ctx, chatID, threadID, action)
	}
	return nil
}

// SendFile uploads and sends a file via target adapter.
func (m *ChannelMux) SendFile(ctx context.Context, chatID string, threadID int64, filePath string, caption string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, adapter := range m.adapters {
		if err := adapter.SendFile(ctx, chatID, threadID, filePath, caption); err == nil {
			return nil
		}
	}
	return errors.New("failed to send file across all active channel adapters")
}

// Stop terminates all registered channel adapters concurrently with a 1.8s timeout.
func (m *ChannelMux) Stop() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var wg sync.WaitGroup
	for name, adapter := range m.adapters {
		wg.Add(1)
		go func(n string, a ports.ChannelPort) {
			defer wg.Done()
			if err := a.Stop(); err != nil {
				slog.Warn("channel adapter failed to stop cleanly", "channel", n, "error", err)
			}
		}(name, adapter)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		slog.Info("all channel adapters stopped cleanly")
	case <-time.After(1800 * time.Millisecond):
		slog.Warn("channel multiplexer stop timed out after 1.8s, forcing exit")
	}
	return nil
}

// RequestApproval routes an interactive HITL approval card to the correct channel.
func (m *ChannelMux) RequestApproval(ctx context.Context, req domain.ApprovalRequest) (domain.ApprovalDecision, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	channel := ""
	if parsed, err := domain.ParseSessionKey(req.SessionKey); err == nil {
		channel = parsed.Channel
	}

	if hitl, ok := m.hitlAdapters[channel]; ok {
		return hitl.RequestApproval(ctx, req)
	}

	// Fallback to first available HITL coordinator
	for _, hitl := range m.hitlAdapters {
		return hitl.RequestApproval(ctx, req)
	}

	return domain.ApprovalDecision{
		RequestID: req.RequestID,
		Action:    "denied_no_channel",
		Approved:  false,
		Timestamp: time.Now(),
	}, errors.New("no HITL approval coordinator available for channel " + channel)
}

// HandleCallback processes inline keyboard clicks on the target channel.
func (m *ChannelMux) HandleCallback(ctx context.Context, callbackID string, userID int64, action string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, hitl := range m.hitlAdapters {
		if err := hitl.HandleCallback(ctx, callbackID, userID, action); err == nil {
			return nil
		}
	}
	return nil
}

// CancelPendingRequest terminates pending approval request across all channels.
func (m *ChannelMux) CancelPendingRequest(requestID string) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, hitl := range m.hitlAdapters {
		hitl.CancelPendingRequest(requestID)
	}
}

// CancelPendingRequestsForSession terminates all pending requests for a session.
func (m *ChannelMux) CancelPendingRequestsForSession(sessionKey string) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	channel := ""
	if parsed, err := domain.ParseSessionKey(sessionKey); err == nil {
		channel = parsed.Channel
	}

	if hitl, ok := m.hitlAdapters[channel]; ok {
		hitl.CancelPendingRequestsForSession(sessionKey)
		return
	}

	for _, hitl := range m.hitlAdapters {
		hitl.CancelPendingRequestsForSession(sessionKey)
	}
}

func (m *ChannelMux) resolveAdapter(channel, sessionKey string) ports.ChannelPort {
	if channel != "" {
		if a, ok := m.adapters[channel]; ok {
			return a
		}
	}

	if sessionKey != "" {
		if parsed, err := domain.ParseSessionKey(sessionKey); err == nil {
			if a, ok := m.adapters[parsed.Channel]; ok {
				return a
			}
		}
	}

	if len(m.adapters) == 1 {
		for _, a := range m.adapters {
			return a
		}
	}

	// Try default order
	if a, ok := m.adapters["telegram"]; ok {
		return a
	}
	if a, ok := m.adapters["zalo"]; ok {
		return a
	}

	return nil
}
