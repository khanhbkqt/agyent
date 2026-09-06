package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"agyent/internal/config"
	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

var _ ports.AttachmentFetcherPort = (*MediaManager)(nil)

var (
	markdownMediaRegex = regexp.MustCompile(`(!)?\[([^\]]*)\]\(([^)]+)\)`)
	markdownImageRegex = regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)
	multiNewlineRegex  = regexp.MustCompile(`\n{3,}`)
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
	cfg              *config.Config
	bot              *gotgbot.Bot
	botGetter        func(botID int64) *gotgbot.Bot
	botByAgentGetter func(agentName string) *gotgbot.Bot
	httpClient       *http.Client
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

// SetBotGetter configures the bot resolver by botID.
func (m *MediaManager) SetBotGetter(bg func(botID int64) *gotgbot.Bot) {
	m.botGetter = bg
}

// SetBotByAgentGetter configures the bot resolver by agent name.
func (m *MediaManager) SetBotByAgentGetter(bag func(agentName string) *gotgbot.Bot) {
	m.botByAgentGetter = bag
}

func (m *MediaManager) resolveBot(botOpt []*gotgbot.Bot, botIDOpt ...int64) *gotgbot.Bot {
	if len(botOpt) > 0 && botOpt[0] != nil {
		return botOpt[0]
	}
	if len(botIDOpt) > 0 && botIDOpt[0] > 0 && m.botGetter != nil {
		if b := m.botGetter(botIDOpt[0]); b != nil {
			return b
		}
	}
	return m.bot
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

	// Remove path separators and relative directory tokens across platforms
	normalized := strings.ReplaceAll(name, "\\", "/")
	cleaned := filepath.Base(normalized)
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

// ExtractAttachmentRefs extracts metadata references to media attachments without downloading any file to disk.
func (m *MediaManager) ExtractAttachmentRefs(msg *gotgbot.Message, botID int64) []domain.InboundAttachmentRef {
	if msg == nil {
		return nil
	}
	var refs []domain.InboundAttachmentRef

	// 1. Photo (highest resolution)
	if len(msg.Photo) > 0 {
		bestPhoto := msg.Photo[len(msg.Photo)-1]
		fileName := fmt.Sprintf("photo_%d_%s.jpg", time.Now().Unix(), bestPhoto.FileUniqueId)
		refs = append(refs, domain.InboundAttachmentRef{
			Channel:  "telegram",
			ID:       bestPhoto.FileUniqueId,
			SourceID: bestPhoto.FileId,
			FileName: fileName,
			MIMEType: "image/jpeg",
			Size:     bestPhoto.FileSize,
			Type:     "image",
			Caption:  msg.Caption,
			BotID:    botID,
		})
	}

	// 2. Document
	if msg.Document != nil {
		fileName := SanitizeFilename(msg.Document.FileName)
		if fileName == "file" {
			fileName = fmt.Sprintf("doc_%d_%s", time.Now().Unix(), msg.Document.FileUniqueId)
		}
		mimeType := msg.Document.MimeType
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		refs = append(refs, domain.InboundAttachmentRef{
			Channel:  "telegram",
			ID:       msg.Document.FileUniqueId,
			SourceID: msg.Document.FileId,
			FileName: fileName,
			MIMEType: mimeType,
			Size:     msg.Document.FileSize,
			Type:     "document",
			Caption:  msg.Caption,
			BotID:    botID,
		})
	}

	// 3. Audio / Voice
	if msg.Voice != nil {
		fileName := fmt.Sprintf("voice_%d_%s.ogg", time.Now().Unix(), msg.Voice.FileUniqueId)
		refs = append(refs, domain.InboundAttachmentRef{
			Channel:  "telegram",
			ID:       msg.Voice.FileUniqueId,
			SourceID: msg.Voice.FileId,
			FileName: fileName,
			MIMEType: "audio/ogg",
			Size:     msg.Voice.FileSize,
			Type:     "voice",
			Caption:  msg.Caption,
			BotID:    botID,
		})
	}
	if msg.Audio != nil {
		fileName := SanitizeFilename(msg.Audio.FileName)
		if fileName == "file" {
			fileName = fmt.Sprintf("audio_%d_%s.mp3", time.Now().Unix(), msg.Audio.FileUniqueId)
		}
		mimeType := msg.Audio.MimeType
		if mimeType == "" {
			mimeType = "audio/mpeg"
		}
		refs = append(refs, domain.InboundAttachmentRef{
			Channel:  "telegram",
			ID:       msg.Audio.FileUniqueId,
			SourceID: msg.Audio.FileId,
			FileName: fileName,
			MIMEType: mimeType,
			Size:     msg.Audio.FileSize,
			Type:     "audio",
			Caption:  msg.Caption,
			BotID:    botID,
		})
	}

	// 4. Video
	if msg.Video != nil {
		fileName := SanitizeFilename(msg.Video.FileName)
		if fileName == "file" {
			fileName = fmt.Sprintf("video_%d_%s.mp4", time.Now().Unix(), msg.Video.FileUniqueId)
		}
		mimeType := msg.Video.MimeType
		if mimeType == "" {
			mimeType = "video/mp4"
		}
		refs = append(refs, domain.InboundAttachmentRef{
			Channel:  "telegram",
			ID:       msg.Video.FileUniqueId,
			SourceID: msg.Video.FileId,
			FileName: fileName,
			MIMEType: mimeType,
			Size:     msg.Video.FileSize,
			Type:     "video",
			Caption:  msg.Caption,
			BotID:    botID,
		})
	}

	return refs
}

// FetchAttachment implements ports.AttachmentFetcherPort by lazily downloading a file using its reference.
func (m *MediaManager) FetchAttachment(ctx context.Context, ref domain.InboundAttachmentRef, targetDir string) (domain.Attachment, error) {
	var bot *gotgbot.Bot
	if ref.BotID > 0 && m.botGetter != nil {
		bot = m.botGetter(ref.BotID)
	}
	if bot == nil {
		bot = m.bot
	}
	if bot == nil {
		return domain.Attachment{}, errors.New("telegram bot client not available to fetch attachment")
	}

	if targetDir == "" {
		targetDir = filepath.Join(m.cfg.Storage.AgentsDir, "staging")
	}
	if err := os.MkdirAll(targetDir, 0700); err != nil {
		return domain.Attachment{}, fmt.Errorf("failed to create target attachment directory: %w", err)
	}

	return m.downloadFile(ctx, bot, ref.SourceID, ref.FileName, ref.MIMEType, ref.Type, targetDir)
}

// DownloadInboundMedia extracts and downloads media files from a Telegram message into staging directory.
func (m *MediaManager) DownloadInboundMedia(ctx context.Context, msg *gotgbot.Message, botOpt ...*gotgbot.Bot) ([]domain.Attachment, error) {
	bot := m.resolveBot(botOpt)
	if msg == nil || bot == nil {
		return nil, nil
	}

	var attachments []domain.Attachment
	targetDir := filepath.Join(m.cfg.Storage.AgentsDir, "staging")
	if err := os.MkdirAll(targetDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create staging upload directory: %w", err)
	}

	// 1. Photos (Take highest resolution)
	if len(msg.Photo) > 0 {
		bestPhoto := msg.Photo[len(msg.Photo)-1]
		fileName := fmt.Sprintf("photo_%d_%s.jpg", time.Now().Unix(), bestPhoto.FileUniqueId)
		att, err := m.downloadFile(ctx, bot, bestPhoto.FileId, fileName, "image/jpeg", "image", targetDir)
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
		att, err := m.downloadFile(ctx, bot, msg.Document.FileId, fileName, mimeType, "document", targetDir)
		if err == nil {
			attachments = append(attachments, att)
		}
	}

	// 3. Audio / Voice
	if msg.Voice != nil {
		fileName := fmt.Sprintf("voice_%d_%s.ogg", time.Now().Unix(), msg.Voice.FileUniqueId)
		att, err := m.downloadFile(ctx, bot, msg.Voice.FileId, fileName, "audio/ogg", "voice", targetDir)
		if err == nil {
			attachments = append(attachments, att)
		}
	}
	if msg.Audio != nil {
		fileName := SanitizeFilename(msg.Audio.FileName)
		if fileName == "file" {
			fileName = fmt.Sprintf("audio_%d_%s.mp3", time.Now().Unix(), msg.Audio.FileUniqueId)
		}
		att, err := m.downloadFile(ctx, bot, msg.Audio.FileId, fileName, "audio/mpeg", "audio", targetDir)
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
		att, err := m.downloadFile(ctx, bot, msg.Video.FileId, fileName, "video/mp4", "video", targetDir)
		if err == nil {
			attachments = append(attachments, att)
		}
	}

	return attachments, nil
}

func (m *MediaManager) downloadFile(ctx context.Context, bot *gotgbot.Bot, fileID, fileName, mimeType, attType, targetDir string) (domain.Attachment, error) {
	if bot == nil {
		bot = m.bot
	}
	if bot == nil {
		return domain.Attachment{}, fmt.Errorf("bot client is nil")
	}

	tgFile, err := bot.GetFile(fileID, nil)
	if err != nil {
		return domain.Attachment{}, fmt.Errorf("failed to get file info for %s: %w", fileID, err)
	}

	fileURL := tgFile.URL(bot, nil)
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

	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return domain.Attachment{}, fmt.Errorf("failed to create local file: %w", err)
	}

	// Enforce 50MB file size limit to prevent disk exhaustion DoS
	const maxFileSize = 50 << 20 // 50MB
	limitedReader := io.LimitReader(resp.Body, maxFileSize+1)
	size, err := io.Copy(out, limitedReader)
	_ = out.Close()
	if err != nil {
		_ = os.Remove(destPath)
		return domain.Attachment{}, fmt.Errorf("failed to write local file: %w", err)
	}

	if size > maxFileSize {
		_ = os.Remove(destPath)
		return domain.Attachment{}, fmt.Errorf("%w: downloaded bytes exceed 50MB limit", ports.ErrAttachmentTooLarge)
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

// ExtractAndCleanOutboundMedia parses markdown text to find embedded local images and document links,
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

		// Strip query parameters or URL anchors if any (e.g. image.png#123)
		if idx := strings.IndexAny(rawPath, "?#"); idx != -1 {
			rawPath = rawPath[:idx]
		}
		if rawPath == "" {
			return match
		}

		// Web URLs and standard protocols are kept intact
		lowerPath := strings.ToLower(rawPath)
		if strings.HasPrefix(lowerPath, "http://") || strings.HasPrefix(lowerPath, "https://") || strings.HasPrefix(lowerPath, "mailto:") {
			return match
		}

		// Exclude project and documentation files immediately
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

		// If image embed (![caption](path)) or an image file linked via [caption](path.png),
		// strip completely from message text because it will be uploaded directly as a photo
		if isImage || attType == "image" {
			return ""
		}

		// If document link ([caption](path)), replace with a clean icon & title
		if caption != "" {
			return "📄 " + caption
		}
		return "📄 " + filepath.Base(resolvedPath)
	})

	// 2. Remove HTML comments (<!-- slide -->, <!-- carousel -->, etc.)
	cleaned = htmlCommentRegex.ReplaceAllString(cleaned, "")
	// 3. Normalize multiple blank lines
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

	// Normalize separator
	rawPath = filepath.Clean(filepath.FromSlash(rawPath))

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
			return filepath.Clean(wsPath)
		}
	}

	baseName := filepath.Base(rawPath)

	// Check scratch directory locations
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

	// Check brain candidate locations if convID or basename
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

		// Fallback: match generated images with prefix in brain (e.g. rocket_logo_1788507992076.jpg from rocket_logo)
		// Only attempt this if the extension is an image format or omitted
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
						return filepath.Clean(filepath.Join(brainDir, entry.Name())), nil
					}
				}
			} else {
				// Fallback: only look for newest image in the brain folder if imageName is empty
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

	return "", fmt.Errorf("no generated image found for conversation %s", convID)
}

