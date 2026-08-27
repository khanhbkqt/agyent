package telegram

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"agyent/internal/config"
	"agyent/internal/core/domain"
	"agyent/internal/core/eventbus"
)

// Boost coverage for actions.go
func TestBoost_MapToolToChatAction(t *testing.T) {
	tools := map[string]string{
		"generate_image":       "upload_photo",
		"run_command":          "typing",
		"replace_file_content": "typing",
		"write_to_file":        "typing",
		"view_file":            "typing",
		"list_dir":             "typing",
		"grep_search":          "typing",
		"find_by_name":         "typing",
		"read_url_content":     "typing",
		"search_web":           "typing",
		"unknown_tool":         "typing",
	}

	for tool, expectedAction := range tools {
		assert.Equal(t, expectedAction, MapToolToChatAction(tool))
	}

	// nil bot in StartHeartbeat
	cancel := StartHeartbeatTyping(context.Background(), nil, 123, 0, 1*time.Second)
	assert.NotNil(t, cancel)
	cancel()

	// negative interval
	cancel = StartHeartbeatTyping(context.Background(), &gotgbot.Bot{}, 123, 0, -1)
	assert.NotNil(t, cancel)
	cancel()
}

// Boost coverage for filter.go & router.go
func TestBoost_FilterAndRouterEdgeCases(t *testing.T) {
	// nil cfg
	assert.False(t, IsUserAdmin(nil, 123))
	assert.False(t, IsGroupAllowed(nil, 123))

	// nil msg
	proc, ment, rep := IsMessageForBot("bot", 123, nil)
	assert.False(t, proc)
	assert.False(t, ment)
	assert.False(t, rep)

	assert.Equal(t, int64(0), ExtractThreadID(nil))
	assert.Equal(t, "hello", ExtractCleanText("", "hello"))
	assert.Equal(t, "", ExtractCleanText("bot", ""))

	// Mention entities
	msgWithEnt := &gotgbot.Message{
		Chat: gotgbot.Chat{Type: "group"},
		Text: "Hello @testbot",
		Entities: []gotgbot.MessageEntity{
			{Type: "mention", Offset: 6, Length: 8},
		},
	}
	proc, ment, _ = IsMessageForBot("testbot", 999, msgWithEnt)
	assert.True(t, proc)
	assert.True(t, ment)

	// Router edge cases
	cfg := config.DefaultConfig()
	inbound := make(chan domain.CanonicalMessage, 10)
	router := NewRouter(cfg, nil, inbound, nil)

	// Nil update or nil message
	assert.NoError(t, router.HandleUpdate(context.Background(), nil, nil))
	assert.NoError(t, router.HandleUpdate(context.Background(), nil, &gotgbot.Update{}))

	// Channel chat type (ignored)
	assert.NoError(t, router.HandleUpdate(context.Background(), nil, &gotgbot.Update{
		Message: &gotgbot.Message{
			Chat: gotgbot.Chat{Type: "channel"},
			From: &gotgbot.User{Id: 123},
		},
	}))

	// Caption fallback for photo in private chat
	cfg.Telegram.AdminUserIDs = []int64{123}
	err := router.HandleUpdate(context.Background(), nil, &gotgbot.Update{
		Message: &gotgbot.Message{
			MessageId: 303,
			Date:      time.Now().Unix(),
			Chat:      gotgbot.Chat{Id: 123, Type: "private"},
			From:      &gotgbot.User{Id: 123, FirstName: "User"},
			Caption:   "photo caption text",
		},
	})
	assert.NoError(t, err)
	select {
	case msg := <-inbound:
		assert.Equal(t, "photo caption text", msg.Text)
	default:
		t.Fatal("expected message from caption")
	}
}

