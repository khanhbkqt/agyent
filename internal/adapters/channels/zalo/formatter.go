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
	text         string
	styles       []string
	isHeading    bool
	headingLevel int
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

// Color appends colored text with given hex, color code, or named color (red, green, yellow, orange).
func (f *ZaloMessageFormatter) Color(text string, colorCode string) *ZaloMessageFormatter {
	if text != "" {
		code := colorCode
		switch strings.ToLower(code) {
		case "red":
			code = ColorRed
		case "green":
			code = ColorGreen
		case "yellow":
			code = ColorYellow
		case "orange":
			code = ColorOrange
		case "blue":
			code = ColorBlue
		case "purple":
			code = ColorPurple
		case "gray", "grey":
			code = ColorGray
		default:
			if !strings.HasPrefix(code, "c_") {
				code = "c_" + strings.TrimPrefix(code, "#")
			}
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

// Big appends text with a large font size ("f_18" / "big").
func (f *ZaloMessageFormatter) Big(text string) *ZaloMessageFormatter {
	return f.Size(text, FontSizeBig)
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
	if level <= 0 {
		level = 1
	}
	sizeCode := FontSizeBig
	if level == 1 {
		sizeCode = FontSizeHuge
	}
	f.segments = append(f.segments, segment{
		text:         text,
		styles:       []string{"b", sizeCode},
		isHeading:    true,
		headingLevel: level,
	})
	f.NewLine()
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
		isBig := false
		color := ""

		for _, st := range seg.styles {
			switch {
			case st == "b":
				hasBold = true
			case st == "i":
				hasItalic = true
			case st == "u":
				hasUnderline = true
			case st == "s":
				hasStrikethrough = true
			case st == "f_18" || st == "f_20" || st == "big" || st == "huge":
				isBig = true
			case st == ColorRed || strings.ToLower(st) == "red":
				color = "red"
			case st == ColorGreen || strings.ToLower(st) == "green":
				color = "green"
			case st == ColorYellow || strings.ToLower(st) == "yellow":
				color = "yellow"
			case st == ColorOrange || strings.ToLower(st) == "orange":
				color = "orange"
			case strings.HasPrefix(st, "c_"):
				color = st
			}
		}

		if hasBold && !seg.isHeading {
			text = "**" + text + "**"
		}
		if hasItalic {
			text = "_" + text + "_"
		}
		if hasUnderline {
			text = "{underline}" + text + "{/underline}"
		}
		if hasStrikethrough {
			text = "~~" + text + "~~"
		}
		if isBig && !seg.isHeading {
			text = "{big}" + text + "{/big}"
		}
		if color != "" {
			text = fmt.Sprintf("{%s}%s{/%s}", color, text, color)
		}
		if seg.isHeading {
			prefix := strings.Repeat("#", seg.headingLevel)
			text = fmt.Sprintf("%s %s", prefix, text)
		}
		sb.WriteString(text)
	}
	return sb.String()
}

var (
	tagBoldRegex      = regexp.MustCompile(`(?i)<(?:b|strong)>(.*?)</(?:b|strong)>`)
	tagItalicRegex    = regexp.MustCompile(`(?i)<(?:i|em)>(.*?)</(?:i|em)>`)
	tagUnderlineRegex = regexp.MustCompile(`(?i)<(?:u|ins)>(.*?)</(?:u|ins)>`)
	tagStrikeRegex    = regexp.MustCompile(`(?i)<(?:s|del|strike)>(.*?)</(?:s|del|strike)>`)
	tagCodeRegex      = regexp.MustCompile(`(?i)<code>(.*?)</code>`)
	tagPreRegex       = regexp.MustCompile(`(?s)<pre(?:.*?)>(.*?)</pre>`)
	tagAnchorRegex    = regexp.MustCompile(`(?i)<a\s+href="([^"]+)">(.*?)</a>`)
	tagColorRegex     = regexp.MustCompile(`(?i)<font\s+color="(red|green|yellow|orange|#[0-9a-fA-F]{6})">(.*?)</font>`)
	tagBrRegex        = regexp.MustCompile(`(?i)<br\s*/?>`)
	tagPRegex         = regexp.MustCompile(`(?i)<p>(.*?)</p>`)
	tagGeneric        = regexp.MustCompile(`<[^>]+>`)
	homePathWin       = regexp.MustCompile(`[a-zA-Z]:\\(?:Users|Documents and Settings)\\[^\\]+`)
	homePathUnix      = regexp.MustCompile(`/(?:home|Users)/[^/]+`)

	codeBlockRegex      = regexp.MustCompile("(?s)```.*?```")
	inlineCodeRegex     = regexp.MustCompile("`[^`\n]+`")
	bulletRegex         = regexp.MustCompile(`(?m)^([ \t]*)[-*]\s+`)
	singleAsteriskRegex = regexp.MustCompile(`(^|[^*])\*([^*\n\r]+?)\*([^*]|$)`)
	singleTildeRegex    = regexp.MustCompile(`(^|[^~])~([^~\n\r]+?)~([^~]|$)`)
	markdownLinkRegex   = regexp.MustCompile(`\[([^\]]+)\]\((https?://[^\s\)]+)\)`)
	hrRegex             = regexp.MustCompile(`(?m)^([ \t]*)(?:[-*_]){3,}[ \t]*$`)
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

	// 4. Convert <i> / <em> to _..._ (Zalo Markdown italic)
	res = tagItalicRegex.ReplaceAllString(res, "_${1}_")

	// 5. Convert <u> / <ins> to {underline}...{/underline}
	res = tagUnderlineRegex.ReplaceAllString(res, "{underline}$1{/underline}")

	// 6. Convert <s> / <del> / <strike> to ~~...~~
	res = tagStrikeRegex.ReplaceAllString(res, "~~$1~~")

	// 7. Convert <font color="..."> to {color}...{/color}
	res = tagColorRegex.ReplaceAllStringFunc(res, func(m string) string {
		sub := tagColorRegex.FindStringSubmatch(m)
		if len(sub) == 3 {
			c := strings.ToLower(sub[1])
			if strings.HasPrefix(c, "#") {
				c = "c_" + strings.TrimPrefix(c, "#")
			}
			return fmt.Sprintf("{%s}%s{/%s}", c, sub[2], c)
		}
		return m
	})

	// 8. Convert <a href="url">text</a> to [text](url)
	res = tagAnchorRegex.ReplaceAllString(res, "[$2]($1)")

	// 9. Convert <br> and <p>
	res = tagBrRegex.ReplaceAllString(res, "\n")
	res = tagPRegex.ReplaceAllString(res, "$1\n\n")

	// 10. Strip any remaining HTML tags
	res = tagGeneric.ReplaceAllString(res, "")

	// 11. Decode HTML entities for remaining text
	res = html.UnescapeString(res)

	// 12. Restore code blocks and inline code
	for i, cb := range codeBlocks {
		placeholder := fmt.Sprintf("___AGY_PRE_BLOCK_%d___", i)
		res = strings.Replace(res, placeholder, cb, 1)
	}
	for i, ic := range inlineCodes {
		placeholder := fmt.Sprintf("___AGY_INLINE_CODE_%d___", i)
		res = strings.Replace(res, placeholder, ic, 1)
	}

	// 13. Normalize multiple consecutive newlines to at most 2
	multipleNewlines := regexp.MustCompile(`\n{3,}`)
	res = multipleNewlines.ReplaceAllString(res, "\n\n")

	return strings.TrimSpace(res)
}

// FormatToZaloMarkdown converts standard Markdown or HTML text into Zalo-compatible Markdown format.
func FormatToZaloMarkdown(content string) string {
	if strings.TrimSpace(content) == "" {
		return ""
	}

	res := content

	// 1. If content contains HTML tags, convert them first
	if strings.Contains(res, "<") {
		res = ConvertHTMLToZaloMarkdown(res)
	}

	// 2. Protect code blocks with placeholders
	var codeBlocks []string
	res = codeBlockRegex.ReplaceAllStringFunc(res, func(m string) string {
		idx := len(codeBlocks)
		codeBlocks = append(codeBlocks, m)
		return fmt.Sprintf("___AGY_CODE_BLOCK_%d___", idx)
	})

	// 3. Protect inline code with placeholders
	var inlineCodes []string
	res = inlineCodeRegex.ReplaceAllStringFunc(res, func(m string) string {
		idx := len(inlineCodes)
		inlineCodes = append(inlineCodes, m)
		return fmt.Sprintf("___AGY_INLINE_CODE_%d___", idx)
	})

	// 4. Convert markdown list items (- item, * item) to Zalo bullet points (• item)
	res = bulletRegex.ReplaceAllString(res, "${1}• ")

	// 5. Convert single asterisk italics (*italic*) to Zalo italic (_italic_), preserving **bold**
	for i := 0; i < 2; i++ {
		res = singleAsteriskRegex.ReplaceAllString(res, "${1}_${2}_${3}")
	}

	// 6. Convert single tilde strikethrough (~strike~) to Zalo double tildes (~~strike~~)
	for i := 0; i < 2; i++ {
		res = singleTildeRegex.ReplaceAllString(res, "${1}~~${2}~~${3}")
	}

	// 7. Convert Markdown links [Title](url) to Title (url) for clean display & auto-linking on Zalo
	res = markdownLinkRegex.ReplaceAllStringFunc(res, func(m string) string {
		sub := markdownLinkRegex.FindStringSubmatch(m)
		if len(sub) == 3 {
			title := strings.TrimSpace(sub[1])
			url := strings.TrimSpace(sub[2])
			if title == url || title == "" {
				return url
			}
			return fmt.Sprintf("%s (%s)", title, url)
		}
		return m
	})

	// 8. Convert markdown horizontal rules (---, ***, ___) to clean Zalo divider
	res = hrRegex.ReplaceAllString(res, "────────────────────────")

	// 9. Restore code blocks and inline code
	for i, cb := range codeBlocks {
		placeholder := fmt.Sprintf("___AGY_CODE_BLOCK_%d___", i)
		res = strings.Replace(res, placeholder, cb, 1)
	}
	for i, ic := range inlineCodes {
		placeholder := fmt.Sprintf("___AGY_INLINE_CODE_%d___", i)
		res = strings.Replace(res, placeholder, ic, 1)
	}

	// 10. Normalize multiple blank lines to maximum 2
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
