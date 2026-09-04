package zalo_test

import (
	"context"
	"testing"
	"time"

	"agyent/internal/adapters/channels/zalo"

	"github.com/stretchr/testify/assert"
)

func TestThrottler_Prune(t *testing.T) {
	// Dummy client with null HTTP client isn't called if we don't mock it, or we can use mock client
	throttler := zalo.NewThrottler(nil, 5*time.Millisecond)

	// Direct call via SendThrottled won't execute if client is nil (line 35: if client == nil return nil)
	// So let's provide a dummy client with mock or test Prune directly
	client := zalo.NewClient("dummy_token", "")
	throttler = zalo.NewThrottler(client, 5*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// This might fail to send over network, but waitForSlot executes before SendMessage
	_ = throttler.SendThrottled(ctx, zalo.SendMessageRequest{ChatID: "chat_1", Text: "msg 1"})
	_ = throttler.SendThrottled(ctx, zalo.SendMessageRequest{ChatID: "chat_2", Text: "msg 2"})

	// Recent entries shouldn't be pruned with 1 hour maxAge
	pruned := throttler.Prune(1 * time.Hour)
	assert.Equal(t, 0, pruned)

	// Wait for entries to age past 20ms
	time.Sleep(25 * time.Millisecond)
	pruned = throttler.Prune(10 * time.Millisecond)
	assert.Equal(t, 2, pruned)
}
