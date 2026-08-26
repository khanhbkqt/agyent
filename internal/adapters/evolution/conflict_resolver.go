package evolution

import (
	"fmt"
	"strings"
	"time"

	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

var _ ports.ConflictResolverPort = (*ConflictResolver)(nil)

const default4DMemoryTemplate = `# MEMORY.md - Evolved Long-Term Memory & Durable Knowledge

---

## 1. User-Soul Synergy & Collaboration Protocol
*Reflects the synergy between User preferences (USER.md) and Agent personality (SOUL.md):*
- [Communication & Tone]: Direct, structured, and action-oriented communication.
- [Engineering Standards]: Clean Architecture, Zero-CGO, Pure-Go SQLite WAL mode, and complete test suites.

---

## 2. Active Trajectory & Pending State
*Current working focus and ongoing project state:*
- [Active Project]: agyent Gateway Daemon.

---

## 3. Hardened Architectural Decisions
*Core architectural decisions and root-cause rationales (ADRs):*

---

## 4. Evolved Behavioral Guardrails
*Verified behavioral lessons extracted from reflections:*
`

// ConflictResolver performs section-aware 4D Markdown parsing and in-place rule conflict resolution.
type ConflictResolver struct{}

// NewConflictResolver constructs a new ConflictResolver.
func NewConflictResolver() *ConflictResolver {
	return &ConflictResolver{}
}

// ResolveAndMerge4D parses existing 4D markdown, replaces conflicting rules in-place, or appends new rules into appropriate sections.
func (r *ConflictResolver) ResolveAndMerge4D(existingMarkdown string, candidates []domain.MemoryCandidate) (string, []domain.MemoryCandidate, error) {
	if len(candidates) == 0 {
		return existingMarkdown, nil, nil
	}

	// Normalize Windows CRLF line endings to LF for deterministic parsing
	doc := strings.ReplaceAll(existingMarkdown, "\r\n", "\n")
	if strings.TrimSpace(doc) == "" {
		doc = default4DMemoryTemplate
	}

	var applied []domain.MemoryCandidate

	for _, cand := range candidates {
		sectionHeader := r.targetSectionHeader(cand.Category)
		conflictTag := r.formatConflictTag(cand)
		newRuleLine := r.formatRuleLine(cand)

		if strings.Contains(doc, sectionHeader) {
			// Section exists in doc
			doc = r.replaceOrAppendInSection(doc, sectionHeader, conflictTag, newRuleLine)
			applied = append(applied, cand)
		} else {
			// Section missing, append section at the end
			doc = strings.TrimSpace(doc) + "\n\n---\n\n" + sectionHeader + "\n" + newRuleLine + "\n"
			applied = append(applied, cand)
		}
	}

	return strings.TrimSpace(doc) + "\n", applied, nil
}

func (r *ConflictResolver) targetSectionHeader(cat domain.MemoryCategory) string {
	switch cat {
	case domain.CategoryPreference:
		return "## 1. User-Soul Synergy & Collaboration Protocol"
	case domain.CategoryADR:
		return "## 3. Hardened Architectural Decisions"
	case domain.CategoryLesson:
		return "## 4. Evolved Behavioral Guardrails"
	default:
		return "## 4. Evolved Behavioral Guardrails"
	}
}

func (r *ConflictResolver) formatConflictTag(cand domain.MemoryCandidate) string {
	if cand.ConflictKey != "" {
		return fmt.Sprintf("[%s]", cand.ConflictKey)
	}
	if cand.Title != "" {
		return fmt.Sprintf("[%s]", cand.Title)
	}
	return ""
}

func (r *ConflictResolver) formatRuleLine(cand domain.MemoryCandidate) string {
	dateStr := time.Now().Format("2006-01-02")
	tag := r.formatConflictTag(cand)
	if tag == "" {
		tag = "[Lesson]"
	}

	if cand.Rationale != "" {
		return fmt.Sprintf("- **%s - %s:** %s *(Rationale: %s)*", dateStr, tag, cand.Constraint, cand.Rationale)
	}
	return fmt.Sprintf("- **%s - %s:** %s", dateStr, tag, cand.Constraint)
}

func (r *ConflictResolver) replaceOrAppendInSection(doc, sectionHeader, conflictTag, newRuleLine string) string {
	secIdx := strings.Index(doc, sectionHeader)
	if secIdx == -1 {
		return doc + "\n\n" + sectionHeader + "\n" + newRuleLine
	}

	// Find the end of this section (either next "## " or end of doc)
	postSec := doc[secIdx+len(sectionHeader):]
	nextSecIdx := strings.Index(postSec, "\n## ")

	var sectionBody string
	var remainder string
	if nextSecIdx == -1 {
		sectionBody = postSec
		remainder = ""
	} else {
		sectionBody = postSec[:nextSecIdx]
		remainder = postSec[nextSecIdx:]
	}

	// Check if conflictTag exists in sectionBody (In-place replacement)
	lines := strings.Split(sectionBody, "\n")
	replaced := false
	var updatedLines []string

	if conflictTag != "" {
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "-") && strings.Contains(strings.ToLower(trimmed), strings.ToLower(conflictTag)) {
				// Replace line in-place
				updatedLines = append(updatedLines, newRuleLine)
				replaced = true
			} else {
				updatedLines = append(updatedLines, line)
			}
		}
	}

	if !replaced {
		// Append to section
		updatedLines = append(updatedLines, newRuleLine)
	}

	newSectionBody := strings.Join(updatedLines, "\n")
	return doc[:secIdx+len(sectionHeader)] + newSectionBody + remainder
}
