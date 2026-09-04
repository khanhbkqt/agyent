package agy_test

import (
	"testing"

	"agyent/internal/adapters/harness/agy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseModelsOutput(t *testing.T) {
	rawCLIOutput := `Fetching available models...
gemini-3.7-flash-high	Gemini 3.7 Flash (High)
gemini-3.7-flash-medium	Gemini 3.7 Flash (Medium)
gemini-3.7-flash-low	Gemini 3.7 Flash (Low)
gemini-3.1-pro-high	Gemini 3.1 Pro (High)
gemini-3.1-pro-low	Gemini 3.1 Pro (Low)
claude-sonnet-4-6	Claude Sonnet 4.6
gpt-oss-120b-medium	GPT-OSS 120B (Medium)
`

	parsed := agy.ParseModelsOutput(rawCLIOutput)
	require.Len(t, parsed, 4, "Should group suffixed models into 4 distinct base models")

	// 1. Check Gemini 3.7 Flash
	flash := parsed[0]
	assert.Equal(t, "gemini-3.7-flash", flash.ID)
	assert.Equal(t, "Gemini 3.7 Flash", flash.DisplayName)
	assert.Equal(t, []string{"high", "medium", "low"}, flash.SupportedEfforts)
	assert.Equal(t, "high", flash.DefaultEffort)
	assert.Contains(t, flash.Aliases, "flash")
	assert.Equal(t, 1048576, flash.MaxContextTokens)

	// 2. Check Gemini 3.1 Pro
	pro := parsed[1]
	assert.Equal(t, "gemini-3.1-pro", pro.ID)
	assert.Equal(t, "Gemini 3.1 Pro", pro.DisplayName)
	assert.Equal(t, []string{"high", "low"}, pro.SupportedEfforts)
	assert.Equal(t, "high", pro.DefaultEffort)
	assert.Contains(t, pro.Aliases, "pro")

	// 3. Check Claude Sonnet 4.6
	claude := parsed[2]
	assert.Equal(t, "claude-sonnet-4-6", claude.ID)
	assert.Equal(t, "Claude Sonnet 4.6", claude.DisplayName)
	assert.Empty(t, claude.SupportedEfforts)
	assert.Empty(t, claude.DefaultEffort)
	assert.Contains(t, claude.Aliases, "claude")
	assert.Equal(t, 200000, claude.MaxContextTokens)

	// 4. Check GPT OSS
	gpt := parsed[3]
	assert.Equal(t, "gpt-oss-120b", gpt.ID)
	assert.Equal(t, []string{"medium"}, gpt.SupportedEfforts)
	assert.Contains(t, gpt.Aliases, "gpt")
	assert.Equal(t, 128000, gpt.MaxContextTokens)
}
