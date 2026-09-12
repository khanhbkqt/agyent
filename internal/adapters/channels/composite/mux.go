package composite

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

var (
	_ ports.ChannelPort           = (*Mux)(nil)
	_ ports.HITLApprovalPort      = (*Mux)(nil)
	_ ports.AttachmentFetcherPort = (*Mux)(nil)
)

type inboundAuthorizerSetter interface {
	SetInboundAuthorizer(ports.InboundAuthorizer)
}

type urlSafetyEvaluatorSetter interface {
	SetURLSafetyEvaluator(ports.URLSafetyEvaluator)
}

type outboundSanitizerSetter interface {
	SetOutboundSanitizer(ports.OutboundSanitizer)
}

// Mux aggregates registered channel adapters while preserving their provider
// boundaries for inbound authorization, media, and HITL approvals.
type Mux struct {
	mu            sync.RWMutex
	adapters      map[string]ports.ChannelPort
	hitlAdapters  map[string]ports.HITLApprovalPort
	mediaAdapters map[string]ports.AttachmentFetcherPort
	primary       string
	running       bool
}

// ChannelMux remains an alias for configuration and caller compatibility.
type ChannelMux = Mux

// NewMux constructs an empty composite channel multiplexer.
func NewMux() *Mux {
	return &Mux{
		adapters:      make(map[string]ports.ChannelPort),
		hitlAdapters:  make(map[string]ports.HITLApprovalPort),
		mediaAdapters: make(map[string]ports.AttachmentFetcherPort),
	}
}

// NewChannelMux creates an empty channel multiplexer.
func NewChannelMux() *Mux { return NewMux() }

