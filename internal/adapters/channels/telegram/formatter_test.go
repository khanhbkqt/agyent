package telegram

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEscapeHTML(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", ""},
		{"Hello World", "Hello World"},
		{"a < b && c > d", "a &lt; b &amp;&amp; c &gt; d"},
		{"<script>alert(1)</script>", "&lt;script&gt;alert(1)&lt;/script&gt;"},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.expected, EscapeHTML(tt.input))
	}
}

func TestStripHTMLTags(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", ""},
		{"<b>Hello</b> <i>World</i>", "Hello World"},
		{"<pre><code class=\"language-go\">fmt.Println(&lt;tag&gt;)</code></pre>", "fmt.Println(<tag>)"},
		{"No tags here &amp; there", "No tags here & there"},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.expected, StripHTMLTags(tt.input))
	}
}

func TestAutoCloseTelegramHTML(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty",
			input:    "",
			expected: "",
		},
		{
			name:     "already closed",
			input:    "<b>Hello</b>",
			expected: "<b>Hello</b>",
		},
		{
			name:     "unclosed bold",
			input:    "<b>Hello world",
			expected: "<b>Hello world</b>",
		},
		{
			name:     "unclosed code block",
			input:    "<pre><code class=\"language-go\">package main\nfunc main() {",
			expected: "<pre><code class=\"language-go\">package main\nfunc main() {</code></pre>",
		},
		{
			name:     "nested unclosed b and i",
			input:    "<b><i>Important notice",
			expected: "<b><i>Important notice</i></b>",
		},
		{
			name:     "unclosed blockquote",
			input:    "<blockquote>Quoted text line",
			expected: "<blockquote>Quoted text line</blockquote>",
		},
		{
			name:     "unclosed spoiler",
			input:    "<tg-spoiler>Secret message",
			expected: "<tg-spoiler>Secret message</tg-spoiler>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, AutoCloseTelegramHTML(tt.input))
		})
	}
}

func TestFormatMarkdownToTelegramHTML_CodeBlocks(t *testing.T) {
	input := "Here is some code:\n```go\nfunc main() {\n    if a < b && c > d {\n        fmt.Println(\"Hello\")\n    }\n}\n```\nDone."
	result := FormatMarkdownToTelegramHTML(input)

	assert.Contains(t, result, "<pre><code class=\"language-go\">")
	assert.Contains(t, result, "if a &lt; b &amp;&amp; c &gt; d")
	assert.Contains(t, result, "</code></pre>")
	assert.Contains(t, result, "Done.")

	// Unclosed code block during streaming
	streamInput := "```python\ndef hello():\n    print('hi')"
	streamResult := FormatMarkdownToTelegramHTML(streamInput)
	assert.Contains(t, streamResult, "<pre><code class=\"language-python\">")
	assert.True(t, strings.HasSuffix(streamResult, "</code></pre>"))
}

func TestFormatMarkdownToTelegramHTML_HeadingsAndLists(t *testing.T) {
	input := "# Heading 1\n## Heading 2\n### Heading 3\n- Item 1\n* Item 2\n+ Item 3"
	result := FormatMarkdownToTelegramHTML(input)

	assert.Contains(t, result, "<b>Heading 1</b>")
	assert.Contains(t, result, "<b>Heading 2</b>")
	assert.Contains(t, result, "<b>Heading 3</b>")
	assert.Contains(t, result, "• Item 1")
	assert.Contains(t, result, "• Item 2")
	assert.Contains(t, result, "• Item 3")
}

func TestFormatMarkdownToTelegramHTML_InlineFormatting(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "bold and italic",
			input:    "This is **bold** and *italic* and ***bold italic***.",
			expected: "This is <b>bold</b> and <i>italic</i> and <b><i>bold italic</i></b>.",
		},
		{
			name:     "strikethrough and inline code",
			input:    "Use `int x = 5;` and ~~strike~~.",
			expected: "Use <code>int x = 5;</code> and <s>strike</s>.",
		},
		{
			name:     "spoiler",
			input:    "Here is ||hidden spoiler|| text.",
			expected: "Here is <tg-spoiler>hidden spoiler</tg-spoiler> text.",
		},
		{
			name:     "links",
			input:    "Visit [Google](https://google.com) now.",
			expected: "Visit <a href=\"https://google.com\">Google</a> now.",
		},
		{
			name:     "identifiers with underscores preserved",
			input:    "Check variable_name_1 and my_func_name.",
			expected: "Check variable_name_1 and my_func_name.",
		},
		{
			name:     "HTML characters escaped in normal prose",
			input:    "if (x < 10 && y > 20) { return; }",
			expected: "if (x &lt; 10 &amp;&amp; y &gt; 20) { return; }",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := FormatMarkdownToTelegramHTML(tt.input)
			assert.Equal(t, tt.expected, actual)
		})
	}
}

func TestFormatMarkdownToTelegramHTML_Blockquotes(t *testing.T) {
	input := "> This is a quote\n> Second line of quote\nNormal line after."
	result := FormatMarkdownToTelegramHTML(input)

	assert.Contains(t, result, "<blockquote>This is a quote\nSecond line of quote</blockquote>")
	assert.Contains(t, result, "Normal line after.")
}

func TestFormatMarkdownToTelegramHTML_FileURILinkSanitization(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "windows file URI markdown link",
			input:    "em có thể mở rộng file [server.py](file:///C:/Users/stevan.nguyen/Desktop/projects/agyent/builtin/plugins/browser-camoufox/server.py) để bổ sung thêm",
			expected: "em có thể mở rộng file <code>server.py</code> để bổ sung thêm",
		},
		{
			name:     "unix file URI markdown link",
			input:    "check out [main.go](file:///home/ubuntu/projects/agyent/main.go) please",
			expected: "check out <code>main.go</code> please",
		},
		{
			name:     "relative local path link",
			input:    "see [config.yaml](./internal/config/config.yaml) for settings",
			expected: "see <code>config.yaml</code> for settings",
		},
		{
			name:     "raw host path masking in prose",
			input:    "Saved to C:\\Users\\stevan.nguyen\\Desktop\\report.pdf successfully.",
			expected: "Saved to ~/Desktop\\report.pdf successfully.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := FormatMarkdownToTelegramHTML(tt.input)
			assert.Equal(t, tt.expected, actual)
			// Ensure no raw host username or file:/// leaks remain in output or plaintext fallback
			assert.NotContains(t, actual, "stevan.nguyen")
			assert.NotContains(t, actual, "file:///")
			plainText := StripHTMLTags(actual)
			assert.NotContains(t, plainText, "stevan.nguyen")
			assert.NotContains(t, plainText, "file:///")
		})
	}
}
