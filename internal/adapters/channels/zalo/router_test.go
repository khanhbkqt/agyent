package zalo_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agyent/internal/adapters/channels/zalo"
	"agyent/internal/config"
	"agyent/internal/core/domain"
	"agyent/internal/core/ports"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testInboundAuthorizer struct {
	allowed bool
	calls   int
}

var _ ports.InboundAuthorizer = (*testInboundAuthorizer)(nil)

func (a *testInboundAuthorizer) AuthorizeInbound(context.Context, string, string, string) (bool, error) {
	a.calls++
	return a.allowed, nil
}

func TestZaloRouter_StandardRouting(t *testing.T) {
	cfg := &config.Config{
		Zalo: config.ZaloConfig{
			AllowedGroupIDs: []string{"allowed_group_1"},
		},
	}
	inboundChan := make(chan domain.CanonicalMessage, 10)
	router := zalo.NewRouter(cfg, nil, nil, inboundChan)

	update := zalo.ZaloUpdate{
		UpdateID: 101,
		Message: &zalo.ZaloInboundMessage{
			MessageID: "zalo_msg_101",
			From: zalo.ZaloUser{
				ID:       "user_abc",
				Name:     "Nguyen Van A",
				Username: "nguyenvana",
			},
			Chat: zalo.ZaloChat{
				ID:   "allowed_group_1",
				Type: "group",
			},
			Text: "Hello Agyent!",
		},
	}

	botCtx := zalo.BotContext{
		BotID:       "888888",
		BotUsername: "agyent_zalo_bot",
		BindAgent:   "dev_expert",
	}

	router.RouteUpdate(context.Background(), update, botCtx)

	select {
	case msg := <-inboundChan:
		assert.Equal(t, "zalo_msg_101", msg.ID)
		assert.Equal(t, "zalo", msg.Channel)
		assert.Equal(t, int64(888888), msg.BotID)
		assert.Equal(t, "agyent_zalo_bot", msg.BotUsername)
		assert.Equal(t, "dev_expert", msg.BindAgent)
		assert.Equal(t, "user_abc", msg.Sender.ID)
		assert.Equal(t, "Nguyen Van A", msg.Sender.FullName)
		assert.Equal(t, "allowed_group_1", msg.Chat.ID)
		assert.Equal(t, "Hello Agyent!", msg.Text)
	default:
		t.Fatal("expected message to be delivered to inbound channel")
	}
}

func TestZaloRouter_AntiLooping_SkipBot(t *testing.T) {
	cfg := &config.Config{}
	inboundChan := make(chan domain.CanonicalMessage, 10)
	router := zalo.NewRouter(cfg, nil, nil, inboundChan)

	update := zalo.ZaloUpdate{
		Message: &zalo.ZaloInboundMessage{
			MessageID: "bot_msg_1",
			From: zalo.ZaloUser{
				ID:    "bot_999",
				IsBot: true,
			},
			Chat: zalo.ZaloChat{ID: "group_1", Type: "group"},
			Text: "Autonomous loop message",
		},
	}

	router.RouteUpdate(context.Background(), update)
	assert.Empty(t, inboundChan, "bot messages must be skipped to avoid infinite loops")
}

func TestZaloRouter_GroupWhitelisting_Denied(t *testing.T) {
	cfg := &config.Config{
		Zalo: config.ZaloConfig{
			AllowedGroupIDs: []string{"white_listed_group"},
		},
	}
	inboundChan := make(chan domain.CanonicalMessage, 10)
	router := zalo.NewRouter(cfg, nil, nil, inboundChan)

	update := zalo.ZaloUpdate{
		Message: &zalo.ZaloInboundMessage{
			MessageID: "msg_unlisted",
			From:      zalo.ZaloUser{ID: "user_1"},
			Chat:      zalo.ZaloChat{ID: "unlisted_group", Type: "group"},
			Text:      "Hello from random group",
		},
	}

	router.RouteUpdate(context.Background(), update)
	assert.Empty(t, inboundChan, "unlisted groups must be ignored when AllowedGroupIDs is configured")
}

