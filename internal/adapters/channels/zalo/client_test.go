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

	t.Run("HTTP 200 with Native Zalo JSON Error 408", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"error": 408, "message": "Request timeout", "data": null}`))
		}))
		defer ts.Close()

		client := zalo.NewClient("token", ts.URL, ts.Client())
		updates, err := client.GetUpdates(context.Background(), 0, 50, 10)
		require.NoError(t, err)
		assert.Empty(t, updates)
	})
}

func TestZaloClient_NativeZaloEnvelope_Success(t *testing.T) {
	t.Run("GetMe with error:0 and data object", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/botnative-tok/getMe", r.URL.Path)
			_, _ = w.Write([]byte(`{
				"error": 0,
				"message": "Success",
				"data": {
					"id": "987654321",
					"name": "Native Zalo Bot",
					"is_bot": true
				}
			}`))
		}))
		defer ts.Close()

		client := zalo.NewClient("native-tok", ts.URL, ts.Client())
		user, err := client.GetMe(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "987654321", user.ID)
		assert.Equal(t, "Native Zalo Bot", user.Name)
	})

	t.Run("SendMessage with error:0 and data object", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/botnative-tok/sendMessage", r.URL.Path)
			_, _ = w.Write([]byte(`{
				"error": 0,
				"message": "Success",
				"data": {
					"message_id": "native-msg-123",
					"text": "Sent successfully"
				}
			}`))
		}))
		defer ts.Close()

		client := zalo.NewClient("native-tok", ts.URL, ts.Client())
		msg, err := client.SendMessage(context.Background(), zalo.SendMessageRequest{
			ChatID: "c1",
			Text:   "Hello",
		})
		require.NoError(t, err)
		assert.Equal(t, "native-msg-123", msg.MessageID)
	})

	t.Run("GetUpdates with error:0 and data array", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/botnative-tok/getUpdates", r.URL.Path)
			_, _ = w.Write([]byte(`{
				"error": 0,
				"message": "Success",
				"data": [
					{
						"update_id": 501,
						"message": {"message_id": "m501", "text": "update from native data"}
					}
				]
			}`))
		}))
		defer ts.Close()

		client := zalo.NewClient("native-tok", ts.URL, ts.Client())
		updates, err := client.GetUpdates(context.Background(), 0, 50, 10)
		require.NoError(t, err)
		require.Len(t, updates, 1)
		assert.Equal(t, int64(501), updates[0].UpdateID)
		assert.Equal(t, "m501", updates[0].Message.MessageID)
		assert.Equal(t, "update from native data", updates[0].Message.Text)
	})
}

func TestZaloClient_NativeZaloEnvelope_Error(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"error": -32, "message": "limit call api"}`))
	}))
	defer ts.Close()

	client := zalo.NewClient("token", ts.URL, ts.Client())
	_, err := client.SendMessage(context.Background(), zalo.SendMessageRequest{
		ChatID: "c1",
		Text:   "Hi",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "-32")
	assert.Contains(t, err.Error(), "limit call api")
}

func TestIsTimeoutError_Variants(t *testing.T) {
	assert.True(t, zalo.IsTimeoutError(context.DeadlineExceeded))
	assert.True(t, zalo.IsTimeoutError(&zalo.APIError{StatusCode: 408}))
	assert.True(t, zalo.IsTimeoutError(&zalo.APIError{ErrorCode: 408}))
	assert.True(t, zalo.IsTimeoutError(&zalo.APIError{Description: "Request timeout"}))
	assert.False(t, zalo.IsTimeoutError(assert.AnError))
}

func TestZaloInboundMessage_UnmarshalJSON_OfficialZaloWebhookImage(t *testing.T) {
	raw := `{
		"ok": true,
		"result": {
			"event_name": "message.image.received",
			"message": {
				"from": {
					"id": "6ede9afa66b88fe6d6a9",
					"display_name": "Ted",
					"is_bot": false
				},
				"chat": {
					"id": "6ede9afa66b88fe6d6a9",
					"chat_type": "PRIVATE"
				},
				"text": "",
				"photo": "https://img.zaloapp.com/v1/image.jpg",
				"caption": "Đây là ảnh demo",
				"message_id": "2d758cb5e222177a4e35",
				"date": 1750316131602
			}
		}
	}`

	var update zalo.ZaloUpdate
	err := json.Unmarshal([]byte(raw), &update)
	require.NoError(t, err)
	require.NotNil(t, update.Message)

	msg := update.Message
	assert.Equal(t, "2d758cb5e222177a4e35", msg.MessageID)
	assert.Equal(t, "Ted", msg.From.GetEffectiveName())
	assert.True(t, msg.Chat.IsPrivate())
	assert.Equal(t, "private", msg.Chat.EffectiveType())
	assert.Equal(t, "Đây là ảnh demo", msg.Caption)

	atts := msg.CollectAttachments()
	require.Len(t, atts, 1)
	assert.Equal(t, "photo", atts[0].Type)
	assert.Equal(t, "https://img.zaloapp.com/v1/image.jpg", atts[0].GetEffectiveURL())
}

