package zalo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"agyent/internal/config"
	"agyent/internal/core/domain"
)

const MaxMediaSizeBytes = 25 * 1024 * 1024 // 25 MB max media size

var (
	sanitizedFilenameRegex = regexp.MustCompile(`[^a-zA-Z0-9._-]`)
	imageExtRegex          = regexp.MustCompile(`(?i)\.(png|jpg|jpeg|webp|gif|bmp)$`)
)

// MediaManager handles inbound and outbound media download and staging.
type MediaManager struct {
	client     *Client
	stagingDir string
	httpClient *http.Client
}

// NewMediaManager initializes media handling for Zalo channel.
func NewMediaManager(client *Client, baseDir string) (*MediaManager, error) {
	staging := filepath.Join(baseDir, "media", "zalo")
	expanded, err := config.ExpandPath(staging)
	if err != nil {
		expanded = staging
	}
	if err := os.MkdirAll(expanded, 0755); err != nil {
		return nil, fmt.Errorf("failed to create zalo media staging dir: %w", err)
	}
	return &MediaManager{
		client:     client,
		stagingDir: expanded,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}, nil
}

// DownloadInboundAttachment downloads a media file from remote URL into local staging.
func (m *MediaManager) DownloadInboundAttachment(ctx context.Context, remoteURL, preferredName string) (string, error) {
	if remoteURL == "" {
		return "", errors.New("remote URL cannot be empty")
	}

	safeName := SanitizeFilename(preferredName)
	if safeName == "" {
		safeName = fmt.Sprintf("zalo_media_%d", time.Now().UnixNano())
	}

	destPath := filepath.Join(m.stagingDir, safeName)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, remoteURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create media download request: %w", err)
	}
	req.Header.Set("User-Agent", "agyent-gateway/1.0")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download media from %s: %w", remoteURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("media download returned HTTP %d", resp.StatusCode)
	}

	if resp.ContentLength > MaxMediaSizeBytes {
		return "", fmt.Errorf("media file exceeds maximum size limit of 25MB (got %d bytes)", resp.ContentLength)
	}

	outFile, err := os.Create(destPath)
	if err != nil {
		return "", fmt.Errorf("failed to create destination file %s: %w", destPath, err)
	}
	defer outFile.Close()

	// Limit reader to prevent decompression bombs
	limitedReader := io.LimitReader(resp.Body, MaxMediaSizeBytes+1)
	written, err := io.Copy(outFile, limitedReader)
	if err != nil {
		_ = os.Remove(destPath)
		return "", fmt.Errorf("failed to save media payload: %w", err)
	}

	if written > MaxMediaSizeBytes {
		_ = os.Remove(destPath)
		return "", fmt.Errorf("downloaded media exceeded 25MB limit")
	}

	return destPath, nil
}

// SendOutboundAttachment uploads and sends a local file to a Zalo chat.
func (m *MediaManager) SendOutboundAttachment(ctx context.Context, chatID string, att domain.OutboundAttachment) error {
	filePath, err := config.ExpandPath(att.FilePath)
	if err != nil {
		filePath = att.FilePath
	}

	if _, err := os.Stat(filePath); err != nil {
		return fmt.Errorf("attachment file not found: %s", filePath)
	}

	isImage := imageExtRegex.MatchString(filePath) || strings.HasPrefix(att.MIMEType, "image/")

	if isImage {
		return m.client.SendPhoto(ctx, chatID, filePath, att.Caption)
	}
	return m.client.SendDocument(ctx, chatID, filePath, att.Caption)
}

// SanitizeFilename removes directory traversal sequences and special characters.
func SanitizeFilename(name string) string {
	base := filepath.Base(name)
	base = strings.ReplaceAll(base, "\x00", "")
	base = sanitizedFilenameRegex.ReplaceAllString(base, "_")
	base = strings.Trim(base, "._-")
	return base
}
