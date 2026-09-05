package ports

import (
	"context"
	"errors"

	"agyent/internal/core/domain"
)

var (
	// ErrAccessDenied is returned when a principal lacks permission for an action on a resource.
	ErrAccessDenied = errors.New("access denied: unauthorized operation")
	// ErrInvalidPrincipal is returned when principal identity is malformed or unauthenticated.
	ErrInvalidPrincipal = errors.New("invalid principal")
	// ErrResourceNotFound is returned when the target resource does not exist.
	ErrResourceNotFound = errors.New("resource not found")
)

// PolicyEngine defines the centralized authorization gate for all ingress, commands, and operations.
type PolicyEngine interface {
	// Authorize evaluates whether the given principal is permitted to perform the specified action on the target resource.
	// Returns nil if permitted, or ErrAccessDenied / specific error if forbidden.
	Authorize(ctx context.Context, principal domain.Principal, action domain.Action, resource domain.Resource) error

	// ResolveAgentRole evaluates the effective AgentRole of a principal on a specific agent.
	ResolveAgentRole(ctx context.Context, principal domain.Principal, agentName string) (domain.AgentRole, error)
}
