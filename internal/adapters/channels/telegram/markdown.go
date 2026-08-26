package telegram

import (
	"strings"
)

// AutoCloseMarkdown inspects unclosed MarkdownV2 formatting tokens and appends closing tags.
// Supports: code blocks (```), inline code (`), bold (*), italic (_), strikethrough (~), spoiler (||).
// Accurately ignores escaped characters (e.g. \* \_ \` \[ \]).
func AutoCloseMarkdown(text string) string {
	if text == "" {
		return ""
	}

	var (
		inCodeBlock  bool
		inInlineCode bool
		openStack    []string // stores opened tokens: "*", "_", "~", "||"
		runes        = []rune(text)
		n            = len(runes)
		i            = 0
	)

	for i < n {
		r := runes[i]

		// Escaped character: skip backslash and next char
		if r == '\\' {
			i += 2
			continue
		}

		// Check for code block ```
		if i+2 < n && runes[i] == '`' && runes[i+1] == '`' && runes[i+2] == '`' {
			if inCodeBlock {
				inCodeBlock = false
				i += 3
				continue
			} else if !inInlineCode {
				inCodeBlock = true
				i += 3
				continue
			}
		}

		// Check for inline code ` (only when not in code block)
		if r == '`' && !inCodeBlock {
			inInlineCode = !inInlineCode
			i++
			continue
		}

		// If inside code block or inline code, skip all other markdown formatting
		if inCodeBlock || inInlineCode {
			i++
			continue
		}

		// Check for spoiler ||
		if i+1 < n && runes[i] == '|' && runes[i+1] == '|' {
			if len(openStack) > 0 && openStack[len(openStack)-1] == "||" {
				openStack = openStack[:len(openStack)-1]
			} else {
				openStack = append(openStack, "||")
			}
			i += 2
			continue
		}

		// Check for bold *
		if r == '*' {
			if len(openStack) > 0 && openStack[len(openStack)-1] == "*" {
				openStack = openStack[:len(openStack)-1]
			} else {
				openStack = append(openStack, "*")
			}
			i++
			continue
		}

		// Check for italic _
		if r == '_' {
			if len(openStack) > 0 && openStack[len(openStack)-1] == "_" {
				openStack = openStack[:len(openStack)-1]
			} else {
				openStack = append(openStack, "_")
			}
			i++
			continue
		}

		// Check for strikethrough ~
		if r == '~' {
			if len(openStack) > 0 && openStack[len(openStack)-1] == "~" {
				openStack = openStack[:len(openStack)-1]
			} else {
				openStack = append(openStack, "~")
			}
			i++
			continue
		}

		i++
	}

	var sb strings.Builder
	sb.WriteString(text)

	// Close code block first if open
	if inCodeBlock {
		sb.WriteString("\n```")
		return sb.String()
	}

	// Close inline code if open
	if inInlineCode {
		sb.WriteString("`")
		return sb.String()
	}

	// Close formatting tokens in LIFO order
	for j := len(openStack) - 1; j >= 0; j-- {
		sb.WriteString(openStack[j])
	}

	return sb.String()
}

// EscapeMarkdownV2 escapes Telegram MarkdownV2 special characters outside code blocks.
// MarkdownV2 requires escaping: _ * [ ] ( ) ~ ` > # + - = | { } . ! \
func EscapeMarkdownV2(text string) string {
	var sb strings.Builder
	for _, r := range text {
		switch r {
		case '_', '*', '[', ']', '(', ')', '~', '`', '>', '#', '+', '-', '=', '|', '{', '}', '.', '!', '\\':
			sb.WriteRune('\\')
			sb.WriteRune(r)
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}
