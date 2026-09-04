package zalo_test

import (
	"strings"
	"testing"

	"agyent/internal/adapters/channels/zalo"

	"github.com/stretchr/testify/assert"
)

func TestChunkZaloMessage_ShortMessage(t *testing.T) {
	short := "Đây là tin nhắn ngắn dưới 2000 ký tự."
	chunks := zalo.ChunkZaloMessage(short)
	assert.Len(t, chunks, 1)
	assert.Equal(t, short, chunks[0])
}

func TestChunkZaloMessage_LongMessageWithCodeBlock(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("Bắt đầu đoạn văn bản dài:\n\n")
	for i := 0; i < 60; i++ {
		sb.WriteString("Dòng mô tả chi tiết nội dung hệ thống cần báo cáo.\n")
	}
	sb.WriteString("```go\n")
	for i := 0; i < 50; i++ {
		sb.WriteString("func ProcessTurn(ctx context.Context) error { return nil }\n")
	}
	sb.WriteString("```\n")

	longText := sb.String()
	chunks := zalo.ChunkZaloMessage(longText)

	assert.True(t, len(chunks) >= 2)
	for i, chunk := range chunks {
		assert.True(t, len([]rune(chunk)) <= zalo.MaxZaloMessageRunes, "chunk %d exceeds max runes", i)
		// Check that code blocks are properly opened and closed in each chunk
		fenceCount := strings.Count(chunk, "```")
		assert.Equal(t, 0, fenceCount%2, "chunk %d has unclosed code fence", i)
	}
}
