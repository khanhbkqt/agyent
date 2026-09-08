package debouncer_test

import (
	"strings"
	"testing"
	"time"

	"agyent/internal/core/debouncer"
	"agyent/internal/core/domain"

	"github.com/stretchr/testify/assert"
)

func TestCoalescer_SingleAndMultipleMessages(t *testing.T) {
	t.Run("SingleMessageSanitization", func(t *testing.T) {
		msg := domain.CanonicalMessage{
			ID:        "msg-1",
			Text:      "Hello\x00 world",
			RawText:   "Raw\x00 text",
			Timestamp: time.Now(),
		}
		coalesced := debouncer.CoalesceMessages([]domain.CanonicalMessage{msg})
		assert.Equal(t, "Hello world", coalesced.Text)
		assert.Equal(t, "Raw text", coalesced.RawText)
	})

	t.Run("MultipleMessagesTextAndFlagsJoining", func(t *testing.T) {
		now := time.Now()
		msgs := []domain.CanonicalMessage{
			{
				ID:               "msg-1",
				Timestamp:        now,
				Text:             "Line 1",
				RawText:          "Raw 1",
				IsMentioned:      false,
				IsReplyToBot:     false,
				ReplyToMessageID: "",
				Attachments: []domain.Attachment{
					{ID: "att-1", FileName: "doc1.pdf", FilePath: "/tmp/doc1.pdf"},
				},
			},
			{
				ID:               "msg-2",
				Timestamp:        now.Add(500 * time.Millisecond),
				Text:             "Line 2",
				RawText:          "Raw 2",
				IsMentioned:      true,
				IsReplyToBot:     false,
				ReplyToMessageID: "reply-123",
				ReplyContext: &domain.ReplyContext{
					MessageID: "reply-123",
					Sender:    "User 1",
					Text:      "Quoted text 1",
				},
				Attachments: []domain.Attachment{
					{ID: "att-2", FileName: "chart.png", FilePath: "/tmp/chart.png"},
				},
			},
			{
				ID:               "msg-3",
				Timestamp:        now.Add(1000 * time.Millisecond),
				Text:             "Line 3",
				RawText:          "Raw 3",
				IsMentioned:      false,
				IsReplyToBot:     true,
				ReplyToMessageID: "reply-456",
				ReplyContext: &domain.ReplyContext{
					MessageID: "reply-456",
					Sender:    "Assistant",
					Text:      "Quoted text 2",
				},
				Attachments: []domain.Attachment{
					// Duplicate attachment
					{ID: "att-1", FileName: "doc1.pdf", FilePath: "/tmp/doc1.pdf"},
				},
			},
		}

		coalesced := debouncer.CoalesceMessages(msgs)
		assert.Equal(t, "Line 1\nLine 2\nLine 3", coalesced.Text)
		assert.Equal(t, "Raw 1\nRaw 2\nRaw 3", coalesced.RawText)
		assert.True(t, coalesced.IsMentioned)
		assert.True(t, coalesced.IsReplyToBot)
		assert.Equal(t, "reply-456", coalesced.ReplyToMessageID)
		assert.NotNil(t, coalesced.ReplyContext)
		assert.Equal(t, "reply-456", coalesced.ReplyContext.MessageID)
		assert.Equal(t, "Assistant", coalesced.ReplyContext.Sender)
		assert.Equal(t, "Quoted text 2", coalesced.ReplyContext.Text)
		assert.Len(t, coalesced.Attachments, 2, "duplicate attachment should be deduplicated")
		assert.Equal(t, "doc1.pdf", coalesced.Attachments[0].FileName)
		assert.Equal(t, "chart.png", coalesced.Attachments[1].FileName)
	})

	t.Run("SizeTruncationAt1MB", func(t *testing.T) {
		largeChunk := strings.Repeat("A", 600*1024) // 600KB
		msgs := []domain.CanonicalMessage{
			{ID: "1", Text: largeChunk},
			{ID: "2", Text: largeChunk},
		}

		coalesced := debouncer.CoalesceMessages(msgs)
		assert.Contains(t, coalesced.Text, "[TRUNCATED DUE TO SIZE LIMIT]")
		assert.LessOrEqual(t, len(coalesced.Text), debouncer.MaxTotalCoalescedBytes+100)
	})
}
