package telegram

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"agyent/internal/core/domain"
)

// StreamState represents the lifecycle phase of a streaming session.
type StreamState int

const (
	StateInit StreamState = iota
	StateFirstTokenPending
	StateStreaming
	StateFinalizing
	StateCompleted
	StateFailed
)

// StreamSession manages state and buffers for an active streaming turn.
type StreamSession struct {
	SessionKey      string
	ConversationID  string
	ChatID          int64
	ThreadID        int64
	CurrentMsgID    int64
	State           StreamState
	Buffer          strings.Builder
	LastSentText    string
	CurrentToolName string
	ActiveAction    string
	Dirty           bool
	LastEditTime    time.Time
	LastActivity    time.Time

	Mu              sync.Mutex
	WakeupChan      chan struct{}
	DoneChan        chan struct{}
	WorkerDone      chan struct{}
	CancelHeartbeat context.CancelFunc
	CancelWorker    context.CancelFunc
}

// DeliveryThrottler handles real-time token buffering and periodic Telegram edits.
type DeliveryThrottler struct {
	bot             *gotgbot.Bot
	mediaMgr        *MediaManager
	throttleSeconds float64
	streamingOn     bool
	sessions        sync.Map // map[string]*StreamSession
	mu              sync.RWMutex
}

// NewDeliveryThrottler creates a new DeliveryThrottler.
func NewDeliveryThrottler(bot *gotgbot.Bot, mediaMgr *MediaManager, throttleIntervalSec float64, streamingOn bool) *DeliveryThrottler {
	if throttleIntervalSec <= 0 {
		throttleIntervalSec = 1.5
	}
	return &DeliveryThrottler{
		bot:             bot,
		mediaMgr:        mediaMgr,
		throttleSeconds: throttleIntervalSec,
		streamingOn:     streamingOn,
	}
}

// ActiveSessionsCount returns the number of currently active streaming sessions.
func (dt *DeliveryThrottler) ActiveSessionsCount() int {
	count := 0
	dt.sessions.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

// ParseSessionKey extracts chatID and threadID from a sessionKey (e.g. "telegram:12345" or "telegram:12345:42").
func ParseSessionKey(key string) (channel string, chatID int64, threadID int64, err error) {
	parts := strings.Split(key, ":")
	if len(parts) < 2 {
		return "", 0, 0, fmt.Errorf("invalid session key format %q", key)
	}
	channel = parts[0]
	chatID, err = strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", 0, 0, fmt.Errorf("invalid chatID in session key %q: %w", key, err)
	}
	if len(parts) >= 3 {
		threadID, _ = strconv.ParseInt(parts[2], 10, 64)
	}
	return channel, chatID, threadID, nil
}

// OnStreamInit initializes a streaming session and starts background worker.
func (dt *DeliveryThrottler) OnStreamInit(ctx context.Context, evt domain.Event) error {
	p, ok := evt.Payload.(domain.StreamInitPayload)
	if !ok {
		return nil
	}

	channel, chatID, threadID, err := ParseSessionKey(p.SessionKey)
	if err != nil || channel != "telegram" {
		return nil
	}

	// Terminate existing session for this key if any
	if existing, ok := dt.sessions.Load(p.SessionKey); ok {
		if s, ok := existing.(*StreamSession); ok {
			s.closeWorker()
		}
	}

	workerCtx, cancelWorker := context.WithCancel(context.Background())

	sess := &StreamSession{
		SessionKey:     p.SessionKey,
		ConversationID: p.ConversationID,
		ChatID:         chatID,
		ThreadID:       threadID,
		State:          StateInit,
		WakeupChan:     make(chan struct{}, 100),
		DoneChan:       make(chan struct{}),
		WorkerDone:     make(chan struct{}),
		CancelWorker:   cancelWorker,
		LastActivity:   time.Now(),
	}
	sess.Buffer.Grow(1024)

	// Start heartbeat typing while thinking
	sess.CancelHeartbeat = StartHeartbeatTyping(workerCtx, dt.bot, chatID, threadID, 4*time.Second)

	dt.sessions.Store(p.SessionKey, sess)

	go dt.runSessionWorker(workerCtx, sess)

	return nil
}

