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
	tmpDir := t.TempDir()
	photoPath := filepath.Join(tmpDir, "sample.png")
	docPath := filepath.Join(tmpDir, "report.pdf")
	require.NoError(t, os.WriteFile(photoPath, []byte("fake-png-data"), 0644))
	require.NoError(t, os.WriteFile(docPath, []byte("fake-pdf-data"), 0644))

	var photoCalled, docCalled bool

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bottoken/sendPhoto" {
			photoCalled = true
			resp := zalo.APIResponse{OK: true}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		if r.URL.Path == "/bottoken/sendDocument" {
			docCalled = true
			resp := zalo.APIResponse{OK: true}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	client := zalo.NewClient("token", ts.URL, ts.Client())

	err := client.SendPhoto(context.Background(), "chat-1", photoPath, "Nice picture")
	require.NoError(t, err)
	assert.True(t, photoCalled)

	err = client.SendDocument(context.Background(), "chat-1", docPath, "PDF Document")
	require.NoError(t, err)
	assert.True(t, docCalled)
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
