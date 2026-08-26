package subagents

import (
	"fmt"
	"strings"

	"agyent/internal/config"
	"agyent/internal/core/domain"
)

// Evaluator manages subagent execution quotas, nesting depth, and role tool matrices.
type Evaluator struct {
	maxWorkers      int
	maxCascadeDepth int
	roles           map[string]config.RolePolicyConfig
}

// NewEvaluator constructs a subagent security evaluator.
func NewEvaluator(cfg config.SubagentGuardrailConfig) *Evaluator {
	maxWorkers := cfg.MaxConcurrentWorkers
	if maxWorkers <= 0 {
		maxWorkers = 3
	}
	maxDepth := cfg.MaxCascadeDepth
	if maxDepth <= 0 {
		maxDepth = 1
	}

	return &Evaluator{
		maxWorkers:      maxWorkers,
		maxCascadeDepth: maxDepth,
		roles:           cfg.Roles,
	}
}

// EvaluateSubagent verifies permission when invoking subagents or executing tools within a subagent context.
func (e *Evaluator) EvaluateSubagent(isSubagent bool, cascadeDepth int, toolName string, role string, activeWorkerCount int) (domain.SecurityDecision, error) {
	// 1. Anti-Fork Bomb & Cascade Depth Limit
	if (toolName == "invoke_subagent" || toolName == "define_subagent") && isSubagent {
		if cascadeDepth >= e.maxCascadeDepth {
			return domain.SecurityDecision{
				Decision: domain.DecisionDeny,
				Reason:   fmt.Sprintf("🛡️ [Subagent Guardrail]: Subagent cannot invoke or define further subagents (max cascade depth = %d reached)", e.maxCascadeDepth),
			}, nil
		}
	}

	// 2. Concurrency Quota Check
	if toolName == "invoke_subagent" && activeWorkerCount >= e.maxWorkers {
		return domain.SecurityDecision{
			Decision: domain.DecisionDeny,
			Reason:   fmt.Sprintf("🛡️ [Subagent Guardrail]: Concurrency limit exceeded (%d/%d active subagent workers)", activeWorkerCount, e.maxWorkers),
		}, nil
	}

	// 3. Subagent Role Tool Capability Matrix
	if isSubagent && role != "" && len(e.roles) > 0 {
		if roleConfig, ok := e.roles[strings.ToLower(role)]; ok {
			// Check Disallowed Tools
			for _, disallowed := range roleConfig.DisallowedTools {
				if disallowed == "*" || disallowed == toolName {
					return domain.SecurityDecision{
						Decision: domain.DecisionDeny,
						Reason:   fmt.Sprintf("🛡️ [Role Policy]: Tool '%s' is explicitly disallowed for role '%s'", toolName, role),
					}, nil
				}
			}

			// Check Allowed Tools
			hasWildcard := false
			isAllowed := false
			for _, allowed := range roleConfig.AllowedTools {
				if allowed == "*" {
					hasWildcard = true
					break
				}
				if allowed == toolName {
					isAllowed = true
					break
				}
			}

			if !hasWildcard && !isAllowed && len(roleConfig.AllowedTools) > 0 {
				return domain.SecurityDecision{
					Decision: domain.DecisionDeny,
					Reason:   fmt.Sprintf("🛡️ [Role Policy]: Tool '%s' is not permitted in the capability matrix for role '%s'", toolName, role),
				}, nil
			}
		}
	}

	return domain.SecurityDecision{
		Decision: domain.DecisionAllow,
		Reason:   "Subagent execution within capability matrix",
	}, nil
}
