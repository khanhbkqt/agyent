package zalo

import (
	"fmt"
	"html"
	"regexp"
	"strings"
	"unicode/utf16"
)

// Pre-defined color codes for Zalo Bot rich text.
const (
	ColorRed    = "c_db342e"
	ColorGreen  = "c_15a85f"
	ColorYellow = "c_f7b503"
	ColorOrange = "c_f27806"
	ColorBlue   = "c_1890ff"
	ColorPurple = "c_722ed1"
	ColorGray   = "c_8c8c8c"
)

// Pre-defined font size codes for Zalo Bot rich text.
const (
	FontSizeNormal = "f_14"
	FontSizeMedium = "f_16"
	FontSizeBig    = "f_18"
	FontSizeHuge   = "f_20"
)

// segment represents an internal chunk of text with associated style tokens.
type segment struct {
	text   string
	styles []string
}

// ZaloMessageFormatter provides a fluent builder for composing Zalo Bot messages.
type ZaloMessageFormatter struct {
	segments []segment
}

// NewFormatter creates a new message formatter builder.
func NewFormatter() *ZaloMessageFormatter {
	return &ZaloMessageFormatter{
		segments: make([]segment, 0),
	}
}

// Text appends unstyled plain text.
func (f *ZaloMessageFormatter) Text(text string) *ZaloMessageFormatter {
	if text != "" {
		f.segments = append(f.segments, segment{text: text})
	}
	return f
}

// Bold appends bold text ("b").
func (f *ZaloMessageFormatter) Bold(text string) *ZaloMessageFormatter {
	if text != "" {
		f.segments = append(f.segments, segment{text: text, styles: []string{"b"}})
	}
	return f
}

// Italic appends italic text ("i").
func (f *ZaloMessageFormatter) Italic(text string) *ZaloMessageFormatter {
	if text != "" {
		f.segments = append(f.segments, segment{text: text, styles: []string{"i"}})
	}
	return f
}

// Underline appends underlined text ("u").
func (f *ZaloMessageFormatter) Underline(text string) *ZaloMessageFormatter {
	if text != "" {
		f.segments = append(f.segments, segment{text: text, styles: []string{"u"}})
	}
	return f
}

// Strikethrough appends strikethrough text ("s").
func (f *ZaloMessageFormatter) Strikethrough(text string) *ZaloMessageFormatter {
	if text != "" {
		f.segments = append(f.segments, segment{text: text, styles: []string{"s"}})
	}
	return f
}

// Color appends colored text with given hex or color code.
func (f *ZaloMessageFormatter) Color(text string, colorCode string) *ZaloMessageFormatter {
	if text != "" {
		code := colorCode
		if !strings.HasPrefix(code, "c_") {
			code = "c_" + strings.TrimPrefix(code, "#")
		}
		f.segments = append(f.segments, segment{text: text, styles: []string{code}})
	}
	return f
}

// Size appends text with a specific font size.
func (f *ZaloMessageFormatter) Size(text string, fontSize string) *ZaloMessageFormatter {
	if text != "" {
		code := fontSize
		if !strings.HasPrefix(code, "f_") {
			code = "f_" + code
		}
		f.segments = append(f.segments, segment{text: text, styles: []string{code}})
	}
	return f
}

// Styled appends text with multiple composite styles (e.g. bold + color + size).
func (f *ZaloMessageFormatter) Styled(text string, styles []string) *ZaloMessageFormatter {
	if text != "" {
		f.segments = append(f.segments, segment{text: text, styles: styles})
	}
	return f
}

// Heading appends a styled heading line with bold and larger font size.
func (f *ZaloMessageFormatter) Heading(text string, level int) *ZaloMessageFormatter {
	sizeCode := FontSizeBig
	if level == 1 {
		sizeCode = FontSizeHuge
	}
	f.segments = append(f.segments, segment{
		text:   text + "\n",
		styles: []string{"b", sizeCode},
	})
	return f
}

// ListItem appends a formatted list item bullet.
func (f *ZaloMessageFormatter) ListItem(text string) *ZaloMessageFormatter {
	f.Text("• ").Text(text).NewLine()
	return f
}

// Divider appends a visual separator line.
func (f *ZaloMessageFormatter) Divider() *ZaloMessageFormatter {
	f.Text("────────────────────────\n")
	return f
}

// InlineCode appends monospaced code.
func (f *ZaloMessageFormatter) InlineCode(code string) *ZaloMessageFormatter {
	f.Text("`" + code + "`")
	return f
}

// CodeBlock appends a multiline code block.
func (f *ZaloMessageFormatter) CodeBlock(code string, lang ...string) *ZaloMessageFormatter {
	language := ""
	if len(lang) > 0 {
		language = lang[0]
	}
	f.Text(fmt.Sprintf("```%s\n%s\n```\n", language, code))
	return f
}

// NewLine appends newline characters.
func (f *ZaloMessageFormatter) NewLine(count ...int) *ZaloMessageFormatter {
	n := 1
	if len(count) > 0 && count[0] > 0 {
		n = count[0]
	}
	f.Text(strings.Repeat("\n", n))
	return f
}

// UTF16Length calculates the length of a string in UTF-16 code units (surrogate pairs count as 2).
func UTF16Length(s string) int {
	return len(utf16.Encode([]rune(s)))
}

// BuildStyledText constructs plain text and calculates exact UTF-16 offset & length for text_styles runs.
func (f *ZaloMessageFormatter) BuildStyledText() (string, []TextStyleItem) {
	var fullBuilder strings.Builder
	var styles []TextStyleItem

	currentOffset := 0
	for _, seg := range f.segments {
		segLen := UTF16Length(seg.text)
		if len(seg.styles) > 0 && segLen > 0 {
			styles = append(styles, TextStyleItem{
				Start:  currentOffset,
				Length: segLen,
				Styles: seg.styles,
			})
		}
		fullBuilder.WriteString(seg.text)
		currentOffset += segLen
	}
	return fullBuilder.String(), styles
}

