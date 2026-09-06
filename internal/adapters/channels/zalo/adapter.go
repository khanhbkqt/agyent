package zalo

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"agyent/internal/config"
	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

var (
	_ ports.ChannelPort           = (*Adapter)(nil)
	_ ports.HITLApprovalPort      = (*Adapter)(nil)
	_ ports.AttachmentFetcherPort = (*Adapter)(nil)
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

	unsubList      []ports.UnsubscribeFunc
	streamSessions sync.Map // map[string]*zaloStreamSession

	running    atomic.Bool
	stopChan   chan struct{}
	wg         sync.WaitGroup
	httpServer *http.Server
	pollDone   chan struct{}
	authorizer ports.InboundAuthorizer

	mu sync.RWMutex
}

type zaloStreamSession struct {
	sessionKey     string
	conversationID string
	turnID         string
	chatID         string
	threadID       int64
	botID          int64
	botIDStr       string
	workspaceDir   string
	buffer         strings.Builder
	cancelTyping   context.CancelFunc
	mu             sync.Mutex
}

func (s *zaloStreamSession) stopTyping() {
	if s.cancelTyping != nil {
		s.cancelTyping()
	}
}

func parseZaloSessionKey(sessionKey string) (chatID string, threadID int64, botID int64, botIDStr string) {
	if !strings.HasPrefix(sessionKey, "zalo:") {
		return "", 0, 0, ""
	}
	parsed, err := domain.ParseSessionKey(sessionKey)
	if err == nil {
		chatID = parsed.ChatID
		threadID = parsed.ThreadID
		botID = parsed.BotID
	}
	parts := strings.Split(sessionKey, ":")
	if len(parts) == 2 {
		chatID = parts[1]
	} else if len(parts) >= 3 {
		if num, err := strconv.ParseInt(parts[1], 10, 64); err == nil && num > 0 {
			botID = num
			botIDStr = parts[1]
			chatID = parts[2]
		} else {
			botIDStr = parts[1]
			chatID = parts[2]
		}
		if len(parts) >= 4 {
			threadID, _ = strconv.ParseInt(parts[3], 10, 64)
		}
	}
	return
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
		name := strings.ToLower(strings.TrimSpace(b.Name))
		if name == "" {
			name = "default"
		}
		c := NewClient(token, cfg.Zalo.APIURL)
		inst := &botInstance{
			client:    c,
			config:    b,
			botID:     name,
			bindAgent: b.BindAgent,
		}
		adapter.bots[name] = inst
		adapter.bots[fmt.Sprintf("%d", ParseNumericID(name))] = inst

		// If token has numeric bot ID prefix (e.g. 4293721026991223652:...), map numeric ID directly
		tokenParts := strings.Split(token, ":")
		if len(tokenParts) >= 2 {
			if prefixID, err := strconv.ParseInt(tokenParts[0], 10, 64); err == nil && prefixID > 0 {
				adapter.bots[tokenParts[0]] = inst
				inst.botID = tokenParts[0]
			}
		}

		// If bind_agent is configured, map bot instance under agent name
		if b.BindAgent != "" {
			agentKey := strings.ToLower(strings.TrimSpace(b.BindAgent))
			adapter.bots[agentKey] = inst
			adapter.bindAgents[name] = b.BindAgent
			adapter.bindAgents[agentKey] = b.BindAgent
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

// SetInboundAuthorizer injects the core-owned ingress admission evaluator.
func (a *Adapter) SetInboundAuthorizer(authorizer ports.InboundAuthorizer) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.authorizer = authorizer
	if a.router != nil {
		a.router.SetInboundAuthorizer(authorizer)
	}
}

// SetURLSafetyEvaluator applies the gateway's network policy to lazy Zalo
// media downloads.
func (a *Adapter) SetURLSafetyEvaluator(evaluator ports.URLSafetyEvaluator) {
	a.mu.RLock()
	media := a.media
	a.mu.RUnlock()
	if media != nil {
		media.SetURLSafetyEvaluator(evaluator)
	}
}

// FetchAttachment lazily materializes a Zalo attachment after core admission.
func (a *Adapter) FetchAttachment(ctx context.Context, ref domain.InboundAttachmentRef, targetDir string) (domain.Attachment, error) {
	a.mu.RLock()
	media := a.media
	a.mu.RUnlock()
	if media == nil {
		return domain.Attachment{}, errors.New("zalo media manager not initialized")
	}
	return media.FetchAttachment(ctx, ref, targetDir)
}

// Start activates the Zalo channel (polling or webhook).
func (a *Adapter) Start(ctx context.Context, inbound chan<- domain.CanonicalMessage) error {
	if !a.running.CompareAndSwap(false, true) {
		return errors.New("zalo adapter is already running")
	}

	a.router = NewRouter(a.cfg, a.hitl, a.media, inbound)
	a.mu.RLock()
	authorizer := a.authorizer
	a.mu.RUnlock()
	a.router.SetInboundAuthorizer(authorizer)

	// Subscribe to EventBus streaming lifecycle events to support AGY streaming mode
	if a.eventBus != nil {
		a.mu.Lock()
		a.unsubList = []ports.UnsubscribeFunc{
			a.eventBus.SubscribeSync(domain.EventStreamInit, a.onStreamInit),
			a.eventBus.SubscribeSync(domain.EventStreamDelta, a.onStreamDelta),
			a.eventBus.SubscribeSync(domain.EventStreamResult, a.onStreamResult),
			a.eventBus.SubscribeSync(domain.EventStreamError, a.onStreamError),
			a.eventBus.SubscribeSync(domain.EventStreamInterrupted, a.onStreamInterrupted),
		}
		a.mu.Unlock()
	}

	mode := strings.ToLower(a.cfg.Zalo.Mode)
	if mode == "webhook" {
		if err := a.startWebhook(ctx); err != nil {
			_ = a.Stop()
			return err
		}
		return nil
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
		a.bots[fmt.Sprintf("%d", ParseNumericID(user.ID))] = inst
		if inst.bindAgent != "" {
			a.bots[strings.ToLower(strings.TrimSpace(inst.bindAgent))] = inst
		}
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
			if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
				return
			}
			if IsTimeoutError(err) {
				backoff = 1 * time.Second
				continue
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
			botCtx.BotName = inst.user.Name
			botCtx.BotUsername = inst.user.Username
		}
		if botCtx.BotName == "" && inst.config.Name != "" {
			botCtx.BotName = inst.config.Name
		}
		if botCtx.BotName == "" && inst.bindAgent != "" && a.cfg != nil && a.cfg.Agents != nil {
			if prof, ok := a.cfg.Agents[inst.bindAgent]; ok && prof.Name != "" {
				botCtx.BotName = prof.Name
			}
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

		// Verify Webhook Secret Token
		if a.cfg.Zalo.SecretToken != "" {
			secretHeader := r.Header.Get("X-Secret-Token")
			if secretHeader == "" {
				secretHeader = r.Header.Get("X-Bot-Token")
			}
			if subtle.ConstantTimeCompare([]byte(secretHeader), []byte(a.cfg.Zalo.SecretToken)) != 1 {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		}

		// Limit request body to 5MB to prevent memory exhaustion / OOM attacks
		r.Body = http.MaxBytesReader(w, r.Body, 5*1024*1024)

		var update ZaloUpdate
		if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}

		var botCtx []BotContext
		a.mu.RLock()
		for _, b := range a.bots {
			bc := BotContext{
				BotID:     b.botID,
				BindAgent: b.bindAgent,
			}
			if b.user != nil {
				bc.BotName = b.user.Name
				bc.BotUsername = b.user.Username
			}
			if bc.BotName == "" && b.config.Name != "" {
				bc.BotName = b.config.Name
			}
			botCtx = append(botCtx, bc)
			break
		}
		a.mu.RUnlock()

		if a.router != nil {
			a.router.RouteUpdate(r.Context(), update, botCtx...)
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
	var numID int64
	if len(numericID) > 0 {
		numID = numericID[0]
	}
	return a.resolveClientEx(botID, numID, "", "")
}

// resolveClientEx performs an exhaustive lookup for the appropriate Zalo Client
// across agent name, bot ID string, numeric bot ID, session key, and fallback.
func (a *Adapter) resolveClientEx(botIDStr string, botID int64, agentName string, sessionKey string) *Client {
	a.mu.RLock()
	defer a.mu.RUnlock()

	// 1. Check by explicit agent name (e.g. "traomofc")
	if agentName != "" {
		cleanAgent := strings.TrimPrefix(strings.TrimSpace(strings.ToLower(agentName)), "@")
		if inst, ok := a.bots[cleanAgent]; ok && inst.client != nil {
			return inst.client
		}
		for bKey, bName := range a.bindAgents {
			if strings.EqualFold(strings.TrimPrefix(bName, "@"), cleanAgent) {
				if inst, ok := a.bots[bKey]; ok && inst.client != nil {
					return inst.client
				}
			}
		}
		for _, inst := range a.bots {
			if inst != nil {
				if strings.EqualFold(strings.TrimPrefix(inst.bindAgent, "@"), cleanAgent) ||
					strings.EqualFold(strings.TrimPrefix(inst.config.BindAgent, "@"), cleanAgent) ||
					strings.EqualFold(strings.TrimPrefix(inst.config.Name, "@"), cleanAgent) {
					if inst.client != nil {
						return inst.client
					}
				}
			}
		}
	}

	// 2. Check by bot ID string (e.g. "4293721026991223652" or "default")
	if botIDStr != "" {
		key := strings.ToLower(strings.TrimSpace(botIDStr))
		if inst, ok := a.bots[key]; ok && inst.client != nil {
			return inst.client
		}
		if num := ParseNumericID(key); num > 0 {
			if inst, ok := a.bots[fmt.Sprintf("%d", num)]; ok && inst.client != nil {
				return inst.client
			}
		}
	}

	// 3. Check by numeric bot ID
	if botID > 0 {
		key := fmt.Sprintf("%d", botID)
		if inst, ok := a.bots[key]; ok && inst.client != nil {
			return inst.client
		}
	}

	// 4. Check by session key (e.g. "zalo:4293721026991223652:zgr-a4781485e9de008059cf")
	if sessionKey != "" {
		if parsed, err := domain.ParseSessionKey(sessionKey); err == nil {
			if parsed.BotID > 0 {
				key := fmt.Sprintf("%d", parsed.BotID)
				if inst, ok := a.bots[key]; ok && inst.client != nil {
					return inst.client
				}
			}
		}
		parts := strings.Split(sessionKey, ":")
		if len(parts) >= 3 {
			cand := strings.ToLower(strings.TrimSpace(parts[1]))
			if inst, ok := a.bots[cand]; ok && inst.client != nil {
				return inst.client
			}
			if num := ParseNumericID(cand); num > 0 {
				if inst, ok := a.bots[fmt.Sprintf("%d", num)]; ok && inst.client != nil {
					return inst.client
				}
			}
		}
	}

	// 5. Fallback to primary client
	if a.client != nil {
		return a.client
	}

	// 6. Fallback to "default" bot
	if inst, ok := a.bots["default"]; ok && inst.client != nil {
		return inst.client
	}

	// 7. Fallback to any first available bot in the pool
	for _, inst := range a.bots {
		if inst != nil && inst.client != nil {
			return inst.client
		}
	}

	return nil
}

// Send formats and delivers an outbound message over Zalo.
func (a *Adapter) Send(ctx context.Context, msg domain.OutboundMessage) error {
	client := a.resolveClientEx(msg.BotIDStr, msg.BotID, msg.AgentName, msg.SessionKey)
	if client == nil {
		return errors.New("zalo client is not initialized")
	}

	sentPaths := make(map[string]bool)
	var allMedia []domain.Attachment
	cleanedText := msg.Text

	// 1. Extract embedded media from markdown text
	if msg.Text != "" {
		var extraMedia []domain.Attachment
		cleanedText, extraMedia = ExtractAndCleanOutboundMedia(msg.Text, msg.WorkspaceDir, msg.ConversationID)
		for _, m := range extraMedia {
			normPath := filepath.Clean(filepath.FromSlash(m.FilePath))
			if !sentPaths[normPath] {
				sentPaths[normPath] = true
				m.FilePath = normPath
				allMedia = append(allMedia, m)
			}
		}
	}

	// 2. Collect any remaining unreferenced outbound attachments from msg.Attachments
	if len(msg.Attachments) > 0 {
		for _, att := range msg.Attachments {
			normPath := filepath.Clean(filepath.FromSlash(att.FilePath))
			if !sentPaths[normPath] {
				sentPaths[normPath] = true
				allMedia = append(allMedia, domain.Attachment{
					FileName: att.FileName,
					FilePath: normPath,
					MIMEType: att.MIMEType,
					Type:     att.Type,
					Caption:  att.Caption,
				})
			}
		}
	}

	textToSend := cleanedText

	// Deliver media attachments first
	if len(allMedia) > 0 && a.media != nil {
		for _, m := range allMedia {
			outAtt := domain.OutboundAttachment{
				FilePath: m.FilePath,
				FileName: m.FileName,
				MIMEType: m.MIMEType,
				Caption:  m.Caption,
				Type:     m.Type,
			}
			if err := a.media.SendOutboundAttachment(ctx, msg.ChatID, outAtt, client); err != nil {
				slog.ErrorContext(ctx, "failed to send Zalo outbound media", "error", err, "chat_id", msg.ChatID, "path", m.FilePath)
			}
		}
	}

	if strings.TrimSpace(textToSend) == "" {
		return nil
	}

	// Format to Zalo-compliant Markdown
	textToSend = FormatToZaloMarkdown(textToSend)

	// Sanitize privacy leaks (home paths)
	textToSend = SanitizePrivacyLeaks(textToSend)

	req := SendMessageRequest{
		ChatID:    msg.ChatID,
		Text:      textToSend,
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
	client := a.resolveClientEx(target.BotID, 0, target.AgentName, "")
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
	client := a.resolveClientEx(target.BotID, 0, target.AgentName, "")
	att := domain.OutboundAttachment{
		FilePath: filePath,
		Caption:  caption,
	}
	return a.media.SendOutboundAttachment(ctx, target.ChatID, att, client)
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

	a.mu.Lock()
	for _, unsub := range a.unsubList {
		if unsub != nil {
			unsub()
		}
	}
	a.unsubList = nil
	a.mu.Unlock()

	a.streamSessions.Range(func(key, val any) bool {
		if sess, ok := val.(*zaloStreamSession); ok {
			sess.stopTyping()
		}
		a.streamSessions.Delete(key)
		return true
	})

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

func (a *Adapter) onStreamInit(ctx context.Context, evt domain.Event) error {
	p, ok := evt.Payload.(domain.StreamInitPayload)
	if !ok {
		return nil
	}
	if !strings.HasPrefix(p.SessionKey, "zalo:") {
		return nil
	}
	chatID, threadID, botID, botIDStr := parseZaloSessionKey(p.SessionKey)
	if chatID == "" {
		return nil
	}

	if existing, loaded := a.streamSessions.LoadAndDelete(p.SessionKey); loaded {
		if s, ok := existing.(*zaloStreamSession); ok {
			s.stopTyping()
		}
	}

	typingCtx, cancelTyping := context.WithCancel(context.Background())
	sess := &zaloStreamSession{
		sessionKey:     p.SessionKey,
		conversationID: p.ConversationID,
		turnID:         p.TurnID,
		chatID:         chatID,
		threadID:       threadID,
		botID:          botID,
		botIDStr:       botIDStr,
		workspaceDir:   p.CWD,
		cancelTyping:   cancelTyping,
	}

	// Send initial typing indicator
	_ = a.SendTyping(ctx, domain.TargetContext{
		ChatID: chatID,
		BotID:  botIDStr,
	})

	a.streamSessions.Store(p.SessionKey, sess)

	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		ticker := time.NewTicker(4 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-typingCtx.Done():
				return
			case <-a.stopChan:
				return
			case <-ticker.C:
				_ = a.SendTyping(typingCtx, domain.TargetContext{
					ChatID: chatID,
					BotID:  botIDStr,
				})
			}
		}
	}()

	return nil
}

func (a *Adapter) onStreamDelta(ctx context.Context, evt domain.Event) error {
	p, ok := evt.Payload.(domain.StreamDeltaPayload)
	if !ok {
		return nil
	}
	if !strings.HasPrefix(p.SessionKey, "zalo:") {
		return nil
	}
	val, ok := a.streamSessions.Load(p.SessionKey)
	if !ok {
		return nil
	}
	sess := val.(*zaloStreamSession)
	if p.TurnID != "" && sess.turnID != "" && p.TurnID != sess.turnID {
		return nil
	}
	sess.mu.Lock()
	sess.buffer.WriteString(p.TextDelta)
	sess.mu.Unlock()
	return nil
}

func (a *Adapter) onStreamResult(ctx context.Context, evt domain.Event) error {
	p, ok := evt.Payload.(domain.StreamResultPayload)
	if !ok {
		return nil
	}
	if !strings.HasPrefix(p.SessionKey, "zalo:") {
		return nil
	}

	var chatID string
	var threadID int64
	var botID int64
	var botIDStr string
	var wsDir string
	var bufferText string

	if val, loaded := a.streamSessions.LoadAndDelete(p.SessionKey); loaded {
		sess := val.(*zaloStreamSession)
		sess.stopTyping()
		if p.TurnID != "" && sess.turnID != "" && p.TurnID != sess.turnID {
			slog.DebugContext(ctx, "ignoring stale stream result from prior turn",
				"session_key", p.SessionKey, "event_turn", p.TurnID, "active_turn", sess.turnID)
			return nil
		}
		chatID = sess.chatID
		threadID = sess.threadID
		botID = sess.botID
		botIDStr = sess.botIDStr
		wsDir = sess.workspaceDir
		sess.mu.Lock()
		bufferText = sess.buffer.String()
		sess.mu.Unlock()
	} else {
		chatID, threadID, botID, botIDStr = parseZaloSessionKey(p.SessionKey)
	}

	if chatID == "" {
		return nil
	}

	responseText := p.Response
	if strings.TrimSpace(responseText) == "" {
		responseText = bufferText
	}

	if (p.Status == "ERROR" || p.Error != "") && p.Error != "" && !strings.Contains(responseText, p.Error) {
		if strings.TrimSpace(responseText) != "" {
			responseText += "\n\n⚠️ [Execution Error: " + p.Error + "]"
		} else {
			responseText = "⚠️ [Execution Error: " + p.Error + "]"
		}
	}

	if strings.TrimSpace(responseText) == "" && len(p.Artifacts) == 0 {
		return nil
	}

	outbound := domain.OutboundMessage{
		Channel:        "zalo",
		SessionKey:     p.SessionKey,
		ChatID:         chatID,
		ThreadID:       threadID,
		BotID:          botID,
		BotIDStr:       botIDStr,
		Text:           responseText,
		WorkspaceDir:   wsDir,
		ConversationID: p.ConversationID,
	}

	for _, art := range p.Artifacts {
		outbound.Attachments = append(outbound.Attachments, domain.OutboundAttachment{
			FilePath: art.FilePath,
			FileName: art.FileName,
			MIMEType: art.MIMEType,
			Caption:  art.Caption,
		})
	}

	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()

	return a.Send(sendCtx, outbound)
}

func (a *Adapter) onStreamError(ctx context.Context, evt domain.Event) error {
	p, ok := evt.Payload.(domain.StreamErrorPayload)
	if !ok {
		return nil
	}
	if !strings.HasPrefix(p.SessionKey, "zalo:") {
		return nil
	}

	var chatID string
	var botID int64
	var botIDStr string
	if val, loaded := a.streamSessions.LoadAndDelete(p.SessionKey); loaded {
		sess := val.(*zaloStreamSession)
		sess.stopTyping()
		chatID = sess.chatID
		botID = sess.botID
		botIDStr = sess.botIDStr
	} else {
		chatID, _, botID, botIDStr = parseZaloSessionKey(p.SessionKey)
	}

	if chatID == "" {
		return nil
	}

	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	return a.Send(sendCtx, domain.OutboundMessage{
		Channel:    "zalo",
		SessionKey: p.SessionKey,
		ChatID:     chatID,
		BotID:      botID,
		BotIDStr:   botIDStr,
		Text:       fmt.Sprintf("⚠️ Execution failed: %s", p.Error),
	})
}

func (a *Adapter) onStreamInterrupted(ctx context.Context, evt domain.Event) error {
	p, ok := evt.Payload.(domain.StreamInterruptedPayload)
	if !ok {
		return nil
	}
	if !strings.HasPrefix(p.SessionKey, "zalo:") {
		return nil
	}

	val, loaded := a.streamSessions.LoadAndDelete(p.SessionKey)
	if !loaded {
		return nil
	}
	sess := val.(*zaloStreamSession)
	sess.stopTyping()

	sess.mu.Lock()
	text := sess.buffer.String()
	sess.mu.Unlock()

	if strings.TrimSpace(text) != "" {
		text += "\n\n[Turn Interrupted by User]"
	} else {
		text = "⚠️ [Turn Interrupted by User]"
	}

	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	return a.Send(sendCtx, domain.OutboundMessage{
		Channel:        "zalo",
		SessionKey:     p.SessionKey,
		ChatID:         sess.chatID,
		BotID:          sess.botID,
		BotIDStr:       sess.botIDStr,
		Text:           text,
		WorkspaceDir:   sess.workspaceDir,
		ConversationID: p.ConversationID,
	})
}

