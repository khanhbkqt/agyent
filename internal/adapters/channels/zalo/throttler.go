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
func (t *Throttler) SendThrottled(ctx context.Context, req SendMessageRequest) error {
	if t.client == nil {
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

		_, err := t.client.SendMessage(ctx, chunkReq)
		if err != nil {
			return err
		}
	}
	return nil
}

func (t *Throttler) waitForSlot(ctx context.Context, chatID string) {
	t.lastSentMu.Lock()
	last, ok := t.lastSent[chatID]
	now := time.Now()
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
