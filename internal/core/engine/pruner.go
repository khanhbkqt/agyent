package engine

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	// MaxToolOutputChars is the threshold beyond which raw tool outputs are pruned on RAM.
	MaxToolOutputChars = 2000
	// PruneHeadChars is the number of characters preserved from the beginning of the tool output.
	PruneHeadChars = 800
	// PruneTailChars is the number of characters preserved from the end of the tool output (stack trace / error tail).
	PruneTailChars = 800
)

// PruneToolOutput performs Head-Tail Sandwich pruning on large stdout/stderr logs on RAM
// to protect the LLM context window while preserving essential error stack traces and balancing code blocks.
func PruneToolOutput(rawOutput string) string {
	runeCount := utf8.RuneCountInString(rawOutput)
	if runeCount <= MaxToolOutputChars {
		return rawOutput
	}

	runes := []rune(rawOutput)
	head := string(runes[:PruneHeadChars])
	tail := string(runes[len(runes)-PruneTailChars:])
	prunedCount := runeCount - (PruneHeadChars + PruneTailChars)

	// Check if the Head ends with an unclosed markdown code block
	inCodeBlock := strings.Count(head, "```")%2 != 0

	var sb strings.Builder
	sb.WriteString(head)
	if inCodeBlock {
		sb.WriteString("\n```") // Temporarily close code block for head
	}
	sb.WriteString(fmt.Sprintf("\n\n⚠️ [Output truncated: %d characters pruned by agyent In-Memory Pruner for token efficiency]\n\n", prunedCount))
	if inCodeBlock {
		sb.WriteString("```\n") // Re-open code block for tail
	}
	sb.WriteString(tail)

	return sb.String()
}