// Register adds an adapter before startup. A nil or unnamed adapter is ignored.
func (m *Mux) Register(adapter ports.ChannelPort) {
	if adapter == nil {
		return
	}
	name := strings.ToLower(strings.TrimSpace(adapter.Name()))
	if name == "" || name == "composite" {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.adapters[name] = adapter
	if m.primary == "" {
		m.primary = name
	}
	if hitl, ok := adapter.(ports.HITLApprovalPort); ok {
		m.hitlAdapters[name] = hitl
	}
	if media, ok := adapter.(ports.AttachmentFetcherPort); ok {
		m.mediaAdapters[name] = media
	}
}

// RegisterAdapter is retained for callers from the pre-standardization branch.
func (m *Mux) RegisterAdapter(adapter ports.ChannelPort) { m.Register(adapter) }

// Get retrieves a registered adapter by its canonical channel name.
func (m *Mux) Get(name string) (ports.ChannelPort, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	adapter, ok := m.adapters[strings.ToLower(strings.TrimSpace(name))]
	return adapter, ok
}

// Name returns the composite channel identifier.
func (m *Mux) Name() string { return "composite" }

type namedAdapter struct {
	name    string
	adapter ports.ChannelPort
}

func (m *Mux) adapterSnapshot() []namedAdapter {
	m.mu.RLock()
	defer m.mu.RUnlock()
	adapters := make([]namedAdapter, 0, len(m.adapters))
	for name, adapter := range m.adapters {
		adapters = append(adapters, namedAdapter{name: name, adapter: adapter})
	}
	sort.Slice(adapters, func(i, j int) bool { return adapters[i].name < adapters[j].name })
	return adapters
}

func (m *Mux) resolveAdapter(channel, sessionKey string) (ports.ChannelPort, error) {
	channel = strings.ToLower(strings.TrimSpace(channel))
	if channel == "" && sessionKey != "" {
		if parsed, err := domain.ParseSessionKey(sessionKey); err == nil {
			channel = strings.ToLower(strings.TrimSpace(parsed.Channel))
		}
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.adapters) == 0 {
		return nil, errors.New("no channel adapters registered in composite mux")
	}
	if channel != "" {
		if adapter, ok := m.adapters[channel]; ok {
			return adapter, nil
		}
		return nil, fmt.Errorf("channel adapter %q is not registered", channel)
	}
	if m.primary != "" {
		if adapter, ok := m.adapters[m.primary]; ok {
			return adapter, nil
		}
	}
	return nil, errors.New("no primary channel adapter is registered")
}

// SetInboundAuthorizer applies the core admission policy to every adapter that
// supports ingress. It never invokes adapter code while the mux lock is held.
func (m *Mux) SetInboundAuthorizer(authorizer ports.InboundAuthorizer) {
	for _, named := range m.adapterSnapshot() {
		if setter, ok := named.adapter.(inboundAuthorizerSetter); ok {
			setter.SetInboundAuthorizer(authorizer)
		}
	}
}

// SetURLSafetyEvaluator applies the central network policy to adapters that
// materialize provider-supplied media URLs.
func (m *Mux) SetURLSafetyEvaluator(evaluator ports.URLSafetyEvaluator) {
	for _, named := range m.adapterSnapshot() {
		if setter, ok := named.adapter.(urlSafetyEvaluatorSetter); ok {
			setter.SetURLSafetyEvaluator(evaluator)
		}
	}
}

// SetOutboundSanitizer applies the central secret masking and DLP policy to adapters.
func (m *Mux) SetOutboundSanitizer(sanitizer ports.OutboundSanitizer) {
	for _, named := range m.adapterSnapshot() {
		if setter, ok := named.adapter.(outboundSanitizerSetter); ok {
			setter.SetOutboundSanitizer(sanitizer)
		}
	}
}

// Start launches adapters in a deterministic order. If any adapter fails to
// start, that adapter and every adapter already started in this attempt are
// stopped before returning. An adapter Start implementation may allocate a
// listener or goroutine before reporting its error.
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
	m.mu.Unlock()

	adapters := m.adapterSnapshot()
	started := make([]namedAdapter, 0, len(adapters))
	for _, named := range adapters {
		if err := named.adapter.Start(ctx, inbound); err != nil {
			if stopErr := named.adapter.Stop(); stopErr != nil {
				slog.WarnContext(ctx, "failed to stop channel after startup failure", "channel", named.name, "error", stopErr)
			}
			for i := len(started) - 1; i >= 0; i-- {
				if stopErr := started[i].adapter.Stop(); stopErr != nil {
					slog.WarnContext(ctx, "failed to stop channel after startup failure", "channel", started[i].name, "error", stopErr)
				}
			}
			m.mu.Lock()
			m.running = false
			m.mu.Unlock()
			return fmt.Errorf("start channel adapter %q: %w", named.name, err)
		}
		started = append(started, named)
	}
	return nil
}

// Send routes outbound text and attachments using explicit channel identity or
// the session key. Empty routing fields retain the configured primary fallback.
func (m *Mux) Send(ctx context.Context, msg domain.OutboundMessage) error {
	adapter, err := m.resolveAdapter(msg.Channel, msg.SessionKey)
	if err != nil {
		return err
	}
	return adapter.Send(ctx, msg)
}

func (m *Mux) SendTyping(ctx context.Context, target domain.TargetContext) error {
	adapter, err := m.resolveAdapter(target.Channel, "")
	if err != nil {
		return err
	}
	return adapter.SendTyping(ctx, target)
}

func (m *Mux) SendChatAction(ctx context.Context, target domain.TargetContext, action string) error {
	adapter, err := m.resolveAdapter(target.Channel, "")
	if err != nil {
		return err
	}
	return adapter.SendChatAction(ctx, target, action)
}

func (m *Mux) SendFile(ctx context.Context, target domain.TargetContext, filePath string, caption string) error {
	adapter, err := m.resolveAdapter(target.Channel, "")
	if err != nil {
		return err
	}
	return adapter.SendFile(ctx, target, filePath, caption)
}

