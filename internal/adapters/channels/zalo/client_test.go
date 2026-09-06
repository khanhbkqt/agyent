package zalo_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"agyent/internal/adapters/channels/zalo"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestZaloClient_GetMe_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/bottest-token/getMe", r.URL.Path)
		resp := zalo.APIResponse{
			OK: true,
			Result: json.RawMessage(`{
				"id": "123456789",
				"name": "Agyent Test Bot",
				"username": "agyent_bot",
				"is_bot": true
			}`),
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	client := zalo.NewClient("test-token", ts.URL, ts.Client())
	user, err := client.GetMe(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "123456789", user.ID)
	assert.Equal(t, "Agyent Test Bot", user.Name)
	assert.True(t, user.IsBot)
}

func TestZaloClient_SendMessage_RetryOn500(t *testing.T) {
	var attempts atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := attempts.Add(1)
		if cur < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"ok": false, "error_code": 500, "description": "Internal Server Error"}`))
			return
		}
		resp := zalo.APIResponse{
			OK: true,
			Result: json.RawMessage(`{
				"message_id": "msg-999",
				"text": "Hello world"
			}`),
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	client := zalo.NewClient("test-token", ts.URL, ts.Client())
	msg, err := client.SendMessage(context.Background(), zalo.SendMessageRequest{
		ChatID: "chat-123",
		Text:   "Hello world",
	})
	require.NoError(t, err)
	assert.Equal(t, "msg-999", msg.MessageID)
	assert.Equal(t, int32(2), attempts.Load())
}

func TestZaloClient_SendMessage_NonRetryable401(t *testing.T) {
	var attempts atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"ok": false, "error_code": 401, "description": "Unauthorized"}`))
	}))
	defer ts.Close()

	client := zalo.NewClient("invalid-token", ts.URL, ts.Client())
	_, err := client.SendMessage(context.Background(), zalo.SendMessageRequest{
		ChatID: "chat-123",
		Text:   "Hello world",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
	// Should fail immediately without wasting retries
	assert.Equal(t, int32(1), attempts.Load())
}

func TestZaloClient_SendPhotoAndDocument(t *testing.T) {
	restore := zalo.SetPublicMediaUploaderForTest(func(ctx context.Context, filePath string) (string, error) {
		return "https://cdn.example.com/" + filepath.Base(filePath), nil
	})
	defer restore()

	tmpDir := t.TempDir()
	photoPath := filepath.Join(tmpDir, "sample.png")
	docPath := filepath.Join(tmpDir, "report.pdf")
	require.NoError(t, os.WriteFile(photoPath, []byte("fake-png-data"), 0644))
	require.NoError(t, os.WriteFile(docPath, []byte("fake-pdf-data"), 0644))

	var photoReq struct {
		ChatID  string `json:"chat_id"`
		Photo   string `json:"photo"`
		Caption string `json:"caption"`
	}
	var msgReq zalo.SendMessageRequest
	var photoCalled, msgCalled bool

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bottoken/sendPhoto" {
			photoCalled = true
			_ = json.NewDecoder(r.Body).Decode(&photoReq)
			resp := zalo.APIResponse{OK: true}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		if r.URL.Path == "/bottoken/sendMessage" {
			msgCalled = true
			_ = json.NewDecoder(r.Body).Decode(&msgReq)
			resp := zalo.APIResponse{
				OK: true,
				Result: json.RawMessage(`{"message_id":"doc-msg-1","message_type":"CHAT_TEXT"}`),
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	client := zalo.NewClient("token", ts.URL, ts.Client())

	// 1. SendPhoto with local file (triggers public media upload)
	err := client.SendPhoto(context.Background(), "chat-1", photoPath, "Nice picture")
	require.NoError(t, err)
	assert.True(t, photoCalled)
	assert.Equal(t, "chat-1", photoReq.ChatID)
	assert.Equal(t, "https://cdn.example.com/sample.png", photoReq.Photo)
	assert.Equal(t, "Nice picture", photoReq.Caption)

	// 2. SendPhoto with direct HTTPS URL
	photoCalled = false
	err = client.SendPhoto(context.Background(), "chat-1", "https://example.com/image.jpg", "Direct URL")
	require.NoError(t, err)
	assert.True(t, photoCalled)
	assert.Equal(t, "https://example.com/image.jpg", photoReq.Photo)
	assert.Equal(t, "Direct URL", photoReq.Caption)

	err = client.SendDocument(context.Background(), "chat-1", docPath, "PDF Document")
	require.NoError(t, err)
	assert.True(t, msgCalled)
	assert.Equal(t, "chat-1", msgReq.ChatID)
	assert.Contains(t, msgReq.Text, "📄 **Tài liệu:** [report.pdf](https://cdn.example.com/report.pdf)")
	assert.Contains(t, msgReq.Text, "PDF Document")
}

func TestZaloClient_WebhookLifecycle(t *testing.T) {
	var setCalled, delCalled bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bottoken/setWebhook" {
			setCalled = true
			_ = json.NewEncoder(w).Encode(zalo.APIResponse{OK: true})
			return
		}
		if r.URL.Path == "/bottoken/deleteWebhook" {
			delCalled = true
			_ = json.NewEncoder(w).Encode(zalo.APIResponse{OK: true})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	client := zalo.NewClient("token", ts.URL, ts.Client())

	err := client.SetWebhook(context.Background(), "https://example.com/zalo", "secret123")
	require.NoError(t, err)
	assert.True(t, setCalled)

	err = client.DeleteWebhook(context.Background())
	require.NoError(t, err)
	assert.True(t, delCalled)
}

func TestParseNumericID_StableForOpaqueProviderIDs(t *testing.T) {
	assert.Equal(t, int64(12345), zalo.ParseNumericID("12345"))
	assert.NotZero(t, zalo.ParseNumericID("bot_opaque_id"))
	assert.Equal(t, zalo.ParseNumericID("bot_opaque_id"), zalo.ParseNumericID("bot_opaque_id"))
	assert.NotEqual(t, zalo.ParseNumericID("bot_opaque_id"), zalo.ParseNumericID("another_bot"))
}

func TestZaloClient_GetUpdates_ArrayAndObject(t *testing.T) {
	tests := []struct {
		name          string
		jsonResult    string
		expectedCount int
		expectedID    int64
	}{
		{
			name: "array with multiple updates",
			jsonResult: `[
				{"update_id": 101, "message": {"message_id": "m1", "text": "first"}},
				{"update_id": 102, "message": {"message_id": "m2", "text": "second"}}
			]`,
			expectedCount: 2,
			expectedID:    101,
		},
		{
			name: "single object update (Zalo server quirk)",
			jsonResult: `{
				"update_id": 201,
				"message": {"message_id": "m201", "text": "single update object"}
			}`,
			expectedCount: 1,
			expectedID:    201,
		},
		{
			name:          "empty object returns empty slice",
			jsonResult:    `{}`,
			expectedCount: 0,
		},
		{
			name:          "empty array returns empty slice",
			jsonResult:    `[]`,
			expectedCount: 0,
		},
		{
			name:          "null result returns empty slice",
			jsonResult:    `null`,
			expectedCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/bottoken/getUpdates", r.URL.Path)
				resp := zalo.APIResponse{
					OK:     true,
					Result: json.RawMessage(tt.jsonResult),
				}
				_ = json.NewEncoder(w).Encode(resp)
			}))
			defer ts.Close()

			client := zalo.NewClient("token", ts.URL, ts.Client())
			updates, err := client.GetUpdates(context.Background(), 0, 50, 10)
			require.NoError(t, err)
			assert.Len(t, updates, tt.expectedCount)
			if tt.expectedCount > 0 {
				assert.Equal(t, tt.expectedID, updates[0].UpdateID)
			}
		})
	}
}

