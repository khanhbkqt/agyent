package zalo_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agyent/internal/adapters/channels/zalo"
	"agyent/internal/config"
	"agyent/internal/core/domain"
	"agyent/internal/core/eventbus"

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

func TestZaloAdapter_Polling_408TimeoutDoesNotBackoff(t *testing.T) {
	var pollAttempts atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Path == "/botpoll_token/getMe" {
			_ = json.NewEncoder(w).Encode(zalo.APIResponse{
				OK:     true,
				Result: json.RawMessage(`{"id": "bot_poll", "name": "Poll Bot", "is_bot": true}`),
			})
			return
		}

		if r.URL.Path == "/botpoll_token/getUpdates" {
			count := pollAttempts.Add(1)
			if count == 1 {
				// 1st attempt: HTTP 408
				w.WriteHeader(http.StatusRequestTimeout)
				_, _ = w.Write([]byte(`{"error_code": 408, "description": "Request timeout"}`))
				return
			}
			if count == 2 {
				// 2nd attempt: HTTP 200 with error_code 408
				_ = json.NewEncoder(w).Encode(zalo.APIResponse{
					OK:          false,
					ErrorCode:   408,
					Description: "Request timeout",
				})
				return
			}
			// 3rd attempt: successfully deliver a message immediately
			_ = json.NewEncoder(w).Encode(zalo.APIResponse{
				OK: true,
				Result: json.RawMessage(`[
					{
						"update_id": 100,
						"message": {
							"message_id": "msg_after_timeout",
							"from": {"id": "user_1", "name": "Active User"},
							"chat": {"id": "chat_1", "type": "private"},
							"date": 1724947200,
							"text": "fast response after idle timeout"
						}
					}
				]`),
			})
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	cfg := config.DefaultConfig()
	cfg.Zalo.BotToken = "poll_token"
	cfg.Zalo.APIURL = server.URL
	cfg.Zalo.Mode = "polling"
	cfg.Storage.AgentsDir = t.TempDir()

	adapter, err := zalo.NewAdapter(cfg, nil)
	require.NoError(t, err)

	inboundChan := make(chan domain.CanonicalMessage, 10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	start := time.Now()
	err = adapter.Start(ctx, inboundChan)
	require.NoError(t, err)

	select {
	case msg := <-inboundChan:
		elapsed := time.Since(start)
		assert.Equal(t, "fast response after idle timeout", msg.Text)
		// Should complete swiftly without 1s/2s/4s/8s/16s backoff delays
		assert.Less(t, elapsed, 2*time.Second, "polling should not trigger backoff sleep on 408 timeout")
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for message after 408 idle polling")
	}

	err = adapter.Stop()
	require.NoError(t, err)
}

func TestZaloAdapter_StreamingSupport_EventBus(t *testing.T) {
	var mu sync.Mutex
	var sentMessages []zalo.SendMessageRequest
	var sentActions []zalo.SendChatActionRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/botstream_token/getMe" {
			_ = json.NewEncoder(w).Encode(zalo.APIResponse{
				OK:     true,
				Result: json.RawMessage(`{"id": "bot_stream", "name": "Stream Bot", "is_bot": true}`),
			})
			return
		}
		if r.URL.Path == "/botstream_token/getUpdates" {
			w.WriteHeader(http.StatusRequestTimeout)
			_, _ = w.Write([]byte(`{"error_code": 408, "description": "Request timeout"}`))
			return
		}
		if r.URL.Path == "/botstream_token/sendChatAction" {
			var act zalo.SendChatActionRequest
			_ = json.NewDecoder(r.Body).Decode(&act)
			mu.Lock()
			sentActions = append(sentActions, act)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(zalo.APIResponse{OK: true})
			return
		}
		if r.URL.Path == "/botstream_token/sendMessage" {
			var msg zalo.SendMessageRequest
			_ = json.NewDecoder(r.Body).Decode(&msg)
			mu.Lock()
			sentMessages = append(sentMessages, msg)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(zalo.APIResponse{
				OK:     true,
				Result: json.RawMessage(`{"message_id": "out_101", "date": 1724947200, "text": "ok"}`),
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	bus := eventbus.NewEventBus(100, 2)
	defer bus.Close()

	cfg := config.DefaultConfig()
	cfg.Zalo.BotToken = "stream_token"
	cfg.Zalo.APIURL = server.URL
	cfg.Zalo.Mode = "polling"
	cfg.Storage.AgentsDir = t.TempDir()

	adapter, err := zalo.NewAdapter(cfg, bus)
	require.NoError(t, err)

	inboundChan := make(chan domain.CanonicalMessage, 10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = adapter.Start(ctx, inboundChan)
	require.NoError(t, err)

	sessionKey := "zalo:stream_token:chat_streaming_123"

	// 1. Emit EventStreamInit
	err = bus.SyncEmit(ctx, domain.NewEvent(domain.EventStreamInit, domain.StreamInitPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_1",
		TurnID:         "turn_1",
		CWD:            t.TempDir(),
	}))
	require.NoError(t, err)

	// 2. Emit EventStreamDelta
	err = bus.SyncEmit(ctx, domain.NewEvent(domain.EventStreamDelta, domain.StreamDeltaPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_1",
		TurnID:         "turn_1",
		TextDelta:      "Streaming chunk 1... ",
	}))
	require.NoError(t, err)

	err = bus.SyncEmit(ctx, domain.NewEvent(domain.EventStreamDelta, domain.StreamDeltaPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_1",
		TurnID:         "turn_1",
		TextDelta:      "Chunk 2 complete!",
	}))
	require.NoError(t, err)

	// 3. Emit EventStreamResult
	err = bus.SyncEmit(ctx, domain.NewEvent(domain.EventStreamResult, domain.StreamResultPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_1",
		TurnID:         "turn_1",
		Status:         "SUCCESS",
		Response:       "Streaming chunk 1... Chunk 2 complete!",
	}))
	require.NoError(t, err)

	// Verify message was delivered to Zalo
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(sentMessages) == 1
	}, 2*time.Second, 50*time.Millisecond, "expected finalized stream message to be sent to Zalo")

	mu.Lock()
	assert.Equal(t, "chat_streaming_123", sentMessages[0].ChatID)
	assert.Contains(t, sentMessages[0].Text, "Streaming chunk 1... Chunk 2 complete!")
	assert.NotEmpty(t, sentActions, "expected typing actions to be sent during streaming")
	mu.Unlock()

	err = adapter.Stop()
	require.NoError(t, err)
}