// openFileWithRetry attempts to open a file with retries to gracefully handle Windows file locks or write races.
func openFileWithRetry(filePath string, maxAttempts int, delay time.Duration) (*os.File, error) {
	var file *os.File
	var err error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		file, err = os.Open(filePath)
		if err == nil {
			return file, nil
		}
		if os.IsNotExist(err) || os.IsPermission(err) {
			return nil, err
		}
		if attempt < maxAttempts {
			time.Sleep(delay)
		}
	}
	return nil, err
}

// SendBrainImage uploads and sends a brain image to Telegram chat.
// If SendPhoto fails or is rejected, it automatically falls back to SendDocument so artifacts are never lost.
func (m *MediaManager) SendBrainImage(ctx context.Context, chatID int64, threadID int64, imagePath string, caption string, botOpt ...*gotgbot.Bot) error {
	bot := m.resolveBot(botOpt)
	if imagePath == "" || bot == nil {
		return fmt.Errorf("invalid image path or bot client")
	}

	file, err := openFileWithRetry(imagePath, 5, 100*time.Millisecond)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to open image for upload", "path", imagePath, "error", err)
		return fmt.Errorf("failed to open brain image %s: %w", imagePath, err)
	}
	defer file.Close()

	plainCaption := caption
	if utf8.RuneCountInString(plainCaption) > 1024 {
		plainCaption = string([]rune(plainCaption)[:1021]) + "..."
	}

	formattedCaption := FormatMarkdownToTelegramHTML(plainCaption)
	targetParseMode := "HTML"
	if utf8.RuneCountInString(formattedCaption) > 1024 {
		targetParseMode = ""
		formattedCaption = plainCaption
	}

	opts := &gotgbot.SendPhotoOpts{
		Caption:   formattedCaption,
		ParseMode: targetParseMode,
	}
	if threadID != 0 {
		opts.MessageThreadId = threadID
	}

	inputFile := &gotgbot.FileReader{Name: filepath.Base(imagePath), Data: file}
	_, err = bot.SendPhoto(chatID, inputFile, opts)
	if err != nil {
		var tgErr *gotgbot.TelegramError
		if errors.As(err, &tgErr) {
			if tgErr.Code == 429 {
				retrySec := 1
				if tgErr.ResponseParams != nil && tgErr.ResponseParams.RetryAfter > 0 {
					retrySec = int(tgErr.ResponseParams.RetryAfter)
				}
				slog.WarnContext(ctx, "Telegram sendPhoto rate limited (429), waiting and retrying",
					"chat_id", chatID, "retry_after", retrySec, "path", imagePath)
				time.Sleep(time.Duration(retrySec) * time.Second)

				if _, seekErr := file.Seek(0, io.SeekStart); seekErr == nil {
					_, err = bot.SendPhoto(chatID, inputFile, opts)
				}
			} else if tgErr.Code == 400 && targetParseMode != "" && (strings.Contains(strings.ToLower(tgErr.Description), "parse") || strings.Contains(strings.ToLower(tgErr.Description), "entity")) {
				// Fallback to plain text caption if HTML parse mode fails
				opts.ParseMode = ""
				opts.Caption = plainCaption
				if _, seekErr := file.Seek(0, io.SeekStart); seekErr == nil {
					_, err = bot.SendPhoto(chatID, inputFile, opts)
				}
			}
		}
	}

	// Fallback to SendDocument if sendPhoto failed (e.g. photo processing error, invalid dimensions, etc.)
	if err != nil {
		slog.WarnContext(ctx, "Telegram sendPhoto failed, falling back to SendDocument",
			"chat_id", chatID, "path", imagePath, "error", err)

		if _, seekErr := file.Seek(0, io.SeekStart); seekErr == nil {
			docOpts := &gotgbot.SendDocumentOpts{
				Caption:   formattedCaption,
				ParseMode: targetParseMode,
			}
			if threadID != 0 {
				docOpts.MessageThreadId = threadID
			}
			docFile := &gotgbot.FileReader{Name: filepath.Base(imagePath), Data: file}
			_, docErr := bot.SendDocument(chatID, docFile, docOpts)
			if docErr != nil && targetParseMode != "" {
				docOpts.ParseMode = ""
				docOpts.Caption = plainCaption
				if _, sErr := file.Seek(0, io.SeekStart); sErr == nil {
					_, docErr = bot.SendDocument(chatID, docFile, docOpts)
				}
			}
			if docErr == nil {
				slog.InfoContext(ctx, "Successfully uploaded image via SendDocument fallback",
					"chat_id", chatID, "path", imagePath)
				return nil
			}
			slog.ErrorContext(ctx, "Telegram SendDocument fallback also failed",
				"chat_id", chatID, "path", imagePath, "error", docErr)
		}
		return fmt.Errorf("failed to send photo %s (and fallback failed): %w", imagePath, err)
	}

	slog.InfoContext(ctx, "Successfully uploaded image", "chat_id", chatID, "path", imagePath)
	return nil
}

