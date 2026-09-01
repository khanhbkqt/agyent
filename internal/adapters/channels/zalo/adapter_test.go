package zalo_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agyent/internal/adapters/channels/zalo"
	"agyent/internal/config"
	"agyent/internal/core/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestZaloAdapter_FullLifecycleWithMockServer(t *testing.T) {
	var receivedSendReq zalo.SendMessageRequest
	var receivedActionReq zalo.SendChatActionRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Path == "/botmock_token/getMe" {
			_ = json.NewEncoder(w).Encode(zalo.APIResponse{
				OK: true,
				Result: json.RawMessage(`{"id": "bot_123", "name": "Agyent Bot", "is_bot": true}`),
			})
			return
		}

		if r.URL.Path == "/botmock_token/sendMessage" {
			_ = json.NewDecoder(r.Body).Decode(&receivedSendReq)
			_ = json.NewEncoder(w).Encode(zalo.APIResponse{
				OK: true,
				Result: json.RawMessage(`{"message_id": "msg_999", "date": 1724947200, "text": "ok"}`),
			})
			return
		}

		if r.URL.Path == "/botmock_token/sendChatAction" {
			_ = json.NewDecoder(r.Body).Decode(&receivedActionReq)
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
	assert.Equal(t, "group_789", receivedSendReq.ChatID)
	assert.Contains(t, receivedSendReq.Text, "**Thông báo:** Đã xử lý lệnh `/help` thành công.")

	// Test Send Typing
	err = adapter.SendTyping(ctx, "group_789", 0)
	require.NoError(t, err)
	assert.Equal(t, "group_789", receivedActionReq.ChatID)
	assert.Equal(t, "typing", receivedActionReq.Action)

	// Test Stop
	err = adapter.Stop()
	require.NoError(t, err)
}
