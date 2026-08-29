package ports

import (
	"context"

	"agyent/internal/core/domain"
)

// WorkspacePort defines the interface for workspace filesystem operations,
// including preparing inbound media attachments, maintaining directory structures,
// and ensuring workspace cleanliness (.gitignore generation).
type WorkspacePort interface {
	// PrepareInboundAttachments relocates or copies inbound attachments from staging
	// to the active workspace's uploads directory (<workspaceDir>/uploads/<safe_name>),
	// ensures directory and .gitignore existence, and returns updated attachment metadata.
	PrepareInboundAttachments(ctx context.Context, workspaceDir string, attachments []domain.Attachment) ([]domain.Attachment, error)

	// EnsureWorkspaceUploadsDir ensures that <workspaceDir>/uploads exists and has a .gitignore.
	EnsureWorkspaceUploadsDir(workspaceDir string) (string, error)
}
