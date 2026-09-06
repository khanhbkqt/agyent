package zalo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
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
	markdownMediaRegex     = regexp.MustCompile(`(!)?\[([^\]]*)\]\(([^)]+)\)`)
	multiNewlineRegex      = regexp.MustCompile(`\n{3,}`)
	htmlCommentRegex       = regexp.MustCompile(`(?s)<!--.*?-->`)
)

var excludedDocFiles = map[string]bool{
	"identity.md":       true,
	"soul.md":           true,
	"user.md":           true,
	"memory.md":         true,
	"agents.md":         true,
	"go.mod":            true,
	"go.sum":            true,
	"package.json":      true,
	"package-lock.json": true,
	"tsconfig.json":     true,
	".gitignore":        true,
	"license":           true,
	"license.md":        true,
	"readme.md":         true,
}

var allowedArtifactExts = map[string]bool{
	".png":    true,
	".jpg":    true,
	".jpeg":   true,
	".gif":    true,
	".webp":   true,
	".svg":    true,
	".pdf":    true,
	".zip":    true,
	".tar.gz": true,
	".tar":    true,
	".gz":     true,
	".7z":     true,
	".csv":    true,
	".xlsx":   true,
	".docx":   true,
	".json":   true,
	".txt":    true,
	".md":     true,
	".yaml":   true,
	".yml":    true,
	".xml":    true,
	".html":   true,
	".mp4":    true,
	".mp3":    true,
	".wav":    true,
}

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

// SendOutboundAttachment delivers an attachment to chatID via the specified client.
func (m *MediaManager) SendOutboundAttachment(ctx context.Context, chatID string, att domain.OutboundAttachment, clientOpt ...*Client) error {
	c := m.client
	if len(clientOpt) > 0 && clientOpt[0] != nil {
		c = clientOpt[0]
	}
	if c == nil {
		return errors.New("zalo media client is not initialized")
	}
	filePath := att.FilePath
	if !strings.HasPrefix(filePath, "http://") && !strings.HasPrefix(filePath, "https://") {
		expanded, err := config.ExpandPath(filePath)
		if err == nil {
			filePath = expanded
		}
		if _, err := os.Stat(filePath); err != nil {
			return fmt.Errorf("Zalo attachment file unavailable: %w", err)
		}
	}
	if imageExtRegex.MatchString(filePath) || strings.HasPrefix(att.MIMEType, "image/") || att.Type == "image" {
		return c.SendPhoto(ctx, chatID, filePath, att.Caption)
	}
	return c.SendDocument(ctx, chatID, filePath, att.Caption)
}

// SanitizeFilename removes path traversal and unsupported filename characters.
func SanitizeFilename(name string) string {
	base := filepath.Base(name)
	base = strings.ReplaceAll(base, "\x00", "")
	base = sanitizedFilenameRegex.ReplaceAllString(base, "_")
	return strings.Trim(base, "._-")
}

var (
	mediaUploaderMu     sync.RWMutex
	publicMediaUploader func(ctx context.Context, filePath string) (string, error)
)

// SetPublicMediaUploaderForTest overrides public media upload during testing.
// It returns a restore function.
func SetPublicMediaUploaderForTest(fn func(ctx context.Context, filePath string) (string, error)) func() {
	mediaUploaderMu.Lock()
	prev := publicMediaUploader
	publicMediaUploader = fn
	mediaUploaderMu.Unlock()
	return func() {
		mediaUploaderMu.Lock()
		publicMediaUploader = prev
		mediaUploaderMu.Unlock()
	}
}

