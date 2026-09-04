package zalo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"agyent/internal/config"
	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

var (
	_ ports.ChannelPort      = (*Adapter)(nil)
	_ ports.HITLApprovalPort = (*Adapter)(nil)
)

type botInstance struct {
	client    *Client
	config    config.BotConfig
	user      *ZaloUser
	botID     string
	bindAgent string
}

// Adapter implements ports.ChannelPort and ports.HITLApprovalPort for Zalo Bot Platform with multi-bot support.
type Adapter struct {
	cfg        *config.Config
	client     *Client                 // Primary fallback client
	bots       map[string]*botInstance // Keyed by bot name and bot ID
	bindAgents map[string]string       // Keyed by bot identifier
	router     *Router
	hitl       *HITLCoordinator
	media      *MediaManager
	throttler  *Throttler
	eventBus   ports.EventBusPort

	running    atomic.Bool
	stopChan   chan struct{}
	wg         sync.WaitGroup
	httpServer *http.Server
	pollDone   chan struct{}

	mu sync.RWMutex
}

// NewAdapter initializes a new Zalo channel adapter.
func NewAdapter(cfg *config.Config, bus ports.EventBusPort) (*Adapter, error) {
	if cfg == nil {
		return nil, errors.New("config cannot be nil")
	}

	normalizedBots := cfg.Zalo.GetNormalizedBots()
	if len(normalizedBots) == 0 {
		return nil, errors.New("no Zalo bot configured in settings")
	}

	primaryToken := strings.TrimSpace(normalizedBots[0].BotToken)
	primaryClient := NewClient(primaryToken, cfg.Zalo.APIURL)

	mediaMgr, err := NewMediaManager(primaryClient, cfg.Storage.AgentsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create Zalo media manager: %w", err)
	}

	hitl := NewHITLCoordinator(primaryClient, cfg)
	throttler := NewThrottler(primaryClient, 500*time.Millisecond)

	adapter := &Adapter{
		cfg:        cfg,
		client:     primaryClient,
		bots:       make(map[string]*botInstance),
		bindAgents: make(map[string]string),
		hitl:       hitl,
		media:      mediaMgr,
		throttler:  throttler,
		eventBus:   bus,
		stopChan:   make(chan struct{}),
		pollDone:   make(chan struct{}),
	}

	// Initialize all configured bots
	for _, b := range normalizedBots {
		token := strings.TrimSpace(b.BotToken)
		if token == "" {
			continue
		}
		c := NewClient(token, cfg.Zalo.APIURL)
		inst := &botInstance{
			client:    c,
			config:    b,
			bindAgent: b.BindAgent,
		}
		name := strings.ToLower(strings.TrimSpace(b.Name))
		if name == "" {
			name = "default"
		}
		adapter.bots[name] = inst
		if b.BindAgent != "" {
			adapter.bindAgents[name] = b.BindAgent
		}
	}

	return adapter, nil
}

// Name returns the unique channel identifier.
func (a *Adapter) Name() string {
	return "zalo"
}

// HITLCoordinator returns the Zalo HITL coordinator.
func (a *Adapter) HITLCoordinator() *HITLCoordinator {
	return a.hitl
}

// Start activates the Zalo channel (polling or webhook).
func (a *Adapter) Start(ctx context.Context, inbound chan<- domain.CanonicalMessage) error {
	if !a.running.CompareAndSwap(false, true) {
		return errors.New("zalo adapter is already running")
	}

	a.router = NewRouter(a.cfg, a.hitl, a.media, inbound)

	mode := strings.ToLower(a.cfg.Zalo.Mode)
	if mode == "webhook" {
		return a.startWebhook(ctx)
	}

	// Default mode: Polling
	a.startPolling(ctx)
	slog.Info("Zalo adapter started in polling mode", "api_url", a.cfg.Zalo.APIURL, "bot_count", len(a.bots))
	return nil
}

