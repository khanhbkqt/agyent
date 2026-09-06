package zalo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"agyent/internal/core/domain"
	"agyent/internal/core/ports"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type denyingURLSafetyEvaluator struct{}

func (denyingURLSafetyEvaluator) EvaluateURL(context.Context, string) (domain.SecurityDecision, error) {
	return domain.SecurityDecision{Decision: domain.DecisionDeny}, nil
}

func TestMediaManager_RejectsNetworkPolicyDeniedURLBeforeFetch(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	manager := &MediaManager{
		stagingDir: t.TempDir(),
		httpClient: server.Client(),
	}
	manager.SetURLSafetyEvaluator(denyingURLSafetyEvaluator{})

	_, err := manager.DownloadInboundAttachment(context.Background(), server.URL+"/attachment", "image.jpg")
	require.Error(t, err)
	assert.ErrorIs(t, err, ports.ErrAttachmentNotFound)
	assert.Zero(t, requests.Load())
}

func TestSanitizeFilename(t *testing.T) {
	assert.Equal(t, "test_file.png", SanitizeFilename("../../test_file.png"))
	assert.Equal(t, "report.pdf", SanitizeFilename("report.pdf"))
	assert.Equal(t, "image.jpg", SanitizeFilename("image\x00.jpg"))
}

func TestExtractAndCleanOutboundMedia_ImagesAndDocs(t *testing.T) {
	tmpDir := t.TempDir()
	imgFile := filepath.Join(tmpDir, "diagram.png")
	docFile := filepath.Join(tmpDir, "spec.pdf")
	require.NoError(t, os.WriteFile(imgFile, []byte("fake-png-content"), 0644))
	require.NoError(t, os.WriteFile(docFile, []byte("fake-pdf-content"), 0644))

	input := `Here is the architecture:
![Architecture Diagram](` + imgFile + `)
<!-- slide -->
And here is the specification:
[Download Spec](` + docFile + `)
Also check this doc which should not be extracted:
[Project README](file:///Users/khanhnguyen/Projects/agyent/README.md)`

	cleaned, atts := ExtractAndCleanOutboundMedia(input, tmpDir, "")
	assert.NotContains(t, cleaned, "![Architecture Diagram]")
	assert.NotContains(t, cleaned, "<!-- slide -->")
	assert.Contains(t, cleaned, "📄 Download Spec")
	assert.Contains(t, cleaned, "[Project README]")
	assert.Len(t, atts, 2)

	assert.Equal(t, "image", atts[0].Type)
	assert.Equal(t, "diagram.png", atts[0].FileName)
	assert.Equal(t, "Architecture Diagram", atts[0].Caption)

	assert.Equal(t, "document", atts[1].Type)
	assert.Equal(t, "spec.pdf", atts[1].FileName)
	assert.Equal(t, "Download Spec", atts[1].Caption)
}

func TestExtractAndCleanOutboundMedia_PublicURLImage(t *testing.T) {
	input := `Visual result:
![Cat Picture](https://example.com/images/cat.jpg)
Enjoy!`

	cleaned, atts := ExtractAndCleanOutboundMedia(input, "", "")
	assert.NotContains(t, cleaned, "![Cat Picture]")
	assert.Contains(t, cleaned, "Visual result:")
	assert.Contains(t, cleaned, "Enjoy!")
	require.Len(t, atts, 1)
	assert.Equal(t, "https://example.com/images/cat.jpg", atts[0].FilePath)
	assert.Equal(t, "Cat Picture", atts[0].Caption)
	assert.Equal(t, "image", atts[0].Type)
}

func TestUploadPublicMedia_TestHookAndErrors(t *testing.T) {
	ctx := context.Background()

	// 1. Direct HTTPS URL
	u, err := UploadPublicMedia(ctx, "https://files.catbox.moe/test.png")
	require.NoError(t, err)
	assert.Equal(t, "https://files.catbox.moe/test.png", u)

	// 2. Empty path
	_, err = UploadPublicMedia(ctx, "")
	require.Error(t, err)

	// 3. Test hook override
	tmpFile := filepath.Join(t.TempDir(), "dummy.txt")
	require.NoError(t, os.WriteFile(tmpFile, []byte("hello"), 0644))

	restore := SetPublicMediaUploaderForTest(func(ctx context.Context, filePath string) (string, error) {
		return "https://custom-cdn.local/" + filepath.Base(filePath), nil
	})
	defer restore()

	u, err = UploadPublicMedia(ctx, tmpFile)
	require.NoError(t, err)
	assert.Equal(t, "https://custom-cdn.local/dummy.txt", u)
}

