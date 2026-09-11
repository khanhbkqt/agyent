package zalo

import (
	"context"
	"strings"
	"sync"
	"time"

	"agyent/internal/core/ports"
)

// Throttler enforces per-chat delivery rate limits and flushes chunks smoothly.
type Throttler struct {
	client     *Client
	minDelay   time.Duration
	sanitizer  ports.OutboundSanitizer
	mu         sync.RWMutex
	lastSentMu sync.Mutex
	lastSent   map[string]time.Time
	terminalMu sync.Mutex
	terminal   map[string]time.Time
}

// NewThrottler creates a new outbound message throttler for Zalo.
func NewThrottler(client *Client, minDelay time.Duration) *Throttler {
	if minDelay <= 0 {
		minDelay = 500 * time.Millisecond
	}
	return &Throttler{
		client:   client,
		minDelay: minDelay,
		lastSent: make(map[string]time.Time),
		terminal: make(map[string]time.Time),
	}
}

// SetOutboundSanitizer sets the central outbound DLP sanitizer for Zalo Throttler.
func (t *Throttler) SetOutboundSanitizer(sanitizer ports.OutboundSanitizer) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sanitizer = sanitizer
}

// redact applies secret masking and DLP filtering with lookback buffer.
func (t *Throttler) redact(text string, lookback ...string) string {
	if t == nil || text == "" {
		return text
	}
	t.mu.RLock()
	s := t.sanitizer
	t.mu.RUnlock()
	if s == nil {
		return text
	}

	lb := ""
	if len(lookback) > 0 {
		lb = lookback[0]
	}

	if lb == "" {
		return s.RedactSecrets(text)
	}

	combined := lb + text
	redactedCombined := s.RedactSecrets(combined)

	redactedLB := s.RedactSecrets(lb)
	if strings.HasPrefix(redactedCombined, redactedLB) {
		return redactedCombined[len(redactedLB):]
	}

	idx := 0
	for idx < len(lb) && idx < len(redactedCombined) && lb[idx] == redactedCombined[idx] {
		idx++
	}
	return redactedCombined[idx:]
}

const terminalEventRetention = 30 * time.Minute

func (t *Throttler) acceptTerminalEvent(sessionKey, turnID string) bool {
	if sessionKey == "" || turnID == "" {
		return true
	}
	now := time.Now()
	key := sessionKey + "\x00" + turnID
	t.terminalMu.Lock()
	defer t.terminalMu.Unlock()
	for existingKey, seenAt := range t.terminal {
		if now.Sub(seenAt) > terminalEventRetention {
			delete(t.terminal, existingKey)
		}
	}
	if _, exists := t.terminal[key]; exists {
		return false
	}
	t.terminal[key] = now
	return true
}

// SendThrottled sends a message with per-chat interval throttling and automatic chunking.
func (t *Throttler) SendThrottled(ctx context.Context, req SendMessageRequest, clientOpt ...*Client) error {
	client := t.client
	if len(clientOpt) > 0 && clientOpt[0] != nil {
		client = clientOpt[0]
	}
	if client == nil {
		return nil
	}

	fullText := t.redact(req.Text)
	chunks := ChunkZaloMessage(fullText)
	if len(chunks) == 0 {
		return nil
	}

	lookback := ""
	for i, chunk := range chunks {
		t.waitForSlot(ctx, req.ChatID)

		chunk = t.redact(chunk, lookback)
		if len(chunk) > 64 {
			lookback = chunk[len(chunk)-64:]
		} else {
			lookback = chunk
		}

		chunkReq := req
		chunkReq.Text = chunk
		// Only attach reply ID to first chunk
		if i > 0 {
			chunkReq.ReplyToID = ""
		}

		_, err := client.SendMessage(ctx, chunkReq)
		if err != nil {
			return err
		}
	}
	return nil
}

// Prune removes entries from lastSent that are older than maxAge.
func (t *Throttler) Prune(maxAge time.Duration) int {
	t.lastSentMu.Lock()
	defer t.lastSentMu.Unlock()
	return t.pruneLocked(time.Now(), maxAge)
}

func (t *Throttler) pruneLocked(now time.Time, maxAge time.Duration) int {
	cutoff := now.Add(-maxAge)
	pruned := 0
	for id, ts := range t.lastSent {
		if ts.Before(cutoff) {
			delete(t.lastSent, id)
			pruned++
		}
	}
	return pruned
}

func (t *Throttler) waitForSlot(ctx context.Context, chatID string) {
	t.lastSentMu.Lock()
	now := time.Now()

	// Evict stale entries if map size grows beyond threshold to prevent unbounded memory growth
	if len(t.lastSent) > 256 {
		t.pruneLocked(now, 10*time.Minute)
	}

	last, ok := t.lastSent[chatID]
	var wait time.Duration
	if ok {
		elapsed := now.Sub(last)
		if elapsed < t.minDelay {
			wait = t.minDelay - elapsed
		}
	}
	t.lastSent[chatID] = now.Add(wait)
	t.lastSentMu.Unlock()

	if wait > 0 {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
		}
	}
}
