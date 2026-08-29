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

// GrossInputTokens returns the total cumulative input tokens evaluated by the model across all tool-call iterations in the turn(s).
func (t TokenUsage) GrossInputTokens() int {
	if t.CacheReadTokens > t.InputTokens {
		// Multi-step tool calls accumulated cache reads across iterations while InputTokens is snapshot context
		return t.CacheReadTokens + t.InputTokens
	}
	return t.InputTokens
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
	grossInput := t.GrossInputTokens()
	if grossInput <= 0 {
		return 0.0
	}
	ratio := (float64(t.CacheReadTokens) / float64(grossInput)) * 100.0
	if ratio > 100.0 {
		return 100.0
	}
	return ratio
}

// EffectiveCostSavingsRatio computes estimated monetary/resource savings ratio based on Gemini caching discount (cache read = 25% cost of standard input).
func (t TokenUsage) EffectiveCostSavingsRatio() float64 {
	grossInput := t.GrossInputTokens()
	if grossInput <= 0 {
		return 0.0
	}
	savedEquivalentTokens := float64(t.CacheReadTokens) * 0.75
	ratio := (savedEquivalentTokens / float64(grossInput)) * 100.0
	if ratio > 75.0 {
		return 75.0 // Maximum possible discount on 100% cache hit with 0.25x price is 75%
	}
	return ratio
}

// EffectiveTotalTokens returns total gross billed tokens taking into account gross evaluated input.
func (t TokenUsage) EffectiveTotalTokens() int {
	return t.GrossInputTokens() + t.OutputTokens
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

// ModelTokenBreakdown tracks aggregated token usage for a specific model.
type ModelTokenBreakdown struct {
	Model       string     `json:"model"`
	DisplayName string     `json:"display_name,omitempty"`
	TurnCount   int        `json:"turn_count"`
	Usage       TokenUsage `json:"usage"`
}

// AgentTokenBreakdown tracks aggregated token usage for a specific agent persona.
type AgentTokenBreakdown struct {
	AgentName string     `json:"agent_name"`
	TurnCount int        `json:"turn_count"`
	Usage     TokenUsage `json:"usage"`
}

// TokenEfficiencyReport aggregates session and global analytics over temporal windows and compaction metrics.
type TokenEfficiencyReport struct {
	SessionKey        string                `json:"session_key,omitempty"`
	AgentFilter       string                `json:"agent_filter,omitempty"`
	GeneratedAt       time.Time             `json:"generated_at"`
	TodayUsage        TokenUsage            `json:"today_usage"`
	TodayTurns        int                   `json:"today_turns"`
	Past7DaysUsage    TokenUsage            `json:"past_7_days_usage"`
	Past7DaysTurns    int                   `json:"past_7_days_turns"`
	AllTimeUsage      TokenUsage            `json:"all_time_usage"`
	AllTimeTurns      int                   `json:"all_time_turns"`
	ModelBreakdown    []ModelTokenBreakdown `json:"model_breakdown"`
	AgentBreakdown    []AgentTokenBreakdown `json:"agent_breakdown"`
	TotalCompactions  int                   `json:"total_compactions"`
	EstTokensSaved    int64                 `json:"est_tokens_saved"`
	AvgCacheHitRatio  float64               `json:"avg_cache_hit_ratio"`
	TotalCostSavedPct float64               `json:"total_cost_saved_pct"`
}