// OnStreamDelta accumulates incremental text delta on the zero-alloc hot-path.
func (dt *DeliveryThrottler) OnStreamDelta(ctx context.Context, evt domain.Event) error {
	p, ok := evt.Payload.(domain.StreamDeltaPayload)
	if !ok {
		return nil
	}

	val, exists := dt.sessions.Load(p.SessionKey)
	if !exists {
		return nil
	}

	sess := val.(*StreamSession)
	sess.Mu.Lock()
	sess.Buffer.WriteString(p.TextDelta)
	sess.Dirty = true
	sess.LastActivity = time.Now()
	sess.Mu.Unlock()

	// Notify worker loop non-blockingly (<5µs total execution time)
	select {
	case sess.WakeupChan <- struct{}{}:
	default:
	}

	return nil
}

// OnStreamTool handles tool state transitions, chat actions, and instant brain image sync.
// Executes network calls asynchronously to preserve SyncEmit hot-path throughput.
func (dt *DeliveryThrottler) OnStreamTool(ctx context.Context, evt domain.Event) error {
	p, ok := evt.Payload.(domain.StreamToolPayload)
	if !ok {
		return nil
	}

	val, exists := dt.sessions.Load(p.SessionKey)
	if !exists {
		return nil
	}
	sess := val.(*StreamSession)

	if p.State == "ACTIVE" {
		sess.Mu.Lock()
		sess.CurrentToolName = p.ToolName
		action := MapToolToChatAction(p.ToolName)
		sess.ActiveAction = action
		sess.LastActivity = time.Now()
		sess.Mu.Unlock()

		if dt.bot != nil {
			go func(chatID, threadID int64, act string) {
				opts := &gotgbot.SendChatActionOpts{}
				if threadID != 0 {
					opts.MessageThreadId = threadID
				}
				_, _ = dt.bot.SendChatAction(chatID, act, opts)
			}(sess.ChatID, sess.ThreadID, action)
		}
	} else if p.State == "DONE" {
		sess.Mu.Lock()
		sess.CurrentToolName = ""
		sess.ActiveAction = ""
		sess.LastActivity = time.Now()
		sess.Mu.Unlock()

		// If tool is generate_image, trigger instant brain image sync asynchronously
		if p.ToolName == "generate_image" && dt.mediaMgr != nil {
			imageName := ""
			if p.Parameters != nil {
				if in, ok := p.Parameters["ImageName"].(string); ok {
					imageName = in
				}
			}
			go func(chatID, threadID int64, convID, imgName string) {
				if imgPath, err := FindBrainImage(convID, imgName); err == nil && imgPath != "" {
					_ = dt.mediaMgr.SendBrainImage(context.Background(), chatID, threadID, imgPath, "🎨 Generated Image")
				}
			}(sess.ChatID, sess.ThreadID, p.ConversationID, imageName)
		}
	}

	return nil
}

// OnStreamResult handles stream completion, flushes remaining buffer, and uploads artifacts.
func (dt *DeliveryThrottler) OnStreamResult(ctx context.Context, evt domain.Event) error {
	p, ok := evt.Payload.(domain.StreamResultPayload)
	if !ok {
		return nil
	}

	val, exists := dt.sessions.Load(p.SessionKey)
	if !exists {
		return nil
	}
	sess := val.(*StreamSession)

	sess.Mu.Lock()
	sess.State = StateFinalizing
	if p.Response != "" && (sess.Buffer.Len() == 0 || len(p.Response) > sess.Buffer.Len()) {
		sess.Buffer.Reset()
		sess.Buffer.WriteString(p.Response)
		sess.Dirty = true
	}
	sess.Mu.Unlock()

	// Trigger worker to finalize
	sess.closeWorker()
	<-sess.WorkerDone

	// Outbound turn artifacts auto-upload (async non-blocking)
	if len(p.Artifacts) > 0 && dt.mediaMgr != nil {
		go func(chatID, threadID int64, arts []domain.Attachment) {
			_ = dt.mediaMgr.UploadTurnArtifacts(context.Background(), chatID, threadID, arts)
		}(sess.ChatID, sess.ThreadID, p.Artifacts)
	}

	// Zero-idle cleanup
	dt.sessions.Delete(p.SessionKey)

	return nil
}

// OnStreamError handles unexpected stream failure or crash.
func (dt *DeliveryThrottler) OnStreamError(ctx context.Context, evt domain.Event) error {
	p, ok := evt.Payload.(domain.StreamErrorPayload)
	if !ok {
		return nil
	}

	val, exists := dt.sessions.Load(p.SessionKey)
	if !exists {
		return nil
	}
	sess := val.(*StreamSession)

	sess.Mu.Lock()
	sess.State = StateFailed
	if p.Error != "" {
		sess.Buffer.WriteString("\n\n⚠️ [Execution interrupted: " + p.Error + "]")
		sess.Dirty = true
	}
	sess.Mu.Unlock()

	// Trigger worker to finalize with error note
	sess.closeWorker()
	<-sess.WorkerDone

	// Zero-idle cleanup
	dt.sessions.Delete(p.SessionKey)

	return nil
}