func (a *Adapter) startPolling(ctx context.Context) {
	// Start polling loop for each unique bot client
	polledTokens := make(map[string]bool)

	for name, inst := range a.bots {
		token := inst.client.BotToken()
		if polledTokens[token] {
			continue
		}
		polledTokens[token] = true

		a.wg.Add(1)
		go a.pollBotUpdates(ctx, name, inst)
	}
}

func (a *Adapter) pollBotUpdates(ctx context.Context, botName string, inst *botInstance) {
	defer a.wg.Done()

	// Initial authentication check (optional probe)
	if user, err := inst.client.GetMe(ctx); err == nil {
		inst.user = user
		inst.botID = user.ID
		a.mu.Lock()
		a.bots[user.ID] = inst
		a.mu.Unlock()
		slog.Info("Zalo bot authenticated", "bot_name", botName, "user_id", user.ID, "display_name", user.Name)
	} else {
		slog.Warn("failed to fetch Zalo bot profile during startup", "bot_name", botName, "error", err)
	}

	var offset int64 = 0
	backoff := 1 * time.Second

	for {
		select {
		case <-a.stopChan:
			return
		case <-ctx.Done():
			return
		default:
		}

		updates, err := inst.client.GetUpdates(ctx, offset, 50, 10)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			slog.Warn("failed to poll Zalo updates, backing off", "bot_name", botName, "error", err, "backoff", backoff)
			select {
			case <-time.After(backoff):
			case <-a.stopChan:
				return
			case <-ctx.Done():
				return
			}
			if backoff < 15*time.Second {
				backoff *= 2
			}
			continue
		}

		backoff = 1 * time.Second

		botCtx := BotContext{
			BotID:     inst.botID,
			BindAgent: inst.bindAgent,
		}
		if inst.user != nil {
			botCtx.BotUsername = inst.user.Username
		}

		for _, u := range updates {
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
			if a.router != nil {
				a.router.RouteUpdate(ctx, u, botCtx)
			}
		}
	}
}

func (a *Adapter) startWebhook(ctx context.Context) error {
	webhookPath := "/zalo/webhook"
	if a.cfg.Zalo.WebhookURL != "" {
		if parsed, err := url.Parse(a.cfg.Zalo.WebhookURL); err == nil && parsed.Path != "" {
			webhookPath = parsed.Path
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc(webhookPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}

		// Secret verification if configured
		if a.cfg.Zalo.SecretToken != "" {
			secretHeader := r.Header.Get("X-Secret-Token")
			if secretHeader == "" {
				secretHeader = r.Header.Get("X-Bot-Token")
			}
			if secretHeader != a.cfg.Zalo.SecretToken {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		}

		var update ZaloUpdate
		if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}

		if a.router != nil {
			a.router.RouteUpdate(r.Context(), update)
		}
		w.WriteHeader(http.StatusOK)
	})

	serverAddr := "127.0.0.1:8080"
	if a.cfg != nil && a.cfg.Server.Port > 0 {
		serverAddr = fmt.Sprintf("%s:%d", a.cfg.Server.Host, a.cfg.Server.Port)
	}

	server := &http.Server{
		Addr:    serverAddr,
		Handler: mux,
	}
	a.httpServer = server

	go func() {
		defer close(a.pollDone)
		_ = server.ListenAndServe()
	}()

	// Register webhook with Zalo Bot Platform
	if err := a.client.SetWebhook(ctx, a.cfg.Zalo.WebhookURL, a.cfg.Zalo.SecretToken); err != nil {
		return fmt.Errorf("failed to register Zalo webhook: %w", err)
	}

	slog.Info("Zalo adapter started in webhook mode", "listen_addr", serverAddr, "path", webhookPath, "webhook_url", a.cfg.Zalo.WebhookURL)
	return nil
}

// resolveClient picks the appropriate Zalo Client based on botID or fallback.
func (a *Adapter) resolveClient(botID string, numericID ...int64) *Client {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if botID != "" {
		if inst, ok := a.bots[strings.ToLower(botID)]; ok && inst.client != nil {
			return inst.client
		}
	}
	if len(numericID) > 0 && numericID[0] > 0 {
		key := fmt.Sprintf("%d", numericID[0])
		if inst, ok := a.bots[key]; ok && inst.client != nil {
			return inst.client
		}
	}
	return a.client
}

