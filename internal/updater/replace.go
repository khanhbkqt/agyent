package updater

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// ReplaceExecutable safely replaces the executable at targetPath with newBinaryBytes.
func ReplaceExecutable(newBinaryBytes []byte, targetPath string) error {
	absPath, err := filepath.Abs(targetPath)
	if err != nil {
		return fmt.Errorf("failed to resolve absolute target path %s: %w", targetPath, err)
	}

	targetDir := filepath.Dir(absPath)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", targetDir, err)
	}

	if runtime.GOOS == "windows" {
		return replaceWindows(newBinaryBytes, absPath)
	}

	return replaceUnix(newBinaryBytes, absPath)
}

func replaceWindows(newBinaryBytes []byte, targetPath string) error {
	// If file exists, move it aside because Windows locks active binaries
	if _, err := os.Stat(targetPath); err == nil {
		oldPath := fmt.Sprintf("%s.old.%d", targetPath, time.Now().UnixNano())
		if err := os.Rename(targetPath, oldPath); err != nil {
			if os.IsPermission(err) {
				return fmt.Errorf("permission denied moving existing binary at %s. Please run terminal as Administrator: %w", targetPath, err)
			}
			return fmt.Errorf("failed to move existing binary out of the way: %w", err)
		}
		// Try to delete old file, ignore error if locked
		_ = os.Remove(oldPath)
	}

	// Write new binary directly
	if err := os.WriteFile(targetPath, newBinaryBytes, 0755); err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("permission denied writing to %s. Please run terminal as Administrator: %w", targetPath, err)
		}
		return fmt.Errorf("failed to write new binary to %s: %w", targetPath, err)
	}

	return nil
}

func replaceUnix(newBinaryBytes []byte, targetPath string) error {
	targetDir := filepath.Dir(targetPath)
	tmpFile := filepath.Join(targetDir, fmt.Sprintf(".agyent_update_%d.tmp", time.Now().UnixNano()))

	if err := os.WriteFile(tmpFile, newBinaryBytes, 0755); err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("permission denied writing to %s. If agyent is installed in a system directory (e.g. /usr/local/bin), try running: 'sudo agyent update': %w", targetDir, err)
		}
		return fmt.Errorf("failed to write temporary binary: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpFile, targetPath); err != nil {
		_ = os.Remove(tmpFile)
		if os.IsPermission(err) {
			return fmt.Errorf("permission denied replacing %s. If agyent is installed in a system directory (e.g. /usr/local/bin), try running: 'sudo agyent update': %w", targetPath, err)
		}
		return fmt.Errorf("failed to atomically replace binary: %w", err)
	}

	return nil
}
