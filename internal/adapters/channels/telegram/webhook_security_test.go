package telegram_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"agyent/internal/adapters/channels/telegram"
	"agyent/internal/config"
	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

func TestWebhook_SecurityHardening(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Telegram.BotToken = "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11"
	cfg.Telegram.SecretToken = "my-super-secret-token-1234567890abcdef"
	cfg.Telegram.AdminUserIDs = []int64{123456}

	mockBot := &gotgbot.Bot{
		User: gotgbot.User{
			Id:        123456,
			IsBot:     true,
			FirstName: "TestBot",
			Username:  "test_bot",
		},
		Token: cfg.Telegram.BotToken,
	}

	adapter := telegram.NewAdapter(cfg, nil, telegram.WithBot(mockBot))
	require.NotNil(t, adapter)
	handler := adapter.WebhookHandler()
	require.NotNil(t, handler)

	t.Run("Rejects non-POST requests", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/telegram/webhook", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	})

	t.Run("Rejects requests with missing secret token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/telegram/webhook", bytes.NewReader([]byte(`{"update_id":1}`)))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("Rejects requests with wrong secret token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/telegram/webhook", bytes.NewReader([]byte(`{"update_id":1}`)))
		req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "wrong-secret-token")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("Rejects requests with payload exceeding 1MB limit", func(t *testing.T) {
		hugePayload := bytes.Repeat([]byte("a"), (1<<20)+1024)
		req := httptest.NewRequest(http.MethodPost, "/telegram/webhook", bytes.NewReader(hugePayload))
		req.Header.Set("X-Telegram-Bot-Api-Secret-Token", cfg.Telegram.SecretToken)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("Accepts valid POST with secret token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/telegram/webhook", bytes.NewReader([]byte(`{"update_id":1}`)))
		req.Header.Set("X-Telegram-Bot-Api-Secret-Token", cfg.Telegram.SecretToken)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})
}

func TestMediaManager_LazyFetchAttachment(t *testing.T) {
	cfg := config.DefaultConfig()
	tmpDir := t.TempDir()
	cfg.Storage.AgentsDir = tmpDir

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bot123/getMe" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":123,"is_bot":true,"first_name":"TestBot","username":"test_bot"}}`))
			return
		}
		if r.URL.Path == "/bot123/getFile" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true,"result":{"file_id":"file_over_50mb","file_unique_id":"uniq1","file_size":60000000,"file_path":"large.bin"}}`))
			return
		}
		if r.URL.Path == "/file/bot123/large.bin" {
			// Stream 51MB of data
			chunk := bytes.Repeat([]byte("A"), 1024*1024) // 1MB chunk
			for i := 0; i < 51; i++ {
				_, _ = w.Write(chunk)
			}
			return
		}
		http.NotFound(w, r)
	}))
	defer mockServer.Close()

	botOpts := &gotgbot.BotOpts{
		BotClient: &gotgbot.BaseBotClient{
			DefaultRequestOpts: &gotgbot.RequestOpts{
				APIURL: mockServer.URL,
			},
		},
	}
	bot, err := gotgbot.NewBot("123", botOpts)
	require.NoError(t, err)

	mm := telegram.NewMediaManager(cfg, bot)
	require.NotNil(t, mm)

	t.Run("FetchAttachment enforces 50MB limit and cleans up partial file", func(t *testing.T) {
		ref := domain.InboundAttachmentRef{
			ID:       "uniq1",
			SourceID: "file_over_50mb",
			FileName: "test_large.bin",
			MIMEType: "application/octet-stream",
			Type:     "document",
		}
		destDir := filepath.Join(tmpDir, "staging")
		_, err := mm.FetchAttachment(context.Background(), ref, destDir)
		require.Error(t, err)
		assert.ErrorIs(t, err, ports.ErrAttachmentTooLarge)

		// Verify no dangling partial file left behind in destDir
		files, _ := os.ReadDir(destDir)
		assert.Empty(t, files, "Partial over-limit file must be strictly removed")
	})
}
