package telegram

import (
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"unicode"
)

var (
	winUserPathRegex      = regexp.MustCompile(`(?i)[a-zA-Z]:[/\\]Users[/\\][^/\\\s"'\)\]]+[/\\]?`)
	unixUserPathRegex     = regexp.MustCompile(`/(home|Users)/[^/\\\s"'\)\]]+[/\\]?`)
	validTelegramTagRegex = regexp.MustCompile(`^(?i)</?(b|strong|i|em|code|s|strike|del|u|pre|blockquote|tg-spoiler|a|tg-emoji)(\s+[a-zA-Z0-9_-]+(=("[^"]*"|'[^']*'|[^\s>]+))?)*\s*/?>`)
	validHTMLEntityRegex  = regexp.MustCompile(`^&(amp|lt|gt|quot|apos|#\d+|#x[0-9a-fA-F]+);`)
	htmlCommentRegex      = regexp.MustCompile(`(?s)<!--.*?-->`)
)

// SanitizePrivacyLeaks masks host user directory paths (e.g. C:\Users\username\ -> ~/)
// to prevent accidental host path or OS username leaks in chat channels.
func SanitizePrivacyLeaks(text string) string {
	if text == "" {
		return ""
	}
	text = winUserPathRegex.ReplaceAllString(text, "~/")
	text = unixUserPathRegex.ReplaceAllString(text, "~/")
	return text
}

var builderPool = sync.Pool{
	New: func() any {
		var sb strings.Builder
		sb.Grow(1024)
		return &sb
	},
}

// EscapeHTML escapes the minimal Telegram HTML entities (&, <, >).
func EscapeHTML(s string) string {
	if s == "" {
		return ""
	}
	var sb strings.Builder
	sb.Grow(len(s) + 16)
	for _, r := range s {
		switch r {
		case '&':
			sb.WriteString("&amp;")
		case '<':
			sb.WriteString("&lt;")
		case '>':
			sb.WriteString("&gt;")
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// StripHTMLTags removes all HTML tags and unescapes basic entities for clean plaintext fallback.
func StripHTMLTags(s string) string {
	if s == "" {
		return ""
	}
	var sb strings.Builder
	sb.Grow(len(s))
	inTag := false
	for _, r := range s {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' {
			inTag = false
			continue
		}
		if !inTag {
			sb.WriteRune(r)
		}
	}
	return html.UnescapeString(sb.String())
}

// FormatMarkdownToTelegramHTML converts CommonMark / GitHub-Flavored Markdown (GFM)
// into valid, beautifully formatted Telegram HTML with automatic tag closure.
func FormatMarkdownToTelegramHTML(md string) string {
	if strings.TrimSpace(md) == "" {
		return md
	}

	md = htmlCommentRegex.ReplaceAllString(md, "")
	md = SanitizePrivacyLeaks(md)

	sb := builderPool.Get().(*strings.Builder)
	sb.Reset()
	defer builderPool.Put(sb)

	lines := strings.Split(md, "\n")
	inCodeBlock := false
	codeBlockLang := ""
	inBlockquote := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// 1. Code Block Fence Check (```)
		if strings.HasPrefix(trimmed, "```") {
			if inCodeBlock {
				// Close active code block
				sb.WriteString("</code></pre>")
				inCodeBlock = false
				codeBlockLang = ""
			} else {
				// Close any open blockquote before code block (Telegram forbids nested pre in blockquote)
				if inBlockquote {
					sb.WriteString("</blockquote>")
					inBlockquote = false
				}
				// Start code block
				lang := strings.TrimPrefix(trimmed, "```")
				lang = sanitizeLangName(lang)
				codeBlockLang = lang
				inCodeBlock = true

				if codeBlockLang != "" {
					sb.WriteString(fmt.Sprintf("<pre><code class=\"language-%s\">", EscapeHTML(codeBlockLang)))
				} else {
					sb.WriteString("<pre><code>")
				}
			}

			// Add newline if not last line
			if i < len(lines)-1 {
				sb.WriteRune('\n')
			}
			continue
		}

		// If inside a code block, output literal line escaped, without parsing any markdown
		if inCodeBlock {
			sb.WriteString(EscapeHTML(line))
			if i < len(lines)-1 {
				sb.WriteRune('\n')
			}
			continue
		}

		// 2. Blockquote Check (> quote)
		if strings.HasPrefix(trimmed, ">") {
			quoteContent := strings.TrimPrefix(line, ">")
			quoteContent = strings.TrimPrefix(quoteContent, " ")

			if !inBlockquote {
				sb.WriteString("<blockquote>")
				inBlockquote = true
			} else {
				sb.WriteRune('\n')
			}

			formattedLine := formatInlineMarkdown(quoteContent)
			sb.WriteString(formattedLine)
			continue
		} else if inBlockquote {
			// Line is not a blockquote, close active blockquote
			sb.WriteString("</blockquote>\n")
			inBlockquote = false
		}

		// 3. Horizontal Rule Check (---, ***, ___)
		if isHorizontalRule(trimmed) {
			sb.WriteString("──────────────")
			if i < len(lines)-1 {
				sb.WriteRune('\n')
			}
			continue
		}

		// 4. Heading Check (# Heading)
		if headingText, isHeading := parseHeading(line); isHeading {
			formatted := formatInlineMarkdown(headingText)
			sb.WriteString("<b>")
			sb.WriteString(formatted)
			sb.WriteString("</b>")
			if i < len(lines)-1 {
				sb.WriteRune('\n')
			}
			continue
		}

		// 5. Bullet List Check (- item, * item, + item)
		if bulletText, isBullet := parseBulletList(line); isBullet {
			formatted := formatInlineMarkdown(bulletText)
			sb.WriteString("• ")
			sb.WriteString(formatted)
			if i < len(lines)-1 {
				sb.WriteRune('\n')
			}
			continue
		}

		// 6. Regular line: parse inline formatting
		formatted := formatInlineMarkdown(line)
		sb.WriteString(formatted)
		if i < len(lines)-1 {
			sb.WriteRune('\n')
		}
	}

	// Close open blockquote at EOF
	if inBlockquote {
		sb.WriteString("</blockquote>")
	}

	// Auto-close open code block or unclosed tags
	rawResult := sb.String()
	if inCodeBlock {
		rawResult += "</code></pre>"
	}

	return AutoCloseTelegramHTML(rawResult)
}

func sanitizeLangName(raw string) string {
	raw = strings.TrimSpace(raw)
	if idx := strings.IndexAny(raw, " :\t\r\n"); idx != -1 {
		raw = raw[:idx]
	}
	var clean strings.Builder
	for _, r := range raw {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '+' || r == '#' {
			clean.WriteRune(r)
		}
	}
	return clean.String()
}

func isHorizontalRule(s string) bool {
	if len(s) < 3 {
		return false
	}
	char := s[0]
	if char != '-' && char != '*' && char != '_' {
		return false
	}
	for i := 1; i < len(s); i++ {
		if s[i] != char {
			return false
		}
	}
	return true
}

func parseHeading(line string) (string, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(trimmed, "#") {
		return "", false
	}

	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}

	if level > 0 && level <= 6 && level < len(trimmed) && (trimmed[level] == ' ' || trimmed[level] == '\t') {
		content := strings.TrimSpace(trimmed[level:])
		return content, true
	}
	return "", false
}

