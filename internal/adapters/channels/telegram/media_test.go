package telegram

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"agyent/internal/config"
	"agyent/internal/core/domain"
)

// TC-ACT-01: Tool ACTIVE Chat Action Dispatch (generate_image -> upload_photo)
func TestMedia_ToolActiveChatActionImage(t *testing.T) {
	mockServer := NewMockTelegramServer("token_act_01")
	defer mockServer.Close()

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	throttler := NewDeliveryThrottler(bot, nil, 1.5, true)
	defer throttler.Stop()

	sessionKey := "telegram:123456:0"
	ctx := context.Background()

	_ = throttler.OnStreamInit(ctx, domain.NewEvent(domain.EventStreamInit, domain.StreamInitPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_act_01",
	}))

	err = throttler.OnStreamTool(ctx, domain.NewEvent(domain.EventStreamTool, domain.StreamToolPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_act_01",
		State:          "ACTIVE",
		ToolName:       "generate_image",
	}))
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		mockServer.mu.Lock()
		defer mockServer.mu.Unlock()
		for _, a := range mockServer.ChatActions {
			if a.Action == "upload_photo" {
				return true
			}
		}
		return false
	}, 1*time.Second, 10*time.Millisecond, "upload_photo action must be dispatched")
}

// TC-ACT-02: Tool ACTIVE Chat Action Dispatch (run_command -> typing)
func TestMedia_ToolActiveChatActionCommand(t *testing.T) {
	mockServer := NewMockTelegramServer("token_act_02")
	defer mockServer.Close()

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	throttler := NewDeliveryThrottler(bot, nil, 1.5, true)
	defer throttler.Stop()

	sessionKey := "telegram:123456:0"
	ctx := context.Background()

	_ = throttler.OnStreamInit(ctx, domain.NewEvent(domain.EventStreamInit, domain.StreamInitPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_act_02",
	}))

	err = throttler.OnStreamTool(ctx, domain.NewEvent(domain.EventStreamTool, domain.StreamToolPayload{
		SessionKey:     sessionKey,
		ConversationID: "conv_act_02",
		State:          "ACTIVE",
		ToolName:       "run_command",
	}))
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		mockServer.mu.Lock()
		defer mockServer.mu.Unlock()
		for _, a := range mockServer.ChatActions {
			if a.Action == "typing" {
				return true
			}
		}
		return false
	}, 1*time.Second, 10*time.Millisecond, "typing action must be dispatched")
}

// TC-ACT-03: Heartbeat Typing Dispatcher (Every 4.0s)
func TestMedia_HeartbeatTypingDispatcher(t *testing.T) {
	mockServer := NewMockTelegramServer("token_act_03")
	defer mockServer.Close()

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	ctx := context.Background()
	// Use 100ms interval for fast testing
	cancel := StartHeartbeatTyping(ctx, bot, 123456, 0, 100*time.Millisecond)
	defer cancel()

	// Wait for multiple heartbeat ticks
	time.Sleep(350 * time.Millisecond)
	cancel()

	mockServer.mu.Lock()
	actionCount := len(mockServer.ChatActions)
	mockServer.mu.Unlock()

	assert.GreaterOrEqual(t, actionCount, 2, "Heartbeat must dispatch periodic typing actions")
}

// TC-ACT-04: Inbound Photo / Document Download to uploads/
func TestMedia_InboundDownload(t *testing.T) {
	mockServer := NewMockTelegramServer("token_act_04")
	defer mockServer.Close()

	tmpDir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Storage.AgentsDir = tmpDir

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	mediaMgr := NewMediaManager(cfg, bot)

	msg := &gotgbot.Message{
		MessageId: 101,
		Photo: []gotgbot.PhotoSize{
			{FileId: "photo_small", FileUniqueId: "u_s", FileSize: 100},
			{FileId: "photo_large", FileUniqueId: "u_l", FileSize: 5000},
		},
		Document: &gotgbot.Document{
			FileId:       "doc_123",
			FileUniqueId: "doc_u",
			FileName:     "test_spec.pdf",
			MimeType:     "application/pdf",
			FileSize:     2048,
		},
	}

	atts, err := mediaMgr.DownloadInboundMedia(context.Background(), msg)
	require.NoError(t, err)
	require.Equal(t, 2, len(atts))

	assert.Equal(t, "image", atts[0].Type)
	assert.Equal(t, "image/jpeg", atts[0].MIMEType)
	assert.FileExists(t, atts[0].FilePath)

	assert.Equal(t, "document", atts[1].Type)
	assert.Equal(t, "application/pdf", atts[1].MIMEType)
	assert.FileExists(t, atts[1].FilePath)
}

