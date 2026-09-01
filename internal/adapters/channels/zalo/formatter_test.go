package zalo_test

import (
	"testing"

	"agyent/internal/adapters/channels/zalo"

	"github.com/stretchr/testify/assert"
)

func TestZaloMessageFormatter_Builder(t *testing.T) {
	f := zalo.NewFormatter()
	f.Heading("THÔNG BÁO QUAN TRỌNG", 1)
	f.Text("Chào mừng ")
	f.Bold("Stevan Nguyen")
	f.Text(" đến với ")
	f.Color("Agyent Gateway", "c_15a85f")
	f.NewLine(2)
	f.ListItem("Hỗ trợ đa kênh siêu tốc")
	f.ListItem("Prefix KV-Cache 90%")
	f.Divider()
	f.CodeBlock("go run ./cmd/agyent run", "bash")

	md := f.BuildMarkdown()
	assert.Contains(t, md, "THÔNG BÁO QUAN TRỌNG")
	assert.Contains(t, md, "**Stevan Nguyen**")
	assert.Contains(t, md, "• Hỗ trợ đa kênh siêu tốc")
	assert.Contains(t, md, "```bash\ngo run ./cmd/agyent run\n```")
}

func TestZaloMessageFormatter_UTF16OffsetCalculation(t *testing.T) {
	f := zalo.NewFormatter()
	// Vietnamese characters with tones & Emoji (surrogate pair)
	f.Text("Xin chào 🤖 ")
	f.Bold("Nguyễn Văn A")
	f.Text("! Chúc một ngày tốt lành ⚽🔥")

	text, styles := f.BuildStyledText()
	assert.Equal(t, "Xin chào 🤖 Nguyễn Văn A! Chúc một ngày tốt lành ⚽🔥", text)
	assert.Len(t, styles, 1)

	// "Xin chào 🤖 " in UTF-16:
	// "Xin chào " = 9 code units
	// "🤖" (U+1F916) = 2 code units (surrogate pair)
	// " " = 1 code unit
	// Total offset = 12
	assert.Equal(t, 12, styles[0].Start)
	// "Nguyễn Văn A" in UTF-16 = 12 code units
	assert.Equal(t, 12, styles[0].Length)
	assert.Equal(t, []string{"b"}, styles[0].Styles)
}

func TestConvertHTMLToZaloMarkdown(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Bold and Code tags",
			input:    "<b>Important:</b> Use <code>/help</code> command &amp; explore.",
			expected: "**Important:** Use `/help` command & explore.",
		},
		{
			name:     "Pre Code block",
			input:    "<pre><code>fmt.Println(\"Hello &lt;World&gt;\")</code></pre>",
			expected: "```\nfmt.Println(\"Hello <World>\")\n```",
		},
		{
			name:     "Anchor and Italic",
			input:    "Visit <i>our website</i> at <a href=\"https://example.com\">Example</a>.",
			expected: "Visit *our website* at [Example](https://example.com).",
		},
		{
			name:     "Paragraphs and Line breaks",
			input:    "<p>First paragraph</p><br/>Second line",
			expected: "First paragraph\n\nSecond line",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := zalo.ConvertHTMLToZaloMarkdown(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSanitizePrivacyLeaks(t *testing.T) {
	input := `Error reading file C:\Users\john_doe\Desktop\secret.txt or /home/alice/project/main.go`
	cleaned := zalo.SanitizePrivacyLeaks(input)
	assert.NotContains(t, cleaned, `C:\Users\john_doe`)
	assert.NotContains(t, cleaned, `/home/alice`)
	assert.Contains(t, cleaned, `~\Desktop\secret.txt`)
	assert.Contains(t, cleaned, `~/project/main.go`)
}