func (s *StreamSession) closeWorker() {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if s.CancelWorker != nil {
		s.CancelWorker()
	}
	select {
	case <-s.DoneChan:
	default:
		close(s.DoneChan)
	}
}

func sleepCancellable(ctx context.Context, doneChan <-chan struct{}, d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
	case <-doneChan:
		return false
	}
}

// runSessionWorker handles the sub-second first-token dispatch, 1.5s sliding edit ticker, and inactivity timeout.
func (dt *DeliveryThrottler) runSessionWorker(ctx context.Context, sess *StreamSession) {
	defer close(sess.WorkerDone)
	defer func() {
		if sess.CancelHeartbeat != nil {
			sess.CancelHeartbeat()
		}
		if sess.CancelWorker != nil {
			sess.CancelWorker()
		}
	}()

	interval := time.Duration(dt.throttleSeconds * float64(time.Second))
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Inactivity timeout: 5 minutes without any stream activity
	inactivityCheck := time.NewTicker(30 * time.Second)
	defer inactivityCheck.Stop()

	for {
		select {
		case <-ctx.Done():
			dt.flushFinalSession(sess)
			return

		case <-sess.DoneChan:
			dt.flushFinalSession(sess)
			return

		case <-sess.WakeupChan:
			// If message hasn't been sent yet, send first token immediately (<1.0s sub-second latency)
			sess.Mu.Lock()
			state := sess.State
			bufLen := sess.Buffer.Len()
			sess.Mu.Unlock()

			if (state == StateInit || state == StateFirstTokenPending) && bufLen > 0 {
				dt.sendInitialMessage(ctx, sess)
			}

		case <-ticker.C:
			dt.performThrottledEdit(ctx, sess)

		case <-inactivityCheck.C:
			sess.Mu.Lock()
			lastAct := sess.LastActivity
			sess.Mu.Unlock()
			if time.Since(lastAct) > 5*time.Minute {
				dt.flushFinalSession(sess)
				dt.sessions.Delete(sess.SessionKey)
				return
			}
		}
	}
}

func (dt *DeliveryThrottler) sendInitialMessage(ctx context.Context, sess *StreamSession) {
	if dt.bot == nil {
		return
	}

	sess.Mu.Lock()
	text := sess.Buffer.String()
	sess.State = StateStreaming
	sess.Mu.Unlock()

	if strings.TrimSpace(text) == "" {
		return
	}

	msg := dt.sendMessageWithFallback(sess.ChatID, sess.ThreadID, text)
	if msg != nil {
		sess.Mu.Lock()
		sess.CurrentMsgID = msg.MessageId
		sess.LastSentText = text
		sess.Dirty = false
		sess.LastEditTime = time.Now()
		sess.Mu.Unlock()
	}
}

func (dt *DeliveryThrottler) performThrottledEdit(ctx context.Context, sess *StreamSession) {
	if dt.bot == nil {
		return
	}

	sess.Mu.Lock()
	if !sess.Dirty || sess.State == StateCompleted {
		sess.Mu.Unlock()
		return
	}

	text := sess.Buffer.String()
	msgID := sess.CurrentMsgID
	chatID := sess.ChatID
	sess.Mu.Unlock()

	if msgID == 0 {
		dt.sendInitialMessage(ctx, sess)
		return
	}

	// Check for Multi-Message Overflow (>4000 characters during streaming)
	runeCount := len([]rune(text))
	if runeCount > 4000 {
		dt.handleMultiMessageOverflow(sess, text)
		return
	}

	dt.editMessageWithFallback(chatID, msgID, text)

	sess.Mu.Lock()
	sess.LastSentText = text
	sess.Dirty = false
	sess.LastEditTime = time.Now()
	sess.Mu.Unlock()
}

