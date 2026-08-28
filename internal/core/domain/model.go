package domain

import (
	"strings"
	"sync"
)

// ModelCapability defines the capability profile, token constraints, and effort parameters for an AGY model.
type ModelCapability struct {
	ID                    string   `json:"id"`                      // Canonical base model ID passed to agy (e.g. "gemini-3.7-flash")
	DisplayName           string   `json:"display_name"`            // Human-readable title (e.g. "Gemini 3.7 Flash")
	Aliases               []string `json:"aliases"`                 // User-friendly shorthand aliases (e.g. ["flash", "fast", "3.7-flash"])
	SupportedEfforts      []string `json:"supported_efforts"`       // Supported effort levels (e.g. ["low", "medium", "high"], or empty if --effort is rejected)
	DefaultEffort         string   `json:"default_effort"`          // Default effort level when none is specified
	MaxContextTokens      int      `json:"max_context_tokens"`      // Maximum context window size (e.g. 1048576, 200000, 128000)
	MaxOutputTokens       int      `json:"max_output_tokens"`       // Maximum output generation limit (e.g. 65536, 8192, 16384)
	CompactThresholdRatio float64  `json:"compact_threshold_ratio"` // Ratio of MaxContextTokens triggering auto-compaction (default: 0.70)
}

// EffectiveMaxContext returns MaxContextTokens or fallback default 1,048,576.
func (m ModelCapability) EffectiveMaxContext() int {
	if m.MaxContextTokens > 0 {
		return m.MaxContextTokens
	}
	idLower := strings.ToLower(m.ID)
	if strings.Contains(idLower, "claude") || strings.Contains(idLower, "sonnet") || strings.Contains(idLower, "opus") {
		return 200000
	}
	if strings.Contains(idLower, "gpt") || strings.Contains(idLower, "oss") {
		return 128000
	}
	return 1048576
}

// EffectiveMaxOutput returns MaxOutputTokens or fallback default 65,536 (or 8192 for Claude, 16384 for GPT).
func (m ModelCapability) EffectiveMaxOutput() int {
	if m.MaxOutputTokens > 0 {
		return m.MaxOutputTokens
	}
	idLower := strings.ToLower(m.ID)
	if strings.Contains(idLower, "claude") || strings.Contains(idLower, "sonnet") || strings.Contains(idLower, "opus") {
		return 8192
	}
	if strings.Contains(idLower, "gpt") || strings.Contains(idLower, "oss") {
		return 16384
	}
	return 65536
}

// EffectiveCompactThreshold returns token count threshold that triggers auto-compaction.
func (m ModelCapability) EffectiveCompactThreshold() int {
	ratio := m.CompactThresholdRatio
	if ratio <= 0 || ratio > 1.0 {
		ratio = 0.70
	}
	return int(float64(m.EffectiveMaxContext()) * ratio)
}

// DefaultModelCapabilities contains the fallback catalog of models supported by Antigravity CLI (agy).
var DefaultModelCapabilities = []ModelCapability{
	{
		ID:                    "gemini-3.7-flash",
		DisplayName:           "Gemini 3.7 Flash",
		Aliases:               []string{"flash", "fast", "3.7-flash", "gemini-flash"},
		SupportedEfforts:      []string{"low", "medium", "high"},
		DefaultEffort:         "high",
		MaxContextTokens:      1048576,
		MaxOutputTokens:       65536,
		CompactThresholdRatio: 0.70,
	},
	{
		ID:                    "gemini-3.1-pro",
		DisplayName:           "Gemini 3.1 Pro",
		Aliases:               []string{"pro", "smart", "3.1-pro", "gemini-pro"},
		SupportedEfforts:      []string{"low", "high"}, // Note: gemini-3.1-pro has NO "medium" effort in agy
		DefaultEffort:         "high",
		MaxContextTokens:      1048576,
		MaxOutputTokens:       65536,
		CompactThresholdRatio: 0.70,
	},
	{
		ID:                    "gemini-3.6-flash",
		DisplayName:           "Gemini 3.6 Flash",
		Aliases:               []string{"3.6-flash", "gemini-3.6"},
		SupportedEfforts:      []string{"low", "medium", "high"},
		DefaultEffort:         "high",
		MaxContextTokens:      1048576,
		MaxOutputTokens:       65536,
		CompactThresholdRatio: 0.70,
	},
	{
		ID:                    "gemini-3.5-flash",
		DisplayName:           "Gemini 3.5 Flash",
		Aliases:               []string{"3.5-flash", "gemini-3.5"},
		SupportedEfforts:      []string{"low", "medium", "high"},
		DefaultEffort:         "high",
		MaxContextTokens:      1048576,
		MaxOutputTokens:       65536,
		CompactThresholdRatio: 0.70,
	},
	{
		ID:                    "claude-sonnet-4-6",
		DisplayName:           "Claude Sonnet 4.6",
		Aliases:               []string{"claude", "sonnet", "claude-sonnet"},
		SupportedEfforts:      []string{}, // agy rejects --effort for claude-sonnet-4-6
		DefaultEffort:         "",
		MaxContextTokens:      200000,
		MaxOutputTokens:       8192,
		CompactThresholdRatio: 0.70,
	},
	{
		ID:                    "claude-opus-4-6-thinking",
		DisplayName:           "Claude Opus 4.6 (Thinking)",
		Aliases:               []string{"opus", "claude-opus"},
		SupportedEfforts:      []string{}, // agy rejects --effort for claude-opus-4-6-thinking (thinking is built-in)
		DefaultEffort:         "",
		MaxContextTokens:      200000,
		MaxOutputTokens:       8192,
		CompactThresholdRatio: 0.70,
	},
	{
		ID:                    "gpt-oss-120b-medium",
		DisplayName:           "GPT-OSS 120B (Medium)",
		Aliases:               []string{"gpt-oss", "oss-120b"},
		SupportedEfforts:      []string{}, // agy rejects --effort flag for fixed gpt-oss-120b-medium
		DefaultEffort:         "",
		MaxContextTokens:      128000,
		MaxOutputTokens:       16384,
		CompactThresholdRatio: 0.70,
	},
}