func TestZaloRouter_AuthorizesBeforeEmittingLazyAttachmentReference(t *testing.T) {
	cfg := &config.Config{}
	inbound := make(chan domain.CanonicalMessage, 1)
	router := zalo.NewRouter(cfg, nil, nil, inbound)
	authorizer := &testInboundAuthorizer{allowed: true}
	router.SetInboundAuthorizer(authorizer)

	router.RouteUpdate(context.Background(), zalo.ZaloUpdate{Message: &zalo.ZaloInboundMessage{
		MessageID: "media-message",
		From:      zalo.ZaloUser{ID: "user-1"},
		Chat:      zalo.ZaloChat{ID: "chat-1", Type: "private"},
		Attachments: []zalo.ZaloAttachment{{
			Type:     "photo",
			FileID:   "file-1",
			FileName: "photo.jpg",
			URL:      "https://cdn.zalo.example/photo.jpg",
		}},
	}})

	select {
	case message := <-inbound:
		assert.Equal(t, 1, authorizer.calls)
		assert.Empty(t, message.Attachments)
		if assert.Len(t, message.AttachmentRefs, 1) {
			assert.Equal(t, "zalo", message.AttachmentRefs[0].Channel)
			assert.Equal(t, "https://cdn.zalo.example/photo.jpg", message.AttachmentRefs[0].SourceID)
		}
	case <-time.After(time.Second):
		t.Fatal("expected authorized Zalo message")
	}
}

func TestZaloRouter_DeniesBeforeEmittingAttachmentReference(t *testing.T) {
	inbound := make(chan domain.CanonicalMessage, 1)
	router := zalo.NewRouter(&config.Config{}, nil, nil, inbound)
	authorizer := &testInboundAuthorizer{allowed: false}
	router.SetInboundAuthorizer(authorizer)

	router.RouteUpdate(context.Background(), zalo.ZaloUpdate{Message: &zalo.ZaloInboundMessage{
		MessageID: "denied-media-message",
		From:      zalo.ZaloUser{ID: "user-1"},
		Chat:      zalo.ZaloChat{ID: "chat-1", Type: "private"},
		Attachments: []zalo.ZaloAttachment{{
			URL: "https://cdn.zalo.example/private.jpg",
		}},
	}})

	assert.Equal(t, 1, authorizer.calls)
	assert.Empty(t, inbound)
}

func TestZaloRouter_HITLSlashCommandIntercept(t *testing.T) {
	cfg := &config.Config{
		Zalo: config.ZaloConfig{
			AdminUserIDs: []string{"admin_1"},
		},
	}
	hitl := zalo.NewHITLCoordinator(nil, cfg)
	inboundChan := make(chan domain.CanonicalMessage, 10)
	router := zalo.NewRouter(cfg, hitl, nil, inboundChan)

	req := domain.ApprovalRequest{
		RequestID:  "req_cmd_1",
		SessionKey: "zalo:group_1",
	}

	decisionChan := make(chan domain.ApprovalDecision, 1)
	go func() {
		dec, _ := hitl.RequestApproval(context.Background(), req)
		decisionChan <- dec
	}()

	time.Sleep(50 * time.Millisecond)

	// Inbound message with /approve req_cmd_1 session
	update := zalo.ZaloUpdate{
		Message: &zalo.ZaloInboundMessage{
			MessageID: "cmd_msg_1",
			From:      zalo.ZaloUser{ID: "admin_1"},
			Chat:      zalo.ZaloChat{ID: "group_1", Type: "group"},
			Text:      "/approve req_cmd_1 session",
		},
	}

	router.RouteUpdate(context.Background(), update)

	// Verify that HITL command did NOT leak into normal inbound queue
	assert.Empty(t, inboundChan, "HITL commands must be intercepted and not pushed to LLM inbound")

	// Verify approval decision was completed
	select {
	case dec := <-decisionChan:
		assert.Equal(t, "req_cmd_1", dec.RequestID)
		assert.True(t, dec.Approved)
		assert.Equal(t, "allow_session", dec.Action)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for approval via router command")
	}
}

