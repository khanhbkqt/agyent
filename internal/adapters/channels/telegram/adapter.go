package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"agyent/internal/config"
	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

var _ ports.ChannelPort = (*Adapter)(nil)

// Adapter implements ports.ChannelPort for Telegram messaging.
type Adapter struct {
	cfg        *config.Config
	bot        *gotgbot.Bot
	botOpts    *gotgbot.BotOpts
	eventBus   ports.EventBusPort
	throttler  *DeliveryThrottler
	mediaMgr   *MediaManager
	router     *Router
	unsubList  []ports.UnsubscribeFunc
	cancelPoll context.CancelFunc
	pollDone   chan struct{}
	httpServer *http.Server

	mu      sync.RWMutex
	running bool
}

// Option configures Adapter options.
type Option func(*Adapter)

// WithBotOpts sets custom gotgbot.BotOpts (useful for mock servers).
func WithBotOpts(opts *gotgbot.BotOpts) Option {
	return func(a *Adapter) {
		a.botOpts = opts
	}
}

// WithBot sets an existing gotgbot.Bot instance directly.
func WithBot(bot *gotgbot.Bot) Option {
	return func(a *Adapter) {
		a.bot = bot
	}
}

// NewAdapter constructs a new Telegram channel adapter.
func NewAdapter(cfg *config.Config, bus ports.EventBusPort, opts ...Option) *Adapter {
	a := &Adapter{
		cfg:      cfg,
		eventBus: bus,
		pollDone: make(chan struct{}),
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Name returns the channel identifier.
func (a *Adapter) Name() string {
	return "telegram"
}

// Start initializes Telegram bot, connects event listeners, and starts polling or webhook.
func (a *Adapter) Start(ctx context.Context, inbound chan<- domain.CanonicalMessage) error {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return errors.New("telegram adapter already running")
	}

	// 1. Initialize gotgbot.Bot if not injected
	if a.bot == nil {
		if a.cfg == nil || a.cfg.Telegram.BotToken == "" {
			a.mu.Unlock()
			return errors.New("telegram bot token is missing in config")
		}
		bot, err := gotgbot.NewBot(a.cfg.Telegram.BotToken, a.botOpts)
		if err != nil {
			a.mu.Unlock()
			return fmt.Errorf("failed to create telegram bot: %w", err)
		}
		a.bot = bot
	}

	// 2. Initialize subsystems
	a.mediaMgr = NewMediaManager(a.cfg, a.bot)
	throttleInterval := 1.5
	streamingOn := true
	if a.cfg != nil {
		if a.cfg.AGY.StreamingThrottleIntervalSeconds > 0 {
			throttleInterval = a.cfg.AGY.StreamingThrottleIntervalSeconds
		}
		streamingOn = a.cfg.AGY.StreamingEnabled
	}
	a.throttler = NewDeliveryThrottler(a.bot, a.mediaMgr, throttleInterval, streamingOn)
	a.router = NewRouter(a.cfg, a.bot, inbound, a.mediaMgr)

	// 3. Bind EventBus subscriptions
	if a.eventBus != nil {
		a.unsubList = append(a.unsubList,
			a.eventBus.SubscribeSync(domain.EventStreamInit, a.throttler.OnStreamInit),
			a.eventBus.SubscribeSync(domain.EventStreamDelta, a.throttler.OnStreamDelta),
			a.eventBus.SubscribeSync(domain.EventStreamTool, a.throttler.OnStreamTool),
			a.eventBus.SubscribeSync(domain.EventStreamResult, a.throttler.OnStreamResult),
			a.eventBus.SubscribeSync(domain.EventStreamError, a.throttler.OnStreamError),
		)
	}

	a.running = true
	pollCtx, cancelPoll := context.WithCancel(context.Background())
	a.cancelPoll = cancelPoll
	a.pollDone = make(chan struct{})
	a.mu.Unlock()

	// 4. Automatically register bot commands with Telegram API
	_ = a.RegisterCommands(pollCtx, DefaultBotCommands)

	// 5. Start polling or webhook
	mode := "polling"
	if a.cfg != nil && a.cfg.Telegram.Mode != "" {
		mode = a.cfg.Telegram.Mode
	}

	slog.InfoContext(ctx, "Starting Telegram channel adapter", slog.String("mode", mode))

	if mode == "webhook" {
		return a.startWebhook(pollCtx)
	}

	go a.startPolling(pollCtx)
	return nil
}

func (a *Adapter) startPolling(ctx context.Context) {
	defer close(a.pollDone)

	// Delete any existing webhook before polling
	_, _ = a.bot.DeleteWebhook(&gotgbot.DeleteWebhookOpts{DropPendingUpdates: false})

	var offset int64 = 0
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		updates, err := a.bot.GetUpdates(&gotgbot.GetUpdatesOpts{
			Offset:  offset,
			Limit:   100,
			Timeout: 1, // Short timeout to allow rapid cancellation
		})

		if err != nil {
			slog.WarnContext(ctx, "Telegram polling error", slog.String("error", err.Error()))
			select {
			case <-ctx.Done():
				return
			case <-time.After(100 * time.Millisecond):
				continue
			}
		}

		for _, u := range updates {
			if u.UpdateId >= offset {
				offset = u.UpdateId + 1
			}
			_ = a.router.HandleUpdate(ctx, a.bot, &u)
		}
	}
}