func (dt *DeliveryThrottler) handleMultiMessageOverflow(sess *StreamSession, fullText string) {
	chunks := SplitMarkdownPreservingCodeBlocks(fullText, 4000)
	if len(chunks) < 2 {
		return
	}

	// 1. Finalize Chunk 0 on current message
	dt.editMessageWithFallback(sess.ChatID, sess.CurrentMsgID, chunks[0])

	// 2. Spawn Chunk 1 on new message
	newMsg := dt.sendMessageWithFallback(sess.ChatID, sess.ThreadID, chunks[1])
	if newMsg != nil {
		sess.Mu.Lock()
		sess.CurrentMsgID = newMsg.MessageId
		sess.Buffer.Reset()
		sess.Buffer.WriteString(chunks[1])
		sess.LastSentText = chunks[1]
		sess.Dirty = false
		sess.LastEditTime = time.Now()
		sess.Mu.Unlock()
	}
}

func (dt *DeliveryThrottler) flushFinalSession(sess *StreamSession) {
	if dt.bot == nil {
		return
	}

	sess.Mu.Lock()
	text := sess.Buffer.String()
	msgID := sess.CurrentMsgID
	chatID := sess.ChatID
	threadID := sess.ThreadID
	sess.State = StateCompleted
	sess.Mu.Unlock()

	if strings.TrimSpace(text) == "" {
		return
	}

	chunks := SplitMarkdownPreservingCodeBlocks(text, 4000)
	if len(chunks) == 0 {
		return
	}

	// Case 1: Initial message was never sent yet
	if msgID == 0 {
		for _, chunk := range chunks {
			dt.sendMessageWithFallback(chatID, threadID, chunk)
		}
		return
	}

	// Case 2: Edit first chunk into existing message
	dt.editMessageWithFallback(chatID, msgID, chunks[0])

	// Case 3: Send any subsequent overflow chunks (>4000 chars total) as new messages
	for i := 1; i < len(chunks); i++ {
		dt.sendMessageWithFallback(chatID, threadID, chunks[i])
	}
}

func (dt *DeliveryThrottler) editMessageWithFallback(chatID, msgID int64, text string) {
	if dt.bot == nil || strings.TrimSpace(text) == "" {
		return
	}
	formatted := FormatMarkdownToTelegramHTML(text)
	opts := &gotgbot.EditMessageTextOpts{
		ChatId:    chatID,
		MessageId: msgID,
		Text:      formatted,
		ParseMode: "HTML",
	}
	if _, _, err := dt.bot.EditMessageText(opts); err != nil {
		var tgErr *gotgbot.TelegramError
		if errors.As(err, &tgErr) {
			if tgErr.Code == 400 {
				opts.ParseMode = ""
				opts.Text = StripHTMLTags(formatted)
				_, _, _ = dt.bot.EditMessageText(opts)
			} else if tgErr.Code == 429 {
				retrySec := 1
				if tgErr.ResponseParams != nil && tgErr.ResponseParams.RetryAfter > 0 {
					retrySec = int(tgErr.ResponseParams.RetryAfter)
				}
				time.Sleep(time.Duration(retrySec) * time.Second)
				_, _, _ = dt.bot.EditMessageText(opts)
			}
		}
	}
}

func (dt *DeliveryThrottler) sendMessageWithFallback(chatID, threadID int64, text string) *gotgbot.Message {
	if dt.bot == nil || strings.TrimSpace(text) == "" {
		return nil
	}
	formatted := FormatMarkdownToTelegramHTML(text)
	opts := &gotgbot.SendMessageOpts{
		ParseMode: "HTML",
	}
	if threadID != 0 {
		opts.MessageThreadId = threadID
	}
	msg, err := dt.bot.SendMessage(chatID, formatted, opts)
	if err != nil {
		var tgErr *gotgbot.TelegramError
		if errors.As(err, &tgErr) {
			if tgErr.Code == 400 {
				opts.ParseMode = ""
				msg, _ = dt.bot.SendMessage(chatID, StripHTMLTags(formatted), opts)
			} else if tgErr.Code == 429 {
				retrySec := 1
				if tgErr.ResponseParams != nil && tgErr.ResponseParams.RetryAfter > 0 {
					retrySec = int(tgErr.ResponseParams.RetryAfter)
				}
				time.Sleep(time.Duration(retrySec) * time.Second)
				msg, _ = dt.bot.SendMessage(chatID, formatted, opts)
			}
		}
	}
	return msg
}

// Stop drains all active streaming sessions.
func (dt *DeliveryThrottler) Stop() {
	dt.sessions.Range(func(key, val any) bool {
		if s, ok := val.(*StreamSession); ok {
			s.closeWorker()
			<-s.WorkerDone
			dt.sessions.Delete(key)
		}
		return true
	})
}