func TestZaloRouter_MentionStripping(t *testing.T) {
	cfg := &config.Config{
		Zalo: config.ZaloConfig{
			AllowedGroupIDs: []string{"group_101"},
		},
	}
	inboundChan := make(chan domain.CanonicalMessage, 10)
	router := zalo.NewRouter(cfg, nil, nil, inboundChan)

	botCtx := zalo.BotContext{
		BotID:       "9999",
		BotName:     "Trao Mơ FC",
		BotUsername: "traomofc",
		BindAgent:   "traomofc",
	}

	// 1. Tag bot with slash command
	router.RouteUpdate(context.Background(), zalo.ZaloUpdate{
		UpdateID: 201,
		Message: &zalo.ZaloInboundMessage{
			MessageID: "msg_tag_cmd",
			From:      zalo.ZaloUser{ID: "user_1", Name: "Tester"},
			Chat:      zalo.ZaloChat{ID: "group_101", Type: "group"},
			Text:      "@Trao Mơ FC /new Bàn về dự án AI",
		},
	}, botCtx)

	msg1 := <-inboundChan
	assert.Equal(t, "/new Bàn về dự án AI", msg1.Text)
	assert.Equal(t, "@Trao Mơ FC /new Bàn về dự án AI", msg1.RawText)
	assert.True(t, msg1.IsMentioned)
	assert.True(t, msg1.IsCommand())

	// 2. Tag bot with normal conversation
	router.RouteUpdate(context.Background(), zalo.ZaloUpdate{
		UpdateID: 202,
		Message: &zalo.ZaloInboundMessage{
			MessageID: "msg_tag_chat",
			From:      zalo.ZaloUser{ID: "user_1", Name: "Tester"},
			Chat:      zalo.ZaloChat{ID: "group_101", Type: "group"},
			Text:      "@traomofc Thời tiết hôm nay thế nào?",
		},
	}, botCtx)

	msg2 := <-inboundChan
	assert.Equal(t, "Thời tiết hôm nay thế nào?", msg2.Text)
	assert.Equal(t, "@traomofc Thời tiết hôm nay thế nào?", msg2.RawText)
	assert.True(t, msg2.IsMentioned)
	assert.False(t, msg2.IsCommand())

	// 3. Fallback generic tag with slash command
	router.RouteUpdate(context.Background(), zalo.ZaloUpdate{
		UpdateID: 203,
		Message: &zalo.ZaloInboundMessage{
			MessageID: "msg_generic_tag_cmd",
			From:      zalo.ZaloUser{ID: "user_1", Name: "Tester"},
			Chat:      zalo.ZaloChat{ID: "group_101", Type: "group"},
			Text:      "@bot /reset",
		},
	}, botCtx)

	msg3 := <-inboundChan
	assert.Equal(t, "/reset", msg3.Text)
	assert.True(t, msg3.IsMentioned)
	assert.True(t, msg3.IsCommand())

	// 4. Normal message without tag
	router.RouteUpdate(context.Background(), zalo.ZaloUpdate{
		UpdateID: 204,
		Message: &zalo.ZaloInboundMessage{
			MessageID: "msg_no_tag",
			From:      zalo.ZaloUser{ID: "user_1", Name: "Tester"},
			Chat:      zalo.ZaloChat{ID: "group_101", Type: "group"},
			Text:      "Tin nhắn không tag bot",
		},
	}, botCtx)

	msg4 := <-inboundChan
	assert.Equal(t, "Tin nhắn không tag bot", msg4.Text)
	assert.False(t, msg4.IsMentioned)

	// 5. Bot name with colon punctuation "@Trao Mơ FC: /new Dự án mới"
	router.RouteUpdate(context.Background(), zalo.ZaloUpdate{
		UpdateID: 205,
		Message: &zalo.ZaloInboundMessage{
			MessageID: "msg_colon_cmd",
			From:      zalo.ZaloUser{ID: "user_1", Name: "Tester"},
			Chat:      zalo.ZaloChat{ID: "group_101", Type: "group"},
			Text:      "@Trao Mơ FC: /new Dự án mới",
		},
	}, botCtx)

	msg5 := <-inboundChan
	assert.Equal(t, "/new Dự án mới", msg5.Text)
	assert.True(t, msg5.IsMentioned)
	assert.True(t, msg5.IsCommand())

	// 6. Long bot name with 4 words and spaces fallback
	router.RouteUpdate(context.Background(), zalo.ZaloUpdate{
		UpdateID: 206,
		Message: &zalo.ZaloInboundMessage{
			MessageID: "msg_long_name",
			From:      zalo.ZaloUser{ID: "user_1", Name: "Tester"},
			Chat:      zalo.ZaloChat{ID: "group_101", Type: "group"},
			Text:      "@Trợ Lý Ảo Toàn Diện /new Topic AI",
		},
	}, zalo.BotContext{BotID: "123"})

	msg6 := <-inboundChan
	assert.Equal(t, "/new Topic AI", msg6.Text)
	assert.True(t, msg6.IsMentioned)
	assert.True(t, msg6.IsCommand())
}

