package zalo_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"agyent/internal/adapters/channels/zalo"
	"agyent/internal/config"
	"agyent/internal/core/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestZaloAdapter_FullLifecycleWithMockServer(t *testing.T) {
	var mu sync.Mutex
	var receivedSendReq zalo.SendMessageRequest
	var receivedActionReq zalo.SendChatActionRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Path == "/botmock_token/getMe" {
			_ = json.NewEncoder(w).Encode(zalo.APIResponse{
				OK:     true,
				Result: json.RawMessage(`{"id": "bot_123", "name": "Agyent Bot", "is_bot": true}`),
			})
			return
		}

		if r.URL.Path == "/botmock_token/sendMessage" {
			mu.Lock()
			_ = json.NewDecoder(r.Body).Decode(&receivedSendReq)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(zalo.APIResponse{
				OK:     true,
				Result: json.RawMessage(`{"message_id": "msg_999", "date": 1724947200, "text": "ok"}`),
			})
			return
		}

		if r.URL.Path == "/botmock_token/sendChatAction" {
			mu.Lock()
			_ = json.NewDecoder(r.Body).Decode(&receivedActionReq)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(zalo.APIResponse{
				OK: true,
			})
			return
		}

		if r.URL.Path == "/botmock_token/getUpdates" {
			_ = json.NewEncoder(w).Encode(zalo.APIResponse{
				OK: true,
				Result: json.RawMessage(`[
					{
						"update_id": 1,
						"message": {
							"message_id": "in_msg_1",
							"from": {"id": "user_456", "name": "Test User"},
							"chat": {"id": "group_789", "type": "group"},
							"date": 1724947200,
							"text": "/help"
						}
					}
				]`),
			})
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	cfg := config.DefaultConfig()
	cfg.Zalo.BotToken = "mock_token"
	cfg.Zalo.APIURL = server.URL
	cfg.Zalo.Mode = "polling"
	cfg.Storage.AgentsDir = t.TempDir()

	adapter, err := zalo.NewAdapter(cfg, nil)
	require.NoError(t, err)
	assert.Equal(t, "zalo", adapter.Name())

	inboundChan := make(chan domain.CanonicalMessage, 10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = adapter.Start(ctx, inboundChan)
	require.NoError(t, err)

	// Wait for inbound update from mock getUpdates
	select {
	case msg := <-inboundChan:
		assert.Equal(t, "in_msg_1", msg.ID)
		assert.Equal(t, "zalo", msg.Channel)
		assert.Equal(t, "user_456", msg.Sender.ID)
		assert.Equal(t, "group_789", msg.Chat.ID)
		assert.Equal(t, "/help", msg.Text)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for inbound message")
	}

	// Test Send Outbound Message with HTML conversion
	outbound := domain.OutboundMessage{
		ChatID:    "group_789",
		Text:      "<b>Thông báo:</b> Đã xử lý lệnh <code>/help</code> thành công.",
		ParseMode: "HTML",
	}
	err = adapter.Send(ctx, outbound)
	require.NoError(t, err)

	mu.Lock()
	assert.Equal(t, "group_789", receivedSendReq.ChatID)
	assert.Contains(t, receivedSendReq.Text, "**Thông báo:** Đã xử lý lệnh `/help` thành công.")
	mu.Unlock()

	// Test Send Typing
	err = adapter.SendTyping(ctx, domain.TargetContext{Channel: "zalo", ChatID: "group_789"})
	require.NoError(t, err)

	mu.Lock()
	assert.Equal(t, "group_789", receivedActionReq.ChatID)
	assert.Equal(t, "typing", receivedActionReq.Action)
	mu.Unlock()

	// Test Stop
	err = adapter.Stop()
	require.NoError(t, err)
}

func TestZaloAdapter_MultiBotRouting(t *testing.T) {
	var mu sync.Mutex
	calls := make(map[string]int)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		calls[r.URL.Path]++
		mu.Unlock()

		if r.URL.Path == "/bottoken_primary/getMe" {
			_ = json.NewEncoder(w).Encode(zalo.APIResponse{
				OK:     true,
				Result: json.RawMessage(`{"id": "bot_primary_id", "name": "Primary Bot"}`),
			})
			return
		}
		if r.URL.Path == "/bottoken_secondary/getMe" {
			_ = json.NewEncoder(w).Encode(zalo.APIResponse{
				OK:     true,
				Result: json.RawMessage(`{"id": "bot_secondary_id", "name": "Coder Bot"}`),
			})
			return
		}
		if r.URL.Path == "/bottoken_primary/sendMessage" || r.URL.Path == "/bottoken_secondary/sendMessage" {
			_ = json.NewEncoder(w).Encode(zalo.APIResponse{OK: true})
			return
		}
		if r.URL.Path == "/bottoken_primary/getUpdates" || r.URL.Path == "/bottoken_secondary/getUpdates" {
			_ = json.NewEncoder(w).Encode(zalo.APIResponse{OK: true, Result: json.RawMessage(`[]`)})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	cfg := config.DefaultConfig()
	cfg.Zalo.APIURL = server.URL
	cfg.Zalo.Bots = []config.BotConfig{
		{Name: "primary", BotToken: "token_primary"},
		{Name: "coder", BotToken: "token_secondary", BindAgent: "deep_coder"},
	}
	cfg.Storage.AgentsDir = t.TempDir()

	adapter, err := zalo.NewAdapter(cfg, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	inboundChan := make(chan domain.CanonicalMessage, 10)
	err = adapter.Start(ctx, inboundChan)
	require.NoError(t, err)
	defer func() { _ = adapter.Stop() }()

	// Send message targeting the coder bot by name
	err = adapter.Send(ctx, domain.OutboundMessage{
		BotIDStr: "coder",
		ChatID:   "chat_123",
		Text:     "Message from coder bot",
	})
	require.NoError(t, err)

	// Send message targeting primary bot
	err = adapter.Send(ctx, domain.OutboundMessage{
		BotIDStr: "primary",
		ChatID:   "chat_123",
		Text:     "Message from primary bot",
	})
	require.NoError(t, err)

	mu.Lock()
	assert.True(t, calls["/bottoken_secondary/sendMessage"] >= 1)
	assert.True(t, calls["/bottoken_primary/sendMessage"] >= 1)
	mu.Unlock()
}

func TestZaloAdapter_WebhookMode(t *testing.T) {
	mockZaloServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/bottoken_webhook/setWebhook" {
			_ = json.NewEncoder(w).Encode(zalo.APIResponse{OK: true})
			return
		}
		if r.URL.Path == "/bottoken_webhook/deleteWebhook" {
			_ = json.NewEncoder(w).Encode(zalo.APIResponse{OK: true})
			return
		}
		http.NotFound(w, r)
	}))
	defer mockZaloServer.Close()

	cfg := config.DefaultConfig()
	cfg.Zalo.BotToken = "token_webhook"
	cfg.Zalo.APIURL = mockZaloServer.URL
	cfg.Zalo.Mode = "webhook"
	cfg.Zalo.WebhookURL = "https://example.com/zalo/webhook"
	cfg.Zalo.SecretToken = "my_secret_token_xyz"
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.Port = 49152 // Dynamic non-conflicting port
	cfg.Storage.AgentsDir = t.TempDir()

	adapter, err := zalo.NewAdapter(cfg, nil)
	require.NoError(t, err)

	inboundChan := make(chan domain.CanonicalMessage, 10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = adapter.Start(ctx, inboundChan)
	require.NoError(t, err)
	defer func() { _ = adapter.Stop() }()

	// Wait for server to start listening
	time.Sleep(50 * time.Millisecond)

	webhookURL := "http://127.0.0.1:49152/zalo/webhook"

	// 1. Post with invalid secret -> Expect 401
	payload := `{"update_id": 1, "message": {"message_id": "wh_1", "from": {"id": "u1"}, "chat": {"id": "c1"}, "text": "test"}}`
	req, _ := http.NewRequest(http.MethodPost, webhookURL, bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Secret-Token", "wrong_secret")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp.Body.Close()

	// 2. Post with valid secret -> Expect 200 OK and message delivered to inboundChan
	req, _ = http.NewRequest(http.MethodPost, webhookURL, bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Secret-Token", "my_secret_token_xyz")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	select {
	case msg := <-inboundChan:
		assert.Equal(t, "wh_1", msg.ID)
		assert.Equal(t, "c1", msg.Chat.ID)
		assert.Equal(t, "test", msg.Text)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for webhook update to reach inbound channel")
	}

	// 3. Post oversized payload (> 5MB) -> Expect 400 Bad Request due to MaxBytesReader
	largePayload := bytes.Repeat([]byte("a"), 6*1024*1024)
	req, _ = http.NewRequest(http.MethodPost, webhookURL, bytes.NewReader(largePayload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Secret-Token", "my_secret_token_xyz")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()
}

func TestZaloAdapter_HITLCoordination(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Zalo.BotToken = "mock_hitl_token"
	cfg.Zalo.AdminUserIDs = []string{"admin_007"}
	cfg.Storage.AgentsDir = t.TempDir()

	adapter, err := zalo.NewAdapter(cfg, nil)
	require.NoError(t, err)

	req := domain.ApprovalRequest{
		RequestID:  "hitl_test_req",
		SessionKey: "zalo:chat_100",
		ToolName:   "write_to_file",
		ExpiresAt:  time.Now().Add(5 * time.Second),
	}

	done := make(chan domain.ApprovalDecision, 1)
	go func() {
		dec, _ := adapter.RequestApproval(context.Background(), req)
		done <- dec
	}()

	time.Sleep(50 * time.Millisecond)

	// Admin executes callback or command approval
	err = adapter.HandleCallback(context.Background(), "cb_1", 7, "hitl:hitl_test_req:allow_once")
	// userID 7 does not match string "admin_007"
	assert.Error(t, err)

	// Admin executes command with matching string
	err = adapter.HITLCoordinator().HandleCommandApproval(context.Background(), "hitl_test_req", "admin_007", "allow_once")
	require.NoError(t, err)

	select {
	case dec := <-done:
		assert.Equal(t, "hitl_test_req", dec.RequestID)
		assert.True(t, dec.Approved)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for HITL approval via adapter")
	}
}

func TestZaloAdapter_MultiBotRouting_AgentNameAndSessionKey(t *testing.T) {
	var mu sync.Mutex
	calls := make(map[string]int)
	var lastReceivedText string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		calls[r.URL.Path]++
		if strings.HasSuffix(r.URL.Path, "/sendMessage") {
			var body zalo.SendMessageRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			lastReceivedText = body.Text
		}
		mu.Unlock()

		_ = json.NewEncoder(w).Encode(zalo.APIResponse{OK: true})
	}))
	defer server.Close()

	cfg := config.DefaultConfig()
	cfg.Zalo.APIURL = server.URL
	cfg.Zalo.Bots = []config.BotConfig{
		{Name: "default", BotToken: "token_default"},
		{Name: "traomo_bot", BotToken: "4293721026991223652:secret_traomo", BindAgent: "traomofc"},
	}
	cfg.Storage.AgentsDir = t.TempDir()

	adapter, err := zalo.NewAdapter(cfg, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// 1. Route by AgentName: "traomofc"
	err = adapter.Send(ctx, domain.OutboundMessage{
		AgentName: "traomofc",
		ChatID:    "zgr-test",
		Text:      "Hello from traomofc agent",
	})
	require.NoError(t, err)

	// 2. Route by SessionKey: "zalo:4293721026991223652:zgr-test"
	err = adapter.Send(ctx, domain.OutboundMessage{
		SessionKey: "zalo:4293721026991223652:zgr-test",
		ChatID:     "zgr-test",
		Text:       "Hello from numeric session key",
	})
	require.NoError(t, err)

	// 3. Route by BotID: 4293721026991223652
	err = adapter.Send(ctx, domain.OutboundMessage{
		BotID:  4293721026991223652,
		ChatID: "zgr-test",
		Text:   "Hello from numeric bot ID",
	})
	require.NoError(t, err)

	// 4. Fallback when unknown bot is specified: should route to default without error
	err = adapter.Send(ctx, domain.OutboundMessage{
		BotIDStr: "unknown_bot",
		ChatID:   "zgr-test",
		Text:     "Hello fallback",
	})
	require.NoError(t, err)

	// 5. Verify format conversion on outbound message
	err = adapter.Send(ctx, domain.OutboundMessage{
		AgentName: "traomofc",
		ChatID:    "zgr-test",
		Text:      "**TraoMo FC**\n- Cầu thủ A\n*italic*\n[Traomo Web](https://traomofc.thevibecoding.dev)\n---",
	})
	require.NoError(t, err)

	mu.Lock()
	assert.Equal(t, 4, calls["/bot4293721026991223652:secret_traomo/sendMessage"], "expected 4 messages to traomo_bot")
	assert.Equal(t, 1, calls["/bottoken_default/sendMessage"], "expected 1 message to fallback default bot")
	assert.Contains(t, lastReceivedText, "• Cầu thủ A")
	assert.Contains(t, lastReceivedText, "_italic_")
	assert.Contains(t, lastReceivedText, "Traomo Web (https://traomofc.thevibecoding.dev)")
	assert.Contains(t, lastReceivedText, "────────────────────────")
	mu.Unlock()
}