func TestZaloInboundMessage_UnmarshalJSON_ArrayOfPhotoStrings(t *testing.T) {
	raw := `{
		"message_id": "m100",
		"from": {"id": "u1", "name": "Alice"},
		"chat": {"id": "c1", "type": "private"},
		"photo": ["https://img1.jpg", "https://img2.png"]
	}`

	var msg zalo.ZaloInboundMessage
	err := json.Unmarshal([]byte(raw), &msg)
	require.NoError(t, err)

	atts := msg.CollectAttachments()
	require.Len(t, atts, 2)
	assert.Equal(t, "https://img1.jpg", atts[0].GetEffectiveURL())
	assert.Equal(t, "https://img2.png", atts[1].GetEffectiveURL())
}

func TestZaloInboundMessage_UnmarshalJSON_NestedPayload(t *testing.T) {
	raw := `{
		"message_id": "m200",
		"from": {"id": "u2"},
		"chat": {"id": "c2"},
		"photo": [
			{
				"type": "photo",
				"payload": {
					"url": "https://nested.com/pic.png",
					"caption": "nested caption",
					"file_size": 1048576,
					"file_name": "pic.png"
				}
			}
		]
	}`

	var msg zalo.ZaloInboundMessage
	err := json.Unmarshal([]byte(raw), &msg)
	require.NoError(t, err)

	atts := msg.CollectAttachments()
	require.Len(t, atts, 1)
	assert.Equal(t, "https://nested.com/pic.png", atts[0].GetEffectiveURL())
	assert.Equal(t, "nested caption", atts[0].GetEffectiveCaption())
	assert.Equal(t, "pic.png", atts[0].GetEffectiveFileName())
	assert.Equal(t, int64(1048576), atts[0].GetEffectiveFileSize())
}

func TestZaloInboundMessage_UnmarshalJSON_VoiceAudioDocumentSticker(t *testing.T) {
	raw := `{
		"message_id": "m300",
		"from": {"id": "u3"},
		"chat": {"id": "c3"},
		"voice_url": "https://voice.zalo.me/v1.ogg",
		"document": "https://doc.zalo.me/report.pdf",
		"sticker": "https://sticker.zalo.me/s1.png"
	}`

	var msg zalo.ZaloInboundMessage
	err := json.Unmarshal([]byte(raw), &msg)
	require.NoError(t, err)

	atts := msg.CollectAttachments()
	require.Len(t, atts, 3)

	var foundVoice, foundDoc, foundSticker bool
	for _, a := range atts {
		switch a.Type {
		case "voice":
			foundVoice = true
			assert.Equal(t, "https://voice.zalo.me/v1.ogg", a.GetEffectiveURL())
		case "document":
			foundDoc = true
			assert.Equal(t, "https://doc.zalo.me/report.pdf", a.GetEffectiveURL())
		case "sticker":
			foundSticker = true
			assert.Equal(t, "https://sticker.zalo.me/s1.png", a.GetEffectiveURL())
		}
	}
	assert.True(t, foundVoice, "expected voice attachment")
	assert.True(t, foundDoc, "expected document attachment")
	assert.True(t, foundSticker, "expected sticker attachment")
}

func TestZaloInboundMessage_UnmarshalJSON_RealZaloPlatformPhotoURL(t *testing.T) {
	raw := `{
		"ok": true,
		"result": {
			"event_name": "message.image.received",
			"message": {
				"chat": {
					"id": "704aebb9aff246ac1fe3",
					"chat_type": "PRIVATE"
				},
				"message_id": "2e4c923d75b4e1edb8a2",
				"date": 1788780810265,
				"message_type": "CHAT_PHOTO",
				"from": {
					"id": "704aebb9aff246ac1fe3",
					"is_bot": false,
					"display_name": "Khánh Nguyễn"
				},
				"photo_url": "https://photo-stal-22.zdn.vn/no/jpg/5bff10c65d12874cde03/2aOboQwymKj2LsvOdya2Km2y6nWmXwUA0coI68mG.jpg",
				"caption": "photo with captions"
			}
		},
		"error_code": 0
	}`

	var update zalo.ZaloUpdate
	err := json.Unmarshal([]byte(raw), &update)
	require.NoError(t, err)
	require.NotNil(t, update.Message)

	msg := update.Message
	assert.Equal(t, "2e4c923d75b4e1edb8a2", msg.MessageID)
	assert.Equal(t, "Khánh Nguyễn", msg.From.GetEffectiveName())
	assert.True(t, msg.Chat.IsPrivate())
	assert.Equal(t, "photo with captions", msg.Caption)

	atts := msg.CollectAttachments()
	require.Len(t, atts, 1)
	assert.Equal(t, "photo", atts[0].Type)
	assert.Equal(t, "https://photo-stal-22.zdn.vn/no/jpg/5bff10c65d12874cde03/2aOboQwymKj2LsvOdya2Km2y6nWmXwUA0coI68mG.jpg", atts[0].GetEffectiveURL())
}

