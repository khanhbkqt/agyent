package telegram

import (
	"strconv"
	"strings"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"agyent/internal/config"
)

// IsUserAdmin checks if the given user ID is configured as an admin.
func IsUserAdmin(cfg *config.Config, userID int64) bool {
	if cfg == nil {
		return false
	}
	for _, adminID := range cfg.Telegram.AdminUserIDs {
		if adminID == userID {
			return true
		}
	}
	return false
}

// IsGroupAllowed checks if the given group chat ID is whitelisted.
func IsGroupAllowed(cfg *config.Config, chatID int64) bool {
	if cfg == nil {
		return false
	}
	chatIDStr := strconv.FormatInt(chatID, 10)
	for _, allowed := range cfg.Telegram.AllowedGroupIDs {
		if allowed == chatIDStr {
			return true
		}
	}
	return false
}

// IsMessageForBot determines if a message in group/supergroup should be processed by the bot.
// It checks for slash commands, @mentions, or direct replies to bot messages.
func IsMessageForBot(botUsername string, botID int64, msg *gotgbot.Message) (shouldProcess bool, isMentioned bool, isReplyToBot bool) {
	if msg == nil {
		return false, false, false
	}

	// 1-1 Private chats are always for the bot
	if msg.Chat.Type == "private" {
		return true, false, false
	}

	text := msg.Text
	if text == "" {
		text = msg.Caption
	}
	trimmed := strings.TrimSpace(text)

	// 1. Check if message is a slash command
	if strings.HasPrefix(trimmed, "/") {
		fields := strings.Fields(trimmed)
		if len(fields) > 0 {
			cmdToken := fields[0]
			if atIdx := strings.Index(cmdToken, "@"); atIdx != -1 {
				targetBot := cmdToken[atIdx+1:]
				if botUsername != "" && !strings.EqualFold(targetBot, botUsername) {
					return false, false, false
				}
				isMentioned = true
			}
		}
		shouldProcess = true
	}

	// 2. Check if bot is mentioned via @username
	if botUsername != "" {
		mention := "@" + strings.ToLower(botUsername)
		lowerText := strings.ToLower(text)
		if strings.Contains(lowerText, mention) {
			shouldProcess = true
			isMentioned = true
		}
	}

	// 3. Check Telegram Message Entities for mentions
	for _, ent := range msg.Entities {
		if ent.Type == "mention" && botUsername != "" {
			runes := []rune(text)
			if int(ent.Offset+ent.Length) <= len(runes) {
				entText := string(runes[ent.Offset : ent.Offset+ent.Length])
				if strings.EqualFold(entText, "@"+botUsername) {
					shouldProcess = true
					isMentioned = true
				}
			}
		}
	}

	// 4. Check if message is a reply to the bot
	if msg.ReplyToMessage != nil && msg.ReplyToMessage.From != nil {
		if msg.ReplyToMessage.From.Id == botID {
			shouldProcess = true
			isReplyToBot = true
		}
	}

	return shouldProcess, isMentioned, isReplyToBot
}

// ExtractCleanText removes @bot_username mentions from text while preserving slash commands.
func ExtractCleanText(botUsername string, rawText string) string {
	if rawText == "" || botUsername == "" {
		return strings.TrimSpace(rawText)
	}

	mention := "@" + botUsername
	lowerMention := strings.ToLower(mention)

	// If command like "/p@my_bot hello", transform to "/p hello"
	words := strings.Fields(rawText)
	var cleanWords []string

	for _, w := range words {
		lowerW := strings.ToLower(w)
		if lowerW == lowerMention {
			continue // Skip pure mention token
		}
		if strings.HasPrefix(w, "/") && strings.Contains(lowerW, lowerMention) {
			w = strings.Replace(w, mention, "", -1)
			w = strings.Replace(w, strings.ToUpper(mention), "", -1)
			w = strings.Replace(w, lowerMention, "", -1)
		}
		cleanWords = append(cleanWords, w)
	}

	return strings.TrimSpace(strings.Join(cleanWords, " "))
}

// ExtractThreadID gets the forum topic thread ID from a message if present.
func ExtractThreadID(msg *gotgbot.Message) int64 {
	if msg == nil {
		return 0
	}
	if msg.IsTopicMessage || msg.MessageThreadId != 0 {
		return msg.MessageThreadId
	}
	return 0
}
