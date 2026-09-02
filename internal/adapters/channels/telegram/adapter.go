package telegram

import (
	"context"
	"encoding/json"
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

// Adapter implements ports.ChannelPort for Telegram messaging with multi-bot lifecycle support.
type Adapter struct {
	cfg        *config.Config
	bots       map[int64]*gotgbot.Bot     // Active bot pool keyed by Bot ID
	botConfigs map[int64]config.BotConfig // Bot configurations keyed by Bot ID
	bindAgents map[int64]string           // Dedicated agent persona bindings
	bot        *gotgbot.Bot               // Primary/fallback bot instance
	botOpts    *gotgbot.BotOpts
	eventBus   ports.EventBusPort
	throttler  *DeliveryThrottler
	mediaMgr   *MediaManager
	hitlCoord  *HITLCoordinator
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
		if bot != nil {
			if a.bots == nil {
				a.bots = make(map[int64]*gotgbot.Bot)
			}
			a.bots[bot.Id] = bot
		}
	}
}

// WithBots sets multiple existing gotgbot.Bot instances directly (e.g. for multi-bot testing).
func WithBots(bots ...*gotgbot.Bot) Option {
	return func(a *Adapter) {
		for _, b := range bots {
			if b != nil {
				if a.bots == nil {
					a.bots = make(map[int64]*gotgbot.Bot)
				}
				a.bots[b.Id] = b
				if a.bot == nil {
					a.bot = b
				}
			}
		}
	}
}

// NewAdapter constructs a new Telegram channel adapter.
func NewAdapter(cfg *config.Config, bus ports.EventBusPort, opts ...Option) *Adapter {
	a := &Adapter{
		cfg:        cfg,
		bots:       make(map[int64]*gotgbot.Bot),
		botConfigs: make(map[int64]config.BotConfig),
		bindAgents: make(map[int64]string),
		eventBus:   bus,
		hitlCoord:  NewHITLCoordinator(nil, cfg, nil),
		pollDone:   make(chan struct{}),
	}
	for _, opt := range opts {
		opt(a)
	}
	if a.bot != nil {
		a.bots[a.bot.Id] = a.bot
		if a.hitlCoord != nil {
			a.hitlCoord.SetBot(a.bot)
		}
	}
	return a
}

// Name returns the channel identifier.
func (a *Adapter) Name() string {
	return "telegram"
}

// HITLCoordinator returns the active HITL coordinator.
func (a *Adapter) HITLCoordinator() *HITLCoordinator {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.hitlCoord
}

func (a *Adapter) getBot(botID int64) *gotgbot.Bot {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if botID > 0 {
		if b, exists := a.bots[botID]; exists {
			return b
		}
	}
	return a.bot
}