func TestZaloRouter_AttachmentFilenameAndExtensionInference(t *testing.T) {
	cfg := &config.Config{}
	inbound := make(chan domain.CanonicalMessage, 1)
	router := zalo.NewRouter(cfg, nil, nil, inbound)

	router.RouteUpdate(context.Background(), zalo.ZaloUpdate{
		Message: &zalo.ZaloInboundMessage{
			MessageID: "media-msg-multi",
			From:      zalo.ZaloUser{ID: "user-123"},
			Chat:      zalo.ZaloChat{ID: "chat-123", Type: "private"},
			Attachments: []zalo.ZaloAttachment{
				{
					Type:   "photo",
					FileID: "photo123",
					URL:    "https://cdn.zalo.example/p1",
				},
				{
					Type:   "voice",
					FileID: "voice456",
					URL:    "https://cdn.zalo.example/v1",
				},
				{
					Type:   "video",
					FileID: "video789",
					URL:    "https://cdn.zalo.example/vid1",
				},
				{
					Type:     "document",
					FileID:   "doc000",
					FileName: "custom_report.pdf",
					URL:      "https://cdn.zalo.example/d1",
				},
			},
		},
	})

	select {
	case msg := <-inbound:
		require.Len(t, msg.AttachmentRefs, 4)
		assert.Equal(t, "image", msg.AttachmentRefs[0].Type)
		assert.Equal(t, "image/jpeg", msg.AttachmentRefs[0].MIMEType)
		assert.True(t, strings.HasSuffix(msg.AttachmentRefs[0].FileName, ".jpg"))
		assert.True(t, strings.HasPrefix(msg.AttachmentRefs[0].FileName, "photo_"))

		assert.Equal(t, "audio", msg.AttachmentRefs[1].Type)
		assert.Equal(t, "audio/ogg", msg.AttachmentRefs[1].MIMEType)
		assert.True(t, strings.HasSuffix(msg.AttachmentRefs[1].FileName, ".ogg"))
		assert.True(t, strings.HasPrefix(msg.AttachmentRefs[1].FileName, "audio_"))

		assert.Equal(t, "video", msg.AttachmentRefs[2].Type)
		assert.Equal(t, "video/mp4", msg.AttachmentRefs[2].MIMEType)
		assert.True(t, strings.HasSuffix(msg.AttachmentRefs[2].FileName, ".mp4"))
		assert.True(t, strings.HasPrefix(msg.AttachmentRefs[2].FileName, "video_"))

		assert.Equal(t, "document", msg.AttachmentRefs[3].Type)
		assert.Equal(t, "custom_report.pdf", msg.AttachmentRefs[3].FileName)
	case <-time.After(time.Second):
		t.Fatal("expected canonical message with inferred attachment extensions")
	}
}

