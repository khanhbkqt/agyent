package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestEngine_JanitorPass(t *testing.T) {
	eng := &Engine{}
	ctx := context.Background()

	// 1. Run janitor pass safely without panic
	eng.runJanitorPass(ctx)

	// 2. Test mock lock file cleanup
	homeDir, err := os.UserHomeDir()
	if err == nil {
		testLockDir := filepath.Join(homeDir, ".gemini", "antigravity-cli", "presence")
		_ = os.MkdirAll(testLockDir, 0755)
		testLockFile := filepath.Join(testLockDir, "dummy_janitor_test.lock")
		_ = os.WriteFile(testLockFile, []byte(""), 0644)
		defer os.Remove(testLockFile)

		// Set mod time to 25 hours ago to satisfy 24h safe rule
		oldTime := time.Now().Add(-25 * time.Hour)
		_ = os.Chtimes(testLockFile, oldTime, oldTime)

		// Run janitor pass
		eng.runJanitorPass(ctx)

		// Assert dummy lock file was purged
		assert.NoFileExists(t, testLockFile)
	}
}
