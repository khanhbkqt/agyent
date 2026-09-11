package agy

import (
	"strings"

	"agyent/internal/core/domain"
)

// ParseModelsOutput parses the stdout of 'agy models' into structured ModelCapability objects.
// Handles grouping suffixed models (-high, -medium, -low) into base models with supported efforts.
func ParseModelsOutput(output string) []domain.ModelCapability {
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

	var results []domain.ModelCapability
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

		capObj := domain.ModelCapability{
			ID:                    grp.baseID,
			DisplayName:           grp.displayName,
			Aliases:               aliases,
			SupportedEfforts:      grp.efforts,
			DefaultEffort:         defaultEffort,
			CompactThresholdRatio: 0.90,
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
		aliases = append(aliases, "claude", "sonnet", "claude-sonnet")
	}
	if strings.Contains(idLower, "opus") {
		aliases = append(aliases, "opus", "claude-opus")
	}
	if strings.Contains(idLower, "gpt") || strings.Contains(idLower, "oss") {
		aliases = append(aliases, "gpt-oss", "oss-120b", "gpt")
	}

	// Version-specific shorthand aliases (e.g. "3.8", "3.8-flash", "gemini-3.8")
	for _, ver := range []string{"3.8", "3.7", "3.6", "3.5", "3.1", "2.5", "2.0"} {
		if strings.Contains(idLower, ver) {
			aliases = append(aliases, ver)
			if strings.Contains(idLower, "flash") {
				aliases = append(aliases, ver+"-flash", "gemini-"+ver)
			} else if strings.Contains(idLower, "pro") {
				aliases = append(aliases, ver+"-pro", "gemini-"+ver)
			}
			break
		}
	}

	return aliases
}