// UploadPublicMedia uploads a local file to a public CDN and returns an HTTPS URL.
func UploadPublicMedia(ctx context.Context, filePath string) (string, error) {
	mediaUploaderMu.RLock()
	uploader := publicMediaUploader
	mediaUploaderMu.RUnlock()
	if uploader != nil {
		return uploader(ctx, filePath)
	}

	filePath = strings.TrimSpace(filePath)
	if filePath == "" {
		return "", errors.New("empty file path")
	}
	if strings.HasPrefix(filePath, "http://") || strings.HasPrefix(filePath, "https://") {
		return filePath, nil
	}

	expanded, err := config.ExpandPath(filePath)
	if err == nil {
		filePath = expanded
	}

	stat, err := os.Stat(filePath)
	if err != nil {
		return "", fmt.Errorf("local file unavailable: %w", err)
	}
	if stat.IsDir() {
		return "", fmt.Errorf("cannot upload directory: %s", filePath)
	}
	if stat.Size() > 50*1024*1024 {
		return "", fmt.Errorf("file size %d exceeds 50MB limit", stat.Size())
	}

	// 1. Try Catbox.moe
	if u, err := uploadToCatbox(ctx, filePath); err == nil && u != "" {
		return u, nil
	}

	// 2. Fallback to Litterbox (72h retention)
	if u, err := uploadToLitterbox(ctx, filePath); err == nil && u != "" {
		return u, nil
	}

	// 3. Fallback to 0x0.st
	if u, err := uploadTo0x0(ctx, filePath); err == nil && u != "" {
		return u, nil
	}

	return "", fmt.Errorf("all public media upload providers failed for %s", filePath)
}

func uploadToCatbox(ctx context.Context, filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("reqtype", "fileupload")
	part, err := writer.CreateFormFile("fileToUpload", filepath.Base(filePath))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, file); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}

	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, "https://catbox.moe/user/api.php", body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("User-Agent", "agyent-gateway/1.0")

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return "", err
	}

	res := strings.TrimSpace(string(respBytes))
	if resp.StatusCode == http.StatusOK && strings.HasPrefix(res, "https://") {
		return res, nil
	}
	return "", fmt.Errorf("catbox upload failed with HTTP %d: %s", resp.StatusCode, res)
}

func uploadToLitterbox(ctx context.Context, filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("reqtype", "fileupload")
	_ = writer.WriteField("time", "72h")
	part, err := writer.CreateFormFile("fileToUpload", filepath.Base(filePath))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, file); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}

	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, "https://litterbox.catbox.moe/resources/internals/api.php", body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("User-Agent", "agyent-gateway/1.0")

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return "", err
	}

	res := strings.TrimSpace(string(respBytes))
	if resp.StatusCode == http.StatusOK && strings.HasPrefix(res, "https://") {
		return res, nil
	}
	return "", fmt.Errorf("litterbox upload failed with HTTP %d: %s", resp.StatusCode, res)
}

func uploadTo0x0(ctx context.Context, filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, file); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}

	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, "https://0x0.st", body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("User-Agent", "agyent-gateway/1.0")

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return "", err
	}

	res := strings.TrimSpace(string(respBytes))
	if resp.StatusCode == http.StatusOK && (strings.HasPrefix(res, "https://") || strings.HasPrefix(res, "http://")) {
		return res, nil
	}
	return "", fmt.Errorf("0x0 upload failed with HTTP %d: %s", resp.StatusCode, res)
}