func TestZaloClient_GetUpdates_InvalidFormat(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := zalo.APIResponse{
			OK:     true,
			Result: json.RawMessage(`"just a string"`),
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	client := zalo.NewClient("token", ts.URL, ts.Client())
	_, err := client.GetUpdates(context.Background(), 0, 50, 10)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected JSON format")
}

func TestZaloClient_GetUpdates_408TimeoutReturnsEmptyUpdates(t *testing.T) {
	t.Run("HTTP 408 Status", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusRequestTimeout)
			_, _ = w.Write([]byte(`{"error_code": 408, "description": "Request timeout"}`))
		}))
		defer ts.Close()

		client := zalo.NewClient("token", ts.URL, ts.Client())
		updates, err := client.GetUpdates(context.Background(), 0, 50, 10)
		require.NoError(t, err)
		assert.Empty(t, updates)
	})

	t.Run("HTTP 200 with JSON ErrorCode 408", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resp := zalo.APIResponse{
				OK:          false,
				ErrorCode:   408,
				Description: "Request timeout",
			}
			_ = json.NewEncoder(w).Encode(resp)
		}))
		defer ts.Close()

		client := zalo.NewClient("token", ts.URL, ts.Client())
		updates, err := client.GetUpdates(context.Background(), 0, 50, 10)
		require.NoError(t, err)
		assert.Empty(t, updates)
	})
}

