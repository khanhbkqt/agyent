package telegram

import (
	"context"
	"fmt"
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

// TC-ACT-08: ExtractAndCleanOutboundMedia with 6 carousel mockup screens
func TestMedia_ExtractAndCleanOutboundMedia_CarouselsAndImages(t *testing.T) {
	homeDir, err := os.UserHomeDir()
	require.NoError(t, err)

	convID := "bab26f82-b8e2-4633-acf6-049faccea7fc"
	brainDir := filepath.Join(homeDir, ".gemini", "antigravity-cli", "brain", convID)
	err = os.MkdirAll(brainDir, 0755)
	require.NoError(t, err)
	defer os.RemoveAll(brainDir)

	// Create 6 dummy mockup images
	for i := 1; i <= 6; i++ {
		imgName := fmt.Sprintf("mockup_flow%d.png", i)
		imgPath := filepath.Join(brainDir, imgName)
		_ = os.WriteFile(imgPath, []byte(fmt.Sprintf("png data %d", i)), 0644)
	}

	inputText := `Dạ, em gửi anh ảnh chụp trực quan của cả 6 luồng màn hình thực chiến nhé:


![Luồng 1: Quét Tem Đơn Siêu Tốc (CameraX NPU OCR 24ms)](~/.gemini/antigravity-cli/brain/bab26f82-b8e2-4633-acf6-049faccea7fc/mockup_flow1.png)
<!-- slide -->
![Luồng 2: Giao Tại Chỗ (Kiosk Privacy Mode VietQR Napas 24/7)](~/.gemini/antigravity-cli/brain/bab26f82-b8e2-4633-acf6-049faccea7fc/mockup_flow2.png)
<!-- slide -->
![Luồng 3: Khách Hẹn CK Sau (Bắn Zalo Bridge & Sổ Nợ Live)](~/.gemini/antigravity-cli/brain/bab26f82-b8e2-4633-acf6-049faccea7fc/mockup_flow3.png)
<!-- slide -->
![Luồng 4: Đối Soát Ngầm Rảnh Tay (Push Bank Nổ & Đọc Loa TTS)](~/.gemini/antigravity-cli/brain/bab26f82-b8e2-4633-acf6-049faccea7fc/mockup_flow4.png)
<!-- slide -->
![Luồng 5: Xử Lý Ngoại Lệ Thực Chiến (Trả Thiếu & Tiền Bo)](~/.gemini/antigravity-cli/brain/bab26f82-b8e2-4633-acf6-049faccea7fc/mockup_flow5.png)
<!-- slide -->
![Luồng 6: Quyết Toán Ca Giao & Cân Bằng Tiền Nộp Hub)](~/.gemini/antigravity-cli/brain/bab26f82-b8e2-4633-acf6-049faccea7fc/mockup_flow6.png)


──────────────

📋 Tóm Tắt Từng Luồng Trên Ảnh:
• Ảnh 1: Quét Tem
• Ảnh 2: Giao Tại Chỗ`

	cleaned, media := ExtractAndCleanOutboundMedia(inputText, "", convID)

	// Verify all 6 images extracted
	require.Equal(t, 6, len(media))
	assert.Equal(t, "mockup_flow1.png", media[0].FileName)
	assert.Equal(t, "Luồng 1: Quét Tem Đơn Siêu Tốc (CameraX NPU OCR 24ms)", media[0].Caption)
	assert.Equal(t, "image", media[0].Type)

	assert.Equal(t, "mockup_flow6.png", media[5].FileName)
	assert.Equal(t, "Luồng 6: Quyết Toán Ca Giao & Cân Bằng Tiền Nộp Hub)", media[5].Caption)

	// Verify text is cleaned of image markdown and slide comments
	assert.NotContains(t, cleaned, "mockup_flow")
	assert.NotContains(t, cleaned, "<!-- slide -->")
	assert.NotContains(t, cleaned, "<!--")
	assert.Contains(t, cleaned, "Dạ, em gửi anh ảnh chụp trực quan của cả 6 luồng màn hình thực chiến nhé:")
	assert.Contains(t, cleaned, "📋 Tóm Tắt Từng Luồng Trên Ảnh:")
}

// TC-ACT-09: SendMediaGroup Album Delivery
func TestMedia_SendMediaGroup_AlbumDelivery(t *testing.T) {
	mockServer := NewMockTelegramServer("token_act_09")
	defer mockServer.Close()

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	tmpDir := t.TempDir()
	img1 := filepath.Join(tmpDir, "screen1.png")
	img2 := filepath.Join(tmpDir, "screen2.png")
	_ = os.WriteFile(img1, []byte("png1"), 0644)
	_ = os.WriteFile(img2, []byte("png2"), 0644)

	cfg := config.DefaultConfig()
	mediaMgr := NewMediaManager(cfg, bot)

	photos := []domain.Attachment{
		{FileName: "screen1.png", FilePath: img1, Type: "image", Caption: "Screen 1"},
		{FileName: "screen2.png", FilePath: img2, Type: "image", Caption: "Screen 2"},
	}

	err = mediaMgr.SendMediaGroup(context.Background(), 123456, 42, photos)
	require.NoError(t, err)

	mockServer.mu.Lock()
	defer mockServer.mu.Unlock()
	require.Equal(t, 2, len(mockServer.SentMedia))
	assert.Equal(t, "photo", mockServer.SentMedia[0].Type)
	assert.Equal(t, int64(42), mockServer.SentMedia[0].ThreadID)
}

// TC-ACT-10: UploadTurnArtifacts with multiple photos sends album
func TestMedia_UploadTurnArtifacts_SendsAlbum(t *testing.T) {
	mockServer := NewMockTelegramServer("token_act_10")
	defer mockServer.Close()

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	tmpDir := t.TempDir()
	img1 := filepath.Join(tmpDir, "chart1.png")
	img2 := filepath.Join(tmpDir, "chart2.png")
	doc1 := filepath.Join(tmpDir, "data.csv")
	_ = os.WriteFile(img1, []byte("png1"), 0644)
	_ = os.WriteFile(img2, []byte("png2"), 0644)
	_ = os.WriteFile(doc1, []byte("a,b,c"), 0644)

	cfg := config.DefaultConfig()
	mediaMgr := NewMediaManager(cfg, bot)

	artifacts := []domain.Attachment{
		{FileName: "chart1.png", FilePath: img1, Type: "image", Caption: "Chart 1"},
		{FileName: "chart2.png", FilePath: img2, Type: "image", Caption: "Chart 2"},
		{FileName: "data.csv", FilePath: doc1, Type: "document", Caption: "Data"},
	}

	err = mediaMgr.UploadTurnArtifacts(context.Background(), 123456, 0, artifacts)
	require.NoError(t, err)

	mockServer.mu.Lock()
	defer mockServer.mu.Unlock()
	// 2 photos (sent as media group) + 1 doc = 3 items in SentMedia
	require.Equal(t, 3, len(mockServer.SentMedia))
}

// TC-ACT-11: Real AGY Response Outbound Media & Carousel Verification
func TestRealAGY_OutboundMediaComparison(t *testing.T) {
	mockServer := NewMockTelegramServer("token_real_agy_comp")
	defer mockServer.Close()

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	homeDir, err := os.UserHomeDir()
	require.NoError(t, err)

	convID := "bab26f82-b8e2-4633-acf6-049faccea7fc"
	brainDir := filepath.Join(homeDir, ".gemini", "antigravity-cli", "brain", convID)
	err = os.MkdirAll(brainDir, 0755)
	require.NoError(t, err)
	defer os.RemoveAll(brainDir)

	// Create the 6 mock image files in brain directory matching real AGY output
	for i := 1; i <= 6; i++ {
		imgName := fmt.Sprintf("mockup_flow%d.png", i)
		imgPath := filepath.Join(brainDir, imgName)
		_ = os.WriteFile(imgPath, []byte(fmt.Sprintf("png binary content for flow %d", i)), 0644)
	}

	// Exact real AGY response from the user's issue
	rawAGYResponse := `Dạ, em gửi anh ảnh chụp trực quan của cả 6 luồng màn hình thực chiến nhé:


![Luồng 1: Quét Tem Đơn Siêu Tốc (CameraX NPU OCR 24ms)](~/.gemini/antigravity-cli/brain/bab26f82-b8e2-4633-acf6-049faccea7fc/mockup_flow1.png)
<!-- slide -->
![Luồng 2: Giao Tại Chỗ (Kiosk Privacy Mode VietQR Napas 24/7)](~/.gemini/antigravity-cli/brain/bab26f82-b8e2-4633-acf6-049faccea7fc/mockup_flow2.png)
<!-- slide -->
![Luồng 3: Khách Hẹn CK Sau (Bắn Zalo Bridge & Sổ Nợ Live)](~/.gemini/antigravity-cli/brain/bab26f82-b8e2-4633-acf6-049faccea7fc/mockup_flow3.png)
<!-- slide -->
![Luồng 4: Đối Soát Ngầm Rảnh Tay (Push Bank Nổ & Đọc Loa TTS)](~/.gemini/antigravity-cli/brain/bab26f82-b8e2-4633-acf6-049faccea7fc/mockup_flow4.png)
<!-- slide -->
![Luồng 5: Xử Lý Ngoại Lệ Thực Chiến (Trả Thiếu & Tiền Bo)](~/.gemini/antigravity-cli/brain/bab26f82-b8e2-4633-acf6-049faccea7fc/mockup_flow5.png)
<!-- slide -->
![Luồng 6: Quyết Toán Ca Giao & Cân Bằng Tiền Nộp Hub)](~/.gemini/antigravity-cli/brain/bab26f82-b8e2-4633-acf6-049faccea7fc/mockup_flow6.png)


──────────────

📋 Tóm Tắt Từng Luồng Trên Ảnh:

• Ảnh 1 (Quét Tem): Camera rà qua gói hàng -> Tự động nhận diện Tên, SĐT, COD 250k -> Bấm nút to 64dp dưới đáy lưu đơn trong 0.2s.
• Ảnh 2 (Giao Tại Chỗ): Màn hình Kiosk VietQR cỡ lớn cho khách quét chuyển khoản, khóa toàn bộ danh bạ cá nhân.
• Ảnh 3 (Hẹn CK Sau): Bắn ảnh QR sang Zalo khách (Zalo tự hiện nút Chuyển Khoản), đơn chuyển sang nhãn Vàng Chờ CK (15p).
• Ảnh 4 (Đối Soát Rảnh Tay): Push Vietcombank rơi xuống khi đang lái xe -> Tự gạch nợ -> Loa Bluetooth đọc: "Ting ting! Chị Mai đã thanh toán 250k!".
• Ảnh 5 (Ngoại Lệ): Khách chuyển thiếu 20k -> Thẻ đỏ cảnh báo + Nút 1-chạm gửi tin nhắn Zalo kèm QR bù 20k; Khách bo thêm 20k -> Tự động cộng vào Quỹ Tiền Bo.
• Ảnh 6 (Chốt Ca): Báo cáo tiền mặt nộp kho (3.850.000đ), tiền bank đã khớp, kiểm kê kiện hàng tồn xe và nút xuất ảnh gửi Zalo Hub.

Anh thấy bố cục và mạch luồng này đã ưng ý chưa ạ?`

	cfg := config.DefaultConfig()
	mediaMgr := NewMediaManager(cfg, bot)

	// Step 1: Extract and Clean
	cleanedText, extractedMedia := ExtractAndCleanOutboundMedia(rawAGYResponse, "", convID)

	t.Logf("--- CLEANED TEXT ---\n%s\n--------------------", cleanedText)
	t.Logf("Extracted %d media attachments", len(extractedMedia))

	// Assertions on Extracted Media
	require.Equal(t, 6, len(extractedMedia), "Must extract all 6 images")
	for idx, att := range extractedMedia {
		assert.Equal(t, fmt.Sprintf("mockup_flow%d.png", idx+1), att.FileName)
		assert.Equal(t, "image", att.Type)
		assert.FileExists(t, att.FilePath)
		assert.NotEmpty(t, att.Caption)
		t.Logf("Photo #%d: File=%s, Caption=%s", idx+1, att.FileName, att.Caption)
	}

	// Assertions on Cleaned Text
	assert.NotContains(t, cleanedText, "mockup_flow")
	assert.NotContains(t, cleanedText, "<!-- slide -->")
	assert.NotContains(t, cleanedText, "<!--")
	assert.NotContains(t, cleanedText, "-->")
	assert.Contains(t, cleanedText, "Dạ, em gửi anh ảnh chụp trực quan của cả 6 luồng màn hình thực chiến nhé:")
	assert.Contains(t, cleanedText, "📋 Tóm Tắt Từng Luồng Trên Ảnh:")
	assert.Contains(t, cleanedText, "Anh thấy bố cục và mạch luồng này đã ưng ý chưa ạ?")

	// Step 2: Test Telegram HTML Formatting
	formattedHTML := FormatMarkdownToTelegramHTML(cleanedText)
	t.Logf("--- TELEGRAM FORMATTED HTML ---\n%s\n-------------------------------", formattedHTML)

	assert.NotContains(t, formattedHTML, "!<code>")
	assert.NotContains(t, formattedHTML, "&lt;!--")
	assert.Contains(t, formattedHTML, "──────────────")
	assert.Contains(t, formattedHTML, "-&gt;")

	// Step 3: Test Full Outbound Delivery via DeliveryThrottler
	throttler := NewDeliveryThrottler(bot, mediaMgr, 1.5, true)
	defer throttler.Stop()

	sessionKey := "telegram:123456:0"
	ctx := context.Background()

	_ = throttler.OnStreamInit(ctx, domain.NewEvent(domain.EventStreamInit, domain.StreamInitPayload{
		SessionKey:     sessionKey,
		ConversationID: convID,
	}))

	// Simulate Stream Result
	_ = throttler.OnStreamResult(ctx, domain.NewEvent(domain.EventStreamResult, domain.StreamResultPayload{
		SessionKey:     sessionKey,
		ConversationID: convID,
		Status:         "SUCCESS",
		Response:       rawAGYResponse,
	}))

	// Verify on Mock Telegram Server:
	// 1 text message sent and 6 photos sent via sendMediaGroup
	require.Eventually(t, func() bool {
		return len(mockServer.SentMessages) >= 1 && len(mockServer.SentMedia) == 6
	}, 1*time.Second, 20*time.Millisecond, "Must deliver cleaned text message and media group photos")

	sentText := mockServer.SentMessages[0].Text
	assert.NotContains(t, sentText, "!<code>")
	assert.NotContains(t, sentText, "&lt;!--")
	assert.Contains(t, sentText, "📋 Tóm Tắt Từng Luồng Trên Ảnh:")

	t.Logf("Successfully verified real AGY response comparison and delivery!")
}
