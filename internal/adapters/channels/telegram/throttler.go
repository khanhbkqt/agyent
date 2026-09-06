package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	TurnID          string
	Generation      uint64
	BotID           int64
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
	WorkspaceDir    string
	SentMediaPaths  map[string]bool
	PendingMedia    []domain.Attachment

	Mu              sync.Mutex
	WakeupChan      chan struct{}
	DoneChan        chan struct{}
	WorkerDone      chan struct{}
	CancelHeartbeat context.CancelFunc
	CancelWorker    context.CancelFunc
}

// DeliveryThrottler handles real-time token buffering and periodic Telegram edits.
type DeliveryThrottler struct {
	bot               *gotgbot.Bot
	botGetter         func(botID int64) *gotgbot.Bot
	mediaMgr          *MediaManager
	throttleSeconds   float64
	streamingOn       bool
	generationCounter atomic.Uint64
	sessions          sync.Map // map[string]*StreamSession
	mu                sync.RWMutex
}

// NewDeliveryThrottler creates a new DeliveryThrottler.
func NewDeliveryThrottler(bot *gotgbot.Bot, mediaMgr *MediaManager, throttleIntervalSec float64, streamingOn bool, botGetter ...func(botID int64) *gotgbot.Bot) *DeliveryThrottler {
	if throttleIntervalSec <= 0 {
		throttleIntervalSec = 1.5
	}
	var bg func(botID int64) *gotgbot.Bot
	if len(botGetter) > 0 {
		bg = botGetter[0]
	}
	return &DeliveryThrottler{
		bot:             bot,
		botGetter:       bg,
		mediaMgr:        mediaMgr,
		throttleSeconds: throttleIntervalSec,
		streamingOn:     streamingOn,
	}
}

