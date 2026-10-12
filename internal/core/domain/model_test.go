package domain_test

import (
	"fmt"
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
			name:             "Canonical Gemini 3.8 Flash with explicit high effort",
			rawModel:         "gemini-3.8-flash",
			rawEffort:        "high",
			expectedModel:    "gemini-3.8-flash",
			expectedEffort:   "high",
			expectedIsCustom: false,
		},
		{
			name:             "Alias 'flash' with empty effort defaults to gemini-3.8-flash high",
			rawModel:         "flash",
			rawEffort:        "",
			expectedModel:    "gemini-3.8-flash",
			expectedEffort:   "high",
			expectedIsCustom: false,
		},
		{
			name:             "Alias '3.8' maps to gemini-3.8-flash",
			rawModel:         "3.8",
			rawEffort:        "low",
			expectedModel:    "gemini-3.8-flash",
			expectedEffort:   "low",
			expectedIsCustom: false,
		},
		{
			name:             "Alias '3.7' maps to gemini-3.7-flash",
			rawModel:         "3.7",
			rawEffort:        "high",
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
			name:             "Edge Case 2: Claude Sonnet 5.5 preserves effort",
			rawModel:         "claude-sonnet-5-5",
			rawEffort:        "high",
			expectedModel:    "claude-sonnet-5-5",
			expectedEffort:   "high",
			expectedIsCustom: false,
		},
		{
			name:             "Edge Case 2b: Alias 'claude' defaults to high effort",
			rawModel:         "claude",
			rawEffort:        "",
			expectedModel:    "claude-sonnet-5-5",
			expectedEffort:   "high",
			expectedIsCustom: false,
		},
		{
			name:             "Edge Case 2b-2: Alias 'sonnet' maps to claude-sonnet-5-5 with requested effort",
			rawModel:         "sonnet",
			rawEffort:        "low",
			expectedModel:    "claude-sonnet-5-5",
			expectedEffort:   "low",
			expectedIsCustom: false,
		},
		{
			name:             "Edge Case 2b-3: Alias 'claude-sonnet' maps to claude-sonnet-5-5",
			rawModel:         "claude-sonnet",
			rawEffort:        "medium",
			expectedModel:    "claude-sonnet-5-5",
			expectedEffort:   "medium",
			expectedIsCustom: false,
		},
		{
			name:             "Edge Case 2b-4: Legacy alias 'claude-sonnet-4-6' maps to claude-sonnet-5-5",
			rawModel:         "claude-sonnet-4-6",
			rawEffort:        "high",
			expectedModel:    "claude-sonnet-5-5",
			expectedEffort:   "high",
			expectedIsCustom: false,
		},
		{
			name:             "Edge Case 2c: claude-opus-5-5 preserves effort",
			rawModel:         "claude-opus-5-5",
			rawEffort:        "high",
			expectedModel:    "claude-opus-5-5",
			expectedEffort:   "high",
			expectedIsCustom: false,
		},
		{
			name:             "Edge Case 2c-2: Alias 'opus' maps to claude-opus-5-5",
			rawModel:         "opus",
			rawEffort:        "high",
			expectedModel:    "claude-opus-5-5",
			expectedEffort:   "high",
			expectedIsCustom: false,
		},
		{
			name:             "Edge Case 2c-3: Alias 'claude-opus' maps to claude-opus-5-5",
			rawModel:         "claude-opus",
			rawEffort:        "low",
			expectedModel:    "claude-opus-5-5",
			expectedEffort:   "low",
			expectedIsCustom: false,
		},
		{
			name:             "Edge Case 2c-4: Legacy alias 'claude-opus-4-6-thinking' maps to claude-opus-5-5",
			rawModel:         "claude-opus-4-6-thinking",
			rawEffort:        "medium",
			expectedModel:    "claude-opus-5-5",
			expectedEffort:   "medium",
			expectedIsCustom: false,
		},
		{
			name:             "Edge Case 2d: gpt-oss-120b preserves medium effort",
			rawModel:         "gpt-oss-120b",
			rawEffort:        "medium",
			expectedModel:    "gpt-oss-120b",
			expectedEffort:   "medium",
			expectedIsCustom: false,
		},
		{
			name:             "Edge Case 2d-2: gpt-oss-120b clamps unsupported 'high' to 'medium'",
			rawModel:         "gpt-oss-120b",
			rawEffort:        "high",
			expectedModel:    "gpt-oss-120b",
			expectedEffort:   "medium",
			expectedIsCustom: false,
		},
		{
			name:             "Edge Case 2d-3: gpt-oss-120b empty effort defaults to 'medium'",
			rawModel:         "gpt-oss-120b",
			rawEffort:        "",
			expectedModel:    "gpt-oss-120b",
			expectedEffort:   "medium",
			expectedIsCustom: false,
		},
		{
			name:             "Edge Case 2e: gemini-3.6-flash with explicit effort",
			rawModel:         "gemini-3.6-flash",
			rawEffort:        "low",
			expectedModel:    "gemini-3.6-flash",
			expectedEffort:   "low",
			expectedIsCustom: false,
		},
		{
			name:             "Edge Case 2e-2: Legacy alias 'gemini-3.5-flash' maps to gemini-3.6-flash",
			rawModel:         "gemini-3.5-flash",
			rawEffort:        "high",
			expectedModel:    "gemini-3.6-flash",
			expectedEffort:   "high",
			expectedIsCustom: false,
		},
		{
			name:             "Edge Case 2e-3: Legacy alias '3.5-flash' maps to gemini-3.6-flash",
			rawModel:         "3.5-flash",
			rawEffort:        "medium",
			expectedModel:    "gemini-3.6-flash",
			expectedEffort:   "medium",
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

	t.Run("Rule 1: Model with NO effort support strips effort", func(t *testing.T) {
		domain.SetDynamicModelCapabilities([]domain.ModelCapability{
			{
				ID:               "fixed-no-effort",
				Aliases:          []string{"fixed"},
				SupportedEfforts: []string{},
			},
		})
		defer domain.SetDynamicModelCapabilities(domain.DefaultModelCapabilities)

		model, effort, isCustom := domain.NormalizeModelAndEffort("fixed", "high", nil)
		assert.Equal(t, "fixed-no-effort", model)
		assert.Equal(t, "", effort, "effort must be stripped when SupportedEfforts is empty")
		assert.False(t, isCustom)
	})
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
	assert.Equal(t, 2097152, capGemini.EffectiveMaxContext())
	assert.Equal(t, 65536, capGemini.EffectiveMaxOutput())
	assert.Equal(t, 1887436, capGemini.EffectiveCompactThreshold())

	capClaude, _, ok := domain.LookupModelCapability("claude", nil)
	assert.True(t, ok)
	assert.Equal(t, 200000, capClaude.EffectiveMaxContext())
	assert.Equal(t, 8192, capClaude.EffectiveMaxOutput())
	assert.Equal(t, 180000, capClaude.EffectiveCompactThreshold())

	capGPT, _, ok := domain.LookupModelCapability("gpt-oss", nil)
	assert.True(t, ok)
	assert.Equal(t, 128000, capGPT.EffectiveMaxContext())
	assert.Equal(t, 16384, capGPT.EffectiveMaxOutput())
	assert.Equal(t, 115200, capGPT.EffectiveCompactThreshold())

	// Test Fallback for dynamic/custom capability with zero values
	emptyCap := domain.ModelCapability{ID: "custom-claude-variant"}
	assert.Equal(t, 200000, emptyCap.EffectiveMaxContext())
	assert.Equal(t, 8192, emptyCap.EffectiveMaxOutput())
	assert.Equal(t, 180000, emptyCap.EffectiveCompactThreshold())

	genericCap := domain.ModelCapability{ID: "unknown-custom-model"}
	assert.Equal(t, 2097152, genericCap.EffectiveMaxContext())
	assert.Equal(t, 65536, genericCap.EffectiveMaxOutput())
	assert.Equal(t, 1887436, genericCap.EffectiveCompactThreshold())
}

func TestIsEffortError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		result   *domain.ExecutionResult
		expected bool
	}{
		{
			name:     "AGY error: --effort is not supported for model flash",
			err:      fmt.Errorf("exit status 1: invalid model selection (--model \"flash\" --effort \"high\"): --effort is not supported for model \"flash\""),
			result:   nil,
			expected: true,
		},
		{
			name:     "Result error: effort not supported",
			err:      nil,
			result:   &domain.ExecutionResult{Success: false, Error: "effort not supported for this model"},
			expected: true,
		},
		{
			name:     "Flag not defined: -effort",
			err:      fmt.Errorf("flag provided but not defined: -effort"),
			result:   nil,
			expected: true,
		},
		{
			name:     "Unknown flag: --effort",
			err:      fmt.Errorf("unknown flag: --effort"),
			result:   nil,
			expected: true,
		},
		{
			name:     "Unrelated network error",
			err:      fmt.Errorf("connection refused: dial tcp 127.0.0.1:80"),
			result:   nil,
			expected: false,
		},
		{
			name:     "Successful execution",
			err:      nil,
			result:   &domain.ExecutionResult{Success: true},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := domain.IsEffortError(tt.err, tt.result)
			assert.Equal(t, tt.expected, actual)
		})
	}
}
