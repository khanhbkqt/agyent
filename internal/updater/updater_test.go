package updater

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVersion_ParsingAndComparison(t *testing.T) {
	tests := []struct {
		remote  string
		current string
		isNewer bool
	}{
		{"v1.0.1", "v1.0.0", true},
		{"1.1.0", "1.0.9", true},
		{"v2.0.0", "v1.9.9", true},
		{"v1.0.0", "v1.0.0", false},
		{"v0.9.0", "v1.0.0", false},
		{"v1.0.0", "v1.0.0-dev", true},
		{"1.0.0", "0.1.0-dev", true},
	}

	for _, tt := range tests {
		t.Run(tt.remote+"_vs_"+tt.current, func(t *testing.T) {
			assert.Equal(t, tt.isNewer, IsNewerVersion(tt.remote, tt.current))
		})
	}
}

func TestExtract_Zip(t *testing.T) {
	// Create sample zip in memory
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	binWriter, err := zw.Create("agyent.exe")
	require.NoError(t, err)
	_, err = binWriter.Write([]byte("dummy binary content"))
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	extracted, err := ExtractBinaryFromZip(buf.Bytes(), "agyent")
	require.NoError(t, err)
	assert.Equal(t, "dummy binary content", string(extracted))
}

func TestReplaceExecutable(t *testing.T) {
	tempDir := t.TempDir()
	targetPath := filepath.Join(tempDir, "agyent_test.exe")

	// Write initial file
	err := os.WriteFile(targetPath, []byte("old binary"), 0755)
	require.NoError(t, err)

	// Replace
	err = ReplaceExecutable([]byte("new binary content"), targetPath)
	require.NoError(t, err)

	// Verify
	content, err := os.ReadFile(targetPath)
	require.NoError(t, err)
	assert.Equal(t, "new binary content", string(content))
}

func TestUpdater_EndToEndMock(t *testing.T) {
	// Build mock zip archive
	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)
	binWriter, err := zw.Create("agyent.exe")
	require.NoError(t, err)
	_, err = binWriter.Write([]byte("mock binary version 2.0.0"))
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	// Start Mock HTTP Server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/khanhbkqt/agyent/releases/latest" {
			rel := GitHubRelease{
				TagName:     "v2.0.0",
				Name:        "agyent v2.0.0 Release",
				Body:        "## What's New in v2.0.0\n- New doctor command\n- New self-updater",
				PublishedAt: time.Now(),
				HTMLURL:     "https://github.com/khanhbkqt/agyent/releases/tag/v2.0.0",
				Assets: []GitHubAsset{
					{
						Name:               "agyent-v2.0.0-" + runtime.GOOS + "-" + runtime.GOARCH + ".zip",
						Size:               int64(zipBuf.Len()),
						BrowserDownloadURL: "http://" + r.Host + "/download/archive.zip",
					},
				},
			}
			_ = json.NewEncoder(w).Encode(rel)
			return
		}

		if r.URL.Path == "/download/archive.zip" {
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(zipBuf.Bytes())
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	tempDir := t.TempDir()
	targetExe := filepath.Join(tempDir, "agyent.exe")
	_ = os.WriteFile(targetExe, []byte("version 1.0.0 binary"), 0755)

	opts := Options{
		Repository:     "khanhbkqt/agyent",
		CurrentVersion: "v1.0.0",
		TargetVersion:  "latest",
		ExecutablePath: targetExe,
		APIBaseURL:     server.URL,
		HTTPClient:     server.Client(),
	}

	ctx := context.Background()

	// 1. Test CheckOnly
	checkRes, err := CheckOnly(ctx, opts)
	require.NoError(t, err)
	assert.True(t, checkRes.IsNewer)
	assert.Equal(t, "v2.0.0", checkRes.TargetVersion)

	// 2. Test DryRun
	opts.DryRun = true
	dryRes, err := Execute(ctx, opts)
	require.NoError(t, err)
	assert.False(t, dryRes.Updated)

	// 3. Test Full Update
	opts.DryRun = false
	upRes, err := Execute(ctx, opts)
	require.NoError(t, err)
	assert.True(t, upRes.Updated)

	// Verify file content replaced
	data, err := os.ReadFile(targetExe)
	require.NoError(t, err)
	assert.Equal(t, "mock binary version 2.0.0", string(data))
}