func (dt *DeliveryThrottler) getBot(botID int64) *gotgbot.Bot {
	if dt.botGetter != nil && botID > 0 {
		if b := dt.botGetter(botID); b != nil {
			return b
		}
	}
	return dt.bot
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

// ParseSessionKey extracts chatID and threadID from a sessionKey (e.g. "telegram:12345", "telegram:botID:12345", or "telegram:botID:12345:42").
func ParseSessionKey(key string) (channel string, chatID int64, threadID int64, err error) {
	parsed, err := domain.ParseSessionKey(key)
	if err != nil {
		return "", 0, 0, err
	}
	cID, err := strconv.ParseInt(parsed.ChatID, 10, 64)
	if err != nil {
		return "", 0, 0, fmt.Errorf("invalid chatID in session key %q: %w", key, err)
	}
	return parsed.Channel, cID, parsed.ThreadID, nil
}

// OnStreamInit initializes a streaming session and starts background worker.
func (dt *DeliveryThrottler) OnStreamInit(ctx context.Context, evt domain.Event) error {
	p, ok := evt.Payload.(domain.StreamInitPayload)
	if !ok {
		return nil
	}

	parsed, err := domain.ParseSessionKey(p.SessionKey)
	if err != nil || parsed.Channel != "telegram" {
		return nil
	}

	chatID, err := strconv.ParseInt(parsed.ChatID, 10, 64)
	if err != nil {
		slog.WarnContext(ctx, "Failed to parse chatID from session key", "session_key", p.SessionKey, "error", err)
		return nil
	}

	// Terminate existing session for this key if any
	if existing, ok := dt.sessions.Load(p.SessionKey); ok {
		if s, ok := existing.(*StreamSession); ok {
			s.closeWorker()
		}
	}

	gen := dt.generationCounter.Add(1)
	workerCtx, cancelWorker := context.WithCancel(context.Background())

	sess := &StreamSession{
		SessionKey:     p.SessionKey,
		ConversationID: p.ConversationID,
		TurnID:         p.TurnID,
		Generation:     gen,
		BotID:          parsed.BotID,
		ChatID:         chatID,
		ThreadID:       parsed.ThreadID,
		State:          StateInit,
		WakeupChan:     make(chan struct{}, 100),
		DoneChan:       make(chan struct{}),
		WorkerDone:     make(chan struct{}),
		CancelWorker:   cancelWorker,
		LastActivity:   time.Now(),
		WorkspaceDir:   p.CWD,
		SentMediaPaths: make(map[string]bool),
	}
	sess.Buffer.Grow(1024)

	// Start heartbeat typing while thinking
	bot := dt.getBot(sess.BotID)
	sess.CancelHeartbeat = StartHeartbeatTyping(workerCtx, bot, chatID, parsed.ThreadID, 4*time.Second)

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
	if p.TurnID != "" && sess.TurnID != "" && p.TurnID != sess.TurnID {
		return nil
	}

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
	if p.TurnID != "" && sess.TurnID != "" && p.TurnID != sess.TurnID {
		return nil
	}

	if p.State == "ACTIVE" {
		sess.Mu.Lock()
		sess.CurrentToolName = p.ToolName
		action := MapToolToChatAction(p.ToolName)
		sess.ActiveAction = action
		sess.LastActivity = time.Now()
		sess.Mu.Unlock()

		bot := dt.getBot(sess.BotID)
		if bot != nil {
			go func(b *gotgbot.Bot, chatID, threadID int64, act string) {
				opts := &gotgbot.SendChatActionOpts{}
				if threadID != 0 {
					opts.MessageThreadId = threadID
				}
				_, _ = b.SendChatAction(chatID, act, opts)
			}(bot, sess.ChatID, sess.ThreadID, action)
		}
	} else if p.State == "DONE" {
		sess.Mu.Lock()
		sess.CurrentToolName = ""
		sess.ActiveAction = ""
		sess.LastActivity = time.Now()
		sess.Mu.Unlock()
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
	if p.TurnID != "" && sess.TurnID != "" && p.TurnID != sess.TurnID {
		slog.DebugContext(ctx, "Ignoring stale stream result from prior turn",
			"session_key", p.SessionKey, "event_turn", p.TurnID, "active_turn", sess.TurnID)
		return nil
	}

	sess.Mu.Lock()
	sess.State = StateFinalizing
	responseText := p.Response
	if responseText == "" || len(sess.Buffer.String()) > len(responseText) {
		responseText = sess.Buffer.String()
	}

	// If result status is ERROR or error message is present, ensure error is displayed to user
	if (p.Status == "ERROR" || p.Error != "") && p.Error != "" && !strings.Contains(responseText, p.Error) {
		if strings.TrimSpace(responseText) != "" {
			responseText += "\n\n⚠️ [Execution Error: " + p.Error + "]"
		} else {
			responseText = "⚠️ [Execution Error: " + p.Error + "]"
		}
	}

	wsDir := sess.WorkspaceDir
	// Extract outbound media from markdown response and clean the text
	cleanedText, extractedMedia := ExtractAndCleanOutboundMedia(responseText, wsDir, sess.ConversationID)
	sess.Buffer.Reset()
	sess.Buffer.WriteString(cleanedText)
	sess.Dirty = true

	// Filter out any media already sent earlier (e.g. via OnStreamTool instant sync)
	if sess.SentMediaPaths == nil {
		sess.SentMediaPaths = make(map[string]bool)
	}
	for _, em := range extractedMedia {
		normPath := filepath.Clean(filepath.FromSlash(em.FilePath))
		if !sess.SentMediaPaths[normPath] {
			em.FilePath = normPath
			sess.PendingMedia = append(sess.PendingMedia, em)
			sess.SentMediaPaths[normPath] = true
		}
	}
	sess.Mu.Unlock()

	// Trigger worker to finalize
	sess.closeWorker()
	<-sess.WorkerDone

	// Zero-idle cleanup: CompareAndDelete guarantees we do not remove a newer active session
	dt.sessions.CompareAndDelete(p.SessionKey, sess)
	return nil
}

// OnStreamInterrupted handles graceful stream interruption by appending a pause indicator and finalizing the message cleanly.
func (dt *DeliveryThrottler) OnStreamInterrupted(ctx context.Context, evt domain.Event) error {
	p, ok := evt.Payload.(domain.StreamInterruptedPayload)
	if !ok {
		return nil
	}

	val, exists := dt.sessions.Load(p.SessionKey)
	if !exists {
		return nil
	}
	sess := val.(*StreamSession)
	if p.TurnID != "" && sess.TurnID != "" && p.TurnID != sess.TurnID {
		slog.DebugContext(ctx, "Ignoring stale stream interrupted event from prior turn",
			"session_key", p.SessionKey, "event_turn", p.TurnID, "active_turn", sess.TurnID)
		return nil
	}

	sess.Mu.Lock()
	sess.State = StateFinalizing
	currentText := sess.Buffer.String()
	if strings.TrimSpace(currentText) != "" {
		sess.Buffer.WriteString("\n\n⏸️ <i>Đã tạm dừng lượt này để nhận chỉ dẫn mới...</i>")
	} else {
		sess.Buffer.WriteString("⏸️ <i>Đã tạm dừng lượt này để nhận chỉ dẫn mới...</i>")
	}
	sess.Dirty = true
	sess.Mu.Unlock()

	// Trigger worker to finalize edit cleanly
	sess.closeWorker()
	<-sess.WorkerDone

	dt.sessions.CompareAndDelete(p.SessionKey, sess)
	return nil
}

// OnStreamError handles stream failure by flushing error and closing session.
func (dt *DeliveryThrottler) OnStreamError(ctx context.Context, evt domain.Event) error {
	p, ok := evt.Payload.(domain.StreamErrorPayload)
	if !ok {
		return nil
	}

	val, exists := dt.sessions.Load(p.SessionKey)
	if !exists {
		parsed, err := domain.ParseSessionKey(p.SessionKey)
		if err == nil && parsed.Channel == "telegram" {
			chatID, _ := strconv.ParseInt(parsed.ChatID, 10, 64)
			bot := dt.getBot(parsed.BotID)
			if bot != nil && chatID != 0 {
				dt.sendMessageWithFallback(bot, chatID, parsed.ThreadID, fmt.Sprintf("⚠️ [Execution interrupted: %s]", p.Error))
			}
		}
		return nil
	}
	sess := val.(*StreamSession)
	if p.TurnID != "" && sess.TurnID != "" && p.TurnID != sess.TurnID {
		slog.DebugContext(ctx, "Ignoring stale stream error from prior turn",
			"session_key", p.SessionKey, "event_turn", p.TurnID, "active_turn", sess.TurnID)
		return nil
	}

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

	// Zero-idle cleanup: CompareAndDelete guarantees we do not remove a newer active session
	dt.sessions.CompareAndDelete(p.SessionKey, sess)

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
		if r := recover(); r != nil {
			slog.ErrorContext(ctx, "Recovered from panic in stream session worker",
				slog.String("session_key", sess.SessionKey),
				slog.Any("panic", r),
			)
		}
	}()
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

	// Inactivity timeout: 30 minutes without any stream activity
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
			if time.Since(lastAct) > 30*time.Minute {
				dt.flushFinalSession(sess)
				dt.sessions.Delete(sess.SessionKey)
				return
			}
		}
	}
}

