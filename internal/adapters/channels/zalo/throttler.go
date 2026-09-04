package zalo

import (
	"context"
	"sync"
	"time"
)

// Throttler enforces per-chat delivery rate limits and flushes chunks smoothly.
type Throttler struct {
	client     *Client
	minDelay   time.Duration
	lastSentMu sync.Mutex
	lastSent   map[string]time.Time
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
	}
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

	chunks := ChunkZaloMessage(req.Text)
	if len(chunks) == 0 {
		return nil
	}

	for i, chunk := range chunks {
		t.waitForSlot(ctx, req.ChatID)

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
