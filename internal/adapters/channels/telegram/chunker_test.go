package telegram

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TC-MKD-01: AutoClose unclosed code block (```go...)
func TestAutoClose_UnclosedCodeBlock(t *testing.T) {
	input := "Here is some code:\n```go\nfmt.Println(\"hello\")"
	expected := "Here is some code:\n```go\nfmt.Println(\"hello\")\n```"
	actual := AutoCloseMarkdown(input)
	assert.Equal(t, expected, actual)
}

// TC-MKD-02: AutoClose unclosed bold/italic/strikethrough/spoiler
func TestAutoClose_UnclosedFormatting(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "unclosed bold",
			input:    "This is *bold text",
			expected: "This is *bold text*",
		},
		{
			name:     "unclosed italic",
			input:    "This is _italic text",
			expected: "This is _italic text_",
		},
		{
			name:     "unclosed strikethrough",
			input:    "This is ~strike",
			expected: "This is ~strike~",
		},
		{
			name:     "unclosed spoiler",
			input:    "This is ||spoiler",
			expected: "This is ||spoiler||",
		},
		{
			name:     "nested unclosed bold and italic",
			input:    "This is *bold and _italic",
			expected: "This is *bold and _italic_*",
		},
		{
			name:     "unclosed inline code",
			input:    "Use the `print command",
			expected: "Use the `print command`",
		},
		{
			name:     "already closed code block",
			input:    "```go\nfmt.Println()\n```",
			expected: "```go\nfmt.Println()\n```",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, AutoCloseMarkdown(tt.input))
		})
	}
}

// TC-MKD-03: Ignore escaped Markdown characters (\*, \_, \`)
func TestAutoClose_EscapedCharacters(t *testing.T) {
	input := `This has \*escaped\* and \_italic\_ but *unclosed bold`
	expected := `This has \*escaped\* and \_italic\_ but *unclosed bold*`
	actual := AutoCloseMarkdown(input)
	assert.Equal(t, expected, actual)
}

// TC-MKD-05: SplitMarkdownPreservingCodeBlocks (>4000 chars)
func TestSplitMarkdownPreservingCodeBlocks_CodeBlocks(t *testing.T) {
	// Create a long code block
	longBody := strings.Repeat("    x := 1 + 1\n", 400) // ~6400 chars
	input := "```go\npackage main\n" + longBody + "```"

	chunks := SplitMarkdownPreservingCodeBlocks(input, 4000)
	require.GreaterOrEqual(t, len(chunks), 2)

	// Chunk 1 should close the code block
	assert.True(t, strings.HasSuffix(strings.TrimSpace(chunks[0]), "```"), "Chunk 0 should end with closing code block")

	// Chunk 2 should reopen the code block with go language
	assert.True(t, strings.HasPrefix(strings.TrimSpace(chunks[1]), "```go"), "Chunk 1 should start with ```go")
	assert.True(t, strings.HasSuffix(strings.TrimSpace(chunks[len(chunks)-1]), "```"), "Last chunk should close code block")

	// Ensure no chunk exceeds 4000 runes
	for i, chunk := range chunks {
		assert.LessOrEqual(t, utf8.RuneCountInString(chunk), 4050, "Chunk %d rune count should be bounded", i)
	}
}

// TC-MKD-06: Multi-byte UTF-8 string boundary slicing
func TestSplitMarkdown_UnicodeBoundary(t *testing.T) {
	vietnameseSample := "Xin chào thế giới! 🚀 Đây là kiểm thử Unicode Tiếng Việt có dấu và Emoji 🎉. "
	repeated := strings.Repeat(vietnameseSample, 100) // ~7500 chars

	chunks := SplitMarkdownPreservingCodeBlocks(repeated, 4000)
	require.GreaterOrEqual(t, len(chunks), 2)

	for i, chunk := range chunks {
		assert.True(t, utf8.ValidString(chunk), "Chunk %d must be valid UTF-8", i)
		assert.LessOrEqual(t, utf8.RuneCountInString(chunk), 4000)
	}
}

// TC-MKD-07: Split extremely long non-breaking string (URL/Hex)
func TestSplitMarkdown_ExtremelyLongNonBreakingString(t *testing.T) {
	nonBreaking := strings.Repeat("A0b1C2d3E4F5", 500) // 6000 chars without whitespace

	chunks := SplitMarkdownPreservingCodeBlocks(nonBreaking, 4000)
	require.Equal(t, 2, len(chunks))
	assert.Equal(t, 4000, utf8.RuneCountInString(chunks[0]))
	assert.Equal(t, 2000, utf8.RuneCountInString(chunks[1]))
	assert.Equal(t, nonBreaking, chunks[0]+chunks[1])
}

// Test EscapeMarkdownV2
func TestEscapeMarkdownV2(t *testing.T) {
	input := "Hello [World] (test)! Version 1.0_beta *important* ~strike~ `code` >quote #tag + - = | { } ."
	escaped := EscapeMarkdownV2(input)
	assert.Contains(t, escaped, `\[World\]`)
	assert.Contains(t, escaped, `\(test\)\!`)
	assert.Contains(t, escaped, `1\.0\_beta`)
	assert.Contains(t, escaped, `\*important\*`)
}