func (dt *DeliveryThrottler) sendInitialMessage(ctx context.Context, sess *StreamSession) {
	bot := dt.getBot(sess.BotID)
	if bot == nil {
		return
	}

	sess.Mu.Lock()
	text := sess.Buffer.String()
	sess.State = StateStreaming
	sess.Mu.Unlock()

	if strings.TrimSpace(text) == "" {
		return
	}

	msg := dt.sendMessageWithFallback(bot, sess.ChatID, sess.ThreadID, text)
	if msg != nil {
		sess.Mu.Lock()
		sess.CurrentMsgID = msg.MessageId
		sess.LastSentText = text
		sess.Dirty = (sess.Buffer.String() != text)
		sess.LastEditTime = time.Now()
		sess.Mu.Unlock()
	}
}

func (dt *DeliveryThrottler) performThrottledEdit(ctx context.Context, sess *StreamSession) {
	bot := dt.getBot(sess.BotID)
	if bot == nil {
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
	threadID := sess.ThreadID
	sess.Mu.Unlock()

	if msgID == 0 {
		dt.sendInitialMessage(ctx, sess)
		return
	}

	// Check for Multi-Message Overflow (>SafeTelegramMessageLimit characters during streaming)
	runeCount := len([]rune(text))
	if runeCount > SafeTelegramMessageLimit {
		dt.handleMultiMessageOverflow(sess, text)
		return
	}

	dt.editMessageWithFallback(bot, chatID, threadID, msgID, text, false)

	sess.Mu.Lock()
	sess.LastSentText = text
	sess.Dirty = (sess.Buffer.String() != text)
	sess.LastEditTime = time.Now()
	sess.Mu.Unlock()
}

func (dt *DeliveryThrottler) handleMultiMessageOverflow(sess *StreamSession, fullText string) {
	bot := dt.getBot(sess.BotID)
	if bot == nil {
		return
	}
	chunks := SplitMarkdownPreservingCodeBlocks(fullText, SafeTelegramMessageLimit)
	if len(chunks) < 2 {
		return
	}

	// 1. Finalize Chunk 0 on current message
	dt.editMessageWithFallback(bot, sess.ChatID, sess.ThreadID, sess.CurrentMsgID, chunks[0], true)

	// 2. Deliver all intermediate chunks (chunks 1 to len-2)
	for i := 1; i < len(chunks)-1; i++ {
		dt.sendMessageWithFallback(bot, sess.ChatID, sess.ThreadID, chunks[i])
	}

	// 3. Spawn the last chunk on new active streaming message
	lastChunk := chunks[len(chunks)-1]
	newMsg := dt.sendMessageWithFallback(bot, sess.ChatID, sess.ThreadID, lastChunk)
	if newMsg != nil {
		sess.Mu.Lock()
		sess.CurrentMsgID = newMsg.MessageId
		sess.Buffer.Reset()
		sess.Buffer.WriteString(lastChunk)
		sess.LastSentText = lastChunk
		sess.Dirty = false
		sess.LastEditTime = time.Now()
		sess.Mu.Unlock()
	}
}

func (dt *DeliveryThrottler) flushFinalSession(sess *StreamSession) {
	bot := dt.getBot(sess.BotID)
	if bot == nil {
		return
	}

	sess.Mu.Lock()
	text := sess.Buffer.String()
	msgID := sess.CurrentMsgID
	chatID := sess.ChatID
	threadID := sess.ThreadID
	convID := sess.ConversationID
	wsDir := sess.WorkspaceDir
	pendingMedia := sess.PendingMedia
	sess.PendingMedia = nil
	sess.State = StateCompleted
	sess.Mu.Unlock()

	// Clean any remaining markdown image references or comments before final dispatch
	cleanedText, extractedMedia := ExtractAndCleanOutboundMedia(text, wsDir, convID)
	sess.Mu.Lock()
	for _, em := range extractedMedia {
		normPath := filepath.Clean(filepath.FromSlash(em.FilePath))
		if sess.SentMediaPaths == nil {
			sess.SentMediaPaths = make(map[string]bool)
		}
		if !sess.SentMediaPaths[normPath] {
			em.FilePath = normPath
			pendingMedia = append(pendingMedia, em)
			sess.SentMediaPaths[normPath] = true
		}
	}
	hasSentMedia := len(sess.SentMediaPaths) > 0
	sess.Mu.Unlock()

	trimmed := strings.TrimSpace(cleanedText)
	if trimmed == "" {
		if !hasSentMedia && len(pendingMedia) == 0 {
			cleanedText = "⚠️ [Lượt xử lý hoàn tất nhưng không có nội dung phản hồi. Hãy thử gửi lại câu hỏi hoặc yêu cầu khác.]"
		} else {
			cleanedText = ""
		}
	}

	// 1. Deliver media attachments first (synchronously) so photos/albums arrive before the text message
	if len(pendingMedia) > 0 && dt.mediaMgr != nil {
		if err := dt.mediaMgr.UploadTurnArtifacts(context.Background(), chatID, threadID, pendingMedia, bot); err != nil {
			slog.Error("Failed to upload turn artifacts from final flush", "chat_id", chatID, "count", len(pendingMedia), "error", err)
		}
	}

	chunks := SplitMarkdownPreservingCodeBlocks(cleanedText, SafeTelegramMessageLimit)
	if len(chunks) == 0 {
		// If response only contained images and no text, delete the typing/thinking placeholder message if one was sent
		if msgID != 0 {
			_, _ = bot.DeleteMessage(chatID, msgID, nil)
		}
		return
	}

	// Case 1: Initial message was never sent yet
	if msgID == 0 {
		for _, chunk := range chunks {
			dt.sendMessageWithFallback(bot, chatID, threadID, chunk)
		}
		return
	}

	// Case 2: Edit first chunk into existing message
	dt.editMessageWithFallback(bot, chatID, threadID, msgID, chunks[0], true)

	// Case 3: Send any subsequent overflow chunks (>SafeTelegramMessageLimit chars total) as new messages
	for i := 1; i < len(chunks); i++ {
		dt.sendMessageWithFallback(bot, chatID, threadID, chunks[i])
	}
}

func (dt *DeliveryThrottler) editMessageWithFallback(bot *gotgbot.Bot, chatID, threadID, msgID int64, text string, isFinal bool) {
	if bot == nil || strings.TrimSpace(text) == "" {
		return
	}
	formatted := FormatMarkdownToTelegramHTML(text)
	opts := &gotgbot.EditMessageTextOpts{
		ChatId:    chatID,
		MessageId: msgID,
		Text:      formatted,
		ParseMode: "HTML",
		RequestOpts: &gotgbot.RequestOpts{
			Timeout: 30 * time.Second,
		},
	}
	if _, _, err := bot.EditMessageText(opts); err != nil {
		var tgErr *gotgbot.TelegramError
		if errors.As(err, &tgErr) {
			// Benign: exact same content was already displayed
			if strings.Contains(strings.ToLower(tgErr.Description), "message is not modified") {
				return
			}

			if tgErr.Code == 400 {
				// If message too long, edit first chunk and send remainder as new message
				if strings.Contains(strings.ToLower(tgErr.Description), "too long") || len([]rune(formatted)) > 4000 {
					chunks := SplitMarkdownPreservingCodeBlocks(text, SafeTelegramMessageLimit)
					if len(chunks) < 2 {
						half := len([]rune(text)) / 2
						if half > 0 {
							chunks = SplitMarkdownPreservingCodeBlocks(text, half)
						}
					}
					if len(chunks) >= 2 {
						dt.editMessageWithFallback(bot, chatID, threadID, msgID, chunks[0], isFinal)
						for i := 1; i < len(chunks); i++ {
							dt.sendMessageWithFallback(bot, chatID, threadID, chunks[i])
						}
						return
					}
				}

				opts.ParseMode = ""
				opts.Text = StripHTMLTags(formatted)
				if _, _, retryErr := bot.EditMessageText(opts); retryErr == nil {
					return
				}
				slog.Warn("Telegram editMessageText plain text fallback failed", "chat_id", chatID, "thread_id", threadID, "msg_id", msgID, "error", err)
			} else if tgErr.Code == 429 {
				retrySec := 1
				if tgErr.ResponseParams != nil && tgErr.ResponseParams.RetryAfter > 0 {
					retrySec = int(tgErr.ResponseParams.RetryAfter)
				}
				time.Sleep(time.Duration(retrySec) * time.Second)
				if _, _, retryErr := bot.EditMessageText(opts); retryErr == nil {
					return
				}
			} else {
				slog.Warn("Telegram editMessageText failed", "chat_id", chatID, "thread_id", threadID, "msg_id", msgID, "error", err)
			}
		} else {
			slog.Warn("Telegram editMessageText network error", "chat_id", chatID, "thread_id", threadID, "msg_id", msgID, "error", err)
		}

		// Check if message to edit was permanently lost or deleted (400 "not found" / "can't be edited")
		var isPermanentNotFound bool
		var tgErr2 *gotgbot.TelegramError
		if errors.As(err, &tgErr2) && tgErr2.Code == 400 {
			desc := strings.ToLower(tgErr2.Description)
			if strings.Contains(desc, "not found") || strings.Contains(desc, "can't be edited") || strings.Contains(desc, "cannot be edited") {
				isPermanentNotFound = true
			}
		}

		// If intermediate edit and not permanently deleted, do not spam new partial messages into the chat;
		// the next 1.5s ticker will retry editing the latest accumulated buffer.
		if !isFinal && !isPermanentNotFound {
			return
		}

		// Edit failed permanently or on final flush: fallback to sending fresh message so user response is NEVER swallowed.
		slog.Warn("Telegram edit failed; falling back to sending fresh message to prevent message drop",
			"chat_id", chatID, "thread_id", threadID, "msg_id", msgID, "is_final", isFinal)
		dt.sendMessageWithFallback(bot, chatID, threadID, text)
	}
}

func (dt *DeliveryThrottler) sendMessageWithFallback(bot *gotgbot.Bot, chatID, threadID int64, text string) *gotgbot.Message {
	if bot == nil || strings.TrimSpace(text) == "" {
		return nil
	}

	msg := dt.doSendWithRetry(bot, chatID, threadID, text)
	if msg == nil && dt.bot != nil && bot != dt.bot {
		slog.Warn("Dedicated bot failed to deliver message, retrying with primary bot",
			"chat_id", chatID,
			"thread_id", threadID,
		)
		msg = dt.doSendWithRetry(dt.bot, chatID, threadID, text)
	}
	return msg
}

func (dt *DeliveryThrottler) doSendWithRetry(bot *gotgbot.Bot, chatID, threadID int64, text string) *gotgbot.Message {
	if bot == nil || strings.TrimSpace(text) == "" {
		return nil
	}

	formatted := FormatMarkdownToTelegramHTML(text)
	opts := &gotgbot.SendMessageOpts{
		ParseMode: "HTML",
		RequestOpts: &gotgbot.RequestOpts{
			Timeout: 30 * time.Second,
		},
	}
	if threadID != 0 {
		opts.MessageThreadId = threadID
	}

	maxAttempts := 3
	backoffs := []time.Duration{500 * time.Millisecond, 1500 * time.Millisecond, 3000 * time.Millisecond}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		msg, err := bot.SendMessage(chatID, formatted, opts)
		if err == nil {
			return msg
		}

		var tgErr *gotgbot.TelegramError
		if errors.As(err, &tgErr) {
			// Case A: Message too long (>4096 characters / UTF-16 code units)
			isTooLong := tgErr.Code == 400 && (strings.Contains(strings.ToLower(tgErr.Description), "too long") || len([]rune(formatted)) > 4000)
			if isTooLong {
				slog.Warn("Telegram message exceeds character limit, recursively splitting into smaller chunks",
					"chat_id", chatID, "thread_id", threadID, "length", len([]rune(formatted)))
				splitRunes := []rune(text)
				if len(splitRunes) > 1 {
					half := len(splitRunes) / 2
					chunks := SplitMarkdownPreservingCodeBlocks(text, half)
					if len(chunks) >= 2 {
						var lastMsg *gotgbot.Message
						for _, c := range chunks {
							if m := dt.sendMessageWithFallback(bot, chatID, threadID, c); m != nil {
								lastMsg = m
							}
						}
						return lastMsg
					}
				}
			}

			// Case B: HTML formatting / entity parse error
			if tgErr.Code == 400 {
				opts.ParseMode = ""
				plainText := StripHTMLTags(formatted)
				if len([]rune(plainText)) > 4000 {
					half := len([]rune(plainText)) / 2
					chunks := SplitMarkdownPreservingCodeBlocks(plainText, half)
					var lastMsg *gotgbot.Message
					for _, c := range chunks {
						if m := dt.sendMessageWithFallback(bot, chatID, threadID, c); m != nil {
							lastMsg = m
						}
					}
					return lastMsg
				}
				msg, retryErr := bot.SendMessage(chatID, plainText, opts)
				if retryErr == nil {
					return msg
				}
				slog.Error("Telegram SendMessage plain text fallback failed", "chat_id", chatID, "thread_id", threadID, "error", retryErr)
				return nil
			}

			// Case C: Rate limit (429)
			if tgErr.Code == 429 {
				retrySec := 1
				if tgErr.ResponseParams != nil && tgErr.ResponseParams.RetryAfter > 0 {
					retrySec = int(tgErr.ResponseParams.RetryAfter)
				}
				slog.Warn("Telegram SendMessage rate limited, waiting to retry", "chat_id", chatID, "retry_sec", retrySec, "attempt", attempt+1)
				time.Sleep(time.Duration(retrySec) * time.Second)
				continue
			}

			// Case D: Server 5xx error
			if tgErr.Code >= 500 && tgErr.Code <= 599 {
				slog.Warn("Telegram server 5xx error, retrying with backoff", "code", tgErr.Code, "attempt", attempt+1)
				if attempt < maxAttempts-1 {
					time.Sleep(backoffs[attempt])
					continue
				}
				return nil
			}

			slog.Error("Telegram SendMessage client error", "chat_id", chatID, "thread_id", threadID, "code", tgErr.Code, "error", err)
			return nil
		}

		// Network error (e.g. context deadline exceeded, connection reset by peer, EOF)
		slog.Warn("Telegram SendMessage network error, retrying with backoff",
			"chat_id", chatID, "thread_id", threadID, "attempt", attempt+1, "error", err)
		if attempt < maxAttempts-1 {
			time.Sleep(backoffs[attempt])
			continue
		}
		slog.Error("Telegram SendMessage network retries exhausted", "chat_id", chatID, "thread_id", threadID, "error", err)
	}

	return nil
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