// Stop stops all registered adapters without holding the mux lock across I/O.
func (m *Mux) Stop() error {
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return nil
	}
	m.running = false
	m.mu.Unlock()

	adapters := m.adapterSnapshot()
	var wg sync.WaitGroup
	errCh := make(chan error, len(adapters))
	for _, named := range adapters {
		wg.Add(1)
		go func(named namedAdapter) {
			defer wg.Done()
			if err := named.adapter.Stop(); err != nil {
				errCh <- fmt.Errorf("stop channel adapter %q: %w", named.name, err)
			}
		}(named)
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		close(errCh)
		var errs []error
		for err := range errCh {
			errs = append(errs, err)
		}
		return errors.Join(errs...)
	case <-time.After(2 * time.Second):
		return errors.New("timed out stopping channel adapters")
	}
}

func (m *Mux) hitlForSession(sessionKey string) (ports.HITLApprovalPort, string, error) {
	parsed, err := domain.ParseSessionKey(sessionKey)
	if err != nil || parsed.Channel == "" {
		return nil, "", fmt.Errorf("invalid HITL session key %q", sessionKey)
	}
	channel := strings.ToLower(strings.TrimSpace(parsed.Channel))
	m.mu.RLock()
	hitl, ok := m.hitlAdapters[channel]
	m.mu.RUnlock()
	if !ok {
		return nil, channel, fmt.Errorf("no HITL adapter registered for channel %q", channel)
	}
	return hitl, channel, nil
}

// RequestApproval is deliberately strict: security approvals never fall back
// to another provider when their session routing is absent or invalid.
func (m *Mux) RequestApproval(ctx context.Context, req domain.ApprovalRequest) (domain.ApprovalDecision, error) {
	hitl, channel, err := m.hitlForSession(req.SessionKey)
	if err != nil {
		return domain.ApprovalDecision{RequestID: req.RequestID, Action: "denied_no_channel", Approved: false, Timestamp: time.Now()}, err
	}
	decision, err := hitl.RequestApproval(ctx, req)
	if err != nil {
		return decision, fmt.Errorf("request HITL approval through %s: %w", channel, err)
	}
	return decision, nil
}

// HandleCallback is retained for providers whose callback ingress is wired at
// the mux level. Adapter-specific ingress normally handles its own callbacks.
func (m *Mux) HandleCallback(ctx context.Context, callbackID string, userID int64, action string) error {
	m.mu.RLock()
	hitls := make([]ports.HITLApprovalPort, 0, len(m.hitlAdapters))
	for _, hitl := range m.hitlAdapters {
		hitls = append(hitls, hitl)
	}
	m.mu.RUnlock()
	var errs []error
	for _, hitl := range hitls {
		if err := hitl.HandleCallback(ctx, callbackID, userID, action); err == nil {
			return nil
		} else {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m *Mux) CancelPendingRequest(requestID string) {
	m.mu.RLock()
	hitls := make([]ports.HITLApprovalPort, 0, len(m.hitlAdapters))
	for _, hitl := range m.hitlAdapters {
		hitls = append(hitls, hitl)
	}
	m.mu.RUnlock()
	for _, hitl := range hitls {
		hitl.CancelPendingRequest(requestID)
	}
}

func (m *Mux) CancelPendingRequestsForSession(sessionKey string) {
	hitl, _, err := m.hitlForSession(sessionKey)
	if err != nil {
		return
	}
	hitl.CancelPendingRequestsForSession(sessionKey)
}

// FetchAttachment routes lazy attachment materialization to its owning provider.
func (m *Mux) FetchAttachment(ctx context.Context, ref domain.InboundAttachmentRef, targetDir string) (domain.Attachment, error) {
	channel := strings.ToLower(strings.TrimSpace(ref.Channel))
	if channel == "" {
		return domain.Attachment{}, fmt.Errorf("%w: attachment channel is required", ports.ErrAttachmentNotFound)
	}
	m.mu.RLock()
	fetcher, ok := m.mediaAdapters[channel]
	m.mu.RUnlock()
	if !ok {
		return domain.Attachment{}, fmt.Errorf("%w: no attachment fetcher for channel %q", ports.ErrAttachmentNotFound, channel)
	}
	return fetcher.FetchAttachment(ctx, ref, targetDir)
}
