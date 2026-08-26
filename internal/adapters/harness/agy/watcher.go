package agy

import (
	"io/fs"
	"mime"
	"path/filepath"
	"sort"
	"strings"

	"agyent/internal/core/domain"
)

const (
	// MaxArtifactSizeBytes limits artifact auto-upload size to 50MB (Telegram Bot API limit).
	MaxArtifactSizeBytes int64 = 50 * 1024 * 1024
)

var excludedDirMap = map[string]bool{
	".git":         true,
	"node_modules": true,
	".venv":        true,
	"venv":         true,
	"vendor":       true,
	".gemini":      true,
	"dist":         true,
	"build":        true,
	"bin":          true,
	"obj":          true,
	".agyent":      true,
	"__pycache__":  true,
	".idea":        true,
	".vscode":      true,
	".cache":       true,
}

var allowedExtMap = map[string]string{
	// Images / Charts
	".png": "image", ".jpg": "image", ".jpeg": "image", ".svg": "image", ".webp": "image", ".gif": "image",
	// Documents / Reports / Data
	".pdf": "document", ".csv": "document", ".xlsx": "document", ".docx": "document", ".json": "document",
	// Archives
	".zip": "archive", ".tar.gz": "archive", ".tar": "archive", ".gz": "archive", ".7z": "archive",
	// Media
	".mp4": "video", ".mp3": "audio", ".wav": "audio",
}

type fileEntry struct {
	ModTimeUnixMs int64
	Size          int64
}

// Snapshot stores relative file paths mapped to modification timestamp and file size.
type Snapshot map[string]fileEntry

// SnapshotWatcher handles directory snapshots and artifacts diffing with whitelist filters.
type SnapshotWatcher struct{}

// NewSnapshotWatcher creates a new SnapshotWatcher instance.
func NewSnapshotWatcher() *SnapshotWatcher {
	return &SnapshotWatcher{}
}

// TakeSnapshot records metadata for all valid files in rootDir, pruning heavy directories immediately.
func (w *SnapshotWatcher) TakeSnapshot(rootDir string) (Snapshot, error) {
	snap := make(Snapshot)
	if rootDir == "" {
		return snap, nil
	}

	cleanRoot := filepath.Clean(rootDir)

	err := filepath.WalkDir(cleanRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Skip files or directories with permission errors gracefully
			return nil
		}

		if d.IsDir() {
			// Do not prune if it is the root directory itself
			if path != cleanRoot && excludedDirMap[strings.ToLower(d.Name())] {
				return filepath.SkipDir
			}
			return nil
		}

		relPath, err := filepath.Rel(cleanRoot, path)
		if err != nil {
			return nil
		}

		normalizedRel := filepath.ToSlash(relPath)
		ext := getFileExtension(relPath)
		isSpecialDir := strings.HasPrefix(strings.ToLower(normalizedRel), "exports/") ||
			strings.HasPrefix(strings.ToLower(normalizedRel), "output/")
		_, isAllowedExt := allowedExtMap[ext]

		// EARLY FILTER: Skip calling d.Info() for non-artifact extensions outside special directories
		if !isAllowedExt && !isSpecialDir {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		snap[relPath] = fileEntry{
			ModTimeUnixMs: info.ModTime().UnixMilli(),
			Size:          info.Size(),
		}
		return nil
	})

	return snap, err
}

// getFileExtension extracts the file extension, supporting compound extensions like .tar.gz.
func getFileExtension(path string) string {
	lower := strings.ToLower(path)
	if strings.HasSuffix(lower, ".tar.gz") {
		return ".tar.gz"
	}
	return filepath.Ext(lower)
}

// DetectArtifacts compares the post-execution workspace against before Snapshot and returns newly generated/modified artifacts.
func (w *SnapshotWatcher) DetectArtifacts(rootDir string, before Snapshot) ([]domain.Attachment, error) {
	after, err := w.TakeSnapshot(rootDir)
	if err != nil {
		return nil, err
	}

	var artifacts []domain.Attachment

	for relPath, currentMeta := range after {
		oldMeta, exists := before[relPath]
		isNew := !exists
		isModified := exists && (currentMeta.ModTimeUnixMs > oldMeta.ModTimeUnixMs || currentMeta.Size != oldMeta.Size)

		if !isNew && !isModified {
			continue
		}

		// Reject empty files or files exceeding the 50MB ceiling
		if currentMeta.Size <= 0 || currentMeta.Size > MaxArtifactSizeBytes {
			continue
		}

		normalizedRel := filepath.ToSlash(relPath)
		ext := getFileExtension(relPath)

		isSpecialDir := strings.HasPrefix(strings.ToLower(normalizedRel), "exports/") || strings.HasPrefix(strings.ToLower(normalizedRel), "output/")
		mediaCategory, isAllowedExt := allowedExtMap[ext]

		// Skip source code files unless generated within dedicated exports/ or output/ folders
		if !isAllowedExt && !isSpecialDir {
			continue
		}

		if mediaCategory == "" {
			mediaCategory = "document"
		}

		mimeType := mime.TypeByExtension(ext)
		if mimeType == "" {
			if ext == ".tar.gz" {
				mimeType = "application/gzip"
			} else {
				mimeType = "application/octet-stream"
			}
		}

		artifacts = append(artifacts, domain.Attachment{
			ID:       normalizedRel,
			FileName: filepath.Base(relPath),
			FilePath: filepath.Join(rootDir, relPath),
			MIMEType: mimeType,
			Size:     currentMeta.Size,
			Type:     mediaCategory,
		})
	}

	// Deterministic sorting by ID (relative path)
	sort.Slice(artifacts, func(i, j int) bool {
		return artifacts[i].ID < artifacts[j].ID
	})

	return artifacts, nil
}