// Boost coverage for ParseSessionKey
func TestBoost_ParseSessionKey(t *testing.T) {
	tests := []struct {
		name      string
		key       string
		shouldErr bool
		expectCh  string
		expectCid int64
		expectTid int64
	}{
		{"2-part DM", "telegram:8544450322", false, "telegram", 8544450322, 0},
		{"3-part Namespaced DM", "telegram:8718145628:8544450322", false, "telegram", 8544450322, 0},
		{"3-part Legacy Topic", "telegram:-100123:456", false, "telegram", -100123, 456},
		{"3-part Legacy Zero Thread", "telegram:123:0", false, "telegram", 123, 0},
		{"4-part Topic", "telegram:8718145628:-100123:456", false, "telegram", -100123, 456},
		{"Invalid chatID", "telegram:invalid:0", true, "", 0, 0},
		{"Short key", "short", true, "", 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch, cid, tid, err := ParseSessionKey(tt.key)
			if tt.shouldErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectCh, ch)
				assert.Equal(t, tt.expectCid, cid)
				assert.Equal(t, tt.expectTid, tid)
			}
		})
	}
}

// Boost coverage for media.go
func TestBoost_MediaDownloads(t *testing.T) {
	mockServer := NewMockTelegramServer("token_boost_media")
	defer mockServer.Close()

	tmpDir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Storage.AgentsDir = tmpDir

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	mediaMgr := NewMediaManager(cfg, bot)

	// Inbound Voice & Audio & Video
	msg := &gotgbot.Message{
		MessageId: 202,
		Voice: &gotgbot.Voice{
			FileId:       "voice_1",
			FileUniqueId: "vu_1",
			MimeType:     "audio/ogg",
		},
		Audio: &gotgbot.Audio{
			FileId:       "audio_1",
			FileUniqueId: "au_1",
			FileName:     "music.mp3",
		},
		Video: &gotgbot.Video{
			FileId:       "video_1",
			FileUniqueId: "vid_1",
			FileName:     "demo.mp4",
		},
	}

	atts, err := mediaMgr.DownloadInboundMedia(context.Background(), msg)
	require.NoError(t, err)
	require.Equal(t, 3, len(atts))

	// FindBrainImage tests
	_, err = FindBrainImage("", "img")
	assert.Error(t, err)

	_, err = FindBrainImage("nonexistent_conv_id_xyz", "img")
	assert.Error(t, err)

	home, _ := os.UserHomeDir()
	convID := "boost_conv_123"
	brainDir := filepath.Join(home, ".gemini", "antigravity", "brain", convID)
	_ = os.MkdirAll(brainDir, 0755)
	defer os.RemoveAll(brainDir)

	testImg := filepath.Join(brainDir, "random_image.png")
	_ = os.WriteFile(testImg, []byte("png"), 0644)

	foundImg, err := FindBrainImage(convID, "")
	require.NoError(t, err)
	assert.Equal(t, testImg, foundImg)

	// Test nil bot or empty file path
	assert.Error(t, mediaMgr.SendBrainImage(context.Background(), 123, 0, "", "cap"))
	nilMgr := &MediaManager{}
	assert.Error(t, nilMgr.SendBrainImage(context.Background(), 123, 0, "nonexistent.jpg", "cap"))

	// Test UploadTurnArtifacts
	assert.NoError(t, mediaMgr.UploadTurnArtifacts(context.Background(), 123, 0, nil))
	assert.NoError(t, nilMgr.UploadTurnArtifacts(context.Background(), 123, 0, []domain.Attachment{{FilePath: testImg}}))

	zipPath := filepath.Join(tmpDir, "archive.zip")
	_ = os.WriteFile(zipPath, []byte("zip"), 0644)
	arts := []domain.Attachment{
		{FileName: "random.png", FilePath: testImg, Type: "image"},
		{FileName: "archive.zip", FilePath: zipPath, Type: "document"},
	}
	assert.NoError(t, mediaMgr.UploadTurnArtifacts(context.Background(), 123, 10, arts))
}