func (a *Adapter) startWebhook(ctx context.Context) error {
	webhookPath := "/telegram/webhook"
	mux := http.NewServeMux()
	mux.HandleFunc(webhookPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		// Dispatch incoming update to router
		w.WriteHeader(http.StatusOK)
	})

	serverAddr := "127.0.0.1:8080"
	if a.cfg != nil && a.cfg.Server.Port > 0 {
		serverAddr = fmt.Sprintf("%s:%d", a.cfg.Server.Host, a.cfg.Server.Port)
	}

	slog.InfoContext(ctx, "Listening for Telegram webhook", slog.String("address", serverAddr), slog.String("path", webhookPath))

	server := &http.Server{
		Addr:    serverAddr,
		Handler: mux,
	}
	a.httpServer = server

	go func() {
		defer close(a.pollDone)
		_ = server.ListenAndServe()
	}()

	return nil
}

// Send dispatches an outbound text message to the target chat/thread.
func (a *Adapter) Send(ctx context.Context, msg domain.OutboundMessage) error {
	a.mu.RLock()
	bot := a.bot
	mediaMgr := a.mediaMgr
	a.mu.RUnlock()

	if bot == nil {
		return errors.New("bot client not initialized")
	}

	chatID, err := strconv.ParseInt(msg.ChatID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid chat_id %q: %w", msg.ChatID, err)
	}

	// 1. Send outbound attachments if present
	if len(msg.Attachments) > 0 && mediaMgr != nil {
		for _, att := range msg.Attachments {
			_ = a.SendFile(ctx, msg.ChatID, msg.ThreadID, att.FilePath, att.Caption)
		}
	}

	// 2. Send text message chunks
	if msg.Text == "" {
		return nil
	}

	chunks := SplitMarkdownPreservingCodeBlocks(msg.Text, 4000)
	for i, chunk := range chunks {
		parseMode := msg.ParseMode
		if parseMode == "" {
			parseMode = "HTML"
		}

		formatted := chunk
		if parseMode == "HTML" {
			formatted = FormatMarkdownToTelegramHTML(chunk)
		} else if parseMode == "MarkdownV2" {
			formatted = AutoCloseMarkdown(chunk)
		}

		var markup gotgbot.ReplyMarkup
		if len(msg.InlineKeyboard) > 0 && i == len(chunks)-1 {
			var tgRows [][]gotgbot.InlineKeyboardButton
			for _, row := range msg.InlineKeyboard {
				var tgRow []gotgbot.InlineKeyboardButton
				for _, btn := range row {
					tgRow = append(tgRow, gotgbot.InlineKeyboardButton{
						Text:         btn.Text,
						CallbackData: btn.CallbackData,
						Url:          btn.URL,
					})
				}
				tgRows = append(tgRows, tgRow)
			}
			markup = &gotgbot.InlineKeyboardMarkup{InlineKeyboard: tgRows}
		}

		opts := &gotgbot.SendMessageOpts{
			ParseMode:   parseMode,
			ReplyMarkup: markup,
		}
		if msg.ThreadID != 0 {
			opts.MessageThreadId = msg.ThreadID
		}
		if msg.ReplyToMessageID != "" {
			if replyID, err := strconv.ParseInt(msg.ReplyToMessageID, 10, 64); err == nil {
				opts.ReplyParameters = &gotgbot.ReplyParameters{MessageId: replyID}
			}
		}

		_, err := bot.SendMessage(chatID, formatted, opts)
		if err != nil {
			// Fallback to plain text on parse error
			var tgErr *gotgbot.TelegramError
			if errors.As(err, &tgErr) && tgErr.Code == 400 {
				slog.WarnContext(ctx, "Telegram HTML format error, retrying as plain text",
					slog.String("chat_id", msg.ChatID),
					slog.String("error", err.Error()),
				)
				opts.ParseMode = ""
				fallbackText := chunk
				if parseMode == "HTML" {
					fallbackText = StripHTMLTags(formatted)
				}
				_, err = bot.SendMessage(chatID, fallbackText, opts)
			}
			if err != nil {
				slog.ErrorContext(ctx, "Failed to send telegram message",
					slog.String("chat_id", msg.ChatID),
					slog.Int64("thread_id", msg.ThreadID),
					slog.String("error", err.Error()),
				)
				return fmt.Errorf("failed to send telegram message: %w", err)
			}
		}
	}

	return nil
}