// Global dynamic model registry (thread-safe)
var (
	registryMu      sync.RWMutex
	dynamicRegistry = append([]ModelCapability(nil), DefaultModelCapabilities...)
)

// SetDynamicModelCapabilities replaces the global registry with dynamically discovered models from agy models.
func SetDynamicModelCapabilities(models []ModelCapability) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if len(models) > 0 {
		dynamicRegistry = append([]ModelCapability(nil), models...)
	}
}

// ListAvailableModels returns the catalogue of known model capabilities (dynamic or fallback).
func ListAvailableModels() []ModelCapability {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return append([]ModelCapability(nil), dynamicRegistry...)
}

// ParseModelsOutput parses the stdout of 'agy models' into structured ModelCapability objects.
// Handles grouping suffixed models (-high, -medium, -low) into base models with supported efforts.
func ParseModelsOutput(output string) []ModelCapability {
	lines := strings.Split(output, "\n")
	type modelGroup struct {
		baseID      string
		displayName string
		efforts     []string
	}

	groups := make(map[string]*modelGroup)
	var order []string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Fetching") || strings.HasPrefix(line, "Usage:") {
			continue
		}

		fields := strings.SplitN(line, "\t", 2)
		modelID := strings.TrimSpace(fields[0])
		displayName := modelID
		if len(fields) > 1 {
			displayName = strings.TrimSpace(fields[1])
		}

		// Detect effort suffixes
		var effort string
		baseID := modelID
		baseDisplayName := displayName

		for _, eff := range []string{"-high", "-medium", "-low"} {
			if strings.HasSuffix(modelID, eff) {
				effort = strings.TrimPrefix(eff, "-")
				baseID = strings.TrimSuffix(modelID, eff)
				// Clean display name, e.g. "Gemini 3.7 Flash (High)" -> "Gemini 3.7 Flash"
				if idx := strings.LastIndex(displayName, "("); idx > 0 {
					baseDisplayName = strings.TrimSpace(displayName[:idx])
				}
				break
			}
		}

		if grp, exists := groups[baseID]; exists {
			if effort != "" {
				// Append effort if not already present
				hasEffort := false
				for _, e := range grp.efforts {
					if e == effort {
						hasEffort = true
						break
					}
				}
				if !hasEffort {
					grp.efforts = append(grp.efforts, effort)
				}
			}
		} else {
			var efforts []string
			if effort != "" {
				efforts = []string{effort}
			}
			groups[baseID] = &modelGroup{
				baseID:      baseID,
				displayName: baseDisplayName,
				efforts:     efforts,
			}
			order = append(order, baseID)
		}
	}

	var results []ModelCapability
	for _, baseID := range order {
		grp := groups[baseID]
		defaultEffort := ""
		if len(grp.efforts) > 0 {
			// Prefer high as default if available, otherwise first effort
			defaultEffort = grp.efforts[0]
			for _, e := range grp.efforts {
				if e == "high" {
					defaultEffort = "high"
					break
				}
			}
		}

		aliases := generateSmartAliases(grp.baseID)

		capObj := ModelCapability{
			ID:                    grp.baseID,
			DisplayName:           grp.displayName,
			Aliases:               aliases,
			SupportedEfforts:      grp.efforts,
			DefaultEffort:         defaultEffort,
			CompactThresholdRatio: 0.70,
		}
		capObj.MaxContextTokens = capObj.EffectiveMaxContext()
		capObj.MaxOutputTokens = capObj.EffectiveMaxOutput()

		results = append(results, capObj)
	}

	return results
}

