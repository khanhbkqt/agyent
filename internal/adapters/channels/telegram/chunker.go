package telegram

import (
	"strings"
	"unicode/utf8"
)

// SafeTelegramMessageLimit is the maximum raw markdown character length for a single chunk.
// It leaves comfortable headroom for HTML tag inflation (<pre><code>, <b>, <i>), HTML entity escaping
// (&lt;, &gt;, &amp;), and UTF-16 code units expansion to guarantee Telegram's hard limit of 4096 is never exceeded.
const SafeTelegramMessageLimit = 3200

// SplitMarkdownPreservingCodeBlocks splits a long text into chunks of at most maxLen runes,
// ensuring that active code blocks (```<lang>) are cleanly closed in chunk N and reopened in chunk N+1.
func SplitMarkdownPreservingCodeBlocks(text string, maxLen int) []string {
	if text == "" {
		return []string{""}
	}
	if maxLen <= 0 {
		maxLen = SafeTelegramMessageLimit
	}

	runeCount := utf8.RuneCountInString(text)
	if runeCount <= maxLen {
		return []string{text}
	}

	var chunks []string
	remaining := text
	activeLang := ""
	inCodeBlock := false

	for len(remaining) > 0 {
		remRunes := []rune(remaining)
		if len(remRunes) <= maxLen {
			chunks = append(chunks, remaining)
			break
		}

		// Find best split point <= maxLen
		targetLen := maxLen
		// Reserve space for potential code block closing "\n```" if inside code block
		if inCodeBlock && targetLen > 10 {
			targetLen -= 5
		}

		splitIdx := findSplitIndex(remRunes, targetLen)
		if splitIdx <= 0 {
			splitIdx = targetLen
		}

		chunkText := string(remRunes[:splitIdx])
		nextRemaining := string(remRunes[splitIdx:])

		// Analyze code blocks inside chunkText
		chunkInCodeBlock, chunkLang := analyzeCodeBlockState(chunkText, inCodeBlock, activeLang)

		var finalChunk string
		var nextPrefix string

		if chunkInCodeBlock {
			// Chunk ends inside code block -> close it in chunk
			finalChunk = chunkText + "\n```"
			// Next chunk must reopen code block
			if chunkLang != "" {
				nextPrefix = "```" + chunkLang + "\n"
			} else {
				nextPrefix = "```\n"
			}
			inCodeBlock = true
			activeLang = chunkLang
		} else {
			finalChunk = chunkText
			inCodeBlock = false
			activeLang = ""
		}

		chunks = append(chunks, finalChunk)
		remaining = nextPrefix + strings.TrimLeft(nextRemaining, "\r\n")
	}

	return chunks
}

func findSplitIndex(runes []rune, limit int) int {
	if limit >= len(runes) {
		return len(runes)
	}

	// 1. Try paragraph break "\n\n" in the last 40% of limit
	minScan := limit * 6 / 10
	if minScan < 1 {
		minScan = 1
	}

	for i := limit - 1; i >= minScan; i-- {
		if runes[i] == '\n' && i > 0 && runes[i-1] == '\n' {
			return i + 1
		}
	}

	// 2. Try single newline "\n"
	for i := limit - 1; i >= minScan; i-- {
		if runes[i] == '\n' {
			return i + 1
		}
	}

	// 3. Try space " "
	for i := limit - 1; i >= minScan; i-- {
		if runes[i] == ' ' || runes[i] == '\t' {
			return i + 1
		}
	}

	// 4. Hard cut at limit
	return limit
}

func analyzeCodeBlockState(text string, initialInCodeBlock bool, initialLang string) (bool, string) {
	inBlock := initialInCodeBlock
	currentLang := initialLang

	lines := strings.Split(text, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if inBlock {
				inBlock = false
				currentLang = ""
			} else {
				inBlock = true
				currentLang = strings.TrimPrefix(trimmed, "```")
				currentLang = strings.TrimSpace(currentLang)
			}
		}
	}

	return inBlock, currentLang
}
