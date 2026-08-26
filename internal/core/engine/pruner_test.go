package engine_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"agyent/internal/core/engine"

	"github.com/stretchr/testify/assert"
)

func TestPruneToolOutput_ShortOutput(t *testing.T) {
	short := "Hello World! Standard output under 2000 chars."
	pruned := engine.PruneToolOutput(short)
	assert.Equal(t, short, pruned)
}

func TestPruneToolOutput_LargeOutput_HeadTailPreserved(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("HEAD_START_12345\n")
	for i := 0; i < 500; i++ {
		sb.WriteString("Middle log lines that consume unnecessary token budget...\n")
	}
	sb.WriteString("TAIL_ERROR_ROOT_CAUSE_99999\n")

	largeLog := sb.String()
	assert.Greater(t, utf8.RuneCountInString(largeLog), 2000)

	pruned := engine.PruneToolOutput(largeLog)

	assert.Contains(t, pruned, "HEAD_START_12345")
	assert.Contains(t, pruned, "TAIL_ERROR_ROOT_CAUSE_99999")
	assert.Contains(t, pruned, "Output truncated:")
	assert.Less(t, utf8.RuneCountInString(pruned), utf8.RuneCountInString(largeLog))
}

func TestPruneToolOutput_CodeBlockBalancing(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("```json\n")
	sb.WriteString("{\n  \"data\": \"")
	sb.WriteString(strings.Repeat("A", 3000))
	sb.WriteString("\"\n}\n```\n")

	largeJSON := sb.String()
	pruned := engine.PruneToolOutput(largeJSON)

	// Code block ticks count must be balanced (even number of ```)
	ticksCount := strings.Count(pruned, "```")
	assert.Equal(t, 0, ticksCount%2, "Markdown code block ticks must remain balanced after pruning")
}
