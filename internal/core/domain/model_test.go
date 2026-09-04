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
		name             string
		rawModel         string
		rawEffort        string
		expectedModel    string
		expectedEffort   string
		expectedIsCustom bool
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

func TestDynamicModelCapabilities_Registry(t *testing.T) {
	mockModels := []domain.ModelCapability{
		{
			ID:               "custom-test-model",
			DisplayName:      "Custom Test Model",
			Aliases:          []string{"custom"},
			SupportedEfforts: []string{"high"},
			DefaultEffort:    "high",
		},
	}

	domain.SetDynamicModelCapabilities(mockModels)
	models := domain.ListAvailableModels()
	assert.GreaterOrEqual(t, len(models), 1)

	cap, _, ok := domain.LookupModelCapability("custom", nil)
	assert.True(t, ok)
	assert.Equal(t, "custom-test-model", cap.ID)

	domain.SetDynamicModelCapabilities(domain.DefaultModelCapabilities)
}

func TestModelCapability_ContextAndThresholds(t *testing.T) {
	// Test Default Capabilities
	capGemini, _, ok := domain.LookupModelCapability("flash", nil)
	assert.True(t, ok)
	assert.Equal(t, 1048576, capGemini.EffectiveMaxContext())
	assert.Equal(t, 65536, capGemini.EffectiveMaxOutput())
	assert.Equal(t, 734003, capGemini.EffectiveCompactThreshold())

	capClaude, _, ok := domain.LookupModelCapability("claude", nil)
	assert.True(t, ok)
	assert.Equal(t, 200000, capClaude.EffectiveMaxContext())
	assert.Equal(t, 8192, capClaude.EffectiveMaxOutput())
	assert.Equal(t, 140000, capClaude.EffectiveCompactThreshold())

	capGPT, _, ok := domain.LookupModelCapability("gpt-oss", nil)
	assert.True(t, ok)
	assert.Equal(t, 128000, capGPT.EffectiveMaxContext())
	assert.Equal(t, 16384, capGPT.EffectiveMaxOutput())
	assert.Equal(t, 89600, capGPT.EffectiveCompactThreshold())

	// Test Fallback for dynamic/custom capability with zero values
	emptyCap := domain.ModelCapability{ID: "custom-claude-variant"}
	assert.Equal(t, 200000, emptyCap.EffectiveMaxContext())
	assert.Equal(t, 8192, emptyCap.EffectiveMaxOutput())
	assert.Equal(t, 140000, emptyCap.EffectiveCompactThreshold())

	genericCap := domain.ModelCapability{ID: "unknown-custom-model"}
	assert.Equal(t, 1048576, genericCap.EffectiveMaxContext())
	assert.Equal(t, 65536, genericCap.EffectiveMaxOutput())
	assert.Equal(t, 734003, genericCap.EffectiveCompactThreshold())
}
