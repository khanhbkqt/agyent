package ports

import (
	"context"

	"agyent/internal/core/domain"
)

// ContextResolverPort handles scanning, resolving, and merging hierarchical context directives and skills.
type ContextResolverPort interface {
	// Resolve combines Global Directives and Workspace Directives based on deterministic precedence.
	Resolve(ctx context.Context, globalHome string, workspaceDir string) (*domain.ResolvedContext, error)

	// DiscoverSkills scans and deduplicates skills from global and workspace paths using Progressive Disclosure.
	DiscoverSkills(ctx context.Context, globalHome string, workspaceDir string) ([]domain.SkillHeader, error)
}
