package evolution

import (
	"bufio"
	"fmt"
	"regexp"
	"strings"

	"agyent/internal/core/domain"
)

// MemoryCompactor performs deduplication and pruning on MEMORY.md to keep it strictly bounded.
type MemoryCompactor struct{}

// NewMemoryCompactor creates a new MemoryCompactor instance.
func NewMemoryCompactor() *MemoryCompactor {
	return &MemoryCompactor{}
}

// Compact evaluates line count and deduplicates redundant rules if exceeding lineLimit.
func (c *MemoryCompactor) Compact(markdown string, lineLimit int) (string, bool, error) {
	if lineLimit <= 0 {
		lineLimit = 200
	}

	lines := strings.Split(markdown, "\n")
	if len(lines) <= lineLimit {
		return markdown, false, nil
	}

	// Parse sections
	var headers []string
	sectionLines := make(map[string][]string)
	currentSection := ""

	scanner := bufio.NewScanner(strings.NewReader(markdown))
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "## ") {
			currentSection = trimmed
			headers = append(headers, currentSection)
			sectionLines[currentSection] = []string{}
			continue
		}

		if currentSection != "" {
			sectionLines[currentSection] = append(sectionLines[currentSection], line)
		} else {
			// Top-level document header
			sectionLines["__header__"] = append(sectionLines["__header__"], line)
		}
	}

	// Deduplicate section "## 4. Evolved Behavioral Guardrails"
	guardrailSec := "## 4. Evolved Behavioral Guardrails"
	if rawLines, exists := sectionLines[guardrailSec]; exists {
		seenTags := make(map[string]bool)
		var deduplicated []string

		// Iterate backwards to keep the most recent entries
		for i := len(rawLines) - 1; i >= 0; i-- {
			line := rawLines[i]
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "-") {
				tag := extractTag(trimmed)
				if tag != "" && seenTags[tag] {
					continue // Duplicate, prune it
				}
				if tag != "" {
					seenTags[tag] = true
				}
				deduplicated = append([]string{line}, deduplicated...)
			} else {
				deduplicated = append([]string{line}, deduplicated...)
			}
		}
		sectionLines[guardrailSec] = deduplicated
	}

	// Reassemble document
	var sb strings.Builder
	if top, ok := sectionLines["__header__"]; ok {
		sb.WriteString(strings.Join(top, "\n"))
		sb.WriteString("\n")
	}

	for _, h := range headers {
		sb.WriteString(h)
		sb.WriteString("\n")
		if body, ok := sectionLines[h]; ok {
			sb.WriteString(strings.Join(body, "\n"))
			sb.WriteString("\n")
		}
	}

	res := strings.TrimSpace(sb.String()) + "\n"
	return res, true, nil
}

var ruleTagRegex = regexp.MustCompile(`^-\s*(?:(?:\*\*[^*]+\*\*|[0-9\-]{4,10})\s*-\s*)?\[([a-zA-Z0-9_\-\s]+)\]`)

func extractTag(line string) string {
	trimmed := strings.TrimSpace(line)
	matches := ruleTagRegex.FindStringSubmatch(trimmed)
	if len(matches) > 1 {
		return strings.ToLower(strings.TrimSpace(matches[1]))
	}
	return ""
}

func (c *MemoryCompactor) FormatCandidateAsRule(cand domain.MemoryCandidate) string {
	dateStr := cand.CreatedAt.Format("2006-01-02")
	tag := cand.ConflictKey
	if tag == "" {
		tag = cand.Title
	}
	if tag == "" {
		tag = "Rule"
	}
	return fmt.Sprintf("- **%s - [%s]:** %s", dateStr, tag, cand.Constraint)
}