// SendMediaGroup sends up to 10 photos as a native Telegram Album / Media Group.
// If SendMediaGroup fails, it falls back to sending photos individually via SendBrainImage.
func (m *MediaManager) SendMediaGroup(ctx context.Context, chatID int64, threadID int64, mediaList []domain.Attachment, botOpt ...*gotgbot.Bot) error {
	bot := m.resolveBot(botOpt)
	if len(mediaList) == 0 || bot == nil {
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

	var sendErrors []error

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
			if err := m.SendBrainImage(ctx, chatID, threadID, chunk[0].FilePath, caption, bot); err != nil {
				sendErrors = append(sendErrors, err)
			}
			continue
		}

		var inputMedias gotgbot.InputMedias
		var openFiles []*os.File

		for _, p := range chunk {
			f, err := openFileWithRetry(p.FilePath, 5, 100*time.Millisecond)
			if err != nil {
				slog.ErrorContext(ctx, "Failed to open photo for media group", "path", p.FilePath, "error", err)
				continue
			}
			openFiles = append(openFiles, f)
			plainCap := p.Caption
			if plainCap == "" {
				plainCap = p.FileName
			}
			if utf8.RuneCountInString(plainCap) > 1024 {
				plainCap = string([]rune(plainCap)[:1021]) + "..."
			}
			formattedCap := FormatMarkdownToTelegramHTML(plainCap)
			parseMode := "HTML"
			if utf8.RuneCountInString(formattedCap) > 1024 {
				parseMode = ""
				formattedCap = plainCap
			}

			inputMedias = append(inputMedias, gotgbot.InputMediaPhoto{
				Media:     &gotgbot.FileReader{Name: filepath.Base(p.FilePath), Data: f},
				Caption:   formattedCap,
				ParseMode: parseMode,
			})
		}

		var sendGroupErr error
		if len(inputMedias) >= 2 {
			opts := &gotgbot.SendMediaGroupOpts{}
			if threadID != 0 {
				opts.MessageThreadId = threadID
			}
			_, sendGroupErr = bot.SendMediaGroup(chatID, inputMedias, opts)
			if sendGroupErr != nil {
				var tgErr *gotgbot.TelegramError
				if errors.As(sendGroupErr, &tgErr) && tgErr.Code == 429 {
					retrySec := 1
					if tgErr.ResponseParams != nil && tgErr.ResponseParams.RetryAfter > 0 {
						retrySec = int(tgErr.ResponseParams.RetryAfter)
					}
					time.Sleep(time.Duration(retrySec) * time.Second)
					for _, f := range openFiles {
						_, _ = f.Seek(0, io.SeekStart)
					}
					_, sendGroupErr = bot.SendMediaGroup(chatID, inputMedias, opts)
				}
			}
		}

		for _, f := range openFiles {
			_ = f.Close()
		}

		// Fallback: If SendMediaGroup failed or less than 2 files could be opened,
		// send them individually via SendBrainImage
		if sendGroupErr != nil || len(inputMedias) < 2 {
			if sendGroupErr != nil {
				slog.WarnContext(ctx, "SendMediaGroup failed, falling back to individual SendBrainImage calls",
					"chat_id", chatID, "error", sendGroupErr)
			}
			for _, p := range chunk {
				caption := p.Caption
				if caption == "" {
					caption = p.FileName
				}
				time.Sleep(250 * time.Millisecond)
				if err := m.SendBrainImage(ctx, chatID, threadID, p.FilePath, caption, bot); err != nil {
					sendErrors = append(sendErrors, err)
				}
			}
		}
	}

	if len(sendErrors) > 0 {
		return fmt.Errorf("errors sending media group (%d failures): %v", len(sendErrors), sendErrors[0])
	}
	return nil
}

