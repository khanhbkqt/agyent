package composite

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

var _ ports.ChannelPort = (*Mux)(nil)

// Mux implements ports.ChannelPort as a composite multiplexer that aggregates multiple messaging channel adapters (e.g. Telegram, Zalo, Slack).
type Mux struct {
	mu       sync.RWMutex
	adapters map[string]ports.ChannelPort
	primary  ports.ChannelPort
	running  bool
}

// NewMux constructs an empty composite channel multiplexer.
func NewMux() *Mux {
	return &Mux{
		adapters: make(map[string]ports.ChannelPort),
	}
}

// Register adds a channel adapter to the multiplexer under its identifier (e.g. "telegram").
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

// getTargetAdapter returns the resolved adapter for a specific channel name or fallback.
func (m *Mux) getTargetAdapter(channelName string) (ports.ChannelPort, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.adapters) == 0 {
		return nil, errors.New("no channel adapters registered in composite mux")
	}

	if channelName != "" {
		if ch, exists := m.adapters[channelName]; exists {
			return ch, nil
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

// Start launches all registered channel adapters using the shared inbound message channel.
func (m *Mux) Start(ctx context.Context, inbound chan<- domain.CanonicalMessage) error {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return errors.New("composite mux already running")
	}
	m.running = true
	adapters := make([]ports.ChannelPort, 0, len(m.adapters))
	for _, ch := range m.adapters {
		adapters = append(adapters, ch)
	}
	m.mu.Unlock()

	for _, ch := range adapters {
		if err := ch.Start(ctx, inbound); err != nil {
			return fmt.Errorf("failed to start channel adapter %q: %w", ch.Name(), err)
		}
	}
	return nil
}

// Send routes an outbound message to the corresponding channel adapter based on msg.Channel.
func (m *Mux) Send(ctx context.Context, msg domain.OutboundMessage) error {
	adapter, err := m.getTargetAdapter(msg.Channel)
	if err != nil {
		return err
	}
	return adapter.Send(ctx, msg)
}

// SendTyping routes typing indicators to the matching channel adapter.
func (m *Mux) SendTyping(ctx context.Context, target domain.TargetContext) error {
	adapter, err := m.getTargetAdapter(target.Channel)
	if err != nil {
		return err
	}
	return adapter.SendTyping(ctx, target)
}

// SendChatAction routes chat action indicators to the matching channel adapter.
func (m *Mux) SendChatAction(ctx context.Context, target domain.TargetContext, action string) error {
	adapter, err := m.getTargetAdapter(target.Channel)
	if err != nil {
		return err
	}
	return adapter.SendChatAction(ctx, target, action)
}

// SendFile routes outbound file attachments to the matching channel adapter.
func (m *Mux) SendFile(ctx context.Context, target domain.TargetContext, filePath string, caption string) error {
	adapter, err := m.getTargetAdapter(target.Channel)
	if err != nil {
		return err
	}
	return adapter.SendFile(ctx, target, filePath, caption)
}

// Stop gracefully shuts down all registered channel adapters.
func (m *Mux) Stop() error {
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return nil
	}
	m.running = false
	adapters := make([]ports.ChannelPort, 0, len(m.adapters))
	for _, ch := range m.adapters {
		adapters = append(adapters, ch)
	}
	m.mu.Unlock()

	var firstErr error
	for _, ch := range adapters {
		if err := ch.Stop(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
