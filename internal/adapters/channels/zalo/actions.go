package zalo

import (
	"context"
	"time"
)

// ActionMapper maps generic action names to Zalo Bot Platform actions.
func ActionMapper(action string) string {
	switch action {
	case "typing", "text":
		return "typing"
	case "upload_photo", "photo", "image":
		return "upload_photo"
	case "upload_document", "document", "file":
		return "upload_document"
	default:
		return "typing"
	}
}

// StartTypingHeartbeat keeps sending typing actions every interval until context is done.
func StartTypingHeartbeat(ctx context.Context, client *Client, chatID string, interval time.Duration) {
	if client == nil || chatID == "" {
		return
	}
	if interval <= 0 {
		interval = 4 * time.Second
	}

	_ = client.SendChatAction(ctx, chatID, "typing")

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = client.SendChatAction(ctx, chatID, "typing")
		}
	}
}