func TestZaloRouter_CaptionAndDescriptionFallback(t *testing.T) {
	cfg := &config.Config{}
	inbound := make(chan domain.CanonicalMessage, 5)
	router := zalo.NewRouter(cfg, nil, nil, inbound)

	// Case 1: Inbound message has empty Text but has message-level Caption
	router.RouteUpdate(context.Background(), zalo.ZaloUpdate{
		Message: &zalo.ZaloInboundMessage{
			MessageID: "msg-cap-1",
			From:      zalo.ZaloUser{ID: "user-1"},
			Chat:      zalo.ZaloChat{ID: "chat-1", Type: "private"},
			Caption:   "Analyze this receipt",
			Attachments: []zalo.ZaloAttachment{
				{
					Type:   "photo",
					FileID: "photo1",
					URL:    "https://cdn.zalo.example/p1",
				},
			},
		},
	})

	select {
	case msg := <-inbound:
		assert.Equal(t, "Analyze this receipt", msg.Text)
		assert.Equal(t, "Analyze this receipt", msg.RawText)
		require.Len(t, msg.AttachmentRefs, 1)
		assert.Equal(t, "Analyze this receipt", msg.AttachmentRefs[0].Caption)
	case <-time.After(time.Second):
		t.Fatal("expected canonical message for msg-cap-1")
	}

	// Case 2: Inbound message has empty Text and Caption, but has attachment-level Caption
	router.RouteUpdate(context.Background(), zalo.ZaloUpdate{
		Message: &zalo.ZaloInboundMessage{
			MessageID: "msg-cap-2",
			From:      zalo.ZaloUser{ID: "user-1"},
			Chat:      zalo.ZaloChat{ID: "chat-1", Type: "private"},
			Attachments: []zalo.ZaloAttachment{
				{
					Type:    "photo",
					FileID:  "photo2",
					URL:     "https://cdn.zalo.example/p2",
					Caption: "Photo specific caption",
				},
			},
		},
	})

	select {
	case msg := <-inbound:
		assert.Equal(t, "Photo specific caption", msg.Text)
		assert.Equal(t, "Photo specific caption", msg.RawText)
		require.Len(t, msg.AttachmentRefs, 1)
		assert.Equal(t, "Photo specific caption", msg.AttachmentRefs[0].Caption)
	case <-time.After(time.Second):
		t.Fatal("expected canonical message for msg-cap-2")
	}

	// Case 3: Inbound message has empty Text and Caption, but has message-level Description
	router.RouteUpdate(context.Background(), zalo.ZaloUpdate{
		Message: &zalo.ZaloInboundMessage{
			MessageID:   "msg-cap-3",
			From:        zalo.ZaloUser{ID: "user-1"},
			Chat:        zalo.ZaloChat{ID: "chat-1", Type: "private"},
			Description: "Document summary description",
			Attachments: []zalo.ZaloAttachment{
				{
					Type:   "document",
					FileID: "doc3",
					URL:    "https://cdn.zalo.example/d3",
				},
			},
		},
	})

	select {
	case msg := <-inbound:
		assert.Equal(t, "Document summary description", msg.Text)
		assert.Equal(t, "Document summary description", msg.RawText)
		require.Len(t, msg.AttachmentRefs, 1)
		assert.Equal(t, "Document summary description", msg.AttachmentRefs[0].Caption)
	case <-time.After(time.Second):
		t.Fatal("expected canonical message for msg-cap-3")
	}

	// Case 4: Zero-Text & Zero-Caption with photo -> fallback prompt must be set
	router.RouteUpdate(context.Background(), zalo.ZaloUpdate{
		Message: &zalo.ZaloInboundMessage{
			MessageID: "msg-no-text-photo",
			From:      zalo.ZaloUser{ID: "user-1"},
			Chat:      zalo.ZaloChat{ID: "chat-1", Type: "private"},
			Attachments: []zalo.ZaloAttachment{
				{
					Type:   "photo",
					FileID: "photo4",
					URL:    "https://cdn.zalo.example/p4.jpg",
				},
			},
		},
	})

	select {
	case msg := <-inbound:
		assert.Contains(t, msg.Text, "Người dùng gửi ảnh/tệp đính kèm")
		require.Len(t, msg.AttachmentRefs, 1)
		assert.Equal(t, "https://cdn.zalo.example/p4.jpg", msg.AttachmentRefs[0].SourceID)
	case <-time.After(time.Second):
		t.Fatal("expected canonical message for msg-no-text-photo")
	}
}