func parseBulletList(line string) (string, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if len(trimmed) < 2 {
		return "", false
	}
	prefix := trimmed[0]
	if (prefix == '-' || prefix == '*' || prefix == '+') && (trimmed[1] == ' ' || trimmed[1] == '\t') {
		content := strings.TrimSpace(trimmed[2:])
		return content, true
	}
	return "", false
}

// formatInlineMarkdown handles inline code (`), spoilers (||), links ([text](url)),
// bold (**, __), italic (*, _), and strikethrough (~~).
func formatInlineMarkdown(text string) string {
	if text == "" {
		return ""
	}

	runes := []rune(text)
	n := len(runes)
	var sb strings.Builder
	sb.Grow(len(text) + 32)
	i := 0

	for i < n {
		r := runes[i]

		// 1. Inline Code (`code`)
		if r == '`' {
			end := -1
			for j := i + 1; j < n; j++ {
				if runes[j] == '`' {
					end = j
					break
				}
			}
			if end != -1 {
				codeContent := string(runes[i+1 : end])
				sb.WriteString("<code>")
				sb.WriteString(EscapeHTML(codeContent))
				sb.WriteString("</code>")
				i = end + 1
				continue
			} else {
				// Unclosed inline code
				codeContent := string(runes[i+1:])
				sb.WriteString("<code>")
				sb.WriteString(EscapeHTML(codeContent))
				sb.WriteString("</code>")
				break
			}
		}

		// 2. Spoiler (||spoiler||)
		if i+1 < n && r == '|' && runes[i+1] == '|' {
			end := -1
			for j := i + 2; j < n-1; j++ {
				if runes[j] == '|' && runes[j+1] == '|' {
					end = j
					break
				}
			}
			if end != -1 {
				spoilerText := formatInlineMarkdown(string(runes[i+2 : end]))
				sb.WriteString("<tg-spoiler>")
				sb.WriteString(spoilerText)
				sb.WriteString("</tg-spoiler>")
				i = end + 2
				continue
			}
		}

		// 3. Strikethrough (~~strike~~)
		if i+1 < n && r == '~' && runes[i+1] == '~' {
			end := -1
			for j := i + 2; j < n-1; j++ {
				if runes[j] == '~' && runes[j+1] == '~' {
					end = j
					break
				}
			}
			if end != -1 {
				strikeText := formatInlineMarkdown(string(runes[i+2 : end]))
				sb.WriteString("<s>")
				sb.WriteString(strikeText)
				sb.WriteString("</s>")
				i = end + 2
				continue
			}
		}

		// 4. Bold + Italic (***bold italic*** or ___bold italic___)
		if i+2 < n && ((r == '*' && runes[i+1] == '*' && runes[i+2] == '*') ||
			(r == '_' && runes[i+1] == '_' && runes[i+2] == '_')) {
			mark := r
			end := -1
			for j := i + 3; j < n-2; j++ {
				if runes[j] == mark && runes[j+1] == mark && runes[j+2] == mark {
					end = j
					break
				}
			}
			if end != -1 {
				inner := formatInlineMarkdown(string(runes[i+3 : end]))
				sb.WriteString("<b><i>")
				sb.WriteString(inner)
				sb.WriteString("</i></b>")
				i = end + 3
				continue
			}
		}

		// 5. Bold (**bold** or __bold__)
		if i+1 < n && ((r == '*' && runes[i+1] == '*') || (r == '_' && runes[i+1] == '_')) {
			mark := r
			end := -1
			for j := i + 2; j < n-1; j++ {
				if runes[j] == mark && runes[j+1] == mark {
					end = j
					break
				}
			}
			if end != -1 {
				inner := formatInlineMarkdown(string(runes[i+2 : end]))
				sb.WriteString("<b>")
				sb.WriteString(inner)
				sb.WriteString("</b>")
				i = end + 2
				continue
			}
		}

		// 6. Italic (*italic* or _italic_)
		if r == '*' || r == '_' {
			// Skip underscore inside words like `my_variable_name`
			if r == '_' && i > 0 && unicode.IsLetter(runes[i-1]) && i+1 < n && unicode.IsLetter(runes[i+1]) {
				sb.WriteRune(r)
				i++
				continue
			}

			end := -1
			for j := i + 1; j < n; j++ {
				if runes[j] == r {
					// Check closing underscore inside word
					if r == '_' && j+1 < n && unicode.IsLetter(runes[j+1]) {
						continue
					}
					end = j
					break
				}
			}
			if end != -1 && end > i+1 {
				inner := formatInlineMarkdown(string(runes[i+1 : end]))
				sb.WriteString("<i>")
				sb.WriteString(inner)
				sb.WriteString("</i>")
				i = end + 1
				continue
			}
		}

		// 7. Images (![alt](url))
		if r == '!' && i+1 < n && runes[i+1] == '[' {
			closeBracket := -1
			for j := i + 2; j < n; j++ {
				if runes[j] == ']' {
					closeBracket = j
					break
				}
			}

			if closeBracket != -1 && closeBracket+1 < n && runes[closeBracket+1] == '(' {
				closeParen := -1
				for k := closeBracket + 2; k < n; k++ {
					if runes[k] == ')' {
						closeParen = k
						break
					}
				}

				if closeParen != -1 {
					alt := string(runes[i+2 : closeBracket])
					rawURL := strings.TrimSpace(string(runes[closeBracket+2 : closeParen]))
					if isValidURL(rawURL) {
						if alt == "" {
							alt = "Photo"
						}
						sb.WriteString(fmt.Sprintf("<a href=\"%s\">[🖼️ %s]</a>", EscapeHTML(rawURL), formatInlineMarkdown(alt)))
					} else {
						if alt != "" {
							sb.WriteString("<code>")
							sb.WriteString(formatInlineMarkdown(alt))
							sb.WriteString("</code>")
						}
					}
					i = closeParen + 1
					continue
				}
			}
		}

		// 8. Links ([label](url))
		if r == '[' {
			closeBracket := -1
			for j := i + 1; j < n; j++ {
				if runes[j] == ']' {
					closeBracket = j
					break
				}
			}

			if closeBracket != -1 && closeBracket+1 < n && runes[closeBracket+1] == '(' {
				closeParen := -1
				for k := closeBracket + 2; k < n; k++ {
					if runes[k] == ')' {
						closeParen = k
						break
					}
				}

				if closeParen != -1 {
					label := string(runes[i+1 : closeBracket])
					rawURL := strings.TrimSpace(string(runes[closeBracket+2 : closeParen]))
					if isValidURL(rawURL) {
						sb.WriteString(fmt.Sprintf("<a href=\"%s\">", EscapeHTML(rawURL)))
						sb.WriteString(formatInlineMarkdown(label))
						sb.WriteString("</a>")
						i = closeParen + 1
						continue
					} else if isFileOrLocalURI(rawURL) {
						// Privacy protection: Sanitize local file:/// URLs or paths and render clean label as inline code
						cleanLabel := formatInlineMarkdown(label)
						sb.WriteString("<code>")
						sb.WriteString(cleanLabel)
						sb.WriteString("</code>")
						i = closeParen + 1
						continue
					}
				}
			}
		}

		// 8. Pass-through for valid existing Telegram HTML tags (e.g. <b>, <code>, <i>, <blockquote>, <a href="...">)
		if r == '<' {
			rem := string(runes[i:])
			loc := validTelegramTagRegex.FindStringIndex(rem)
			if loc != nil && loc[0] == 0 {
				tagStr := rem[:loc[1]]
				sb.WriteString(tagStr)
				i += len([]rune(tagStr))
				continue
			}
		}

		// 9. Pass-through for valid existing HTML entities (e.g. &amp;, &lt;, &gt;)
		if r == '&' {
			rem := string(runes[i:])
			loc := validHTMLEntityRegex.FindStringIndex(rem)
			if loc != nil && loc[0] == 0 {
				entityStr := rem[:loc[1]]
				sb.WriteString(entityStr)
				i += len([]rune(entityStr))
				continue
			}
		}

		// Default: Escape HTML special characters
		switch r {
		case '&':
			sb.WriteString("&amp;")
		case '<':
			sb.WriteString("&lt;")
		case '>':
			sb.WriteString("&gt;")
		default:
			sb.WriteRune(r)
		}
		i++
	}

	return sb.String()
}