// Start initializes Telegram bot pool, connects event listeners, and starts polling or webhook.
func (a *Adapter) Start(ctx context.Context, inbound chan<- domain.CanonicalMessage) error {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return errors.New("telegram adapter already running")
	}

	// 1. Initialize gotgbot.Bot pool from normalized bot configurations
	if a.cfg != nil {
		normalizedBots := a.cfg.Telegram.GetNormalizedBots()
		if len(a.bots) > 0 {
			// Mock bots injected via WithBot/WithBots for testing
			for i, bCfg := range normalizedBots {
				var matchedBot *gotgbot.Bot
				for _, b := range a.bots {
					if b.Token == bCfg.BotToken {
						matchedBot = b
						break
					}
				}
				if matchedBot != nil {
					a.botConfigs[matchedBot.Id] = bCfg
					if bCfg.BindAgent != "" {
						a.bindAgents[matchedBot.Id] = bCfg.BindAgent
					}
				} else if i == 0 && a.bot != nil {
					a.botConfigs[a.bot.Id] = bCfg
					if bCfg.BindAgent != "" {
						a.bindAgents[a.bot.Id] = bCfg.BindAgent
					}
				}
			}
		} else {
			for _, bCfg := range normalizedBots {
				bot, err := gotgbot.NewBot(bCfg.BotToken, a.botOpts)
				if err != nil {
					slog.ErrorContext(ctx, "Failed to initialize telegram bot token",
						slog.String("name", bCfg.Name),
						slog.String("error", err.Error()),
					)
					continue // Fault isolation: failed bot token does not fail other healthy bots
				}

				a.bots[bot.Id] = bot
				a.botConfigs[bot.Id] = bCfg
				if bCfg.BindAgent != "" {
					a.bindAgents[bot.Id] = bCfg.BindAgent
				}
				if a.bot == nil {
					a.bot = bot
				}
			}
		}
	}

	if len(a.bots) == 0 {
		a.mu.Unlock()
		return errors.New("no telegram bots were successfully initialized")
	}

	// 2. Initialize subsystems
	a.mediaMgr = NewMediaManager(a.cfg, a.bot)
	if a.hitlCoord == nil {
		a.hitlCoord = NewHITLCoordinator(a.bot, a.cfg, nil)
	} else {
		a.hitlCoord.SetBot(a.bot)
	}
	throttleInterval := 1.5
	streamingOn := true
	if a.cfg != nil {
		if a.cfg.AGY.StreamingThrottleIntervalSeconds > 0 {
			throttleInterval = a.cfg.AGY.StreamingThrottleIntervalSeconds
		}
		streamingOn = a.cfg.AGY.StreamingEnabled
	}
	a.throttler = NewDeliveryThrottler(a.bot, a.mediaMgr, throttleInterval, streamingOn, a.getBot)
	a.router = NewRouter(a.cfg, a.bot, inbound, a.mediaMgr, a.hitlCoord)
	a.router.SetBotBindings(a.bindAgents)

	// 3. Bind EventBus subscriptions
	if a.eventBus != nil {
		a.unsubList = append(a.unsubList,
			a.eventBus.SubscribeSync(domain.EventStreamInit, a.throttler.OnStreamInit),
			a.eventBus.SubscribeSync(domain.EventStreamDelta, a.throttler.OnStreamDelta),
			a.eventBus.SubscribeSync(domain.EventStreamTool, a.throttler.OnStreamTool),
			a.eventBus.SubscribeSync(domain.EventStreamResult, a.throttler.OnStreamResult),
			a.eventBus.SubscribeSync(domain.EventStreamError, a.throttler.OnStreamError),
			a.eventBus.SubscribeSync(domain.EventStreamInterrupted, a.throttler.OnStreamInterrupted),
		)
	}

	a.running = true
	pollCtx, cancelPoll := context.WithCancel(context.Background())
	a.cancelPoll = cancelPoll
	a.pollDone = make(chan struct{})
	a.mu.Unlock()

	// 4. Automatically register bot commands with Telegram API for all bots
	for _, bot := range a.bots {
		_ = a.RegisterCommandsForBot(pollCtx, bot, DefaultBotCommands)
	}

	// 5. Start polling or webhook
	mode := "polling"
	if a.cfg != nil && a.cfg.Telegram.Mode != "" {
		mode = a.cfg.Telegram.Mode
	}

	slog.InfoContext(ctx, "Starting Telegram channel adapter",
		slog.String("mode", mode),
		slog.Int("active_bots", len(a.bots)),
	)

	if mode == "webhook" {
		return a.startWebhook(pollCtx)
	}

	go a.startPolling(pollCtx)
	return nil
}

func (a *Adapter) startPolling(ctx context.Context) {
	defer close(a.pollDone)

	var wg sync.WaitGroup
	a.mu.RLock()
	bots := make([]*gotgbot.Bot, 0, len(a.bots))
	for _, b := range a.bots {
		bots = append(bots, b)
	}
	a.mu.RUnlock()

	for _, bot := range bots {
		wg.Add(1)
		bCfg := a.botConfigs[bot.Id]
		currentBot := bot
		go func() {
			defer wg.Done()
			a.startPollingForBot(ctx, currentBot, bCfg)
		}()
	}

	wg.Wait()
}

