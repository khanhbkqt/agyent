package ports

import (
	"context"
	"errors"

	"agyent/internal/core/domain"
)

var (
	// ErrAttachmentTooLarge is returned when an attachment exceeds maximum allowed file size.
	ErrAttachmentTooLarge = errors.New("attachment exceeds maximum allowed size")
	// ErrAttachmentNotFound is returned when an attachment reference cannot be resolved or downloaded.
	ErrAttachmentNotFound = errors.New("attachment not found or inaccessible")
)

// AttachmentFetcherPort defines operations for lazily materializing inbound media attachments
// to disk only after authorization and turn admission have succeeded.
type AttachmentFetcherPort interface {
	FetchAttachment(ctx context.Context, ref domain.InboundAttachmentRef, targetDir string) (domain.Attachment, error)
}

// URLSafetyEvaluator validates an adapter-supplied remote URL before it is
// fetched. It keeps ingress media downloads subject to the same network policy
// as other outbound network actions.
type URLSafetyEvaluator interface {
	EvaluateURL(ctx context.Context, rawURL string) (domain.SecurityDecision, error)
}
