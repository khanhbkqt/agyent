package telegram

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
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
	httpServer  *http.Server
	secretToken string

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

// WithMediaManager sets a custom MediaManager (useful for unit tests).
func WithMediaManager(mm *MediaManager) Option {
	return func(a *Adapter) {
		a.mediaMgr = mm
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
	if a.mediaMgr == nil {
		a.mediaMgr = NewMediaManager(cfg, a.bot)
		a.mediaMgr.SetBotGetter(a.getBot)
		a.mediaMgr.SetBotByAgentGetter(a.getBotByAgent)
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

// MediaManager returns the active MediaManager instance.
func (a *Adapter) MediaManager() *MediaManager {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.mediaMgr
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

func (a *Adapter) getBotByAgent(agentName string) *gotgbot.Bot {
	a.mu.RLock()
	defer a.mu.RUnlock()

	cleanTarget := strings.TrimPrefix(strings.TrimSpace(strings.ToLower(agentName)), "@")
	if cleanTarget == "" {
		return a.bot
	}

	// 1. Check explicit agent bindings (bindAgents)
	for botID, name := range a.bindAgents {
		cleanName := strings.TrimPrefix(strings.TrimSpace(strings.ToLower(name)), "@")
		if cleanName == cleanTarget {
			if b, exists := a.bots[botID]; exists {
				return b
			}
		}
	}

	// 2. Check bot configs (BindAgent or Name)
	for botID, bCfg := range a.botConfigs {
		cleanBind := strings.TrimPrefix(strings.TrimSpace(strings.ToLower(bCfg.BindAgent)), "@")
		cleanCfgName := strings.TrimPrefix(strings.TrimSpace(strings.ToLower(bCfg.Name)), "@")
		if cleanBind == cleanTarget || cleanCfgName == cleanTarget {
			if b, exists := a.bots[botID]; exists {
				return b
			}
		}
	}

	// 3. Check bot usernames (e.g. wife_assistant_bot matching wife_assistant)
	for _, b := range a.bots {
		if b == nil {
			continue
		}
		cleanUser := strings.TrimPrefix(strings.TrimSpace(strings.ToLower(b.Username)), "@")
		if cleanUser == cleanTarget || cleanUser == cleanTarget+"_bot" || cleanUser == cleanTarget+"bot" {
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
				agentBinding := bCfg.BindAgent
				if agentBinding == "" && bCfg.Name != "" && bCfg.Name != "default" {
					agentBinding = bCfg.Name
				}
				if matchedBot != nil {
					a.botConfigs[matchedBot.Id] = bCfg
					if agentBinding != "" {
						a.bindAgents[matchedBot.Id] = agentBinding
					}
				} else if i == 0 && a.bot != nil {
					a.botConfigs[a.bot.Id] = bCfg
					if agentBinding != "" {
						a.bindAgents[a.bot.Id] = agentBinding
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
				agentBinding := bCfg.BindAgent
				if agentBinding == "" && bCfg.Name != "" && bCfg.Name != "default" {
					agentBinding = bCfg.Name
				}
				if agentBinding != "" {
					a.bindAgents[bot.Id] = agentBinding
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
	a.mediaMgr.SetBotGetter(a.getBot)
	a.mediaMgr.SetBotByAgentGetter(a.getBotByAgent)
	if a.hitlCoord == nil {
		a.hitlCoord = NewHITLCoordinator(a.bot, a.cfg, nil)
	} else {
		a.hitlCoord.SetBot(a.bot)
	}
	a.hitlCoord.SetBotGetter(a.getBot)
	a.hitlCoord.SetBotByAgentGetter(a.getBotByAgent)
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

// WebhookHandler returns the http.Handler serving the Telegram webhook endpoints.
func (a *Adapter) WebhookHandler() http.Handler {
	a.mu.Lock()
	if a.secretToken == "" {
		if a.cfg != nil && a.cfg.Telegram.SecretToken != "" {
			a.secretToken = a.cfg.Telegram.SecretToken
		} else {
			tokenBytes := make([]byte, 32)
			if _, err := rand.Read(tokenBytes); err == nil {
				a.secretToken = hex.EncodeToString(tokenBytes)
			}
		}
	}
	secretToken := a.secretToken
	a.mu.Unlock()

	mux := http.NewServeMux()
	handleWebhookUpdate := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}

		if secretToken != "" {
			providedToken := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
			if subtle.ConstantTimeCompare([]byte(providedToken), []byte(secretToken)) != 1 {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB body limit to prevent memory DoS
		var u gotgbot.Update
		if err := json.NewDecoder(r.Body).Decode(&u); err != nil {
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}

		// Resolve target bot by URL path /telegram/webhook/{bot} or fallback to default
		var targetBot *gotgbot.Bot
		pathPart := strings.TrimPrefix(r.URL.Path, "/telegram/webhook")
		pathPart = strings.Trim(pathPart, "/")
		if pathPart != "" {
			targetBot = a.getBotByAgent(pathPart)
			if targetBot == nil {
				if id, err := strconv.ParseInt(pathPart, 10, 64); err == nil {
					targetBot = a.getBot(id)
				}
			}
		}
		if targetBot == nil {
			targetBot = a.getBot(0)
		}

		if a.router != nil && targetBot != nil {
			_ = a.router.HandleUpdate(r.Context(), targetBot, &u)
		}
		w.WriteHeader(http.StatusOK)
	}

	mux.HandleFunc("/telegram/webhook", handleWebhookUpdate)
	mux.HandleFunc("/telegram/webhook/", handleWebhookUpdate)
	return mux
}

func (a *Adapter) startWebhook(ctx context.Context) error {
	handler := a.WebhookHandler()

	a.mu.RLock()
	secretToken := a.secretToken
	a.mu.RUnlock()

	serverAddr := "127.0.0.1:8080"
	if a.cfg != nil && a.cfg.Server.Port > 0 {
		serverAddr = fmt.Sprintf("%s:%d", a.cfg.Server.Host, a.cfg.Server.Port)
	}

	listener, err := net.Listen("tcp", serverAddr)
	if err != nil {
		return fmt.Errorf("failed to bind telegram webhook server on %s: %w", serverAddr, err)
	}

	slog.InfoContext(ctx, "Listening for Telegram webhook",
		slog.String("address", serverAddr),
		slog.String("path", "/telegram/webhook"),
		slog.Bool("secret_token_enforced", secretToken != ""),
	)

	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	a.httpServer = server

	// Register Webhook with Telegram API if WebhookURL is configured
	if a.cfg != nil && a.cfg.Telegram.WebhookURL != "" {
		for _, bot := range a.bots {
			webhookURL := a.cfg.Telegram.WebhookURL
			bCfg := a.botConfigs[bot.Id]
			if bCfg.Name != "" && bCfg.Name != "default" {
				webhookURL = strings.TrimSuffix(webhookURL, "/") + "/" + bCfg.Name
			}
			opts := &gotgbot.SetWebhookOpts{
				DropPendingUpdates: false,
				SecretToken:        secretToken,
				RequestOpts: &gotgbot.RequestOpts{
					Timeout: 10 * time.Second,
				},
			}
			if _, setErr := bot.SetWebhook(webhookURL, opts); setErr != nil {
				slog.WarnContext(ctx, "Failed to register Telegram webhook with API",
					slog.Int64("bot_id", bot.Id),
					slog.String("webhook_url", webhookURL),
					slog.String("error", setErr.Error()),
				)
			} else {
				slog.InfoContext(ctx, "Registered Telegram webhook with API",
					slog.Int64("bot_id", bot.Id),
					slog.String("webhook_url", webhookURL),
				)
			}
		}
	}

	go func() {
		defer close(a.pollDone)
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			slog.ErrorContext(ctx, "Telegram webhook server failure", slog.String("error", serveErr.Error()))
		}
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
	var bot *gotgbot.Bot

	// 1. If an explicit AgentName is provided, prefer the dedicated bot bound to that agent.
	// This ensures scheduled tasks, heartbeats, and agent-specific notifications
	// are always delivered by the agent's own bot.
	if msg.AgentName != "" {
		if agentBot := a.getBotByAgent(msg.AgentName); agentBot != nil && (agentBot != a.bot || len(a.bots) == 1) {
			bot = agentBot
		}
	}

	// 2. If no agent-specific bot was resolved, use explicit BotID if present.
	if bot == nil {
		botID := msg.BotID
		if botID == 0 && msg.BotIDStr != "" {
			botID = a.parseBotID(msg.BotIDStr)
		}
		if botID > 0 {
			bot = a.getBot(botID)
		}
	}

	// 3. Fallback to default bot
	if bot == nil {
		bot = a.getBot(0)
	}

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

	sentPaths := make(map[string]bool)

	// 1. Extract any embedded media from text and clean text first (preserves human markdown captions)
	textToSend := msg.Text
	if textToSend != "" {
		cleanedText, extraMedia := ExtractAndCleanOutboundMedia(textToSend, msg.WorkspaceDir, msg.ConversationID)
		var unuploadedMedia []domain.Attachment
		for _, m := range extraMedia {
			normPath := filepath.Clean(filepath.FromSlash(m.FilePath))
			if !sentPaths[normPath] {
				sentPaths[normPath] = true
				m.FilePath = normPath
				unuploadedMedia = append(unuploadedMedia, m)
			}
		}
		if len(unuploadedMedia) > 0 && mediaMgr != nil {
			if err := mediaMgr.UploadTurnArtifacts(ctx, chatID, msg.ThreadID, unuploadedMedia, bot); err != nil {
				slog.ErrorContext(ctx, "Failed to upload embedded media attachments", "chat_id", chatID, "error", err)
			}
		}
		textToSend = cleanedText
	}

	// 2. Send any remaining unreferenced outbound attachments from msg.Attachments
	if len(msg.Attachments) > 0 && mediaMgr != nil {
		var domainAtts []domain.Attachment
		for _, att := range msg.Attachments {
			normPath := filepath.Clean(filepath.FromSlash(att.FilePath))
			if !sentPaths[normPath] {
				sentPaths[normPath] = true
				domainAtts = append(domainAtts, domain.Attachment{
					FileName: att.FileName,
					FilePath: normPath,
					MIMEType: att.MIMEType,
					Type:     att.Type,
					Caption:  att.Caption,
				})
			}
		}
		if len(domainAtts) > 0 {
			if err := mediaMgr.UploadTurnArtifacts(ctx, chatID, msg.ThreadID, domainAtts, bot); err != nil {
				slog.ErrorContext(ctx, "Failed to upload outbound message attachments", "chat_id", chatID, "error", err)
			}
		}
	}

	if textToSend == "" {
		return nil
	}

	chunks := SplitMarkdownPreservingCodeBlocks(textToSend, 4000)
	for i, chunk := range chunks {
		targetParseMode := "HTML"
		formatted := chunk

		if msg.ParseMode == "MarkdownV2" {
			targetParseMode = "MarkdownV2"
			formatted = AutoCloseMarkdown(chunk)
		} else {
			targetParseMode = "HTML"
			formatted = FormatMarkdownToTelegramHTML(chunk)
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
			ParseMode:   targetParseMode,
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
				if targetParseMode == "HTML" {
					fallbackText = StripHTMLTags(formatted)
				}
				_, err = bot.SendMessage(chatID, fallbackText, opts)
			}
			if err != nil && a.bot != nil && bot != a.bot {
				slog.WarnContext(ctx, "Failed to send message via agent-specific bot, falling back to primary bot",
					slog.String("agent", msg.AgentName),
					slog.String("chat_id", msg.ChatID),
					slog.String("error", err.Error()),
				)
				_, err = a.bot.SendMessage(chatID, formatted, opts)
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
	var bot *gotgbot.Bot
	if target.AgentName != "" {
		if agentBot := a.getBotByAgent(target.AgentName); agentBot != nil && (agentBot != a.bot || len(a.bots) == 1) {
			bot = agentBot
		}
	}
	if bot == nil {
		botID := a.parseBotID(target.BotID)
		if botID > 0 {
			bot = a.getBot(botID)
		}
	}
	if bot == nil {
		bot = a.getBot(0)
	}
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
	if err != nil && a.bot != nil && bot != a.bot {
		_, err = a.bot.SendChatAction(chatIDInt, action, opts)
	}
	return err
}

// SendFile uploads and sends a file attachment to the chat/thread.
func (a *Adapter) SendFile(ctx context.Context, target domain.TargetContext, filePath string, caption string) error {
	var bot *gotgbot.Bot
	if target.AgentName != "" {
		if agentBot := a.getBotByAgent(target.AgentName); agentBot != nil && (agentBot != a.bot || len(a.bots) == 1) {
			bot = agentBot
		}
	}
	if bot == nil {
		botID := a.parseBotID(target.BotID)
		if botID > 0 {
			bot = a.getBot(botID)
		}
	}
	if bot == nil {
		bot = a.getBot(0)
	}
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
		if err != nil && a.bot != nil && bot != a.bot {
			if fileFallback, oErr := os.Open(filePath); oErr == nil {
				defer fileFallback.Close()
				inputFileFallback := &gotgbot.FileReader{Name: filepath.Base(filePath), Data: fileFallback}
				_, err = a.bot.SendPhoto(chatIDInt, inputFileFallback, opts)
			}
		}
		return err
	}

	opts := &gotgbot.SendDocumentOpts{
		Caption: caption,
	}
	if target.ThreadID != 0 {
		opts.MessageThreadId = target.ThreadID
	}
	_, err = bot.SendDocument(chatIDInt, inputFile, opts)
	if err != nil && a.bot != nil && bot != a.bot {
		if fileFallback, oErr := os.Open(filePath); oErr == nil {
			defer fileFallback.Close()
			inputFileFallback := &gotgbot.FileReader{Name: filepath.Base(filePath), Data: fileFallback}
			_, err = a.bot.SendDocument(chatIDInt, inputFileFallback, opts)
		}
	}
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
