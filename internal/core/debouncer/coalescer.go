package debouncer

import (
	"strings"

	"agyent/internal/core/domain"
)

// MaxTotalCoalescedBytes is the upper bound for coalesced text size (1MB) to prevent memory bombs.
const MaxTotalCoalescedBytes = 1024 * 1024

// SanitizeText cleans invalid UTF-8 sequences and removes dangerous null bytes (\x00).
func SanitizeText(s string) string {
	if s == "" {
		return ""
	}
	cleaned := strings.ToValidUTF8(s, "")
	cleaned = strings.ReplaceAll(cleaned, "\x00", "")
	return cleaned
}

// CoalesceMessages merges multiple CanonicalMessages from the same debouncing window into a single unified CanonicalMessage.
func CoalesceMessages(msgs []domain.CanonicalMessage) domain.CanonicalMessage {
	if len(msgs) == 0 {
		return domain.CanonicalMessage{}
	}
	if len(msgs) == 1 {
		m := msgs[0]
		m.Text = SanitizeText(m.Text)
		m.RawText = SanitizeText(m.RawText)
		return m
	}

	first := msgs[0]
	var textBuilder, rawTextBuilder strings.Builder
	var attachments []domain.Attachment
	seenAttachments := make(map[string]struct{})

	isMentioned := false
	isReplyToBot := false
	latestReplyToID := ""

	currentBytes := 0
	truncated := false

	for i, m := range msgs {
		cleanT := SanitizeText(m.Text)
		cleanR := SanitizeText(m.RawText)

		if !truncated {
			if i > 0 && cleanT != "" && textBuilder.Len() > 0 {
				textBuilder.WriteString("\n")
				currentBytes++
			}
			if currentBytes+len(cleanT) > MaxTotalCoalescedBytes {
				allowed := MaxTotalCoalescedBytes - currentBytes
				if allowed > 0 && allowed < len(cleanT) {
					textBuilder.WriteString(strings.ToValidUTF8(cleanT[:allowed], ""))
				}
				textBuilder.WriteString("\n[TRUNCATED DUE TO SIZE LIMIT]")
				truncated = true
			} else {
				textBuilder.WriteString(cleanT)
				currentBytes += len(cleanT)
			}
		}

		if rawTextBuilder.Len() < MaxTotalCoalescedBytes {
			if i > 0 && cleanR != "" && rawTextBuilder.Len() > 0 {
				rawTextBuilder.WriteString("\n")
			}
			if rawTextBuilder.Len()+len(cleanR) > MaxTotalCoalescedBytes {
				allowed := MaxTotalCoalescedBytes - rawTextBuilder.Len()
				if allowed > 0 && allowed < len(cleanR) {
					rawTextBuilder.WriteString(strings.ToValidUTF8(cleanR[:allowed], ""))
				}
			} else {
				rawTextBuilder.WriteString(cleanR)
			}
		}

		if m.IsMentioned {
			isMentioned = true
		}
		if m.IsReplyToBot {
			isReplyToBot = true
		}
		if m.ReplyToMessageID != "" {
			latestReplyToID = m.ReplyToMessageID
		}

		for _, att := range m.Attachments {
			key := att.FilePath
			if key == "" {
				key = att.ID + "_" + att.FileName
			}
			if _, exists := seenAttachments[key]; !exists {
				seenAttachments[key] = struct{}{}
				attachments = append(attachments, att)
			}
		}
	}

	result := first
	result.Text = textBuilder.String()
	result.RawText = rawTextBuilder.String()
	result.Attachments = attachments
	result.IsMentioned = isMentioned
	result.IsReplyToBot = isReplyToBot
	if latestReplyToID != "" {
		result.ReplyToMessageID = latestReplyToID
	}

	return result
}
