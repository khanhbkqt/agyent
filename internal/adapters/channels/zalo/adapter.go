package zalo

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"agyent/internal/config"
	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

// Adapter implements ports.ChannelPort for Zalo Bot Platform.
type Adapter struct {
	cfg       *config.Config
	client    *Client
	router    *Router
	hitl      *HITLCoordinator
	media     *MediaManager
	throttler *Throttler
	scheduler *VietnamScheduler
	eventBus  ports.EventBusPort

	running  atomic.Bool
	stopChan chan struct{}
	wg       sync.WaitGroup
}

// NewAdapter initializes a new Zalo channel adapter.
func NewAdapter(cfg *config.Config, bus ports.EventBusPort) (*Adapter, error) {
	if cfg == nil {
		return nil, errors.New("config cannot be nil")
	}

	botToken := strings.TrimSpace(cfg.Zalo.BotToken)
	bots := cfg.Zalo.GetNormalizedBots()
	if len(bots) > 0 && botToken == "" {
		botToken = bots[0].BotToken
	}

	client := NewClient(botToken, cfg.Zalo.APIURL)
	mediaMgr, err := NewMediaManager(client, cfg.Storage.AgentsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create Zalo media manager: %w", err)
	}

	hitl := NewHITLCoordinator(client, cfg)
	throttler := NewThrottler(client, 500*time.Millisecond)
	scheduler := NewVietnamScheduler()

	return &Adapter{
		cfg:       cfg,
		client:    client,
		hitl:      hitl,
		media:     mediaMgr,
		throttler: throttler,
		scheduler: scheduler,
		eventBus:  bus,
		stopChan:  make(chan struct{}),
	}, nil
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
	a.scheduler.Start(ctx)

	mode := strings.ToLower(a.cfg.Zalo.Mode)
	if mode == "" || mode == "polling" {
		a.wg.Add(1)
		go a.startPolling(ctx)
		slog.Info("Zalo adapter started in polling mode", "api_url", a.cfg.Zalo.APIURL)
	} else if mode == "webhook" {
		err := a.client.SetWebhook(ctx, a.cfg.Zalo.WebhookURL, a.cfg.Zalo.SecretToken)
		if err != nil {
			return fmt.Errorf("failed to configure Zalo webhook: %w", err)
		}
		slog.Info("Zalo adapter registered webhook", "url", a.cfg.Zalo.WebhookURL)
	}

	return nil
}

// Send formats and delivers an outbound message over Zalo.
func (a *Adapter) Send(ctx context.Context, msg domain.OutboundMessage) error {
	if a.client == nil {
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
		if err := a.media.SendOutboundAttachment(ctx, msg.ChatID, att); err != nil {
			slog.ErrorContext(ctx, "failed to send Zalo attachment", "error", err, "chat_id", msg.ChatID)
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

	return a.throttler.SendThrottled(ctx, req)
}

// SendTyping triggers the typing chat action.
func (a *Adapter) SendTyping(ctx context.Context, chatID string, threadID int64) error {
	if a.client == nil {
		return nil
	}
	return a.client.SendChatAction(ctx, chatID, "typing")
}

// SendChatAction sends a custom chat action.
func (a *Adapter) SendChatAction(ctx context.Context, chatID string, threadID int64, action string) error {
	if a.client == nil {
		return nil
	}
	mapped := ActionMapper(action)
	return a.client.SendChatAction(ctx, chatID, mapped)
}

// SendFile uploads and sends a file with optional caption.
func (a *Adapter) SendFile(ctx context.Context, chatID string, threadID int64, filePath string, caption string) error {
	if a.media == nil {
		return errors.New("media manager not initialized")
	}
	att := domain.OutboundAttachment{
		FilePath: filePath,
		Caption:  caption,
	}
	return a.media.SendOutboundAttachment(ctx, chatID, att)
}

// Stop terminates long polling and cleans up Zalo adapter resources.
func (a *Adapter) Stop() error {
	if !a.running.CompareAndSwap(true, false) {
		return nil
	}

	close(a.stopChan)
	a.scheduler.Stop()

	// Wait with 1.5s timeout for polling loop to exit cleanly
	done := make(chan struct{})
	go func() {
		a.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(1500 * time.Millisecond):
		slog.Warn("zalo adapter shutdown timed out, proceeding")
	}

	slog.Info("Zalo adapter stopped successfully")
	return nil
}

func (a *Adapter) startPolling(ctx context.Context) {
	defer a.wg.Done()

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

		updates, err := a.client.GetUpdates(ctx, offset, 50, 10)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			slog.Warn("failed to poll Zalo updates, backing off", "error", err, "backoff", backoff)
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

		for _, u := range updates {
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
			if a.router != nil {
				a.router.RouteUpdate(ctx, u)
			}
		}
	}
}