// ExtractAndCleanOutboundMedia extracts embedded media from markdown text,
// resolves their filesystem paths, removes raw markdown image tags and carousel HTML comments,
// and returns the cleaned text along with extracted domain.Attachment objects.
func ExtractAndCleanOutboundMedia(text string, workspaceDir string, convID string) (string, []domain.Attachment) {
	if strings.TrimSpace(text) == "" {
		return text, nil
	}

	var attachments []domain.Attachment
	homeDir, _ := os.UserHomeDir()
	seenPaths := make(map[string]bool)

	// Process all markdown images (![caption](path)) and links ([caption](path))
	cleaned := markdownMediaRegex.ReplaceAllStringFunc(text, func(match string) string {
		submatches := markdownMediaRegex.FindStringSubmatch(match)
		if len(submatches) < 4 {
			return match
		}
		isImage := submatches[1] == "!"
		caption := strings.TrimSpace(submatches[2])
		rawPath := strings.TrimSpace(submatches[3])

		if idx := strings.IndexAny(rawPath, "?#"); idx != -1 {
			rawPath = rawPath[:idx]
		}
		if rawPath == "" {
			return match
		}

		lowerPath := strings.ToLower(rawPath)
		if isImage && (strings.HasPrefix(lowerPath, "http://") || strings.HasPrefix(lowerPath, "https://")) {
			if !seenPaths[rawPath] {
				seenPaths[rawPath] = true
				attachments = append(attachments, domain.Attachment{
					ID:       rawPath,
					FileName: filepath.Base(rawPath),
					FilePath: rawPath,
					MIMEType: "image/jpeg",
					Type:     "image",
					Caption:  caption,
				})
			}
			return ""
		}

		if strings.HasPrefix(lowerPath, "http://") || strings.HasPrefix(lowerPath, "https://") || strings.HasPrefix(lowerPath, "mailto:") {
			return match
		}

		rawBase := strings.ToLower(filepath.Base(rawPath))
		if excludedDocFiles[rawBase] {
			return match
		}

		resolvedPath := resolveLocalMediaPath(rawPath, workspaceDir, convID, homeDir)
		if resolvedPath == "" {
			return match
		}

		baseName := strings.ToLower(filepath.Base(resolvedPath))
		if excludedDocFiles[baseName] {
			return match
		}

		stat, err := os.Stat(resolvedPath)
		if err != nil || stat.IsDir() || stat.Size() == 0 || stat.Size() > 50*1024*1024 {
			return match
		}

		ext := strings.ToLower(filepath.Ext(resolvedPath))
		if !allowedArtifactExts[ext] {
			return match
		}

		attType := "document"
		if ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".webp" || ext == ".gif" || ext == ".svg" {
			attType = "image"
		} else if ext == ".mp4" {
			attType = "video"
		} else if ext == ".mp3" || ext == ".wav" || ext == ".ogg" {
			attType = "audio"
		}

		if !seenPaths[resolvedPath] {
			seenPaths[resolvedPath] = true

			mimeType := mime.TypeByExtension(ext)
			if mimeType == "" {
				switch ext {
				case ".png":
					mimeType = "image/png"
				case ".jpg", ".jpeg":
					mimeType = "image/jpeg"
				case ".webp":
					mimeType = "image/webp"
				case ".gif":
					mimeType = "image/gif"
				default:
					mimeType = "application/octet-stream"
				}
			}

			displayCaption := caption
			if displayCaption == "" {
				displayCaption = filepath.Base(resolvedPath)
			}

			attachments = append(attachments, domain.Attachment{
				ID:       resolvedPath,
				FileName: filepath.Base(resolvedPath),
				FilePath: resolvedPath,
				MIMEType: mimeType,
				Size:     stat.Size(),
				Type:     attType,
				Caption:  displayCaption,
			})
		}

		if isImage || attType == "image" {
			return ""
		}

		if caption != "" {
			return "📄 " + caption
		}
		return "📄 " + filepath.Base(resolvedPath)
	})

	cleaned = htmlCommentRegex.ReplaceAllString(cleaned, "")
	cleaned = multiNewlineRegex.ReplaceAllString(cleaned, "\n\n")
	cleaned = strings.TrimSpace(cleaned)

	return cleaned, attachments
}

