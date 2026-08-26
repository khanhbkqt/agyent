package domain

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	// ValidIdentifierRegex enforces safe alphanumeric characters, hyphens, and underscores.
	ValidIdentifierRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
	// SanitizeIdentifierRegex matches any character NOT in [a-zA-Z0-9_-].
	SanitizeIdentifierRegex = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)
	// ErrInvalidIdentifier indicates an identifier contains illegal characters or colons.
	ErrInvalidIdentifier = errors.New("domain: identifier contains invalid characters or colons (allowed: [a-zA-Z0-9_-], max 64 chars)")
)

// Project represents a managed codebase attached to an Agent.
type Project struct {
	ID          string    `json:"id"`           // e.g. "dev_expert:my_project"
	AgentName   string    `json:"agent_name"`   // Associated agent name
	ProjectName string    `json:"project_name"` // Project identifier
	ProjectPath string    `json:"project_path"` // Absolute filesystem path
	CreatedAt   time.Time `json:"created_at"`
}

// FormatProjectID creates a composite project ID from agent name and project name.
// Delimiters, whitespaces, and illegal characters are sanitized to prevent composite key collisions.
func FormatProjectID(agentName, projectName string) string {
	agentName = strings.TrimSpace(agentName)
	projectName = strings.TrimSpace(projectName)
	if !ValidIdentifierRegex.MatchString(agentName) {
		agentName = SanitizeIdentifierRegex.ReplaceAllString(agentName, "_")
		agentName = strings.Trim(agentName, "_")
		if agentName == "" {
			agentName = "default_agent"
		}
	}
	if !ValidIdentifierRegex.MatchString(projectName) {
		projectName = SanitizeIdentifierRegex.ReplaceAllString(projectName, "_")
		projectName = strings.Trim(projectName, "_")
		if projectName == "" {
			projectName = "default_project"
		}
	}
	return fmt.Sprintf("%s:%s", agentName, projectName)
}

// ValidateProjectIdentifiers verifies that agent and project names do not cause delimiter collisions.
func ValidateProjectIdentifiers(agentName, projectName string) error {
	if !ValidIdentifierRegex.MatchString(strings.TrimSpace(agentName)) {
		return fmt.Errorf("%w: invalid agent name %q", ErrInvalidIdentifier, agentName)
	}
	if !ValidIdentifierRegex.MatchString(strings.TrimSpace(projectName)) {
		return fmt.Errorf("%w: invalid project name %q", ErrInvalidIdentifier, projectName)
	}
	return nil
}
