package telegram

import (
	"context"
	"strings"
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
		RequestID:   "hitl-test-1",
		SessionKey:  "telegram:123456789",
		ToolName:    "run_command",
		CommandLine: "curl https://example.com",
		Reason:      "Sensitive shell execution: curl https://example.com",
		CreatedAt:   time.Now(),
		ExpiresAt:   time.Now().Add(500 * time.Millisecond),
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
		RequestID:   "hitl-test-2",
		SessionKey:  "telegram:123456789",
		ToolName:    "run_command",
		CommandLine: "chmod 777 /var/data",
		Reason:      "Command requires authorization in balanced preset",
		CreatedAt:   time.Now(),
		ExpiresAt:   time.Now().Add(100 * time.Millisecond),
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

func TestHITLCoordinator_LargePayloadTruncation(t *testing.T) {
	cfg := &config.Config{
		Telegram: config.TelegramConfig{
			AdminUserIDs: []int64{123456789},
		},
		Security: config.GetEffectiveSecurityPreset("balanced"),
	}

	coordinator := NewHITLCoordinator(nil, cfg, nil)

	hugeCommand := "echo " + strings.Repeat("A", 10000)
	hugeDiff := "diff --git a/file b/file\n" + strings.Repeat("+ line of large code\n", 500)
	hugeFile := "/path/to/very/long/" + strings.Repeat("subfolder/", 50) + "file.go"

	req := domain.ApprovalRequest{
		RequestID:   "hitl-test-large",
		SessionKey:  "telegram:123456789",
		ToolName:    "write_to_file",
		AgentName:   "coder",
		CommandLine: hugeCommand,
		TargetFile:  hugeFile,
		Reason:      "Configuration file modification",
		DiffPreview: hugeDiff,
		CreatedAt:   time.Now(),
		ExpiresAt:   time.Now().Add(10 * time.Second),
	}

	cardText := coordinator.formatCardText(req)
	formatted := FormatMarkdownToTelegramHTML(cardText)

	// Telegram max text length is 4096. Our formatted text must be comfortably below this limit.
	assert.Less(t, len([]rune(formatted)), 3500)
	assert.Contains(t, cardText, "chars omitted")
	assert.Contains(t, formatted, "<code>coder</code>")
	assert.Contains(t, cardText, "Configuration file modification")
	assert.Contains(t, cardText, "Diff / Changes")
}

func TestTruncateString(t *testing.T) {
	assert.Equal(t, "", truncateString("", 100))
	assert.Equal(t, "hello", truncateString("hello", 10))

	truncatedSingle := truncateString("This is a very long single-line text that needs truncation.", 30)
	assert.Contains(t, truncatedSingle, "chars omitted")
	assert.LessOrEqual(t, len([]rune(truncatedSingle)), 35)

	multiLine := "Line 1\nLine 2\nLine 3\nLine 4\nLine 5\nLine 6"
	truncatedMulti := truncateString(multiLine, 25)
	assert.Contains(t, truncatedMulti, "\n... [")
}
