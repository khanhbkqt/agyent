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
claude-opus-5-5-low	Claude Opus 5.5 (Low)
claude-opus-5-5-medium	Claude Opus 5.5 (Medium)
claude-opus-5-5-high	Claude Opus 5.5 (High)
claude-sonnet-5-5-low	Claude Sonnet 5.5 (Low)
claude-sonnet-5-5-medium	Claude Sonnet 5.5 (Medium)
claude-sonnet-5-5-high	Claude Sonnet 5.5 (High)
gpt-oss-120b-medium	GPT-OSS 120B (Medium)
`

	parsed := agy.ParseModelsOutput(rawCLIOutput)
	require.Len(t, parsed, 4, "Should group suffixed models into 4 distinct base models")

	// 1. Check Gemini 3.8 Flash
	flash38 := parsed[0]
	assert.Equal(t, "gemini-3.8-flash", flash38.ID)
	assert.Equal(t, "Gemini 3.8 Flash", flash38.DisplayName)
	assert.Equal(t, []string{"high", "medium", "low"}, flash38.SupportedEfforts)
	assert.Equal(t, "high", flash38.DefaultEffort)
	assert.Contains(t, flash38.Aliases, "flash")
	assert.Contains(t, flash38.Aliases, "3.8")
	assert.Contains(t, flash38.Aliases, "3.8-flash")
	assert.Contains(t, flash38.Aliases, "3.7")
	assert.Contains(t, flash38.Aliases, "3.7-flash")
	assert.Contains(t, flash38.Aliases, "3.6-flash")
	assert.Contains(t, flash38.Aliases, "3.5-flash")
	assert.Contains(t, flash38.Aliases, "gemini-pro")
	assert.Contains(t, flash38.Aliases, "gemini-3.1-pro")
	assert.Equal(t, 2097152, flash38.MaxContextTokens)

	// 2. Check Claude Opus 5.5
	opus := parsed[1]
	assert.Equal(t, "claude-opus-5-5", opus.ID)
	assert.Equal(t, "Claude Opus 5.5", opus.DisplayName)
	assert.Equal(t, []string{"low", "medium", "high"}, opus.SupportedEfforts)
	assert.Equal(t, "high", opus.DefaultEffort)
	assert.Contains(t, opus.Aliases, "opus")
	assert.Contains(t, opus.Aliases, "claude-opus-4-6-thinking")
	assert.Contains(t, opus.Aliases, "pro")
	assert.Contains(t, opus.Aliases, "smart")
	assert.Equal(t, 200000, opus.MaxContextTokens)

	// 3. Check Claude Sonnet 5.5
	claude := parsed[2]
	assert.Equal(t, "claude-sonnet-5-5", claude.ID)
	assert.Equal(t, "Claude Sonnet 5.5", claude.DisplayName)
	assert.Equal(t, []string{"low", "medium", "high"}, claude.SupportedEfforts)
	assert.Equal(t, "high", claude.DefaultEffort)
	assert.Contains(t, claude.Aliases, "claude")
	assert.Contains(t, claude.Aliases, "claude-sonnet-4-6")
	assert.Equal(t, 200000, claude.MaxContextTokens)

	// 4. Check GPT OSS
	gpt := parsed[3]
	assert.Equal(t, "gpt-oss-120b", gpt.ID)
	assert.Equal(t, []string{"medium"}, gpt.SupportedEfforts)
	assert.Equal(t, "medium", gpt.DefaultEffort)
	assert.Contains(t, gpt.Aliases, "gpt")
	assert.Contains(t, gpt.Aliases, "gpt-oss-120b-medium")
	assert.Equal(t, 128000, gpt.MaxContextTokens)
}

func TestParseModelsOutput_WithArgon(t *testing.T) {
	rawCLIOutput := `Fetching available models...
gemini-4-argon-high	Gemini 4 Argon (High)
gemini-4-argon-medium	Gemini 4 Argon (Medium)
gemini-4-argon-low	Gemini 4 Argon (Low)
gemini-3.8-flash-high	Gemini 3.8 Flash (High)
`

	parsed := agy.ParseModelsOutput(rawCLIOutput)
	require.Len(t, parsed, 2)

	argon := parsed[0]
	assert.Equal(t, "gemini-4-argon", argon.ID)
	assert.Equal(t, "Gemini 4 Argon", argon.DisplayName)
	assert.Equal(t, []string{"high", "medium", "low"}, argon.SupportedEfforts)
	assert.Equal(t, "high", argon.DefaultEffort)
	assert.Contains(t, argon.Aliases, "argon")
	assert.Contains(t, argon.Aliases, "gemini-4")
	assert.Contains(t, argon.Aliases, "4")
	assert.Contains(t, argon.Aliases, "4-argon")
	assert.Equal(t, 1048576, argon.MaxContextTokens)
}

func TestParseModelsOutput_FullFrontierSet(t *testing.T) {
	rawCLIOutput := `Fetching available models...
