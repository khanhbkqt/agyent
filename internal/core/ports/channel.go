package ports

import (
	"context"

	"agyent/internal/core/domain"
)

// ChannelPort defines the communication abstraction for messaging platforms (e.g. Telegram, Discord, Slack).
type ChannelPort interface {
	// Name returns the identifier of the messaging adapter (e.g., "telegram").
	Name() string

	// Start initializes long-polling or webhook listening and pushes inbound messages to the channel.
	Start(ctx context.Context, inbound chan<- domain.CanonicalMessage) error

	// Send dispatches an outbound text message to the target chat/thread.
	Send(ctx context.Context, msg domain.OutboundMessage) error

	// SendTyping broadcasts a typing indicator to keep the user engaged during execution.
	SendTyping(ctx context.Context, target domain.TargetContext) error

	// SendChatAction broadcasts a specific action indicator (e.g. "typing", "upload_photo", "upload_document").
	SendChatAction(ctx context.Context, target domain.TargetContext, action string) error

	// SendFile uploads and sends a file attachment (image, document, archive) to the chat/thread.
	SendFile(ctx context.Context, target domain.TargetContext, filePath string, caption string) error

	// Stop gracefully shuts down network connections and listeners.
	Stop() error
}
