package telegram

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"agyent/internal/config"
	"agyent/internal/core/domain"
)

var (
	markdownImageRegex = regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)
	multiNewlineRegex  = regexp.MustCompile(`\n{3,}`)
)

// Allowed artifact extensions for automatic outbound upload
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

// MediaManager handles inbound and outbound media transfers.
type MediaManager struct {
	cfg        *config.Config
	bot        *gotgbot.Bot
	httpClient *http.Client
}

// NewMediaManager creates a new MediaManager instance.
func NewMediaManager(cfg *config.Config, bot *gotgbot.Bot) *MediaManager {
	return &MediaManager{
		cfg: cfg,
		bot: bot,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

var windowsReservedNames = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true,
	"com5": true, "com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true,
	"lpt5": true, "lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// SanitizeFilename cleans filename to prevent path traversal attacks and Windows device name collisions.
func SanitizeFilename(name string) string {
	if name == "" {
		return "file"
	}

	// Remove path separators and relative directory tokens
	cleaned := filepath.Base(name)
	cleaned = strings.ReplaceAll(cleaned, "..", "")
	cleaned = strings.ReplaceAll(cleaned, "/", "_")
	cleaned = strings.ReplaceAll(cleaned, "\\", "_")
	cleaned = strings.ReplaceAll(cleaned, "\x00", "")

	// Remove any leading/trailing spaces or dots
	cleaned = strings.Trim(cleaned, " .")
	if cleaned == "" {
		cleaned = "file"
	}

	// Check Windows reserved device names
	baseOnly := strings.ToLower(cleaned)
	if idx := strings.Index(baseOnly, "."); idx != -1 {
		baseOnly = baseOnly[:idx]
	}
	if windowsReservedNames[baseOnly] {
		cleaned = "safe_" + cleaned
	}

	return cleaned
}

// DownloadInboundMedia extracts and downloads media files from a Telegram message into staging directory.
func (m *MediaManager) DownloadInboundMedia(ctx context.Context, msg *gotgbot.Message) ([]domain.Attachment, error) {
	if msg == nil || m.bot == nil {
		return nil, nil
	}

	var attachments []domain.Attachment
	targetDir := filepath.Join(m.cfg.Storage.AgentsDir, "staging")
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create staging upload directory: %w", err)
	}

	// 1. Photos (Take highest resolution)
	if len(msg.Photo) > 0 {
		bestPhoto := msg.Photo[len(msg.Photo)-1]
		fileName := fmt.Sprintf("photo_%d_%s.jpg", time.Now().Unix(), bestPhoto.FileUniqueId)
		att, err := m.downloadFile(ctx, bestPhoto.FileId, fileName, "image/jpeg", "image", targetDir)
		if err == nil {
			attachments = append(attachments, att)
		}
	}

	// 2. Documents
	if msg.Document != nil {
		fileName := SanitizeFilename(msg.Document.FileName)
		if fileName == "file" {
			fileName = fmt.Sprintf("doc_%d_%s", time.Now().Unix(), msg.Document.FileUniqueId)
		}
		mimeType := msg.Document.MimeType
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		att, err := m.downloadFile(ctx, msg.Document.FileId, fileName, mimeType, "document", targetDir)
		if err == nil {
			attachments = append(attachments, att)
		}
	}

	// 3. Audio / Voice
	if msg.Voice != nil {
		fileName := fmt.Sprintf("voice_%d_%s.ogg", time.Now().Unix(), msg.Voice.FileUniqueId)
		att, err := m.downloadFile(ctx, msg.Voice.FileId, fileName, "audio/ogg", "voice", targetDir)
		if err == nil {
			attachments = append(attachments, att)
		}
	}
	if msg.Audio != nil {
		fileName := SanitizeFilename(msg.Audio.FileName)
		if fileName == "file" {
			fileName = fmt.Sprintf("audio_%d_%s.mp3", time.Now().Unix(), msg.Audio.FileUniqueId)
		}
		att, err := m.downloadFile(ctx, msg.Audio.FileId, fileName, "audio/mpeg", "audio", targetDir)
		if err == nil {
			attachments = append(attachments, att)
		}
	}

	// 4. Video
	if msg.Video != nil {
		fileName := SanitizeFilename(msg.Video.FileName)
		if fileName == "file" {
			fileName = fmt.Sprintf("video_%d_%s.mp4", time.Now().Unix(), msg.Video.FileUniqueId)
		}
		att, err := m.downloadFile(ctx, msg.Video.FileId, fileName, "video/mp4", "video", targetDir)
		if err == nil {
			attachments = append(attachments, att)
		}
	}

	return attachments, nil
}

func (m *MediaManager) downloadFile(ctx context.Context, fileID, fileName, mimeType, attType, targetDir string) (domain.Attachment, error) {
	tgFile, err := m.bot.GetFile(fileID, nil)
	if err != nil {
		return domain.Attachment{}, fmt.Errorf("failed to get file info for %s: %w", fileID, err)
	}

	fileURL := tgFile.URL(m.bot, nil)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return domain.Attachment{}, fmt.Errorf("failed to create download request: %w", err)
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return domain.Attachment{}, fmt.Errorf("failed to download file %s: %w", fileID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return domain.Attachment{}, fmt.Errorf("bad status %d downloading file %s", resp.StatusCode, fileID)
	}

	safeName := fmt.Sprintf("%d_%s", time.Now().UnixNano(), fileName)
	destPath := filepath.Join(targetDir, safeName)

	out, err := os.Create(destPath)
	if err != nil {
		return domain.Attachment{}, fmt.Errorf("failed to create local file: %w", err)
	}
	defer out.Close()

	// Enforce 50MB file size limit to prevent disk exhaustion DoS
	limitedReader := io.LimitReader(resp.Body, 50<<20)
	size, err := io.Copy(out, limitedReader)
	if err != nil {
		return domain.Attachment{}, fmt.Errorf("failed to write local file: %w", err)
	}

	return domain.Attachment{
		ID:       fileID,
		FileName: fileName,
		FilePath: destPath,
		MIMEType: mimeType,
		Size:     size,
		Type:     attType,
	}, nil
}

// ExtractAndCleanOutboundMedia parses markdown text to find embedded local images/files,
// resolves their filesystem paths, removes raw markdown image tags and carousel HTML comments,
// and returns the cleaned text along with extracted domain.Attachment objects.
func ExtractAndCleanOutboundMedia(text string, workspaceDir string, convID string) (string, []domain.Attachment) {
	if strings.TrimSpace(text) == "" {
		return text, nil
	}

	var attachments []domain.Attachment
	homeDir, _ := os.UserHomeDir()

	// Find all markdown images: ![caption](path)
	matches := markdownImageRegex.FindAllStringSubmatch(text, -1)
	seenPaths := make(map[string]bool)

	for _, match := range matches {
		if len(match) < 3 {
			continue
		}
		caption := strings.TrimSpace(match[1])
		rawPath := strings.TrimSpace(match[2])

		// Strip query parameters or URL anchors if any (e.g. image.png#123)
		if idx := strings.IndexAny(rawPath, "?#"); idx != -1 {
			rawPath = rawPath[:idx]
		}
		if rawPath == "" {
			continue
		}

		resolvedPath := resolveLocalMediaPath(rawPath, workspaceDir, convID, homeDir)
		if resolvedPath == "" || seenPaths[resolvedPath] {
			continue
		}

		stat, err := os.Stat(resolvedPath)
		if err != nil || stat.IsDir() || stat.Size() == 0 || stat.Size() > 50*1024*1024 {
			continue
		}

		ext := strings.ToLower(filepath.Ext(resolvedPath))
		if !allowedArtifactExts[ext] {
			continue
		}

		seenPaths[resolvedPath] = true
		attType := "document"
		if ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".webp" || ext == ".gif" || ext == ".svg" {
			attType = "image"
		} else if ext == ".mp4" {
			attType = "video"
		} else if ext == ".mp3" || ext == ".wav" || ext == ".ogg" {
			attType = "audio"
		}

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

		attachments = append(attachments, domain.Attachment{
			ID:       resolvedPath,
			FileName: filepath.Base(resolvedPath),
			FilePath: resolvedPath,
			MIMEType: mimeType,
			Size:     stat.Size(),
			Type:     attType,
			Caption:  caption,
		})
	}

	// Clean text:
	// 1. Remove markdown image references
	cleaned := markdownImageRegex.ReplaceAllString(text, "")
	// 2. Remove HTML comments (<!-- slide -->, <!-- carousel -->, etc.)
	cleaned = htmlCommentRegex.ReplaceAllString(cleaned, "")
	// 3. Normalize multiple blank lines
	cleaned = multiNewlineRegex.ReplaceAllString(cleaned, "\n\n")
	cleaned = strings.TrimSpace(cleaned)

	return cleaned, attachments
}

func resolveLocalMediaPath(rawPath, workspaceDir, convID, homeDir string) string {
	rawPath = strings.TrimSpace(rawPath)
	if strings.HasPrefix(rawPath, "file://") {
		rawPath = strings.TrimPrefix(rawPath, "file://")
		// On Windows, file:///C:/path -> C:/path
		if len(rawPath) > 2 && rawPath[0] == '/' && (rawPath[2] == ':' || rawPath[2] == '|') {
			rawPath = rawPath[1:]
			if rawPath[1] == '|' {
				rawPath = rawPath[:1] + ":" + rawPath[2:]
			}
		}
	}

	// Expand ~ / ~/
	if strings.HasPrefix(rawPath, "~/") || strings.HasPrefix(rawPath, "~\\") {
		if homeDir != "" {
			rawPath = filepath.Join(homeDir, rawPath[2:])
		}
	} else if rawPath == "~" {
		rawPath = homeDir
	}

	// If absolute and exists
	if filepath.IsAbs(rawPath) {
		if _, err := os.Stat(rawPath); err == nil {
			return rawPath
		}
	}

	// Check workspaceDir
	if workspaceDir != "" {
		wsPath := filepath.Join(workspaceDir, rawPath)
		if _, err := os.Stat(wsPath); err == nil {
			return wsPath
		}
	}

	// Check brain candidate locations if convID or basename
	if convID != "" && homeDir != "" {
		baseName := filepath.Base(rawPath)
		brainCandidates := []string{
			filepath.Join(homeDir, ".gemini", "antigravity", "brain", convID, baseName),
			filepath.Join(homeDir, ".gemini", "antigravity-cli", "brain", convID, baseName),
		}
		for _, bc := range brainCandidates {
			if _, err := os.Stat(bc); err == nil {
				return bc
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

	// Check potential brain directory locations
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

			// First look for exact or prefix matches with imageName
			if imageName != "" {
				lowerName := strings.ToLower(imageName)
				for _, entry := range entries {
					if entry.IsDir() {
						continue
					}
					name := strings.ToLower(entry.Name())
					if (strings.HasPrefix(name, lowerName) || strings.Contains(name, lowerName)) &&
						(strings.HasSuffix(name, ".jpg") || strings.HasSuffix(name, ".png") || strings.HasSuffix(name, ".jpeg") || strings.HasSuffix(name, ".webp") || strings.HasSuffix(name, ".gif")) {
						return filepath.Join(brainDir, entry.Name()), nil
					}
				}
			}

			// Fallback: look for newest image in the brain folder
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
				return newestPath, nil
			}
		}
	}

	return "", fmt.Errorf("no generated image found for conversation %s", convID)
}

// SendBrainImage uploads and sends a brain image to Telegram chat.
func (m *MediaManager) SendBrainImage(ctx context.Context, chatID int64, threadID int64, imagePath string, caption string) error {
	if imagePath == "" || m.bot == nil {
		return fmt.Errorf("invalid image path or bot client")
	}

	file, err := os.Open(imagePath)
	if err != nil {
		return fmt.Errorf("failed to open brain image %s: %w", imagePath, err)
	}
	defer file.Close()

	opts := &gotgbot.SendPhotoOpts{
		Caption: caption,
	}
	if threadID != 0 {
		opts.MessageThreadId = threadID
	}

	inputFile := &gotgbot.FileReader{Name: filepath.Base(imagePath), Data: file}
	_, err = m.bot.SendPhoto(chatID, inputFile, opts)
	return err
}

// SendMediaGroup sends up to 10 photos as a native Telegram Album / Media Group.
func (m *MediaManager) SendMediaGroup(ctx context.Context, chatID int64, threadID int64, mediaList []domain.Attachment) error {
	if len(mediaList) == 0 || m.bot == nil {
		return nil
	}

	var photos []domain.Attachment
	for _, att := range mediaList {
		ext := strings.ToLower(filepath.Ext(att.FilePath))
		if ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".webp" || ext == ".gif" {
			photos = append(photos, att)
		}
	}

	if len(photos) == 0 {
		return nil
	}

	for i := 0; i < len(photos); i += 10 {
		end := i + 10
		if end > len(photos) {
			end = len(photos)
		}
		chunk := photos[i:end]

		if len(chunk) == 1 {
			caption := chunk[0].Caption
			if caption == "" {
				caption = chunk[0].FileName
			}
			_ = m.SendBrainImage(ctx, chatID, threadID, chunk[0].FilePath, caption)
			continue
		}

		var inputMedias gotgbot.InputMedias
		var openFiles []*os.File

		for _, p := range chunk {
			f, err := os.Open(p.FilePath)
			if err != nil {
				continue
			}
			openFiles = append(openFiles, f)
			caption := p.Caption
			if caption == "" {
				caption = p.FileName
			}
			inputMedias = append(inputMedias, gotgbot.InputMediaPhoto{
				Media:   &gotgbot.FileReader{Name: filepath.Base(p.FilePath), Data: f},
				Caption: caption,
			})
		}

		if len(inputMedias) >= 2 {
			opts := &gotgbot.SendMediaGroupOpts{}
			if threadID != 0 {
				opts.MessageThreadId = threadID
			}
			_, _ = m.bot.SendMediaGroup(chatID, inputMedias, opts)
		} else if len(inputMedias) == 1 {
			caption := chunk[0].Caption
			if caption == "" {
				caption = chunk[0].FileName
			}
			_ = m.SendBrainImage(ctx, chatID, threadID, chunk[0].FilePath, caption)
		}

		for _, f := range openFiles {
			_ = f.Close()
		}
	}

	return nil
}

// UploadTurnArtifacts sends whitelisted outbound artifacts after turn completion.
// If multiple photos are present, it sends them as an album (SendMediaGroup).
func (m *MediaManager) UploadTurnArtifacts(ctx context.Context, chatID int64, threadID int64, artifacts []domain.Attachment) error {
	if len(artifacts) == 0 || m.bot == nil {
		return nil
	}

	var photos []domain.Attachment
	var docs []domain.Attachment

	for _, att := range artifacts {
		if att.FilePath == "" {
			continue
		}
		ext := strings.ToLower(filepath.Ext(att.FilePath))
		if !allowedArtifactExts[ext] {
			continue
		}

		if ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".webp" || ext == ".gif" {
			photos = append(photos, att)
		} else {
			docs = append(docs, att)
		}
	}

	// 1. Send photos (grouped as MediaGroup if >= 2)
	if len(photos) >= 2 {
		_ = m.SendMediaGroup(ctx, chatID, threadID, photos)
	} else if len(photos) == 1 {
		caption := photos[0].Caption
		if caption == "" {
			caption = photos[0].FileName
		}
		_ = m.SendBrainImage(ctx, chatID, threadID, photos[0].FilePath, caption)
	}

	// 2. Send non-photo documents
	for _, att := range docs {
		file, err := os.Open(att.FilePath)
		if err != nil {
			continue
		}
		inputFile := &gotgbot.FileReader{Name: att.FileName, Data: file}
		caption := att.Caption
		if caption == "" {
			caption = att.FileName
		}
		opts := &gotgbot.SendDocumentOpts{
			Caption: caption,
		}
		if threadID != 0 {
			opts.MessageThreadId = threadID
		}
		_, _ = m.bot.SendDocument(chatID, inputFile, opts)
		file.Close()
	}

	return nil
}