// UploadTurnArtifacts sends whitelisted outbound artifacts after turn completion.
// If multiple photos are present, it sends them as an album (SendMediaGroup).
func (m *MediaManager) UploadTurnArtifacts(ctx context.Context, chatID int64, threadID int64, artifacts []domain.Attachment, botOpt ...*gotgbot.Bot) error {
	bot := m.resolveBot(botOpt)
	if len(artifacts) == 0 || bot == nil {
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

	var uploadErrors []error

	// 1. Send photos (grouped as MediaGroup if >= 2)
	if len(photos) >= 2 {
		if err := m.SendMediaGroup(ctx, chatID, threadID, photos, bot); err != nil {
			slog.ErrorContext(ctx, "Failed to send photo album", "chat_id", chatID, "count", len(photos), "error", err)
			uploadErrors = append(uploadErrors, err)
		}
	} else if len(photos) == 1 {
		caption := photos[0].Caption
		if caption == "" {
			caption = photos[0].FileName
		}
		if err := m.SendBrainImage(ctx, chatID, threadID, photos[0].FilePath, caption, bot); err != nil {
			slog.ErrorContext(ctx, "Failed to send turn photo", "chat_id", chatID, "path", photos[0].FilePath, "error", err)
			uploadErrors = append(uploadErrors, err)
		}
	}

	// Pace between photo and document uploads if both exist to avoid tripping Telegram rate limits
	if len(photos) > 0 && len(docs) > 0 {
		time.Sleep(300 * time.Millisecond)
	}

	// 2. Send non-photo documents
	for i, att := range docs {
		if i > 0 {
			time.Sleep(250 * time.Millisecond) // Pace consecutive document uploads
		}

		file, err := openFileWithRetry(att.FilePath, 5, 100*time.Millisecond)
		if err != nil {
			slog.ErrorContext(ctx, "Failed to open document artifact", "path", att.FilePath, "error", err)
			uploadErrors = append(uploadErrors, err)
			continue
		}

		inputFile := &gotgbot.FileReader{Name: att.FileName, Data: file}
		plainDocCap := att.Caption
		if plainDocCap == "" {
			plainDocCap = att.FileName
		}
		if utf8.RuneCountInString(plainDocCap) > 1024 {
			plainDocCap = string([]rune(plainDocCap)[:1021]) + "..."
		}
		formattedDocCap := FormatMarkdownToTelegramHTML(plainDocCap)
		docParseMode := "HTML"
		if utf8.RuneCountInString(formattedDocCap) > 1024 {
			docParseMode = ""
			formattedDocCap = plainDocCap
		}

		opts := &gotgbot.SendDocumentOpts{
			Caption:   formattedDocCap,
			ParseMode: docParseMode,
		}
		if threadID != 0 {
			opts.MessageThreadId = threadID
		}

		_, docErr := bot.SendDocument(chatID, inputFile, opts)
		if docErr != nil {
			var tgErr *gotgbot.TelegramError
			if errors.As(docErr, &tgErr) && tgErr.Code == 429 {
				retrySec := 1
				if tgErr.ResponseParams != nil && tgErr.ResponseParams.RetryAfter > 0 {
					retrySec = int(tgErr.ResponseParams.RetryAfter)
				}
				slog.WarnContext(ctx, "Telegram SendDocument rate limited (429), waiting and retrying",
					"chat_id", chatID, "retry_after", retrySec, "path", att.FilePath)
				time.Sleep(time.Duration(retrySec) * time.Second)
				if _, seekErr := file.Seek(0, io.SeekStart); seekErr == nil {
					_, docErr = bot.SendDocument(chatID, inputFile, opts)
				}
			} else if docParseMode != "" {
				// Fallback to plain text caption if HTML parse mode fails
				opts.ParseMode = ""
				opts.Caption = plainDocCap
				if _, seekErr := file.Seek(0, io.SeekStart); seekErr == nil {
					_, docErr = bot.SendDocument(chatID, inputFile, opts)
				}
			}
		}
		file.Close()

		if docErr != nil {
			slog.ErrorContext(ctx, "Failed to send document artifact", "chat_id", chatID, "path", att.FilePath, "error", docErr)
			uploadErrors = append(uploadErrors, docErr)
		} else {
			slog.InfoContext(ctx, "Successfully uploaded turn document", "chat_id", chatID, "path", att.FilePath)
		}
	}

	if len(uploadErrors) > 0 {
		return fmt.Errorf("failed to upload %d artifacts: %v", len(uploadErrors), uploadErrors[0])
	}
	return nil
}
