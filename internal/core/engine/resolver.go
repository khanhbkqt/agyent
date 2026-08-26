package engine

import (
	"agyent/internal/core/domain"
)

// ResolveExecutionParams resolves the final model identifier and reasoning effort level
// following the 5-Tier Precedence Hierarchy and enforcing model capability constraints.
//
// Precedence:
//  1. Tier 1: Per-Turn Explicit Override (e.g. /ask flags or inline directives)
//  2. Tier 2: Session Active Override (session.ActiveModel, session.ActiveEffort)
//  3. Tier 3: Agent Persona Default (agent.DefaultModel, agent.DefaultEffort)
//  4. Tier 4: Global Gateway Config (cfg.AGY.DefaultModel, cfg.AGY.DefaultEffort)
//  5. Tier 5: Runtime Defaults & Normalization (Normalizes aliases & clamps effort based on model capabilities)
func (e *Engine) ResolveExecutionParams(
	explicitModel string,
	explicitEffort string,
	session *domain.Session,
	agent *domain.Agent,
) (resolvedModel string, resolvedEffort string, source string) {
	rawModel := explicitModel
	rawEffort := explicitEffort
	source = "Per-Turn Override"

	if rawModel == "" && session != nil && session.ActiveModel != "" {
		rawModel = session.ActiveModel
		source = "Session Override"
	}
	if rawEffort == "" && session != nil && session.ActiveEffort != "" {
		rawEffort = session.ActiveEffort
	}

	if rawModel == "" && agent != nil && agent.DefaultModel != "" {
		rawModel = agent.DefaultModel
		source = "Agent Default"
	}
	if rawEffort == "" && agent != nil && agent.DefaultEffort != "" {
		rawEffort = agent.DefaultEffort
	}

	if rawModel == "" && e.cfg != nil && e.cfg.AGY.DefaultModel != "" {
		rawModel = e.cfg.AGY.DefaultModel
		source = "Global Config"
	}
	if rawEffort == "" && e.cfg != nil && e.cfg.AGY.DefaultEffort != "" {
		rawEffort = e.cfg.AGY.DefaultEffort
	}

	if rawEffort == "" {
		rawEffort = "high"
	}
	if source == "" || (rawModel == "" && rawEffort == "high") {
		source = "System Default"
	}

	var customAliases map[string]string
	if e.cfg != nil {
		customAliases = e.cfg.AGY.ModelAliases
	}

	// Canonicalize alias & clamp effort to supported capability bounds
	canonicalModel, normalizedEffort, _ := domain.NormalizeModelAndEffort(rawModel, rawEffort, customAliases)

	return canonicalModel, normalizedEffort, source
}
