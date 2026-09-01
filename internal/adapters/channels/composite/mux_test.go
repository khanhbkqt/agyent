package composite

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

type mockAdapter struct {
	name        string
	mu          sync.Mutex
	sent        []domain.OutboundMessage
	typingCalls int
	actionCalls int
	fileCalls   int
	started     bool
	stopped     bool
}

var _ ports.ChannelPort = (*mockAdapter)(nil)

func newMockAdapter(name string) *mockAdapter {
	return &mockAdapter{name: name}
}

func (m *mockAdapter) Name() string { return m.name }

func (m *mockAdapter) Start(ctx context.Context, inbound chan<- domain.CanonicalMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started = true
	return nil
}

func (m *mockAdapter) Send(ctx context.Context, msg domain.OutboundMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return nil
}

func (m *mockAdapter) SendTyping(ctx context.Context, target domain.TargetContext) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.typingCalls++
	return nil
}

func (m *mockAdapter) SendChatAction(ctx context.Context, target domain.TargetContext, action string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.actionCalls++
	return nil
}

func (m *mockAdapter) SendFile(ctx context.Context, target domain.TargetContext, filePath string, caption string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fileCalls++
	return nil
}

func (m *mockAdapter) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopped = true
	return nil
}

func TestCompositeChannelMux_RoutingAndLifecycle(t *testing.T) {
	mux := NewMux()
	assert.Equal(t, "composite", mux.Name())

	tg := newMockAdapter("telegram")
	zl := newMockAdapter("zalo")

	mux.Register(tg)
	mux.Register(zl)

	gotTg, ok := mux.Get("telegram")
	assert.True(t, ok)
	assert.Equal(t, "telegram", gotTg.Name())

	inbound := make(chan domain.CanonicalMessage, 10)
	ctx := context.Background()

	err := mux.Start(ctx, inbound)
	require.NoError(t, err)
	assert.True(t, tg.started)
	assert.True(t, zl.started)

	// Send to Telegram
	err = mux.Send(ctx, domain.OutboundMessage{
		Channel: "telegram",
		ChatID:  "123",
		Text:    "Hello Telegram",
	})
	require.NoError(t, err)

	// Send to Zalo
	err = mux.Send(ctx, domain.OutboundMessage{
		Channel: "zalo",
		ChatID:  "456",
		Text:    "Hello Zalo",
	})
	require.NoError(t, err)

	tg.mu.Lock()
	require.Len(t, tg.sent, 1)
	assert.Equal(t, "Hello Telegram", tg.sent[0].Text)
	tg.mu.Unlock()

	zl.mu.Lock()
	require.Len(t, zl.sent, 1)
	assert.Equal(t, "Hello Zalo", zl.sent[0].Text)
	zl.mu.Unlock()

	// SendTyping to Zalo
	err = mux.SendTyping(ctx, domain.TargetContext{
		Channel: "zalo",
		ChatID:  "456",
	})
	require.NoError(t, err)
	zl.mu.Lock()
	assert.Equal(t, 1, zl.typingCalls)
	zl.mu.Unlock()

	// Stop
	err = mux.Stop()
	require.NoError(t, err)
	assert.True(t, tg.stopped)
	assert.True(t, zl.stopped)
}
