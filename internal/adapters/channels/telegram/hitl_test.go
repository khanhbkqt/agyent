package telegram

import (
	"context"
	"testing"
	"time"

	"agyent/internal/config"
	"agyent/internal/core/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHITLCoordinator_ApprovalFlow(t *testing.T) {
	cfg := &config.Config{
		Telegram: config.TelegramConfig{
			AdminUserIDs: []int64{123456789},
		},
		Security: config.GetEffectiveSecurityPreset("balanced"),
	}

	coordinator := NewHITLCoordinator(nil, cfg, nil)
	ctx := context.Background()

	req := domain.ApprovalRequest{
		RequestID:    "hitl-test-1",
		SessionKey:   "telegram:123456789",
		ToolName:     "run_command",
		CommandLine:  "curl https://example.com",
		DiffPreview:  "Sensitive shell execution",
		CreatedAt:    time.Now(),
		ExpiresAt:    time.Now().Add(500 * time.Millisecond),
	}

	// 1. Simulate background approval by admin
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = coordinator.HandleCallback(ctx, "cb-1", 123456789, "hitl:hitl-test-1:allow_once")
	}()

	decision, err := coordinator.RequestApproval(ctx, req)
	require.NoError(t, err)
	assert.True(t, decision.Approved)
	assert.Equal(t, "allow_once", decision.Action)
	assert.Equal(t, int64(123456789), decision.UserID)
}

func TestHITLCoordinator_NonAdminDenied(t *testing.T) {
	cfg := &config.Config{
		Telegram: config.TelegramConfig{
			AdminUserIDs: []int64{123456789},
		},
		Security: config.GetEffectiveSecurityPreset("balanced"),
	}

	coordinator := NewHITLCoordinator(nil, cfg, nil)
	ctx := context.Background()

	req := domain.ApprovalRequest{
		RequestID:    "hitl-test-2",
		SessionKey:   "telegram:123456789",
		ToolName:     "run_command",
		CommandLine:  "chmod 777 /var/data",
		CreatedAt:    time.Now(),
		ExpiresAt:    time.Now().Add(100 * time.Millisecond),
	}

	// Non-admin user tries to approve
	go func() {
		time.Sleep(20 * time.Millisecond)
		_ = coordinator.HandleCallback(ctx, "cb-2", 999999999, "hitl:hitl-test-2:allow_once")
	}()

	decision, err := coordinator.RequestApproval(ctx, req)
	require.NoError(t, err)
	assert.False(t, decision.Approved)
	assert.Equal(t, "timeout", decision.Action)
}