func generateSmartAliases(modelID string) []string {
	var aliases []string
	idLower := strings.ToLower(modelID)

	if strings.Contains(idLower, "pro") {
		aliases = append(aliases, "pro", "smart")
	}
	if strings.Contains(idLower, "flash") && !strings.Contains(idLower, "lite") {
		aliases = append(aliases, "flash", "fast")
	}
	if strings.Contains(idLower, "lite") {
		aliases = append(aliases, "lite", "flash-lite")
	}
	if strings.Contains(idLower, "sonnet") {
		aliases = append(aliases, "claude", "sonnet")
	}
	if strings.Contains(idLower, "opus") {
		aliases = append(aliases, "opus", "claude-opus")
	}
	if strings.Contains(idLower, "gpt") || strings.Contains(idLower, "oss") {
		aliases = append(aliases, "gpt-oss", "oss-120b", "gpt")
	}

	return aliases
}

// LookupModelCapability finds a model capability by its canonical ID or alias.
// Handles full suffixed IDs (e.g. "gemini-3.7-flash-high" -> base "gemini-3.7-flash" with effort "high").
func LookupModelCapability(nameOrAlias string, customAliases map[string]string) (ModelCapability, string, bool) {
	norm := strings.ToLower(strings.TrimSpace(nameOrAlias))
	if norm == "" {
		return ModelCapability{}, "", false
	}

	// 1. Check user-defined custom aliases from configuration
	if customAliases != nil {
		if target, ok := customAliases[norm]; ok && target != "" {
			norm = strings.ToLower(strings.TrimSpace(target))
		}
	}

	// 2. Extract effort suffix if passed as full model ID (e.g. "gemini-3.7-flash-high")
	var inferredEffort string
	baseCandidate := norm
	for _, eff := range []string{"-high", "-medium", "-low"} {
		if strings.HasSuffix(norm, eff) {
			inferredEffort = strings.TrimPrefix(eff, "-")
			baseCandidate = strings.TrimSuffix(norm, eff)
			break
		}
	}

	// 3. Match against current live registry
	models := ListAvailableModels()
	for _, cap := range models {
		if strings.EqualFold(cap.ID, norm) {
			return cap, "", true
		}
		if strings.EqualFold(cap.ID, baseCandidate) && inferredEffort != "" {
			return cap, inferredEffort, true
		}
		for _, alias := range cap.Aliases {
			if strings.EqualFold(alias, norm) {
				return cap, "", true
			}
			if strings.EqualFold(alias, baseCandidate) && inferredEffort != "" {
				return cap, inferredEffort, true
			}
		}
	}

	return ModelCapability{}, "", false
}

// NormalizeModelAndEffort canonicalizes a model alias and enforces effort capability constraints.
//
// Specific agy Rules:
//  1. If model does NOT support effort (e.g. claude-sonnet-4-6, claude-opus-4-6-thinking, gpt-oss-120b-medium):
//     normalizedEffort is returned as empty string ("") so that --effort is omitted.
//  2. If model requires effort and only supports a subset (e.g. gemini-3.1-pro only supports [low, high]):
//     - If requested effort is "medium", it is normalized/clamped to DefaultEffort ("high").
//     - If effort is empty, it uses DefaultEffort ("high").
//  3. If model is unknown/custom:
//     - Passthrough model name and effort as requested.
func NormalizeModelAndEffort(rawModel, rawEffort string, customAliases map[string]string) (canonicalModel string, normalizedEffort string, isCustom bool) {
	cleanModel := strings.TrimSpace(rawModel)
	cleanEffort := strings.ToLower(strings.TrimSpace(rawEffort))

	if cleanModel == "" {
		return "", cleanEffort, false
	}

	cap, inferredEffort, found := LookupModelCapability(cleanModel, customAliases)
	if !found {
		// Custom / unlisted model: allow passthrough
		return cleanModel, cleanEffort, true
	}

	canonicalModel = cap.ID

	// If effort was embedded in the model name and not explicitly overridden, use inferred effort
	if cleanEffort == "" && inferredEffort != "" {
		cleanEffort = inferredEffort
	}

	// Rule 1: Model has NO effort support (e.g. claude-sonnet-4-6, claude-opus-4-6-thinking)
	if len(cap.SupportedEfforts) == 0 {
		return canonicalModel, "", false
	}

	// Rule 2: Model requires effort
	if cleanEffort == "" {
		normalizedEffort = cap.DefaultEffort
		return canonicalModel, normalizedEffort, false
	}

	for _, eff := range cap.SupportedEfforts {
		if strings.EqualFold(eff, cleanEffort) {
			return canonicalModel, eff, false
		}
	}

	// Effort requested is not supported by this model (e.g. "medium" on "gemini-3.1-pro" which only supports low/high)
	// Clamp/fallback to model's default effort ("high")
	return canonicalModel, cap.DefaultEffort, false
}
