package agy_test

import (
	"testing"

	"agyent/internal/adapters/harness/agy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseModelsOutput(t *testing.T) {
	rawCLIOutput := `Fetching available models...
gemini-3.8-flash-high	Gemini 3.8 Flash (High)
gemini-3.8-flash-medium	Gemini 3.8 Flash (Medium)
gemini-3.8-flash-low	Gemini 3.8 Flash (Low)
gemini-3.7-flash-high	Gemini 3.7 Flash (High)
gemini-3.7-flash-medium	Gemini 3.7 Flash (Medium)
gemini-3.7-flash-low	Gemini 3.7 Flash (Low)
gemini-3.6-flash-high	Gemini 3.6 Flash (High)
gemini-3.6-flash-medium	Gemini 3.6 Flash (Medium)
gemini-3.6-flash-low	Gemini 3.6 Flash (Low)
gemini-3.1-pro-high	Gemini 3.1 Pro (High)
gemini-3.1-pro-low	Gemini 3.1 Pro (Low)
claude-opus-5-5-low	Claude Opus 5.5 (Low)
claude-opus-5-5-medium	Claude Opus 5.5 (Medium)
claude-opus-5-5-high	Claude Opus 5.5 (High)
claude-sonnet-5-5-low	Claude Sonnet 5.5 (Low)
claude-sonnet-5-5-medium	Claude Sonnet 5.5 (Medium)
claude-sonnet-5-5-high	Claude Sonnet 5.5 (High)
gpt-oss-120b-medium	GPT-OSS 120B (Medium)
`

	parsed := agy.ParseModelsOutput(rawCLIOutput)
	require.Len(t, parsed, 7, "Should group suffixed models into 7 distinct base models")

	// 1. Check Gemini 3.8 Flash
	flash38 := parsed[0]
	assert.Equal(t, "gemini-3.8-flash", flash38.ID)
	assert.Equal(t, "Gemini 3.8 Flash", flash38.DisplayName)
	assert.Equal(t, []string{"high", "medium", "low"}, flash38.SupportedEfforts)
	assert.Equal(t, "high", flash38.DefaultEffort)
	assert.Contains(t, flash38.Aliases, "flash")
	assert.Contains(t, flash38.Aliases, "3.8")
	assert.Contains(t, flash38.Aliases, "3.8-flash")
	assert.Equal(t, 2097152, flash38.MaxContextTokens)

	// 2. Check Gemini 3.7 Flash
	flash := parsed[1]
	assert.Equal(t, "gemini-3.7-flash", flash.ID)
	assert.Equal(t, "Gemini 3.7 Flash", flash.DisplayName)
	assert.Equal(t, []string{"high", "medium", "low"}, flash.SupportedEfforts)
	assert.Equal(t, "high", flash.DefaultEffort)
	assert.Contains(t, flash.Aliases, "3.7")
	assert.Contains(t, flash.Aliases, "3.7-flash")
	assert.Equal(t, 2097152, flash.MaxContextTokens)

	// 3. Check Gemini 3.6 Flash
	flash36 := parsed[2]
	assert.Equal(t, "gemini-3.6-flash", flash36.ID)
	assert.Equal(t, "Gemini 3.6 Flash", flash36.DisplayName)
	assert.Equal(t, []string{"high", "medium", "low"}, flash36.SupportedEfforts)
	assert.Contains(t, flash36.Aliases, "3.6-flash")
	assert.Contains(t, flash36.Aliases, "3.5-flash")

	// 4. Check Gemini 3.1 Pro
	pro := parsed[3]
	assert.Equal(t, "gemini-3.1-pro", pro.ID)
	assert.Equal(t, "Gemini 3.1 Pro", pro.DisplayName)
	assert.Equal(t, []string{"high", "low"}, pro.SupportedEfforts)
	assert.Equal(t, "high", pro.DefaultEffort)
	assert.Contains(t, pro.Aliases, "pro")

	// 5. Check Claude Opus 5.5
	opus := parsed[4]
	assert.Equal(t, "claude-opus-5-5", opus.ID)
	assert.Equal(t, "Claude Opus 5.5", opus.DisplayName)
	assert.Equal(t, []string{"low", "medium", "high"}, opus.SupportedEfforts)
	assert.Equal(t, "high", opus.DefaultEffort)
	assert.Contains(t, opus.Aliases, "opus")
	assert.Contains(t, opus.Aliases, "claude-opus-4-6-thinking")
	assert.Equal(t, 200000, opus.MaxContextTokens)

	// 6. Check Claude Sonnet 5.5
	claude := parsed[5]
	assert.Equal(t, "claude-sonnet-5-5", claude.ID)
	assert.Equal(t, "Claude Sonnet 5.5", claude.DisplayName)
	assert.Equal(t, []string{"low", "medium", "high"}, claude.SupportedEfforts)
	assert.Equal(t, "high", claude.DefaultEffort)
	assert.Contains(t, claude.Aliases, "claude")
	assert.Contains(t, claude.Aliases, "claude-sonnet-4-6")
	assert.Equal(t, 200000, claude.MaxContextTokens)

	// 7. Check GPT OSS
	gpt := parsed[6]
	assert.Equal(t, "gpt-oss-120b", gpt.ID)
	assert.Equal(t, []string{"medium"}, gpt.SupportedEfforts)
	assert.Equal(t, "medium", gpt.DefaultEffort)
	assert.Contains(t, gpt.Aliases, "gpt")
	assert.Contains(t, gpt.Aliases, "gpt-oss-120b-medium")
	assert.Equal(t, 128000, gpt.MaxContextTokens)
}
