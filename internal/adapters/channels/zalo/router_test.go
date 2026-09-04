package zalo_test

import (
	"context"
	"testing"
	"time"

	"agyent/internal/adapters/channels/zalo"
	"agyent/internal/config"
	"agyent/internal/core/domain"

	"github.com/stretchr/testify/assert"
)

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