// Send formats and delivers an outbound message over Zalo.
func (a *Adapter) Send(ctx context.Context, msg domain.OutboundMessage) error {
	client := a.resolveClient(msg.BotIDStr, msg.BotID)
	if client == nil {
		return errors.New("zalo client is not initialized")
	}

	text := msg.Text

	// If message was constructed with HTML parse mode, convert it to Zalo Markdown
	if msg.ParseMode == "HTML" || strings.Contains(text, "<") {
		text = ConvertHTMLToZaloMarkdown(text)
	}

	// Sanitize privacy leaks (home paths)
	text = SanitizePrivacyLeaks(text)

	// Send Attachments if present
	for _, att := range msg.Attachments {
		if a.media != nil {
			if err := a.media.SendOutboundAttachment(ctx, msg.ChatID, att); err != nil {
				slog.ErrorContext(ctx, "failed to send Zalo attachment", "error", err, "chat_id", msg.ChatID)
			}
		}
	}

	if strings.TrimSpace(text) == "" {
		return nil
	}

	req := SendMessageRequest{
		ChatID:    msg.ChatID,
		Text:      text,
		ParseMode: "markdown",
		ReplyToID: msg.ReplyToMessageID,
	}

	return a.throttler.SendThrottled(ctx, req, client)
}

// SendTyping triggers the typing chat action.
func (a *Adapter) SendTyping(ctx context.Context, target domain.TargetContext) error {
	return a.SendChatAction(ctx, target, "typing")
}

// SendChatAction sends a custom chat action.
func (a *Adapter) SendChatAction(ctx context.Context, target domain.TargetContext, action string) error {
	client := a.resolveClient(target.BotID)
	if client == nil {
		return nil
	}
	mapped := ActionMapper(action)
	return client.SendChatAction(ctx, target.ChatID, mapped)
}

// SendFile uploads and sends a file with optional caption.
func (a *Adapter) SendFile(ctx context.Context, target domain.TargetContext, filePath string, caption string) error {
	if a.media == nil {
		return errors.New("media manager not initialized")
	}
	att := domain.OutboundAttachment{
		FilePath: filePath,
		Caption:  caption,
	}
	return a.media.SendOutboundAttachment(ctx, target.ChatID, att)
}

// RequestApproval coordinates an interactive approval request over Zalo.
func (a *Adapter) RequestApproval(ctx context.Context, req domain.ApprovalRequest) (domain.ApprovalDecision, error) {
	if a.hitl == nil {
		return domain.ApprovalDecision{}, errors.New("zalo hitl coordinator not initialized")
	}
	return a.hitl.RequestApproval(ctx, req)
}

// HandleCallback processes inline keyboard callbacks if supported.
func (a *Adapter) HandleCallback(ctx context.Context, callbackID string, userID int64, action string) error {
	if a.hitl == nil {
		return nil
	}
	return a.hitl.HandleCallback(ctx, callbackID, userID, action)
}

// CancelPendingRequest terminates a pending approval request.
func (a *Adapter) CancelPendingRequest(requestID string) {
	if a.hitl != nil {
		a.hitl.CancelPendingRequest(requestID)
	}
}

// CancelPendingRequestsForSession terminates all pending requests for a given session.
func (a *Adapter) CancelPendingRequestsForSession(sessionKey string) {
	if a.hitl != nil {
		a.hitl.CancelPendingRequestsForSession(sessionKey)
	}
}

// Stop terminates polling, webhooks, and cleans up Zalo adapter resources.
func (a *Adapter) Stop() error {
	if !a.running.CompareAndSwap(true, false) {
		return nil
	}

	close(a.stopChan)

	if a.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = a.httpServer.Shutdown(ctx)
	}

	// Wait with 2.0s timeout for polling loops to exit cleanly
	done := make(chan struct{})
	go func() {
		a.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2000 * time.Millisecond):
		slog.Warn("zalo adapter shutdown timed out, proceeding")
	}

	slog.Info("Zalo adapter stopped successfully")
	return nil
}