func resolveLocalMediaPath(rawPath, workspaceDir, convID, homeDir string) string {
	rawPath = strings.TrimSpace(rawPath)
	if strings.HasPrefix(rawPath, "http://") || strings.HasPrefix(rawPath, "https://") || strings.HasPrefix(rawPath, "mailto:") {
		return ""
	}

	if strings.HasPrefix(rawPath, "file://") {
		rawPath = strings.TrimPrefix(rawPath, "file://")
		if len(rawPath) > 2 && rawPath[0] == '/' && (rawPath[2] == ':' || rawPath[2] == '|') {
			rawPath = rawPath[1:]
			if rawPath[1] == '|' {
				rawPath = rawPath[:1] + ":" + rawPath[2:]
			}
		}
	}

	if strings.HasPrefix(rawPath, "~/") || strings.HasPrefix(rawPath, "~\\") {
		if homeDir != "" {
			rawPath = filepath.Join(homeDir, rawPath[2:])
		}
	} else if rawPath == "~" {
		rawPath = homeDir
	}

	rawPath = filepath.Clean(filepath.FromSlash(rawPath))

	if filepath.IsAbs(rawPath) {
		if _, err := os.Stat(rawPath); err == nil {
			return rawPath
		}
	}

	if workspaceDir != "" {
		wsPath := filepath.Join(workspaceDir, rawPath)
		if _, err := os.Stat(wsPath); err == nil {
			return filepath.Clean(wsPath)
		}
	}

	baseName := filepath.Base(rawPath)

	if homeDir != "" {
		scratchCandidates := []string{
			filepath.Join(homeDir, ".gemini", "antigravity", "scratch", baseName),
			filepath.Join(homeDir, ".gemini", "antigravity-cli", "scratch", baseName),
		}
		if workspaceDir != "" {
			scratchCandidates = append(scratchCandidates, filepath.Join(workspaceDir, "scratch", baseName))
		}
		for _, sc := range scratchCandidates {
			if _, err := os.Stat(sc); err == nil {
				return filepath.Clean(sc)
			}
		}
	}

	if convID != "" && homeDir != "" {
		brainCandidates := []string{
			filepath.Join(homeDir, ".gemini", "antigravity", "brain", convID, baseName),
			filepath.Join(homeDir, ".gemini", "antigravity-cli", "brain", convID, baseName),
		}
		for _, bc := range brainCandidates {
			if _, err := os.Stat(bc); err == nil {
				return filepath.Clean(bc)
			}
		}

		ext := strings.ToLower(filepath.Ext(baseName))
		if ext == "" || ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".webp" || ext == ".gif" {
			nameWithoutExt := strings.TrimSuffix(baseName, ext)
			if brainImg, err := FindBrainImage(convID, nameWithoutExt); err == nil && brainImg != "" {
				return filepath.Clean(brainImg)
			}
		}
	}

	return ""
}

// FindBrainImage searches for images produced by generate_image in the brain directory.
func FindBrainImage(convID string, imageName string) (string, error) {
	if convID == "" {
		return "", fmt.Errorf("empty conversation id")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	candidates := []string{
		filepath.Join(home, ".gemini", "antigravity", "brain", convID),
		filepath.Join(home, ".gemini", "antigravity-cli", "brain", convID),
	}

	for _, brainDir := range candidates {
		if info, err := os.Stat(brainDir); err == nil && info.IsDir() {
			entries, err := os.ReadDir(brainDir)
			if err != nil {
				continue
			}

			if imageName != "" {
				lowerName := strings.ToLower(imageName)
				for _, entry := range entries {
					if entry.IsDir() {
						continue
					}
					name := strings.ToLower(entry.Name())
					if (strings.HasPrefix(name, lowerName) || strings.Contains(name, lowerName)) &&
						(strings.HasSuffix(name, ".jpg") || strings.HasSuffix(name, ".png") || strings.HasSuffix(name, ".jpeg") || strings.HasSuffix(name, ".webp") || strings.HasSuffix(name, ".gif")) {
						return filepath.Clean(filepath.Join(brainDir, entry.Name())), nil
					}
				}
			} else {
				var newestPath string
				var newestTime time.Time
				for _, entry := range entries {
					if entry.IsDir() {
						continue
					}
					name := strings.ToLower(entry.Name())
					if strings.HasSuffix(name, ".jpg") || strings.HasSuffix(name, ".png") || strings.HasSuffix(name, ".jpeg") || strings.HasSuffix(name, ".webp") || strings.HasSuffix(name, ".gif") {
						info, err := entry.Info()
						if err == nil && info.ModTime().After(newestTime) {
							newestTime = info.ModTime()
							newestPath = filepath.Join(brainDir, entry.Name())
						}
					}
				}
				if newestPath != "" {
					return filepath.Clean(newestPath), nil
				}
			}
		}
	}

	return "", fmt.Errorf("no matching image found in brain for %s", imageName)
}
