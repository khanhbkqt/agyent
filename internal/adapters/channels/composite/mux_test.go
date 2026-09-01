package composite_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"agyent/internal/adapters/channels/composite"
	"agyent/internal/core/domain"
	"agyent/internal/core/ports"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type PureMockChannelPort struct {
	name         string
	mu           sync.Mutex
	sentMessages []domain.OutboundMessage
	stopCalled   bool
}

func NewPureMock(name string) *PureMockChannelPort {
	return &PureMockChannelPort{name: name}
}

func (m *PureMockChannelPort) Name() string {
	return m.name
}

func (m *PureMockChannelPort) Start(ctx context.Context, inbound chan<- domain.CanonicalMessage) error {
	return nil
}

func (m *PureMockChannelPort) Send(ctx context.Context, msg domain.OutboundMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sentMessages = append(m.sentMessages, msg)
	return nil
}

func (m *PureMockChannelPort) SendTyping(ctx context.Context, chatID string, threadID int64) error {
	return nil
}

func (m *PureMockChannelPort) SendChatAction(ctx context.Context, chatID string, threadID int64, action string) error {
	return nil
}

func (m *PureMockChannelPort) SendFile(ctx context.Context, chatID string, threadID int64, filePath string, caption string) error {
	return nil
}

func (m *PureMockChannelPort) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopCalled = true
	return nil
}

func TestChannelMux_PolymorphicDispatch(t *testing.T) {
	mux := composite.NewChannelMux()

	tgMock := NewPureMock("telegram")
	zaloMock := NewPureMock("zalo")

	mux.RegisterAdapter(tgMock)
	mux.RegisterAdapter(zaloMock)

	assert.Equal(t, "composite", mux.Name())

	ctx := context.Background()

	// 1. Send to Telegram by Channel field
	err := mux.Send(ctx, domain.OutboundMessage{
		Channel: "telegram",
		ChatID:  "12345",
		Text:    "Hello Telegram",
	})
	require.NoError(t, err)

	tgMock.mu.Lock()
	assert.Len(t, tgMock.sentMessages, 1)
	assert.Equal(t, "Hello Telegram", tgMock.sentMessages[0].Text)
	tgMock.mu.Unlock()

	// 2. Send to Zalo by SessionKey
	err = mux.Send(ctx, domain.OutboundMessage{
		SessionKey: "zalo:group_zalo",
		ChatID:     "group_zalo",
		Text:       "Hello Zalo",
	})
	require.NoError(t, err)

	zaloMock.mu.Lock()
	assert.Len(t, zaloMock.sentMessages, 1)
	assert.Equal(t, "Hello Zalo", zaloMock.sentMessages[0].Text)
	zaloMock.mu.Unlock()

	// 3. Concurrent graceful shutdown within 1.8s
	start := time.Now()
	err = mux.Stop()
	require.NoError(t, err)
	assert.True(t, time.Since(start) < 1800*time.Millisecond)

	tgMock.mu.Lock()
	assert.True(t, tgMock.stopCalled)
	tgMock.mu.Unlock()

	zaloMock.mu.Lock()
	assert.True(t, zaloMock.stopCalled)
	zaloMock.mu.Unlock()
}

// Compile-time check that ChannelMux implements ChannelPort
var _ ports.ChannelPort = (*composite.ChannelMux)(nil)