gemini-4-carbon-medium	Gemini 4 Carbon (Medium)
gemini-4-carbon-high	Gemini 4 Carbon (High)
gemini-4-carbon-low	Gemini 4 Carbon (Low)
gemini-4-argon-medium	Gemini 4 Argon (Medium)
gemini-4-argon-high	Gemini 4 Argon (High)
gemini-4-argon-low	Gemini 4 Argon (Low)
gemini-4-hydrogen-high	Gemini 4 Hydrogen (High)
gemini-4-hydrogen-xhigh	Gemini 4 Hydrogen (XHigh)
gemini-4-hydrogen-medium	Gemini 4 Hydrogen (Medium)
gemini-4-hydrogen-low	Gemini 4 Hydrogen (Low)
gemini-pro-orchestrator	Orchestrator (Carbon)
gemini-pro-orchestrator-argon	Orchestrator (Argon)
gemini-3.8-flash-high	Gemini 3.8 Flash (High)
gemini-3.8-flash-medium	Gemini 3.8 Flash (Medium)
gemini-3.8-flash-low	Gemini 3.8 Flash (Low)
claude-opus-5-5-low	Claude Opus 5.5 (Low)
claude-opus-5-5-medium	Claude Opus 5.5 (Medium)
claude-opus-5-5-high	Claude Opus 5.5 (High)
claude-sonnet-5-5-low	Claude Sonnet 5.5 (Low)
claude-sonnet-5-5-medium	Claude Sonnet 5.5 (Medium)
claude-sonnet-5-5-high	Claude Sonnet 5.5 (High)
gpt-oss-120b-medium	GPT-OSS 120B (Medium)
`

	parsed := agy.ParseModelsOutput(rawCLIOutput)
	require.Len(t, parsed, 9, "Should group into 9 distinct base models")

	// 1. Carbon
	carbon := parsed[0]
	assert.Equal(t, "gemini-4-carbon", carbon.ID)
	assert.Equal(t, "Gemini 4 Carbon", carbon.DisplayName)
	assert.Equal(t, []string{"medium", "high", "low"}, carbon.SupportedEfforts)
	assert.Equal(t, "high", carbon.DefaultEffort)
	assert.Contains(t, carbon.Aliases, "carbon")
	assert.Contains(t, carbon.Aliases, "gemini-4-carbon")
	assert.Equal(t, 1048576, carbon.MaxContextTokens)

	// 2. Argon
	argon := parsed[1]
	assert.Equal(t, "gemini-4-argon", argon.ID)
	assert.Equal(t, "Gemini 4 Argon", argon.DisplayName)
	assert.Contains(t, argon.Aliases, "argon")
	assert.Contains(t, argon.Aliases, "gemini-4")
	assert.Contains(t, argon.Aliases, "4")
	assert.Equal(t, 1048576, argon.MaxContextTokens)

	// 3. Hydrogen (with XHigh)
	hydrogen := parsed[2]
	assert.Equal(t, "gemini-4-hydrogen", hydrogen.ID)
	assert.Equal(t, "Gemini 4 Hydrogen", hydrogen.DisplayName)
	assert.Equal(t, []string{"high", "xhigh", "medium", "low"}, hydrogen.SupportedEfforts)
	assert.Equal(t, "high", hydrogen.DefaultEffort)
	assert.Contains(t, hydrogen.Aliases, "hydrogen")
	assert.Contains(t, hydrogen.Aliases, "gemini-4-hydrogen")
	assert.Equal(t, 1048576, hydrogen.MaxContextTokens)

	// 4. Orchestrator (Carbon)
	orchCarbon := parsed[3]
	assert.Equal(t, "gemini-pro-orchestrator", orchCarbon.ID)
	assert.Equal(t, "Orchestrator (Carbon)", orchCarbon.DisplayName)
	assert.Empty(t, orchCarbon.SupportedEfforts, "Orchestrator must have no effort options")
	assert.Equal(t, "", orchCarbon.DefaultEffort)
	assert.Contains(t, orchCarbon.Aliases, "orchestrator")
	assert.Contains(t, orchCarbon.Aliases, "orchestrator-carbon")
	assert.NotContains(t, orchCarbon.Aliases, "pro", "Orchestrator should not steal 'pro' alias")
	assert.Equal(t, 1048576, orchCarbon.MaxContextTokens)

	// 5. Orchestrator (Argon)
	orchArgon := parsed[4]
	assert.Equal(t, "gemini-pro-orchestrator-argon", orchArgon.ID)
	assert.Equal(t, "Orchestrator (Argon)", orchArgon.DisplayName)
	assert.Empty(t, orchArgon.SupportedEfforts, "Orchestrator Argon must have no effort options")
	assert.Equal(t, "", orchArgon.DefaultEffort)
	assert.Contains(t, orchArgon.Aliases, "orchestrator-argon")
	assert.Equal(t, 1048576, orchArgon.MaxContextTokens)

	// 6. Flash 3.8
	flash := parsed[5]
	assert.Equal(t, "gemini-3.8-flash", flash.ID)
	assert.Equal(t, 2097152, flash.MaxContextTokens)

	// 7. Claude Opus
	opus := parsed[6]
	assert.Equal(t, "claude-opus-5-5", opus.ID)
	assert.Contains(t, opus.Aliases, "pro")
	assert.Contains(t, opus.Aliases, "smart")
	assert.Equal(t, 200000, opus.MaxContextTokens)
}