func isFileOrLocalURI(raw string) bool {
	if raw == "" {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(raw))
	if strings.HasPrefix(lower, "file:") ||
		strings.HasPrefix(lower, "./") ||
		strings.HasPrefix(lower, "../") ||
		strings.HasPrefix(lower, "/") ||
		strings.HasPrefix(lower, "\\") ||
		strings.HasPrefix(lower, "~/") ||
		(len(lower) >= 2 && lower[1] == ':' && (lower[0] >= 'a' && lower[0] <= 'z')) {
		return true
	}
	return false
}

func isValidURL(raw string) bool {
	if raw == "" {
		return false
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") ||
		strings.HasPrefix(raw, "tg://") || strings.HasPrefix(raw, "mailto:") {
		_, err := url.Parse(raw)
		return err == nil
	}
	return false
}

// AutoCloseTelegramHTML inspects open HTML tags and appends closing tags in LIFO order.
// Supports Telegram tags: pre, code, b, i, s, u, blockquote, tg-spoiler, a.
func AutoCloseTelegramHTML(htmlText string) string {
	if htmlText == "" {
		return ""
	}

	var openStack []string
	runes := []rune(htmlText)
	n := len(runes)
	i := 0

	for i < n {
		if runes[i] == '<' {
			// Find closing '>'
			end := -1
			for j := i + 1; j < n; j++ {
				if runes[j] == '>' {
					end = j
					break
				}
			}

			if end != -1 {
				tagContent := strings.TrimSpace(string(runes[i+1 : end]))
				if strings.HasPrefix(tagContent, "/") {
					// Closing tag, e.g. </code> or </b>
					closingTagName := strings.TrimPrefix(tagContent, "/")
					closingTagName = strings.ToLower(strings.TrimSpace(closingTagName))

					// Remove from stack if matches top, or search backwards
					for k := len(openStack) - 1; k >= 0; k-- {
						if openStack[k] == closingTagName {
							openStack = append(openStack[:k], openStack[k+1:]...)
							break
						}
					}
				} else if !strings.HasSuffix(tagContent, "/") {
					// Opening tag, e.g. <pre>, <code class="...">, <b>, <blockquote>
					fields := strings.Fields(tagContent)
					if len(fields) > 0 {
						tagName := strings.ToLower(fields[0])
						switch tagName {
						case "pre", "code", "b", "i", "s", "u", "blockquote", "tg-spoiler", "a":
							openStack = append(openStack, tagName)
						}
					}
				}
				i = end + 1
				continue
			}
		}
		i++
	}

	if len(openStack) == 0 {
		return htmlText
	}

	var sb strings.Builder
	sb.WriteString(htmlText)

	// Close tags in reverse order (LIFO)
	for k := len(openStack) - 1; k >= 0; k-- {
		sb.WriteString("</")
		sb.WriteString(openStack[k])
		sb.WriteString(">")
	}

	return sb.String()
}