func TestZaloRouter_NestedPayloadAndCompatibilityAttachments(t *testing.T) {
	rawJSON := `{
		"update_id": 9999,
		"message": {
			"message_id": "zalo-nested-123",
			"from": {"id": "user-888", "name": "Nguyen Van A"},
			"chat": {"id": "chat-999", "type": "private"},
			"date": 1725600000,
			"attachments": [
				{
					"type": "photo",
					"payload": {
						"url": "https://cdn.zalo.example/nested-photo.png",
						"file_name": "receipt_2026.png",
						"caption": "Receipt photo in payload"
					}
				}
			],
			"document": {
				"file_id": "doc-top-1",
				"file_name": "annual_report.pdf",
				"url": "https://cdn.zalo.example/report.pdf"
			}
		}
	}`

	var update zalo.ZaloUpdate
	err := json.Unmarshal([]byte(rawJSON), &update)
	require.NoError(t, err)

	inbound := make(chan domain.CanonicalMessage, 1)
	router := zalo.NewRouter(&config.Config{}, nil, nil, inbound)

	router.RouteUpdate(context.Background(), update)

	select {
	case msg := <-inbound:
		assert.Equal(t, "Receipt photo in payload", msg.Text)
		require.Len(t, msg.AttachmentRefs, 2)

		// 1. Nested photo attachment
		assert.Equal(t, "image", msg.AttachmentRefs[0].Type)
		assert.Equal(t, "https://cdn.zalo.example/nested-photo.png", msg.AttachmentRefs[0].SourceID)
		assert.Equal(t, "receipt_2026.png", msg.AttachmentRefs[0].FileName)
		assert.Equal(t, "Receipt photo in payload", msg.AttachmentRefs[0].Caption)

		// 2. Compatibility top-level document
		assert.Equal(t, "document", msg.AttachmentRefs[1].Type)
		assert.Equal(t, "https://cdn.zalo.example/report.pdf", msg.AttachmentRefs[1].SourceID)
		assert.Equal(t, "annual_report.pdf", msg.AttachmentRefs[1].FileName)
	case <-time.After(time.Second):
		t.Fatal("expected message with nested payload attachments")
	}
}

func TestZaloRouter_OfficialZaloWebhookImagePayload(t *testing.T) {

	rawJSON := `{
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
	err := json.Unmarshal([]byte(rawJSON), &update)
	require.NoError(t, err)

	inbound := make(chan domain.CanonicalMessage, 1)
	router := zalo.NewRouter(&config.Config{}, nil, nil, inbound)

	router.RouteUpdate(context.Background(), update)

	select {
	case msg := <-inbound:
		assert.Equal(t, "2d758cb5e222177a4e35", msg.ID)
		assert.Equal(t, "Ted", msg.Sender.FullName)
		assert.Equal(t, "private", msg.Chat.Type)
		assert.Equal(t, "Đây là ảnh demo", msg.Text)
		require.Len(t, msg.AttachmentRefs, 1)
		assert.Equal(t, "https://img.zaloapp.com/v1/image.jpg", msg.AttachmentRefs[0].SourceID)
		assert.Equal(t, "image", msg.AttachmentRefs[0].Type)
		assert.Equal(t, "Đây là ảnh demo", msg.AttachmentRefs[0].Caption)
	case <-time.After(time.Second):
		t.Fatal("expected canonical message for official Zalo webhook image payload")
	}
}



