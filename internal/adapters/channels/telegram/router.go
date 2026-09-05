package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"agyent/internal/config"
	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

// Router handles incoming Telegram updates and transforms them into CanonicalMessages.
type Router struct {
	cfg             *config.Config
	bot             *gotgbot.Bot
	inbound         chan<- domain.CanonicalMessage
	mediaMgr        *MediaManager
	hitlCoordinator *HITLCoordinator
	authorizer      InboundAuthorizer
	bindAgents      map[int64]string
	mu              sync.RWMutex
}

// InboundAuthorizer is retained as an adapter-local alias for compatibility.
type InboundAuthorizer = ports.InboundAuthorizer

// InboundSessionAuthorizer is retained as an adapter-local alias for compatibility.
type InboundSessionAuthorizer = ports.InboundSessionAuthorizer

// NewRouter creates a new update Router.
func NewRouter(cfg *config.Config, bot *gotgbot.Bot, inbound chan<- domain.CanonicalMessage, mediaMgr *MediaManager, hitlCoord ...*HITLCoordinator) *Router {
	r := &Router{
		cfg:        cfg,
		bot:        bot,
		inbound:    inbound,
		mediaMgr:   mediaMgr,
		bindAgents: make(map[int64]string),
	}
	if len(hitlCoord) > 0 {
		r.hitlCoordinator = hitlCoord[0]
	}
	return r
}

// SetInboundAuthorizer injects the inbound authorization evaluator.
func (r *Router) SetInboundAuthorizer(a InboundAuthorizer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.authorizer = a
}

// SetBotBindings updates the mapping of bot IDs to dedicated bound agent personas.
func (r *Router) SetBotBindings(bindings map[int64]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bindAgents = bindings
}

