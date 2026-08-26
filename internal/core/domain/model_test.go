package domain_test

import (
	"testing"

	"agyent/internal/core/domain"

	"github.com/stretchr/testify/assert"
)

func TestModelCapability_LookupAndNormalize(t *testing.T) {
	customAliases := map[string]string{
		"my-pro": "gemini-3.1-pro",
		"fast":   "gemini-3.7-flash",
	}

	tests := []struct {
		name              string
		rawModel          string
		rawEffort         string
		expectedModel     string
		expectedEffort    string
		expectedIsCustom  bool
	}{
		{
			name:             "Canonical Gemini 3.7 Flash with explicit high effort",
			rawModel:         "gemini-3.7-flash",
			rawEffort:        "high",
			expectedModel:    "gemini-3.7-flash",
			expectedEffort:   "high",
			expectedIsCustom: false,
		},
		{
			name:             "Alias 'flash' with empty effort defaults to high",
			rawModel:         "flash",
			rawEffort:        "",
			expectedModel:    "gemini-3.7-flash",
			expectedEffort:   "high",
			expectedIsCustom: false,
		},
		{
			name:             "Full model name 'gemini-3.7-flash-medium' extracts effort 'medium'",
			rawModel:         "gemini-3.7-flash-medium",
			rawEffort:        "",
			expectedModel:    "gemini-3.7-flash",
			expectedEffort:   "medium",
			expectedIsCustom: false,
		},
		{
			name:             "Alias 'pro' with empty effort defaults to high",
			rawModel:         "pro",
			rawEffort:        "",
			expectedModel:    "gemini-3.1-pro",
			expectedEffort:   "high",
			expectedIsCustom: false,
		},
		{
			name:             "Edge Case 1: Model with subset effort (gemini-3.1-pro) requested with unsupported 'medium' clamps to 'high'",
			rawModel:         "gemini-3.1-pro",
			rawEffort:        "medium",
			expectedModel:    "gemini-3.1-pro",
			expectedEffort:   "high",
			expectedIsCustom: false,
		},
		{
			name:             "Edge Case 1b: gemini-3.1-pro with valid 'low' effort is preserved",
			rawModel:         "gemini-3.1-pro",
			rawEffort:        "low",
			expectedModel:    "gemini-3.1-pro",
			expectedEffort:   "low",
			expectedIsCustom: false,
		},
		{
			name:             "Edge Case 2: Model with NO effort support (claude-sonnet-4-6) strips effort",
			rawModel:         "claude-sonnet-4-6",
			rawEffort:        "high",
			expectedModel:    "claude-sonnet-4-6",
			expectedEffort:   "",
			expectedIsCustom: false,
		},
		{
			name:             "Edge Case 2b: Alias 'claude' strips effort",
			rawModel:         "claude",
			rawEffort:        "low",
			expectedModel:    "claude-sonnet-4-6",
			expectedEffort:   "",
			expectedIsCustom: false,
		},
		{
			name:             "Edge Case 2c: claude-opus-4-6-thinking strips effort",
			rawModel:         "claude-opus-4-6-thinking",
			rawEffort:        "high",
			expectedModel:    "claude-opus-4-6-thinking",
			expectedEffort:   "",
			expectedIsCustom: false,
		},
		{
			name:             "Edge Case 2d: gpt-oss-120b-medium strips effort",
			rawModel:         "gpt-oss-120b-medium",
			rawEffort:        "medium",
			expectedModel:    "gpt-oss-120b-medium",
			expectedEffort:   "",
			expectedIsCustom: false,
		},
		{
			name:             "Custom Config Alias: 'my-pro' maps to gemini-3.1-pro",
			rawModel:         "my-pro",
			rawEffort:        "low",
			expectedModel:    "gemini-3.1-pro",
			expectedEffort:   "low",
			expectedIsCustom: false,
		},
		{
			name:             "Edge Case 3: Unknown / Custom BYOK model passes through verbatim",
			rawModel:         "vertex-custom-fine-tuned",
			rawEffort:        "high",
			expectedModel:    "vertex-custom-fine-tuned",
			expectedEffort:   "high",
			expectedIsCustom: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model, effort, isCustom := domain.NormalizeModelAndEffort(tt.rawModel, tt.rawEffort, customAliases)
			assert.Equal(t, tt.expectedModel, model)
			assert.Equal(t, tt.expectedEffort, effort)
			assert.Equal(t, tt.expectedIsCustom, isCustom)
		})
	}
}

func TestParseModelsOutput(t *testing.T) {
	rawCLIOutput := `Fetching available models...
gemini-3.7-flash-high	Gemini 3.7 Flash (High)
gemini-3.7-flash-medium	Gemini 3.7 Flash (Medium)
gemini-3.7-flash-low	Gemini 3.7 Flash (Low)
gemini-3.1-pro-high	Gemini 3.1 Pro (High)
gemini-3.1-pro-low	Gemini 3.1 Pro (Low)
claude-sonnet-4-6	Claude Sonnet 4.6 (Thinking)
claude-opus-4-6-thinking	Claude Opus 4.6 (Thinking)
gpt-oss-120b-medium	GPT-OSS 120B (Medium)`

	parsed := domain.ParseModelsOutput(rawCLIOutput)
	assert.Len(t, parsed, 5)

	// 1. Check Gemini 3.7 Flash
	assert.Equal(t, "gemini-3.7-flash", parsed[0].ID)
	assert.Equal(t, "Gemini 3.7 Flash", parsed[0].DisplayName)
	assert.Equal(t, []string{"high", "medium", "low"}, parsed[0].SupportedEfforts)
	assert.Equal(t, "high", parsed[0].DefaultEffort)
	assert.Contains(t, parsed[0].Aliases, "flash")

	// 2. Check Gemini 3.1 Pro (only high and low)
	assert.Equal(t, "gemini-3.1-pro", parsed[1].ID)
	assert.Equal(t, "Gemini 3.1 Pro", parsed[1].DisplayName)
	assert.Equal(t, []string{"high", "low"}, parsed[1].SupportedEfforts)
	assert.Equal(t, "high", parsed[1].DefaultEffort)
	assert.Contains(t, parsed[1].Aliases, "pro")

	// 3. Check Claude Sonnet 4.6 (no effort)
	assert.Equal(t, "claude-sonnet-4-6", parsed[2].ID)
	assert.Empty(t, parsed[2].SupportedEfforts)
	assert.Equal(t, "", parsed[2].DefaultEffort)
	assert.Contains(t, parsed[2].Aliases, "claude")

	// 4. Test Dynamic Registry injection
	domain.SetDynamicModelCapabilities(parsed)
	models := domain.ListAvailableModels()
	assert.GreaterOrEqual(t, len(models), 5)
}

