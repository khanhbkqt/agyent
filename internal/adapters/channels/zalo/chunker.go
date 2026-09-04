package zalo

import (
	"strings"
)

const MaxZaloMessageRunes = 1950

// ChunkZaloMessage splits a long message into safe chunks (<= 1950 runes) preserving codeblocks.
func ChunkZaloMessage(text string) []string {
	if text == "" {
		return nil
	}

	runes := []rune(text)
	if len(runes) <= MaxZaloMessageRunes {
		return []string{text}
	}

	var chunks []string
	inCodeBlock := false
	codeBlockLang := ""

	remaining := text
	// Use safety buffer for fence injection
	safetyLimit := MaxZaloMessageRunes - 60

	for len([]rune(remaining)) > 0 {
		runesRemaining := []rune(remaining)
		if len(runesRemaining) <= MaxZaloMessageRunes && !inCodeBlock {
			chunks = append(chunks, remaining)
			break
		}

		limit := safetyLimit
		if len(runesRemaining) < limit {
			limit = len(runesRemaining)
		}

		targetChunk := string(runesRemaining[:limit])
		splitIdx := findSplitIndex(targetChunk)

		if splitIdx <= 0 {
			splitIdx = limit
		}

		chunkText := string(runesRemaining[:splitIdx])
		remaining = string(runesRemaining[splitIdx:])

		wasInCodeBlock := inCodeBlock
		fenceCount := strings.Count(chunkText, "```")

		if fenceCount%2 != 0 {
			inCodeBlock = !inCodeBlock
		}

		// If started in code block, inject opening fence
		if wasInCodeBlock {
			chunkText = "```" + codeBlockLang + "\n" + chunkText
		}

		// If ending still inside code block, inject closing fence
		if inCodeBlock {
			lastFenceIdx := strings.LastIndex(chunkText, "```")
			if lastFenceIdx != -1 {
				lineEnd := strings.Index(chunkText[lastFenceIdx:], "\n")
				if lineEnd != -1 {
					langCandidate := strings.TrimSpace(chunkText[lastFenceIdx+3 : lastFenceIdx+lineEnd])
					if len(langCandidate) < 20 {
						codeBlockLang = langCandidate
					}
				}
			}
			chunkText = chunkText + "\n```"
		}

		chunkTrimmed := strings.TrimSpace(chunkText)
		if chunkTrimmed != "" {
			chunks = append(chunks, chunkTrimmed)
		}
	}

	return chunks
}

// findSplitIndex searches backwards from the end for newline or space boundaries.
func findSplitIndex(chunk string) int {
	runes := []rune(chunk)
	n := len(runes)

	// Prefer paragraph boundary (\n\n)
	for i := n - 1; i >= n/2; i-- {
		if i > 0 && runes[i-1] == '\n' && runes[i] == '\n' {
			return i + 1
		}
	}

	// Next prefer single newline (\n)
	for i := n - 1; i >= n/2; i-- {
		if runes[i] == '\n' {
			return i + 1
		}
	}

	// Next prefer sentence boundary (period + space)
	for i := n - 1; i >= n/2; i-- {
		if runes[i] == ' ' && i > 0 && (runes[i-1] == '.' || runes[i-1] == '!' || runes[i-1] == '?') {
			return i + 1
		}
	}

	// Fallback to any space
	for i := n - 1; i >= n/2; i-- {
		if runes[i] == ' ' {
			return i + 1
		}
	}

	return n
}
