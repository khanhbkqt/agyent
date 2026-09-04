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

type mockAdapter struct {
	name        string
	mu          sync.Mutex
	sent        []domain.OutboundMessage
	typingCalls int
	actionCalls int
	fileCalls   int
	started     bool
	stopped     bool
	startErr    error
}

var _ ports.ChannelPort = (*mockAdapter)(nil)

func newMockAdapter(name string) *mockAdapter {
	return &mockAdapter{name: name}
}

func (m *mockAdapter) Name() string { return m.name }

func (m *mockAdapter) Start(ctx context.Context, inbound chan<- domain.CanonicalMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.startErr != nil {
		return m.startErr
	}
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

type mockHITLAdapter struct {
	*mockAdapter
	hitlCalls int
}

func (m *mockHITLAdapter) RequestApproval(ctx context.Context, req domain.ApprovalRequest) (domain.ApprovalDecision, error) {
	m.mu.Lock()
	m.hitlCalls++
	m.mu.Unlock()
	return domain.ApprovalDecision{
		RequestID: req.RequestID,
		Action:    "approved",
		Approved:  true,
		Timestamp: time.Now(),
	}, nil
}

func (m *mockHITLAdapter) HandleCallback(ctx context.Context, callbackID string, userID int64, action string) error {
	return nil
}
func (m *mockHITLAdapter) CancelPendingRequest(requestID string)             {}
func (m *mockHITLAdapter) CancelPendingRequestsForSession(sessionKey string) {}

var (
	_ ports.ChannelPort      = (*mockHITLAdapter)(nil)
	_ ports.HITLApprovalPort = (*mockHITLAdapter)(nil)
)

func TestCompositeChannelMux_RoutingAndLifecycle(t *testing.T) {
	mux := composite.NewChannelMux()
	assert.Equal(t, "composite", mux.Name())

	tg := newMockAdapter("telegram")
	zl := &mockHITLAdapter{mockAdapter: newMockAdapter("zalo")}

	mux.RegisterAdapter(tg)
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

	// 1. Send to Telegram via Channel
	err = mux.Send(ctx, domain.OutboundMessage{
		Channel: "telegram",
		ChatID:  "123",
		Text:    "Hello Telegram",
	})
	require.NoError(t, err)

	// 2. Send to Zalo via SessionKey
	err = mux.Send(ctx, domain.OutboundMessage{
		SessionKey: "zalo:group_zalo",
		ChatID:     "group_zalo",
		Text:       "Hello Zalo",
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

	// 3. SendTyping to Zalo
	err = mux.SendTyping(ctx, domain.TargetContext{
		Channel: "zalo",
		ChatID:  "group_zalo",
	})
	require.NoError(t, err)
	zl.mu.Lock()
	assert.Equal(t, 1, zl.typingCalls)
	zl.mu.Unlock()

	// 4. HITL Approval routing to Zalo
	decision, err := mux.RequestApproval(ctx, domain.ApprovalRequest{
		RequestID:  "req-1",
		SessionKey: "zalo:group_zalo",
	})
	require.NoError(t, err)
	assert.True(t, decision.Approved)
	zl.mu.Lock()
	assert.Equal(t, 1, zl.hitlCalls)
	zl.mu.Unlock()

	// 5. Stop
	err = mux.Stop()
	require.NoError(t, err)
	assert.True(t, tg.stopped)
	assert.True(t, zl.stopped)
}

func TestCompositeChannelMux_StartPartialFailure(t *testing.T) {
	mux := composite.NewChannelMux()

	tg := newMockAdapter("telegram")
	zl := newMockAdapter("zalo")
	zl.startErr = assert.AnError

	mux.Register(tg)
	mux.Register(zl)

	inbound := make(chan domain.CanonicalMessage, 5)
	err := mux.Start(context.Background(), inbound)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "channel adapter \"zalo\"")
}

func TestCompositeChannelMux_DeterministicHITLFallback(t *testing.T) {
	mux := composite.NewChannelMux()

	tg := &mockHITLAdapter{mockAdapter: newMockAdapter("telegram")}
	zl := &mockHITLAdapter{mockAdapter: newMockAdapter("zalo")}

	mux.Register(tg)
	mux.Register(zl)

	// An approval request without session key or channel should deterministically route to primary (or telegram)
	decision, err := mux.RequestApproval(context.Background(), domain.ApprovalRequest{
		RequestID: "req-fallback",
	})
	require.NoError(t, err)
	assert.True(t, decision.Approved)

	tg.mu.Lock()
	assert.Equal(t, 1, tg.hitlCalls)
	tg.mu.Unlock()

	zl.mu.Lock()
	assert.Equal(t, 0, zl.hitlCalls)
	zl.mu.Unlock()
}
