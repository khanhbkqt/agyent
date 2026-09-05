package zalo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"agyent/internal/config"
	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

const MaxMediaSizeBytes = 25 * 1024 * 1024

var (
	_ ports.AttachmentFetcherPort = (*MediaManager)(nil)

	sanitizedFilenameRegex = regexp.MustCompile(`[^a-zA-Z0-9._-]`)
	imageExtRegex          = regexp.MustCompile(`(?i)\.(png|jpg|jpeg|webp|gif|bmp)$`)
)

// MediaManager materializes inbound media only after the engine has completed
// authorization and turn admission, and sends approved outbound artifacts.
type MediaManager struct {
	client     *Client
	stagingDir string
	httpClient *http.Client

	mu                 sync.RWMutex
	urlSafetyEvaluator ports.URLSafetyEvaluator
}

// SetURLSafetyEvaluator injects the gateway-owned remote URL policy.
func (m *MediaManager) SetURLSafetyEvaluator(evaluator ports.URLSafetyEvaluator) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.urlSafetyEvaluator = evaluator
}

func (m *MediaManager) getURLSafetyEvaluator() ports.URLSafetyEvaluator {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.urlSafetyEvaluator
}

func NewMediaManager(client *Client, baseDir string) (*MediaManager, error) {
	staging := filepath.Join(baseDir, "media", "zalo")
	expanded, err := config.ExpandPath(staging)
	if err != nil {
		expanded = staging
	}
	if err := os.MkdirAll(expanded, 0700); err != nil {
		return nil, fmt.Errorf("create zalo media staging dir: %w", err)
	}
	return &MediaManager{
		client:     client,
		stagingDir: expanded,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}, nil
}

// FetchAttachment implements the core lazy-media contract. SourceID is the
// provider-issued media URL retained in the canonical attachment reference.
func (m *MediaManager) FetchAttachment(ctx context.Context, ref domain.InboundAttachmentRef, targetDir string) (domain.Attachment, error) {
	if ref.Channel != "" && !strings.EqualFold(ref.Channel, "zalo") {
		return domain.Attachment{}, fmt.Errorf("%w: Zalo media manager cannot fetch channel %q", ports.ErrAttachmentNotFound, ref.Channel)
	}
	if ref.Size > MaxMediaSizeBytes {
		return domain.Attachment{}, fmt.Errorf("%w: declared size %d exceeds limit", ports.ErrAttachmentTooLarge, ref.Size)
	}
	filePath, size, fileName, err := m.downloadInboundAttachment(ctx, ref.SourceID, ref.FileName, targetDir)
	if err != nil {
		return domain.Attachment{}, err
	}
	return domain.Attachment{
		ID:       ref.ID,
		FileName: fileName,
		FilePath: filePath,
		MIMEType: ref.MIMEType,
		Size:     size,
		Type:     ref.Type,
		Caption:  ref.Caption,
	}, nil
}

// DownloadInboundAttachment remains available to adapter callers, but router
// ingress no longer invokes it before authorization.
func (m *MediaManager) DownloadInboundAttachment(ctx context.Context, remoteURL, preferredName string) (string, error) {
	filePath, _, _, err := m.downloadInboundAttachment(ctx, remoteURL, preferredName, m.stagingDir)
	return filePath, err
}