func (a *Adapter) startPollingForBot(ctx context.Context, bot *gotgbot.Bot, bCfg config.BotConfig) {
	defer func() {
		if r := recover(); r != nil {
			slog.ErrorContext(ctx, "Recovered from panic in bot polling worker",
				slog.Int64("bot_id", bot.Id),
				slog.String("bot_username", bot.Username),
				slog.Any("panic", r),
			)
		}
	}()

	// Delete any existing webhook before polling
	_, _ = bot.DeleteWebhook(&gotgbot.DeleteWebhookOpts{
		DropPendingUpdates: false,
		RequestOpts: &gotgbot.RequestOpts{
			Timeout: 5 * time.Second,
		},
	})

	var offset int64 = 0
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		updates, err := bot.GetUpdates(&gotgbot.GetUpdatesOpts{
			Offset:  offset,
			Limit:   100,
			Timeout: 10, // 10s server-side long polling for immediate delivery and low network/CPU overhead
			RequestOpts: &gotgbot.RequestOpts{
				Timeout: 25 * time.Second, // Generous client timeout buffer to tolerate cross-datacenter latency & TLS handshakes
			},
		})

		if err != nil {
			if errors.Is(ctx.Err(), context.Canceled) {
				return
			}
			slog.WarnContext(ctx, "Telegram polling error",
				slog.Int64("bot_id", bot.Id),
				slog.String("bot_username", bot.Username),
				slog.String("error", err.Error()),
			)
			select {
			case <-ctx.Done():
				return
			case <-time.After(500 * time.Millisecond):
				continue
			}
		}

		for _, u := range updates {
			if u.UpdateId >= offset {
				offset = u.UpdateId + 1
			}
			_ = a.router.HandleUpdate(ctx, bot, &u)
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
		var u gotgbot.Update
		if err := json.NewDecoder(r.Body).Decode(&u); err != nil {
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}
		if a.router != nil {
			_ = a.router.HandleUpdate(r.Context(), a.getBot(0), &u)
		}
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

func (a *Adapter) parseBotID(botIDStr string) int64 {
	if botIDStr == "" {
		return 0
	}
	id, _ := strconv.ParseInt(botIDStr, 10, 64)
	return id
}

// Send dispatches an outbound text message to the target chat/thread.
func (a *Adapter) Send(ctx context.Context, msg domain.OutboundMessage) error {
	botID := msg.BotID
	if botID == 0 && msg.BotIDStr != "" {
		botID = a.parseBotID(msg.BotIDStr)
	}
	bot := a.getBot(botID)
	a.mu.RLock()
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
		var domainAtts []domain.Attachment
		for _, att := range msg.Attachments {
			domainAtts = append(domainAtts, domain.Attachment{
				FileName: att.FileName,
				FilePath: att.FilePath,
				MIMEType: att.MIMEType,
				Type:     att.Type,
				Caption:  att.Caption,
			})
		}
		_ = mediaMgr.UploadTurnArtifacts(ctx, chatID, msg.ThreadID, domainAtts)
	}

	// 2. Extract any embedded media from text and clean text
	textToSend := msg.Text
	if textToSend != "" {
		cleanedText, extraMedia := ExtractAndCleanOutboundMedia(textToSend, "", "")
		if len(extraMedia) > 0 && mediaMgr != nil {
			_ = mediaMgr.UploadTurnArtifacts(ctx, chatID, msg.ThreadID, extraMedia)
		}
		textToSend = cleanedText
	}

	if textToSend == "" {
		return nil
	}

	chunks := SplitMarkdownPreservingCodeBlocks(textToSend, 4000)
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
func (a *Adapter) SendTyping(ctx context.Context, target domain.TargetContext) error {
	return a.SendChatAction(ctx, target, "typing")
}

// SendChatAction broadcasts a specific action indicator.
func (a *Adapter) SendChatAction(ctx context.Context, target domain.TargetContext, action string) error {
	botID := a.parseBotID(target.BotID)
	bot := a.getBot(botID)
	if bot == nil {
		return errors.New("bot client not initialized")
	}

	chatIDInt, err := strconv.ParseInt(target.ChatID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid chat_id %q: %w", target.ChatID, err)
	}

	opts := &gotgbot.SendChatActionOpts{}
	if target.ThreadID != 0 {
		opts.MessageThreadId = target.ThreadID
	}

	_, err = bot.SendChatAction(chatIDInt, action, opts)
	return err
}

// SendFile uploads and sends a file attachment to the chat/thread.
func (a *Adapter) SendFile(ctx context.Context, target domain.TargetContext, filePath string, caption string) error {
	botID := a.parseBotID(target.BotID)
	bot := a.getBot(botID)
	if bot == nil {
		return errors.New("bot client not initialized")
	}

	chatIDInt, err := strconv.ParseInt(target.ChatID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid chat_id %q: %w", target.ChatID, err)
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
		if target.ThreadID != 0 {
			opts.MessageThreadId = target.ThreadID
		}
		_, err = bot.SendPhoto(chatIDInt, inputFile, opts)
		return err
	}

	opts := &gotgbot.SendDocumentOpts{
		Caption: caption,
	}
	if target.ThreadID != 0 {
		opts.MessageThreadId = target.ThreadID
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
