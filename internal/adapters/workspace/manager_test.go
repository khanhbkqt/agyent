package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"agyent/internal/core/domain"
)

func TestEnsureWorkspaceUploadsDir(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(nil)

	uploadsDir, err := mgr.EnsureWorkspaceUploadsDir(tmpDir)
	require.NoError(t, err)
	assert.DirExists(t, uploadsDir)
	assert.Equal(t, filepath.Join(tmpDir, "uploads"), uploadsDir)

	gitignorePath := filepath.Join(uploadsDir, ".gitignore")
	assert.FileExists(t, gitignorePath)

	data, err := os.ReadFile(gitignorePath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "*")
	assert.Contains(t, string(data), "!.gitignore")

	// Idempotent call
	uploadsDir2, err := mgr.EnsureWorkspaceUploadsDir(tmpDir)
	require.NoError(t, err)
	assert.Equal(t, uploadsDir, uploadsDir2)
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"report.pdf", "report.pdf"},
		{"my photo 123.jpg", "my photo 123.jpg"},
		{"../../etc/passwd", "passwd"},
		{"..\\..\\windows\\system32\\cmd.exe", "cmd.exe"},
		{"/var/log/syslog", "syslog"},
		{"CON.txt", "safe_CON.txt"},
		{"prn.log", "safe_prn.log"},
		{"aux", "safe_aux"},
		{"nul.json", "safe_nul.json"},
		{"COM1.png", "safe_COM1.png"},
		{"LPT2.dat", "safe_LPT2.dat"},
		{"", "file"},
		{"   ", "file"},
		{"... ", "file"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			actual := SanitizeFilename(tc.input)
			assert.Equal(t, tc.expected, actual)
		})
	}
}

func TestPrepareInboundAttachments(t *testing.T) {
	stagingDir := t.TempDir()
	workspaceDir := t.TempDir()
	mgr := NewManager(nil)

	// Create test files in staging
	srcFile1 := filepath.Join(stagingDir, "doc.pdf")
	err := os.WriteFile(srcFile1, []byte("test pdf content"), 0644)
	require.NoError(t, err)

	srcFile2 := filepath.Join(stagingDir, "photo.jpg")
	err = os.WriteFile(srcFile2, []byte("test image data 12345"), 0644)
	require.NoError(t, err)

	atts := []domain.Attachment{
		{
			ID:       "att-1",
			FileName: "doc.pdf",
			FilePath: srcFile1,
			Type:     "document",
			MIMEType: "application/pdf",
		},
		{
			ID:       "att-2",
			FileName: "photo.jpg",
			FilePath: srcFile2,
			Type:     "image",
			MIMEType: "image/jpeg",
		},
	}

	prepared, err := mgr.PrepareInboundAttachments(context.Background(), workspaceDir, atts)
	require.NoError(t, err)
	require.Len(t, prepared, 2)

	// Verify attachments moved into workspaceDir/uploads/
	for _, p := range prepared {
		assert.True(t, strings.HasPrefix(p.FilePath, filepath.Join(workspaceDir, "uploads")), "filepath should be inside workspace uploads dir")
		assert.FileExists(t, p.FilePath)
		assert.Greater(t, p.Size, int64(0))
	}

	// Verify original staging files are removed (moved)
	assert.NoFileExists(t, srcFile1)
	assert.NoFileExists(t, srcFile2)

	// Calling again with already relocated attachments should be a no-op
	prepared2, err := mgr.PrepareInboundAttachments(context.Background(), workspaceDir, prepared)
	require.NoError(t, err)
	assert.Equal(t, prepared, prepared2)
}
