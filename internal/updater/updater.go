package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const (
	DefaultRepo = "khanhbkqt/agyent"
)

// GitHubAsset models an asset attached to a GitHub release.
type GitHubAsset struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`
	ContentType        string `json:"content_type"`
}

// GitHubRelease models a GitHub release response.
type GitHubRelease struct {
	ID          int64         `json:"id"`
	TagName     string        `json:"tag_name"`
	Name        string        `json:"name"`
	Body        string        `json:"body"`
	Draft       bool          `json:"draft"`
	Prerelease  bool          `json:"prerelease"`
	PublishedAt time.Time     `json:"published_at"`
	HTMLURL     string        `json:"html_url"`
	Assets      []GitHubAsset `json:"assets"`
}

// Options configures the update operation.
type Options struct {
	Repository     string
	TargetVersion  string // "latest" or specific tag (e.g. "v1.0.0")
	CurrentVersion string
	Force          bool
	DryRun         bool
	ExecutablePath string
	HTTPClient     *http.Client
	APIBaseURL     string // Defaults to https://api.github.com
}

// Result describes the outcome of an update execution.
type Result struct {
	CurrentVersion string
	TargetVersion  string
	IsNewer        bool
	Updated        bool
	ReleaseNotes   string
	ReleaseURL     string
	AssetFilename  string
	ExecutablePath string
}

// FetchRelease gets the release metadata from GitHub for "latest" or a specific tag.
func FetchRelease(ctx context.Context, apiBaseURL, repo, version string, client *http.Client) (*GitHubRelease, error) {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	if apiBaseURL == "" {
		apiBaseURL = "https://api.github.com"
	}
	apiBaseURL = strings.TrimRight(apiBaseURL, "/")

	var reqURL string
	if version == "" || version == "latest" {
		reqURL = fmt.Sprintf("%s/repos/%s/releases/latest", apiBaseURL, repo)
	} else {
		tag := version
		if !strings.HasPrefix(tag, "v") && !strings.Contains(tag, ".") {
			tag = "v" + tag
		}
		reqURL = fmt.Sprintf("%s/repos/%s/releases/tags/%s", apiBaseURL, repo, tag)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build release request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "agyent-updater")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to query GitHub releases: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("github API returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("failed to decode release payload: %w", err)
	}

	return &release, nil
}

// FindPlatformAsset matches release assets with the current OS and CPU architecture.
func FindPlatformAsset(assets []GitHubAsset, goos, goarch string) (*GitHubAsset, error) {
	targetPlatform := fmt.Sprintf("%s-%s", goos, goarch)
	for _, a := range assets {
		name := strings.ToLower(a.Name)
		if strings.Contains(name, targetPlatform) {
			return &a, nil
		}
	}

	// Fallback check
	for _, a := range assets {
		name := strings.ToLower(a.Name)
		if strings.Contains(name, goos) && strings.Contains(name, goarch) {
			return &a, nil
		}
	}

	return nil, fmt.Errorf("no release asset found matching platform '%s-%s'", goos, goarch)
}

// DownloadAsset downloads the binary/archive from downloadURL.
func DownloadAsset(ctx context.Context, downloadURL string, client *http.Client) ([]byte, error) {
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build download request: %w", err)
	}
	req.Header.Set("User-Agent", "agyent-updater")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

// CheckOnly checks if an update is available without downloading or modifying files.
func CheckOnly(ctx context.Context, opts Options) (*Result, error) {
	repo := opts.Repository
	if repo == "" {
		repo = DefaultRepo
	}

	rel, err := FetchRelease(ctx, opts.APIBaseURL, repo, opts.TargetVersion, opts.HTTPClient)
	if err != nil {
		return nil, err
	}

	isNewer := IsNewerVersion(rel.TagName, opts.CurrentVersion)
	return &Result{
		CurrentVersion: opts.CurrentVersion,
		TargetVersion:  rel.TagName,
		IsNewer:        isNewer,
		Updated:        false,
		ReleaseNotes:   rel.Body,
		ReleaseURL:     rel.HTMLURL,
	}, nil
}

// Execute performs the self-update operation.
func Execute(ctx context.Context, opts Options) (*Result, error) {
	repo := opts.Repository
	if repo == "" {
		repo = DefaultRepo
	}

	exePath := opts.ExecutablePath
	if exePath == "" {
		var err error
		exePath, err = os.Executable()
		if err != nil {
			return nil, fmt.Errorf("failed to resolve current executable path: %w", err)
		}
	}

	// 1. Fetch release info
	rel, err := FetchRelease(ctx, opts.APIBaseURL, repo, opts.TargetVersion, opts.HTTPClient)
	if err != nil {
		return nil, err
	}

	isNewer := IsNewerVersion(rel.TagName, opts.CurrentVersion)
	if !isNewer && !opts.Force && (opts.TargetVersion == "" || opts.TargetVersion == "latest") {
		return &Result{
			CurrentVersion: opts.CurrentVersion,
			TargetVersion:  rel.TagName,
			IsNewer:        false,
			Updated:        false,
			ReleaseNotes:   rel.Body,
			ReleaseURL:     rel.HTMLURL,
			ExecutablePath: exePath,
		}, nil
	}

	// 2. Find asset for current OS/Arch
	asset, err := FindPlatformAsset(rel.Assets, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return nil, err
	}

	if opts.DryRun {
		return &Result{
			CurrentVersion: opts.CurrentVersion,
			TargetVersion:  rel.TagName,
			IsNewer:        isNewer,
			Updated:        false,
			ReleaseNotes:   rel.Body,
			ReleaseURL:     rel.HTMLURL,
			AssetFilename:  asset.Name,
			ExecutablePath: exePath,
		}, nil
	}

	// 3. Download asset
	assetBytes, err := DownloadAsset(ctx, asset.BrowserDownloadURL, opts.HTTPClient)
	if err != nil {
		return nil, fmt.Errorf("failed to download release asset %s: %w", asset.Name, err)
	}

	// 4. Extract executable from archive
	var binaryBytes []byte
	binaryBase := "agyent"
	if strings.HasSuffix(asset.Name, ".zip") {
		binaryBytes, err = ExtractBinaryFromZip(assetBytes, binaryBase)
	} else if strings.HasSuffix(asset.Name, ".tar.gz") || strings.HasSuffix(asset.Name, ".tgz") {
		binaryBytes, err = ExtractBinaryFromTarGz(assetBytes, binaryBase)
	} else {
		// Standalone raw binary
		binaryBytes = assetBytes
	}

	if err != nil {
		return nil, fmt.Errorf("failed to extract binary from release archive: %w", err)
	}

	if len(binaryBytes) == 0 {
		return nil, errors.New("extracted binary is empty")
	}

	// 5. Replace current binary
	if err := ReplaceExecutable(binaryBytes, exePath); err != nil {
		return nil, fmt.Errorf("failed to replace executable at %s: %w", exePath, err)
	}

	// 6. Verify replaced binary
	verCmd := exec.CommandContext(ctx, exePath, "version")
	_ = verCmd.Run()

	return &Result{
		CurrentVersion: opts.CurrentVersion,
		TargetVersion:  rel.TagName,
		IsNewer:        isNewer,
		Updated:        true,
		ReleaseNotes:   rel.Body,
		ReleaseURL:     rel.HTMLURL,
		AssetFilename:  asset.Name,
		ExecutablePath: exePath,
	}, nil
}