// SendTyping broadcasts a typing indicator.
func (a *Adapter) SendTyping(ctx context.Context, chatID string, threadID int64) error {
	return a.SendChatAction(ctx, chatID, threadID, "typing")
}

// SendChatAction broadcasts a specific action indicator.
func (a *Adapter) SendChatAction(ctx context.Context, chatID string, threadID int64, action string) error {
	a.mu.RLock()
	bot := a.bot
	a.mu.RUnlock()

	if bot == nil {
		return errors.New("bot client not initialized")
	}

	chatIDInt, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid chat_id %q: %w", chatID, err)
	}

	opts := &gotgbot.SendChatActionOpts{}
	if threadID != 0 {
		opts.MessageThreadId = threadID
	}

	_, err = bot.SendChatAction(chatIDInt, action, opts)
	return err
}

// SendFile uploads and sends a file attachment to the chat/thread.
func (a *Adapter) SendFile(ctx context.Context, chatID string, threadID int64, filePath string, caption string) error {
	a.mu.RLock()
	bot := a.bot
	a.mu.RUnlock()

	if bot == nil {
		return errors.New("bot client not initialized")
	}

	chatIDInt, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid chat_id %q: %w", chatID, err)
	}

	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", filePath, err)
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(filePath))
	inputFile := &gotgbot.FileReader{Name: filepath.Base(filePath), Data: file}

	if ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".webp" || ext == ".gif" {
		opts := &gotgbot.SendPhotoOpts{
			Caption: caption,
		}
		if threadID != 0 {
			opts.MessageThreadId = threadID
		}
		_, err = bot.SendPhoto(chatIDInt, inputFile, opts)
		return err
	}

	opts := &gotgbot.SendDocumentOpts{
		Caption: caption,
	}
	if threadID != 0 {
		opts.MessageThreadId = threadID
	}
	_, err = bot.SendDocument(chatIDInt, inputFile, opts)
	return err
}

// Stop gracefully shuts down network connections, active streams, and listeners.
func (a *Adapter) Stop() error {
	a.mu.Lock()
	if !a.running {
		a.mu.Unlock()
		return nil
	}
	a.running = false

	if a.cancelPoll != nil {
		a.cancelPoll()
	}

	if a.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = a.httpServer.Shutdown(ctx)
	}

	// Drain throttler active streams
	if a.throttler != nil {
		a.throttler.Stop()
	}

	// Unsubscribe from EventBus
	for _, unsub := range a.unsubList {
		unsub()
	}
	a.unsubList = nil
	a.mu.Unlock()

	// Wait for polling loop to exit
	select {
	case <-a.pollDone:
	case <-time.After(3 * time.Second):
	}

	return nil
}