// TC-ACT-05: Path Traversal Sanitization on Inbound File
func TestMedia_PathTraversalSanitization(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "../../etc/passwd",
			expected: "passwd",
		},
		{
			input:    "..\\..\\windows\\system32\\cmd.exe",
			expected: "cmd.exe",
		},
		{
			input:    "normal_file.pdf",
			expected: "normal_file.pdf",
		},
		{
			input:    "../../../malicious.sh",
			expected: "malicious.sh",
		},
		{
			input:    "",
			expected: "file",
		},
		{
			input:    "....//....//",
			expected: "file",
		},
		{
			input:    "CON.txt",
			expected: "safe_CON.txt",
		},
		{
			input:    "prn.log",
			expected: "safe_prn.log",
		},
		{
			input:    "nul",
			expected: "safe_nul",
		},
		{
			input:    "aux.pdf",
			expected: "safe_aux.pdf",
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			actual := SanitizeFilename(tt.input)
			assert.Equal(t, tt.expected, actual)
			assert.False(t, filepath.IsAbs(actual))
			assert.NotContains(t, actual, "..")
			assert.NotContains(t, actual, "/")
			assert.NotContains(t, actual, "\\")
		})
	}
}

// TC-ACT-06: Tool DONE Instant Brain Photo Send
func TestMedia_ToolDoneInstantBrainPhoto(t *testing.T) {
	mockServer := NewMockTelegramServer("token_act_06")
	defer mockServer.Close()

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	homeDir, err := os.UserHomeDir()
	require.NoError(t, err)

	convID := "test_conv_brain_06"
	brainDir := filepath.Join(homeDir, ".gemini", "antigravity", "brain", convID)
	err = os.MkdirAll(brainDir, 0755)
	require.NoError(t, err)
	defer os.RemoveAll(brainDir)

	// Create dummy generated image in brain folder
	testImgPath := filepath.Join(brainDir, "cat_artwork_12345.jpg")
	err = os.WriteFile(testImgPath, []byte("fake jpeg data"), 0644)
	require.NoError(t, err)

	cfg := config.DefaultConfig()
	mediaMgr := NewMediaManager(cfg, bot)
	throttler := NewDeliveryThrottler(bot, mediaMgr, 1.5, true)
	defer throttler.Stop()

	sessionKey := "telegram:123456:0"
	ctx := context.Background()

	_ = throttler.OnStreamInit(ctx, domain.NewEvent(domain.EventStreamInit, domain.StreamInitPayload{
		SessionKey:     sessionKey,
		ConversationID: convID,
	}))

	err = throttler.OnStreamTool(ctx, domain.NewEvent(domain.EventStreamTool, domain.StreamToolPayload{
		SessionKey:     sessionKey,
		ConversationID: convID,
		State:          "DONE",
		ToolName:       "generate_image",
		Parameters: map[string]any{
			"ImageName": "cat_artwork",
		},
	}))
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		mockServer.mu.Lock()
		defer mockServer.mu.Unlock()
		if len(mockServer.SentMedia) >= 1 {
			return mockServer.SentMedia[0].Type == "photo" && strings.Contains(mockServer.SentMedia[0].Caption, "Generated Image")
		}
		return false
	}, 1*time.Second, 10*time.Millisecond, "Instant Brain photo must be sent asynchronously")
}

// TC-ACT-07: Outbound Turn Artifacts Auto-Upload (Whitelist)
func TestMedia_OutboundArtifactsAutoUpload(t *testing.T) {
	mockServer := NewMockTelegramServer("token_act_07")
	defer mockServer.Close()

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	tmpDir := t.TempDir()
	pdfFile := filepath.Join(tmpDir, "report.pdf")
	_ = os.WriteFile(pdfFile, []byte("pdf content"), 0644)
	exeFile := filepath.Join(tmpDir, "evil.exe")
	_ = os.WriteFile(exeFile, []byte("binary content"), 0644)

	cfg := config.DefaultConfig()
	mediaMgr := NewMediaManager(cfg, bot)

	artifacts := []domain.Attachment{
		{
			FileName: "report.pdf",
			FilePath: pdfFile,
			Type:     "document",
		},
		{
			FileName: "evil.exe",
			FilePath: exeFile, // Not whitelisted -> should be skipped
			Type:     "executable",
		},
	}

	err = mediaMgr.UploadTurnArtifacts(context.Background(), 123456, 0, artifacts)
	require.NoError(t, err)

	mockServer.mu.Lock()
	defer mockServer.mu.Unlock()
	require.Equal(t, 1, len(mockServer.SentMedia))
	assert.Equal(t, "document", mockServer.SentMedia[0].Type)
	assert.Equal(t, "report.pdf", mockServer.SentMedia[0].Caption)
}