func (m *MediaManager) downloadInboundAttachment(ctx context.Context, remoteURL, preferredName, targetDir string) (string, int64, string, error) {
	if strings.TrimSpace(remoteURL) == "" {
		return "", 0, "", fmt.Errorf("%w: missing Zalo media URL", ports.ErrAttachmentNotFound)
	}
	evaluator := m.getURLSafetyEvaluator()
	parsedURL, err := validateZaloMediaURL(ctx, remoteURL, evaluator)
	if err != nil {
		return "", 0, "", err
	}
	if targetDir == "" {
		targetDir = m.stagingDir
	}
	if err := os.MkdirAll(targetDir, 0700); err != nil {
		return "", 0, "", fmt.Errorf("create Zalo attachment directory: %w", err)
	}

	fileName := SanitizeFilename(preferredName)
	if fileName == "" {
		fileName = fmt.Sprintf("zalo_media_%d", time.Now().UnixNano())
	}
	destination := filepath.Join(targetDir, fmt.Sprintf("%d_%s", time.Now().UnixNano(), fileName))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsedURL.String(), nil)
	if err != nil {
		return "", 0, "", fmt.Errorf("create Zalo media download request: %w", err)
	}
	req.Header.Set("User-Agent", "agyent-gateway/1.0")
	client := *m.httpClient
	client.CheckRedirect = func(redirect *http.Request, via []*http.Request) error {
		_, err := validateZaloMediaURL(redirect.Context(), redirect.URL.String(), evaluator)
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, "", fmt.Errorf("download Zalo media: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", 0, "", fmt.Errorf("download Zalo media: unexpected HTTP status %d", resp.StatusCode)
	}
	if resp.ContentLength > MaxMediaSizeBytes {
		return "", 0, "", fmt.Errorf("%w: content length %d exceeds limit", ports.ErrAttachmentTooLarge, resp.ContentLength)
	}

	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", 0, "", fmt.Errorf("create Zalo attachment file: %w", err)
	}
	defer file.Close()
	size, err := io.Copy(file, io.LimitReader(resp.Body, MaxMediaSizeBytes+1))
	if err != nil {
		_ = os.Remove(destination)
		return "", 0, "", fmt.Errorf("write Zalo media payload: %w", err)
	}
	if size > MaxMediaSizeBytes {
		_ = os.Remove(destination)
		return "", 0, "", fmt.Errorf("%w: downloaded bytes exceed limit", ports.ErrAttachmentTooLarge)
	}
	return destination, size, fileName, nil
}

func validateZaloMediaURL(ctx context.Context, rawURL string, evaluator ports.URLSafetyEvaluator) (*url.URL, error) {
	parsedURL, err := url.ParseRequestURI(rawURL)
	if err != nil || parsedURL.Host == "" || parsedURL.Scheme != "https" {
		return nil, fmt.Errorf("%w: invalid Zalo media URL", ports.ErrAttachmentNotFound)
	}
	if evaluator == nil {
		return nil, fmt.Errorf("%w: Zalo media network policy is not initialized", ports.ErrAttachmentNotFound)
	}
	decision, err := evaluator.EvaluateURL(ctx, parsedURL.String())
	if err != nil {
		return nil, fmt.Errorf("evaluate Zalo media URL: %w", err)
	}
	if decision.Decision != domain.DecisionAllow {
		return nil, fmt.Errorf("%w: Zalo media URL blocked by network policy", ports.ErrAttachmentNotFound)
	}
	return parsedURL, nil
}

func (m *MediaManager) SendOutboundAttachment(ctx context.Context, chatID string, att domain.OutboundAttachment) error {
	if m.client == nil {
		return errors.New("zalo media client is not initialized")
	}
	filePath, err := config.ExpandPath(att.FilePath)
	if err != nil {
		filePath = att.FilePath
	}
	if _, err := os.Stat(filePath); err != nil {
		return fmt.Errorf("Zalo attachment file unavailable: %w", err)
	}
	if imageExtRegex.MatchString(filePath) || strings.HasPrefix(att.MIMEType, "image/") {
		return m.client.SendPhoto(ctx, chatID, filePath, att.Caption)
	}
	return m.client.SendDocument(ctx, chatID, filePath, att.Caption)
}

// SanitizeFilename removes path traversal and unsupported filename characters.
func SanitizeFilename(name string) string {
	base := filepath.Base(name)
	base = strings.ReplaceAll(base, "\x00", "")
	base = sanitizedFilenameRegex.ReplaceAllString(base, "_")
	return strings.Trim(base, "._-")
}