func TestZaloAdapter_StreamingSupport_ErrorAndInterrupted(t *testing.T) {
	var mu sync.Mutex
	var sentMessages []zalo.SendMessageRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/boterr_token/getMe" {
			_ = json.NewEncoder(w).Encode(zalo.APIResponse{
				OK:     true,
				Result: json.RawMessage(`{"id": "bot_err", "name": "Err Bot", "is_bot": true}`),
			})
			return
		}
		if r.URL.Path == "/boterr_token/getUpdates" {
			w.WriteHeader(http.StatusRequestTimeout)
			_, _ = w.Write([]byte(`{"error_code": 408, "description": "Request timeout"}`))
			return
		}
		if r.URL.Path == "/boterr_token/sendChatAction" {
			_ = json.NewEncoder(w).Encode(zalo.APIResponse{OK: true})
			return
		}
		if r.URL.Path == "/boterr_token/sendMessage" {
			var msg zalo.SendMessageRequest
			_ = json.NewDecoder(r.Body).Decode(&msg)
			mu.Lock()
			sentMessages = append(sentMessages, msg)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(zalo.APIResponse{
				OK:     true,
				Result: json.RawMessage(`{"message_id": "out_err", "date": 1724947200, "text": "ok"}`),
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	bus := eventbus.NewEventBus(100, 2)
	defer bus.Close()

	cfg := config.DefaultConfig()
	cfg.Zalo.BotToken = "err_token"
	cfg.Zalo.APIURL = server.URL
	cfg.Zalo.Mode = "polling"
	cfg.Storage.AgentsDir = t.TempDir()

	adapter, err := zalo.NewAdapter(cfg, bus)
	require.NoError(t, err)

	inboundChan := make(chan domain.CanonicalMessage, 10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = adapter.Start(ctx, inboundChan)
	require.NoError(t, err)

	// 1. Test EventStreamError delivery
	err = bus.SyncEmit(ctx, domain.NewEvent(domain.EventStreamError, domain.StreamErrorPayload{
		SessionKey: "zalo:err_token:chat_err_456",
		Error:      "LLM context window exceeded",
	}))
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(sentMessages) >= 1
	}, 2*time.Second, 50*time.Millisecond)

	mu.Lock()
	assert.Equal(t, "chat_err_456", sentMessages[0].ChatID)
	assert.Contains(t, sentMessages[0].Text, "LLM context window exceeded")
	mu.Unlock()

	// 2. Test EventStreamInterrupted delivery
	sessKey2 := "zalo:err_token:chat_int_789"
	err = bus.SyncEmit(ctx, domain.NewEvent(domain.EventStreamInit, domain.StreamInitPayload{
		SessionKey: sessKey2,
	}))
	require.NoError(t, err)

	err = bus.SyncEmit(ctx, domain.NewEvent(domain.EventStreamDelta, domain.StreamDeltaPayload{
		SessionKey: sessKey2,
		TextDelta:  "Partial answer before cancel",
	}))
	require.NoError(t, err)

	err = bus.SyncEmit(ctx, domain.NewEvent(domain.EventStreamInterrupted, domain.StreamInterruptedPayload{
		SessionKey: sessKey2,
		Reason:     "Preempted",
	}))
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(sentMessages) >= 2
	}, 2*time.Second, 50*time.Millisecond)

	mu.Lock()
	assert.Equal(t, "chat_int_789", sentMessages[1].ChatID)
	assert.Contains(t, sentMessages[1].Text, "Partial answer before cancel")
	assert.Contains(t, sentMessages[1].Text, "[Turn Interrupted by User]")
	mu.Unlock()

	err = adapter.Stop()
	require.NoError(t, err)
}