// HandleUpdate processes a Telegram update with whitelist security and group routing rules.
func (r *Router) HandleUpdate(ctx context.Context, b *gotgbot.Bot, u *gotgbot.Update) error {
	if u == nil {
		return nil
	}

	if u.CallbackQuery != nil {
		return r.HandleCallbackQuery(ctx, b, u.CallbackQuery)
	}

	if u.Message == nil {
		return nil
	}

	msg := u.Message
	if msg.From == nil {
		return nil
	}

	botUsername := ""
	var botID int64
	if b != nil {
		botUsername = b.Username
		botID = b.Id
	}

	// 1. Authorization & Group Routing
	var isMentioned, isReplyToBot bool
	if msg.Chat.Type == "private" {
		r.mu.RLock()
		auth := r.authorizer
		var bindAgent string
		if r.bindAgents != nil && botID > 0 {
			bindAgent = r.bindAgents[botID]
		}
		r.mu.RUnlock()

		if auth != nil {
			allowed, err := authorizeInboundMessage(ctx, auth, strconv.FormatInt(msg.From.Id, 10), bindAgent, "private", domain.FormatSessionKey("telegram", strconv.FormatInt(msg.Chat.Id, 10), ExtractThreadID(msg), botID))
			if err != nil || !allowed {
				// Unauthorized private message: drop with zero side-effects
				return nil
			}
		}
	} else if msg.Chat.Type == "group" || msg.Chat.Type == "supergroup" {
		if !IsGroupAllowed(r.cfg, msg.Chat.Id) {
			// Unauthorized group: silently drop
			return nil
		}

		shouldProcess, mentioned, replied := IsMessageForBot(botUsername, botID, msg)
		if !shouldProcess {
			// Message not directed at bot: ignore
			return nil
		}
		isMentioned = mentioned
		isReplyToBot = replied

		r.mu.RLock()
		auth := r.authorizer
		var bindAgent string
		if r.bindAgents != nil && botID > 0 {
			bindAgent = r.bindAgents[botID]
		}
		r.mu.RUnlock()
		if auth != nil {
			allowed, err := authorizeInboundMessage(ctx, auth, strconv.FormatInt(msg.From.Id, 10), bindAgent, msg.Chat.Type, domain.FormatSessionKey("telegram", strconv.FormatInt(msg.Chat.Id, 10), ExtractThreadID(msg), botID))
			if err != nil || !allowed {
				// The group itself is allowed, but the sender is not authorized for
				// the target agent. Do not create a session or download attachments.
				return nil
			}
		}
	} else {
		// Other chat types (e.g. channels): ignore
		return nil
	}

	// 2. Extract raw and clean text
	rawText := msg.Text
	if rawText == "" {
		rawText = msg.Caption
	}

	cleanText := ExtractCleanText(botUsername, rawText)

	// 3. Extract lazy media references (zero disk download before admission)
	var attachmentRefs []domain.InboundAttachmentRef
	if r.mediaMgr != nil {
		attachmentRefs = r.mediaMgr.ExtractAttachmentRefs(msg, botID)
	}

	// 4. Construct CanonicalMessage
	threadID := ExtractThreadID(msg)
	fullName := strings.TrimSpace(msg.From.FirstName + " " + msg.From.LastName)
	msgTime := time.Unix(msg.Date, 0)
	if msg.Date == 0 {
		msgTime = time.Now()
	}

	var replyToMsgID string
	if msg.ReplyToMessage != nil {
		replyToMsgID = strconv.FormatInt(msg.ReplyToMessage.MessageId, 10)
	}

	r.mu.RLock()
	var bindAgent string
	if r.bindAgents != nil && botID > 0 {
		bindAgent = r.bindAgents[botID]
	}
	r.mu.RUnlock()

	cMsg := domain.CanonicalMessage{
		ID:          strconv.FormatInt(msg.MessageId, 10),
		Timestamp:   msgTime,
		Channel:     "telegram",
		BotID:       botID,
		BotUsername: botUsername,
		BindAgent:   bindAgent,
		Sender: domain.SenderUser{
			ID:       strconv.FormatInt(msg.From.Id, 10),
			Provider: "telegram",
			Username: msg.From.Username,
			FullName: fullName,
		},
		Chat: domain.ChatContext{
			ID:       strconv.FormatInt(msg.Chat.Id, 10),
			Type:     msg.Chat.Type,
			Title:    msg.Chat.Title,
			ThreadID: threadID,
		},
		Text:             cleanText,
		RawText:          rawText,
		AttachmentRefs:   attachmentRefs,
		IsMentioned:      isMentioned,
		IsReplyToBot:     isReplyToBot,
		ReplyToMessageID: replyToMsgID,
	}

	// 5. Proactively trigger typing indicator immediately (<200ms user feedback)
	// so the user gets instant visual feedback while the debouncer coalesces messages
	// and the AGY CLI subprocess initializes.
	if !cMsg.IsCommand() && b != nil {
		go func(chatID, threadID int64) {
			opts := &gotgbot.SendChatActionOpts{}
			if threadID != 0 {
				opts.MessageThreadId = threadID
			}
			_, _ = b.SendChatAction(chatID, "typing", opts)
		}(msg.Chat.Id, threadID)
	}

	// 6. Send to inbound channel
	if r.inbound != nil {
		select {
		case r.inbound <- cMsg:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}

func authorizeInboundMessage(ctx context.Context, auth InboundAuthorizer, senderID, bindAgent, chatType, sessionKey string) (bool, error) {
	if sessionAuthorizer, ok := auth.(InboundSessionAuthorizer); ok {
		return sessionAuthorizer.AuthorizeInboundSession(ctx, senderID, bindAgent, chatType, sessionKey)
	}
	return auth.AuthorizeInbound(ctx, senderID, bindAgent, chatType)
}

// HandleCallbackQuery handles inline button presses and translates them into synthesized canonical commands.
func (r *Router) HandleCallbackQuery(ctx context.Context, b *gotgbot.Bot, cb *gotgbot.CallbackQuery) error {
	if cb == nil {
		return nil
	}

	var botID int64
	var botUsername string
	var bindAgent string
	if b != nil {
		botID = b.Id
		botUsername = b.Username
		r.mu.RLock()
		if r.bindAgents != nil {
			bindAgent = r.bindAgents[botID]
		}
		r.mu.RUnlock()
	}

	data := cb.Data
	if data == "" {
		if b != nil {
			_, _ = b.AnswerCallbackQuery(cb.Id, &gotgbot.AnswerCallbackQueryOpts{})
		}
		return nil
	}

	// 1. Handle HITL interactive approval callback (answered by HandleCallback with alerts/toasts)
	if strings.HasPrefix(data, "hitl:") {
		if r.hitlCoordinator != nil {
			return r.hitlCoordinator.HandleCallbackWithBot(context.Background(), cb.Id, cb.From.Id, data, b)
		}
		if b != nil {
			_, _ = b.AnswerCallbackQuery(cb.Id, &gotgbot.AnswerCallbackQueryOpts{
				Text:      "⚠️ HITL Coordinator is offline.",
				ShowAlert: true,
			})
		}
		return nil
	}

	// 2. Answer other callback queries to dismiss loading state
	if b != nil {
		_, _ = b.AnswerCallbackQuery(cb.Id, &gotgbot.AnswerCallbackQueryOpts{})
	}

	// 3. Map compact callback data into synthesized slash command
	var synthCmd string
	switch {
	case strings.HasPrefix(data, "sec:preset:"):
		presetName := strings.TrimPrefix(data, "sec:preset:")
		synthCmd = fmt.Sprintf("/security preset %s", presetName)
	case strings.HasPrefix(data, "sec:redact:"):
		mode := strings.TrimPrefix(data, "sec:redact:")
		synthCmd = fmt.Sprintf("/security redact %s", mode)
	case strings.HasPrefix(data, "m:set:"):
		modelName := strings.TrimPrefix(data, "m:set:")
		synthCmd = fmt.Sprintf("/model %s", modelName)
	case data == "m:reset":
		synthCmd = "/model reset"
	case strings.HasPrefix(data, "eff:set:"):
		effortLvl := strings.TrimPrefix(data, "eff:set:")
		synthCmd = fmt.Sprintf("/effort %s", effortLvl)
	case data == "eff:reset":
		synthCmd = "/effort reset"
	case strings.HasPrefix(data, "c:sw:"):
		convID := strings.TrimPrefix(data, "c:sw:")
		synthCmd = fmt.Sprintf("/c switch %s", convID)
	case strings.HasPrefix(data, "c:pin:"):
		convID := strings.TrimPrefix(data, "c:pin:")
		synthCmd = fmt.Sprintf("/pin %s", convID)
	case strings.HasPrefix(data, "c:unpin:"):
		convID := strings.TrimPrefix(data, "c:unpin:")
		synthCmd = fmt.Sprintf("/unpin %s", convID)
	case data == "c:new":
		synthCmd = "/new"
	case strings.HasPrefix(data, "c:page:"):
		page := strings.TrimPrefix(data, "c:page:")
		synthCmd = fmt.Sprintf("/c %s", page)
	case strings.HasPrefix(data, "c:arc:"):
		convID := strings.TrimPrefix(data, "c:arc:")
		synthCmd = fmt.Sprintf("/c archive %s", convID)
	case strings.HasPrefix(data, "task:info:"):
		taskID := strings.TrimPrefix(data, "task:info:")
		synthCmd = fmt.Sprintf("/task %s", taskID)
	case strings.HasPrefix(data, "task:cancel:"):
		taskID := strings.TrimPrefix(data, "task:cancel:")
		synthCmd = fmt.Sprintf("/task cancel %s", taskID)
	case data == "task:clean":
		synthCmd = "/task clean"
	case strings.HasPrefix(data, "sched:cancel:"):
		taskID := strings.TrimPrefix(data, "sched:cancel:")
		synthCmd = fmt.Sprintf("/schedule cancel %s", taskID)
	case data == "hb:on":
		synthCmd = "/heartbeat on"
	case data == "hb:off":
		synthCmd = "/heartbeat off"
	case data == "hb:trigger":
		synthCmd = "/heartbeat trigger"
	default:
		return nil
	}

	// 4. Resolve Chat context
	var chatID string
	var chatType string
	var chatTitle string
	var threadID int64
	var msgID int64

	if cb.Message != nil {
		if m, ok := cb.Message.(*gotgbot.Message); ok {
			chatID = strconv.FormatInt(m.Chat.Id, 10)
			chatType = m.Chat.Type
			chatTitle = m.Chat.Title
			threadID = ExtractThreadID(m)
			msgID = m.MessageId
		}
	}

	if chatID == "" {
		chatID = strconv.FormatInt(cb.From.Id, 10)
		chatType = "private"
	}

	fullName := strings.TrimSpace(cb.From.FirstName + " " + cb.From.LastName)

	cMsg := domain.CanonicalMessage{
		ID:          strconv.FormatInt(msgID, 10),
		Timestamp:   time.Now(),
		Channel:     "telegram",
		BotID:       botID,
		BotUsername: botUsername,
		BindAgent:   bindAgent,
		Sender: domain.SenderUser{
			ID:       strconv.FormatInt(cb.From.Id, 10),
			Username: cb.From.Username,
			FullName: fullName,
		},
		Chat: domain.ChatContext{
			ID:       chatID,
			Type:     chatType,
			Title:    chatTitle,
			ThreadID: threadID,
		},
		Text:    synthCmd,
		RawText: synthCmd,
	}

	if r.inbound != nil {
		select {
		case r.inbound <- cMsg:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}
