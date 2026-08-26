package domain

import "time"

// TokenUsage records LLM token consumption metrics including prompt caching.
type TokenUsage struct {
	InputTokens     int `json:"input_tokens"`
	OutputTokens    int `json:"output_tokens"`
	ThinkingTokens  int `json:"thinking_tokens"`
	CacheReadTokens int `json:"cache_read_tokens"`
	TotalTokens     int `json:"total_tokens"`
}

// UncachedInputTokens returns the amount of input tokens that were processed fresh (not read from cache).
func (t TokenUsage) UncachedInputTokens() int {
	if t.InputTokens >= t.CacheReadTokens {
		return t.InputTokens - t.CacheReadTokens
	}
	return t.InputTokens
}

// CacheHitRatio returns the percentage of input tokens served from KV-cache (0.0% to 100.0%).
func (t TokenUsage) CacheHitRatio() float64 {
	if t.InputTokens <= 0 {
		return 0.0
	}
	return (float64(t.CacheReadTokens) / float64(t.InputTokens)) * 100.0
}

// EffectiveCostSavingsRatio computes estimated monetary/resource savings ratio based on Gemini caching discount (cache read = 25% cost of standard input).
func (t TokenUsage) EffectiveCostSavingsRatio() float64 {
	if t.InputTokens <= 0 {
		return 0.0
	}
	savedEquivalentTokens := float64(t.CacheReadTokens) * 0.75
	return (savedEquivalentTokens / float64(t.InputTokens)) * 100.0
}

// AuditLog tracks execution history, latency, token metrics, and operational status.
type AuditLog struct {
	ID              int64      `json:"id"`
	SessionKey      string     `json:"session_key"`
	AgentName       string     `json:"agent_name"`
	ProjectName     string     `json:"project_name,omitempty"`
	ConversationID  string     `json:"conversation_id,omitempty"`
	Model           string     `json:"model,omitempty"`
	Effort          string     `json:"effort,omitempty"`
	PromptLength    int        `json:"prompt_length"`
	ResponseLength  int        `json:"response_length"`
	DurationSeconds float64    `json:"duration_seconds"`
	Usage           TokenUsage `json:"usage"`
	Status          string     `json:"status"` // "SUCCESS" | "ERROR"
	ErrorMessage    string     `json:"error_message,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}
