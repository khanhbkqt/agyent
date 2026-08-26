package telegram

import (
	"context"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
)

// MapToolToChatAction returns the appropriate Telegram chat action for a given tool name.
func MapToolToChatAction(toolName string) string {
	switch toolName {
	case "generate_image":
		return "upload_photo"
	case "run_command", "replace_file_content", "write_to_file", "view_file", "list_dir", "grep_search", "find_by_name":
		return "typing"
	case "read_url_content", "search_web":
		return "typing"
	default:
		return "typing"
	}
}

// StartHeartbeatTyping starts a background goroutine that sends typing action every interval.
// Returns a cancel function to stop the heartbeat.
func StartHeartbeatTyping(ctx context.Context, bot *gotgbot.Bot, chatID int64, threadID int64, interval time.Duration) context.CancelFunc {
	if bot == nil || interval <= 0 {
		return func() {}
	}

	hbCtx, cancel := context.WithCancel(ctx)

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		// Send initial typing action immediately
		opts := &gotgbot.SendChatActionOpts{}
		if threadID != 0 {
			opts.MessageThreadId = threadID
		}
		_, _ = bot.SendChatAction(chatID, "typing", opts)

		for {
			select {
			case <-hbCtx.Done():
				return
			case <-ticker.C:
				_, _ = bot.SendChatAction(chatID, "typing", opts)
			}
		}
	}()

	return cancel
}