// Boost coverage for adapter.go options and send paths
func TestBoost_AdapterOptionsAndMethods(t *testing.T) {
	mockServer := NewMockTelegramServer("token_boost_adapter")
	defer mockServer.Close()

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	// Start without bot or token
	emptyAdapter := NewAdapter(&config.Config{}, nil)
	assert.Error(t, emptyAdapter.Start(context.Background(), nil))

	// Methods with uninitialized adapter
	assert.Error(t, emptyAdapter.Send(context.Background(), domain.OutboundMessage{ChatID: "123"}))
	assert.Error(t, emptyAdapter.SendChatAction(context.Background(), "123", 0, "typing"))
	assert.Error(t, emptyAdapter.SendFile(context.Background(), "123", 0, "file.txt", "cap"))

	cfg := config.DefaultConfig()
	cfg.Telegram.BotToken = "token_boost_adapter"
	cfg.Telegram.AdminUserIDs = []int64{123}

	bus := eventbus.NewEventBus(100, 2)
	defer bus.Close()

	botOpts := &gotgbot.BotOpts{}
	adapter := NewAdapter(cfg, bus, WithBotOpts(botOpts), WithBot(bot))

	inbound := make(chan domain.CanonicalMessage, 10)
	_ = adapter.Start(context.Background(), inbound)
	defer adapter.Stop()

	// OutboundMessage with HTML ParseMode and empty text
	assert.NoError(t, adapter.Send(context.Background(), domain.OutboundMessage{ChatID: "123", Text: ""}))

	outMsg := domain.OutboundMessage{
		ChatID:    "123",
		Text:      "<b>HTML text</b>",
		ParseMode: "HTML",
	}
	assert.NoError(t, adapter.Send(context.Background(), outMsg))

	// Test Send 400 Bad Request fallback in Adapter.Send
	mockServer.Simulate400Once = true
	err = adapter.Send(context.Background(), domain.OutboundMessage{
		ChatID:    "123",
		Text:      "Text with unescaped _ characters",
		ParseMode: "MarkdownV2",
	})
	assert.NoError(t, err)

	// Invalid chat IDs
	assert.Error(t, adapter.Send(context.Background(), domain.OutboundMessage{ChatID: "invalid_id", Text: "hi"}))
	assert.Error(t, adapter.SendTyping(context.Background(), "invalid_id", 0))
	assert.Error(t, adapter.SendFile(context.Background(), "invalid_id", 0, "nonexistent.jpg", "cap"))
}

// Boost coverage for throttler.go edge cases
func TestBoost_ThrottlerEdgeCases(t *testing.T) {
	mockServer := NewMockTelegramServer("token_boost_throttler")
	defer mockServer.Close()

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	// Interval <= 0 fallback
	throttler := NewDeliveryThrottler(bot, nil, 0, true)
	defer throttler.Stop()

	// Non-telegram session key in OnStreamInit
	err = throttler.OnStreamInit(context.Background(), domain.NewEvent(domain.EventStreamInit, domain.StreamInitPayload{
		SessionKey: "discord:123:0",
	}))
	assert.NoError(t, err)

	// Active session count check
	_ = throttler.OnStreamInit(context.Background(), domain.NewEvent(domain.EventStreamInit, domain.StreamInitPayload{
		SessionKey:     "telegram:123:0",
		ConversationID: "conv_count",
	}))
	assert.Equal(t, 1, throttler.ActiveSessionsCount())

	// Edge case events with unknown session
	assert.NoError(t, throttler.OnStreamDelta(context.Background(), domain.NewEvent(domain.EventStreamDelta, domain.StreamDeltaPayload{
		SessionKey: "telegram:unknown:0",
	})))
	assert.NoError(t, throttler.OnStreamTool(context.Background(), domain.NewEvent(domain.EventStreamTool, domain.StreamToolPayload{
		SessionKey: "telegram:unknown:0",
	})))
	assert.NoError(t, throttler.OnStreamResult(context.Background(), domain.NewEvent(domain.EventStreamResult, domain.StreamResultPayload{
		SessionKey: "telegram:unknown:0",
	})))
	assert.NoError(t, throttler.OnStreamError(context.Background(), domain.NewEvent(domain.EventStreamError, domain.StreamErrorPayload{
		SessionKey: "telegram:unknown:0",
	})))
}
