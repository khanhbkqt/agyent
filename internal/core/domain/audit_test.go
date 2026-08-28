package domain_test

import (
	"testing"

	"agyent/internal/core/domain"
	"github.com/stretchr/testify/assert"
)

func TestTokenUsage_SingleStepAndMultiStepCalculations(t *testing.T) {
	// 1. Single-step standard turn
	singleStep := domain.TokenUsage{
		InputTokens:     10000,
		OutputTokens:    500,
		ThinkingTokens:  100,
		CacheReadTokens: 8000,
		TotalTokens:     10500,
	}

	assert.Equal(t, 10000, singleStep.GrossInputTokens())
	assert.Equal(t, 2000, singleStep.UncachedInputTokens())
	assert.Equal(t, 80.0, singleStep.CacheHitRatio())
	assert.Equal(t, 60.0, singleStep.EffectiveCostSavingsRatio())
	assert.Equal(t, 10500, singleStep.EffectiveTotalTokens())

	// 2. Multi-step tool calls turn (where CacheReadTokens accumulated across tool-call loop)
	multiStep := domain.TokenUsage{
		InputTokens:     56980401,
		OutputTokens:    2161875,
		ThinkingTokens:  1029130,
		CacheReadTokens: 274198366,
		TotalTokens:     59142276,
	}

	assert.Equal(t, 331178767, multiStep.GrossInputTokens())
	assert.Equal(t, 56980401, multiStep.UncachedInputTokens())
	assert.InDelta(t, 82.80, multiStep.CacheHitRatio(), 0.05)
	assert.InDelta(t, 62.10, multiStep.EffectiveCostSavingsRatio(), 0.05)
	assert.Equal(t, 333340642, multiStep.EffectiveTotalTokens())

	// 3. Zero / empty case
	emptyUsage := domain.TokenUsage{}
	assert.Equal(t, 0, emptyUsage.GrossInputTokens())
	assert.Equal(t, 0, emptyUsage.UncachedInputTokens())
	assert.Equal(t, 0.0, emptyUsage.CacheHitRatio())
	assert.Equal(t, 0.0, emptyUsage.EffectiveCostSavingsRatio())
	assert.Equal(t, 0, emptyUsage.EffectiveTotalTokens())
}
