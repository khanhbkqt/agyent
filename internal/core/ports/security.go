package ports

import (
	"context"

	"agyent/internal/core/domain"
)

// SecurityPort defines authorization and security inspection contracts.
type SecurityPort interface {
	// AuthorizeUser checks if a given user is whitelisted or permitted to interact with the system.
	AuthorizeUser(ctx context.Context, user domain.User) (bool, error)

	// AuthorizeGroup checks if a given group/supergroup chat is permitted to interact with the system.
	AuthorizeGroup(ctx context.Context, group domain.Group) (bool, error)

	// InspectInput scans input text for dangerous command injection or unauthorized patterns.
	InspectInput(ctx context.Context, text string) error
}