// BuildMarkdown renders segments as Zalo-compatible Markdown with custom color tag conversions.
func (f *ZaloMessageFormatter) BuildMarkdown() string {
	var sb strings.Builder
	for _, seg := range f.segments {
		text := seg.text
		hasBold := false
		hasItalic := false
		hasUnderline := false
		hasStrikethrough := false

		for _, st := range seg.styles {
			switch st {
			case "b":
				hasBold = true
			case "i":
				hasItalic = true
			case "u":
				hasUnderline = true
			case "s":
				hasStrikethrough = true
			}
		}

		if hasStrikethrough {
			text = "~" + text + "~"
		}
		if hasUnderline {
			text = "_" + text + "_"
		}
		if hasItalic {
			text = "*" + text + "*"
		}
		if hasBold {
			text = "**" + text + "**"
		}
		sb.WriteString(text)
	}
	return sb.String()
}

var (
	tagBoldRegex   = regexp.MustCompile(`(?i)<(?:b|strong)>(.*?)</(?:b|strong)>`)
	tagItalicRegex = regexp.MustCompile(`(?i)<(?:i|em)>(.*?)</(?:i|em)>`)
	tagCodeRegex   = regexp.MustCompile(`(?i)<code>(.*?)</code>`)
	tagPreRegex    = regexp.MustCompile(`(?s)<pre(?:.*?)>(.*?)</pre>`)
	tagAnchorRegex = regexp.MustCompile(`(?i)<a\s+href="([^"]+)">(.*?)</a>`)
	tagBrRegex     = regexp.MustCompile(`(?i)<br\s*/?>`)
	tagPRegex      = regexp.MustCompile(`(?i)<p>(.*?)</p>`)
	tagGeneric     = regexp.MustCompile(`<[^>]+>`)
	homePathWin    = regexp.MustCompile(`[a-zA-Z]:\\(?:Users|Documents and Settings)\\[^\\]+`)
	homePathUnix   = regexp.MustCompile(`/(?:home|Users)/[^/]+`)
)

// ConvertHTMLToZaloMarkdown converts HTML entities and tags into Zalo-compatible Markdown.
func ConvertHTMLToZaloMarkdown(htmlContent string) string {
	if htmlContent == "" {
		return ""
	}

	res := htmlContent

	// 1. Extract and protect pre code blocks with placeholders
	var codeBlocks []string
	res = tagPreRegex.ReplaceAllStringFunc(res, func(m string) string {
		inner := tagPreRegex.FindStringSubmatch(m)
		if len(inner) > 1 {
			code := tagCodeRegex.ReplaceAllString(inner[1], "$1")
			codeDecoded := html.UnescapeString(code)
			idx := len(codeBlocks)
			codeBlocks = append(codeBlocks, fmt.Sprintf("\n```\n%s\n```\n", strings.TrimSpace(codeDecoded)))
			return fmt.Sprintf("___AGY_PRE_BLOCK_%d___", idx)
		}
		return m
	})

	// 2. Extract inline code with placeholders
	var inlineCodes []string
	res = tagCodeRegex.ReplaceAllStringFunc(res, func(m string) string {
		inner := tagCodeRegex.FindStringSubmatch(m)
		if len(inner) > 1 {
			codeDecoded := html.UnescapeString(inner[1])
			idx := len(inlineCodes)
			inlineCodes = append(inlineCodes, fmt.Sprintf("`%s`", codeDecoded))
			return fmt.Sprintf("___AGY_INLINE_CODE_%d___", idx)
		}
		return m
	})

	// 3. Convert <b> / <strong> to **...**
	res = tagBoldRegex.ReplaceAllString(res, "**$1**")

	// 4. Convert <i> / <em> to *...*
	res = tagItalicRegex.ReplaceAllString(res, "*$1*")

	// 5. Convert <a href="url">text</a> to [text](url)
	res = tagAnchorRegex.ReplaceAllString(res, "[$2]($1)")

	// 6. Convert <br> and <p>
	res = tagBrRegex.ReplaceAllString(res, "\n")
	res = tagPRegex.ReplaceAllString(res, "$1\n\n")

	// 7. Strip any remaining HTML tags
	res = tagGeneric.ReplaceAllString(res, "")

	// 8. Decode HTML entities for remaining text
	res = html.UnescapeString(res)

	// 9. Restore code blocks and inline code
	for i, cb := range codeBlocks {
		placeholder := fmt.Sprintf("___AGY_PRE_BLOCK_%d___", i)
		res = strings.Replace(res, placeholder, cb, 1)
	}
	for i, ic := range inlineCodes {
		placeholder := fmt.Sprintf("___AGY_INLINE_CODE_%d___", i)
		res = strings.Replace(res, placeholder, ic, 1)
	}

	// 10. Normalize multiple consecutive newlines to at most 2
	multipleNewlines := regexp.MustCompile(`\n{3,}`)
	res = multipleNewlines.ReplaceAllString(res, "\n\n")

	return strings.TrimSpace(res)
}

// SanitizePrivacyLeaks replaces local workstation absolute home paths with generic '~/' prefixes.
func SanitizePrivacyLeaks(text string) string {
	if text == "" {
		return ""
	}
	text = homePathWin.ReplaceAllString(text, "~")
	text = homePathUnix.ReplaceAllString(text, "~")
	return text
}
