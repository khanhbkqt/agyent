package zalo_test

import (
	"context"
	"testing"
	"time"

	"agyent/internal/adapters/channels/zalo"
	"agyent/internal/config"
	"agyent/internal/core/domain"
	"agyent/internal/core/ports"

	"github.com/stretchr/testify/assert"
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