func TestUploadPublicMedia_FallbackChain(t *testing.T) {
	tmpDir := t.TempDir()
	imgFile := filepath.Join(tmpDir, "photo.jpg")
	require.NoError(t, os.WriteFile(imgFile, []byte("fake-photo-bytes"), 0644))

	var freeImageCalled, uguuCalled, catboxCalled, litterboxCalled atomic.Bool
	var freeImageFail, uguuFail, catboxFail atomic.Bool

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/freeimage":
			freeImageCalled.Store(true)
			if freeImageFail.Load() {
				http.Error(w, "server error", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status_code": 200,
				"image": map[string]string{
					"url": "https://iili.io/mock_photo.jpg",
				},
			})
		case "/uguu":
			uguuCalled.Store(true)
			if uguuFail.Load() {
				http.Error(w, "service down", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"files": []map[string]string{
					{"url": "https://n.uguu.se/mock_photo.jpg"},
				},
			})
		case "/catbox":
			catboxCalled.Store(true)
			if catboxFail.Load() {
				http.Error(w, "412 Invalid uploader", http.StatusPreconditionFailed)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("https://files.catbox.moe/mock_photo.jpg"))
		case "/litterbox":
			litterboxCalled.Store(true)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("https://litter.catbox.moe/mock_photo.jpg"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	origFreeImage := freeImageHostAPIURL
	origUguu := uguuAPIURL
	origCatbox := catboxAPIURL
	origLitterbox := litterboxAPIURL
	defer func() {
		freeImageHostAPIURL = origFreeImage
		uguuAPIURL = origUguu
		catboxAPIURL = origCatbox
		litterboxAPIURL = origLitterbox
	}()

	freeImageHostAPIURL = ts.URL + "/freeimage"
	uguuAPIURL = ts.URL + "/uguu"
	catboxAPIURL = ts.URL + "/catbox"
	litterboxAPIURL = ts.URL + "/litterbox"

	ctx := context.Background()

	// 1. First provider (FreeImage) succeeds
	u, err := UploadPublicMedia(ctx, imgFile)
	require.NoError(t, err)
	assert.Equal(t, "https://iili.io/mock_photo.jpg", u)
	assert.True(t, freeImageCalled.Load())
	assert.False(t, uguuCalled.Load())

	// 2. FreeImage fails -> falls back to Uguu
	freeImageFail.Store(true)
	freeImageCalled.Store(false)
	uguuCalled.Store(false)

	u, err = UploadPublicMedia(ctx, imgFile)
	require.NoError(t, err)
	assert.Equal(t, "https://n.uguu.se/mock_photo.jpg", u)
	assert.True(t, freeImageCalled.Load())
	assert.True(t, uguuCalled.Load())
	assert.False(t, catboxCalled.Load())

	// 3. FreeImage and Uguu fail -> falls back to Catbox
	uguuFail.Store(true)
	freeImageCalled.Store(false)
	uguuCalled.Store(false)
	catboxCalled.Store(false)

	u, err = UploadPublicMedia(ctx, imgFile)
	require.NoError(t, err)
	assert.Equal(t, "https://files.catbox.moe/mock_photo.jpg", u)
	assert.True(t, catboxCalled.Load())
	assert.False(t, litterboxCalled.Load())

	// 4. FreeImage, Uguu, and Catbox (412) fail -> falls back to Litterbox
	catboxFail.Store(true)
	litterboxCalled.Store(false)

	u, err = UploadPublicMedia(ctx, imgFile)
	require.NoError(t, err)
	assert.Equal(t, "https://litter.catbox.moe/mock_photo.jpg", u)
	assert.True(t, litterboxCalled.Load())
}

type allowingURLSafetyEvaluator struct{}

func (allowingURLSafetyEvaluator) EvaluateURL(context.Context, string) (domain.SecurityDecision, error) {
	return domain.SecurityDecision{Decision: domain.DecisionAllow}, nil
}

func TestMediaManager_DownloadInboundAttachment_InfersExtensionFromContentType(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("fake-png-payload"))
	}))
	defer server.Close()

	manager := &MediaManager{
		stagingDir: t.TempDir(),
		httpClient: server.Client(),
	}
	manager.SetURLSafetyEvaluator(allowingURLSafetyEvaluator{})

	filePath, err := manager.DownloadInboundAttachment(context.Background(), server.URL+"/raw-photo", "photo_without_ext")
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(filePath, ".png"))

	content, err := os.ReadFile(filePath)
	require.NoError(t, err)
	assert.Equal(t, []byte("fake-png-payload"), content)
}
