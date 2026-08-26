package telegram

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"agyent/internal/config"
	"agyent/internal/core/domain"
)

// Allowed artifact extensions for automatic outbound upload
var allowedArtifactExts = map[string]bool{
	".png":    true,
	".jpg":    true,
	".jpeg":   true,
	".gif":    true,
	".webp":   true,
	".pdf":    true,
	".zip":    true,
	".tar.gz": true,
	".csv":    true,
	".xlsx":   true,
	".json":   true,
	".txt":    true,
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

// SanitizeFilename cleans filename to prevent path traversal attacks.
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
	return cleaned
}

// DownloadInboundMedia extracts and downloads media files from a Telegram message.
func (m *MediaManager) DownloadInboundMedia(ctx context.Context, msg *gotgbot.Message) ([]domain.Attachment, error) {
	if msg == nil || m.bot == nil {
		return nil, nil
	}

	var attachments []domain.Attachment
	targetDir := filepath.Join(m.cfg.Storage.AgentsDir, "uploads")
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create upload directory: %w", err)
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
						(strings.HasSuffix(name, ".jpg") || strings.HasSuffix(name, ".png") || strings.HasSuffix(name, ".jpeg")) {
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
				if strings.HasSuffix(name, ".jpg") || strings.HasSuffix(name, ".png") || strings.HasSuffix(name, ".jpeg") {
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

// UploadTurnArtifacts sends whitelisted outbound artifacts after turn completion.
func (m *MediaManager) UploadTurnArtifacts(ctx context.Context, chatID int64, threadID int64, artifacts []domain.Attachment) error {
	if len(artifacts) == 0 || m.bot == nil {
		return nil
	}

	for _, att := range artifacts {
		if att.FilePath == "" {
			continue
		}

		ext := strings.ToLower(filepath.Ext(att.FilePath))
		if !allowedArtifactExts[ext] {
			continue
		}

		file, err := os.Open(att.FilePath)
		if err != nil {
			continue
		}

		inputFile := &gotgbot.FileReader{Name: att.FileName, Data: file}

		if ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".webp" || ext == ".gif" {
			opts := &gotgbot.SendPhotoOpts{
				Caption: att.FileName,
			}
			if threadID != 0 {
				opts.MessageThreadId = threadID
			}
			_, _ = m.bot.SendPhoto(chatID, inputFile, opts)
		} else {
			opts := &gotgbot.SendDocumentOpts{
				Caption: att.FileName,
			}
			if threadID != 0 {
				opts.MessageThreadId = threadID
			}
			_, _ = m.bot.SendDocument(chatID, inputFile, opts)
		}
		file.Close()
	}

	return nil
}
