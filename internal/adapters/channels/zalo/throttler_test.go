package zalo_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

type mockZaloSanitizer struct{}

func (m *mockZaloSanitizer) RedactSecrets(text string) string {
	if strings.Contains(text, "sk-ant-secret1234567890123456") {
		return strings.ReplaceAll(text, "sk-ant-secret1234567890123456", "[REDACTED_SECRET]")
	}
	return text
}

func TestThrottler_SendThrottled_SecretRedaction(t *testing.T) {
	var sentPayload string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sentPayload = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error": 0, "message": "Success", "data": {"message_id": "msg_001"}}`))
	}))
	defer ts.Close()

	client := zalo.NewClient("test-token", ts.URL, ts.Client())
	throttler := zalo.NewThrottler(client, 5*time.Millisecond)
	throttler.SetOutboundSanitizer(&mockZaloSanitizer{})

	secret := "sk-ant-secret1234567890123456"
	err := throttler.SendThrottled(context.Background(), zalo.SendMessageRequest{
		ChatID: "user_123",
		Text:   "Here is your secret: " + secret,
	})
	assert.NoError(t, err)
	assert.NotContains(t, sentPayload, secret, "Secret must be redacted from Zalo outbound message")
	assert.Contains(t, sentPayload, "[REDACTED_SECRET]", "Redaction token must be present in Zalo outbound message")
}

