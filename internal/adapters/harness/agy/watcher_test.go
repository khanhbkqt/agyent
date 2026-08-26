package agy_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"agyent/internal/adapters/harness/agy"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSnapshotWatcher_TC_WAT_01_WhitelistArtifacts(t *testing.T) {
	tempDir := t.TempDir()
	watcher := agy.NewSnapshotWatcher()

	beforeSnap, err := watcher.TakeSnapshot(tempDir)
	require.NoError(t, err)
	assert.Empty(t, beforeSnap)

	// Create whitelist files
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "chart.png"), []byte("pngdata"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "report.pdf"), []byte("pdfdata"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "data.csv"), []byte("a,b,c"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "bundle.zip"), []byte("zipdata"), 0644))

	artifacts, err := watcher.DetectArtifacts(tempDir, beforeSnap)
	require.NoError(t, err)
	assert.Len(t, artifacts, 4)

	// Deterministic sorting verification
	assert.Equal(t, "bundle.zip", artifacts[0].FileName)
	assert.Equal(t, "archive", artifacts[0].Type)

	assert.Equal(t, "chart.png", artifacts[1].FileName)
	assert.Equal(t, "image", artifacts[1].Type)

	assert.Equal(t, "data.csv", artifacts[2].FileName)
	assert.Equal(t, "document", artifacts[2].Type)

	assert.Equal(t, "report.pdf", artifacts[3].FileName)
	assert.Equal(t, "document", artifacts[3].Type)
}

func TestSnapshotWatcher_TC_WAT_02_IgnoredSourceCode(t *testing.T) {
	tempDir := t.TempDir()
	watcher := agy.NewSnapshotWatcher()

	beforeSnap, err := watcher.TakeSnapshot(tempDir)
	require.NoError(t, err)

	// Create source code files at root
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "main.go"), []byte("package main"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "script.py"), []byte("print('hi')"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "app.js"), []byte("console.log()"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "README.md"), []byte("# Title"), 0644))

	artifacts, err := watcher.DetectArtifacts(tempDir, beforeSnap)
	require.NoError(t, err)
	assert.Empty(t, artifacts, "source code files should not be detected as uploadable artifacts")
}

func TestSnapshotWatcher_TC_WAT_03_ExcludedDirectoriesPruning(t *testing.T) {
	tempDir := t.TempDir()
	watcher := agy.NewSnapshotWatcher()

	beforeSnap, err := watcher.TakeSnapshot(tempDir)
	require.NoError(t, err)

	// Create excluded directories with case variations
	nodeModules := filepath.Join(tempDir, "Node_Modules")
	gitDir := filepath.Join(tempDir, ".git")
	buildDir := filepath.Join(tempDir, "Build")
	require.NoError(t, os.MkdirAll(nodeModules, 0755))
	require.NoError(t, os.MkdirAll(gitDir, 0755))
	require.NoError(t, os.MkdirAll(buildDir, 0755))

	// Put images inside excluded dirs
	require.NoError(t, os.WriteFile(filepath.Join(nodeModules, "pkg_icon.png"), []byte("data"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "commit_graph.png"), []byte("data"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(buildDir, "output.png"), []byte("data"), 0644))

	artifacts, err := watcher.DetectArtifacts(tempDir, beforeSnap)
	require.NoError(t, err)
	assert.Empty(t, artifacts, "files inside excluded directories should be pruned completely")
}

func TestSnapshotWatcher_TC_WAT_04_SpecialExportsAndOutputFolders(t *testing.T) {
	tempDir := t.TempDir()
	watcher := agy.NewSnapshotWatcher()

	beforeSnap, err := watcher.TakeSnapshot(tempDir)
	require.NoError(t, err)

	exportsDir := filepath.Join(tempDir, "exports")
	outputDir := filepath.Join(tempDir, "OUTPUT")
	require.NoError(t, os.MkdirAll(exportsDir, 0755))
	require.NoError(t, os.MkdirAll(outputDir, 0755))

	// Even custom / raw files inside exports/ or output/ should be detected as artifacts
	require.NoError(t, os.WriteFile(filepath.Join(exportsDir, "data.bin"), []byte("binary data"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "generated_script.sh"), []byte("#!/bin/bash"), 0644))

	artifacts, err := watcher.DetectArtifacts(tempDir, beforeSnap)
	require.NoError(t, err)
	assert.Len(t, artifacts, 2)
	// Sorted by ID: "OUTPUT/generated_script.sh" < "exports/data.bin" (uppercase 'O' precedes lowercase 'e')
	assert.Equal(t, "generated_script.sh", artifacts[0].FileName)
	assert.Equal(t, "document", artifacts[0].Type)
	assert.Equal(t, "data.bin", artifacts[1].FileName)
	assert.Equal(t, "document", artifacts[1].Type)
}

func TestSnapshotWatcher_TC_WAT_05_UnicodeAndSpacesFilenames(t *testing.T) {
	tempDir := t.TempDir()
	watcher := agy.NewSnapshotWatcher()

	beforeSnap, err := watcher.TakeSnapshot(tempDir)
	require.NoError(t, err)

	subDir := filepath.Join(tempDir, "báo cáo tài chính")
	require.NoError(t, os.MkdirAll(subDir, 0755))

	require.NoError(t, os.WriteFile(filepath.Join(subDir, "kết quả kinh doanh 2026.xlsx"), []byte("xlsxdata"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "biểu đồ phân tích.png"), []byte("pngdata"), 0644))

	artifacts, err := watcher.DetectArtifacts(tempDir, beforeSnap)
	require.NoError(t, err)
	assert.Len(t, artifacts, 2)
}

func TestSnapshotWatcher_TC_WAT_06_FileSizeBoundaries(t *testing.T) {
	tempDir := t.TempDir()
	watcher := agy.NewSnapshotWatcher()

	beforeSnap, err := watcher.TakeSnapshot(tempDir)
	require.NoError(t, err)

	// 0-byte file (empty)
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "empty.png"), []byte(""), 0644))

	// Normal 1KB file
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "valid.pdf"), []byte("valid pdf content"), 0644))

	artifacts, err := watcher.DetectArtifacts(tempDir, beforeSnap)
	require.NoError(t, err)
	assert.Len(t, artifacts, 1)
	assert.Equal(t, "valid.pdf", artifacts[0].FileName)
}

func TestSnapshotWatcher_TC_WAT_07_CompoundExtensionTarGz(t *testing.T) {
	tempDir := t.TempDir()
	watcher := agy.NewSnapshotWatcher()

	beforeSnap, err := watcher.TakeSnapshot(tempDir)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "backup.tar.gz"), []byte("tar.gz data"), 0644))

	artifacts, err := watcher.DetectArtifacts(tempDir, beforeSnap)
	require.NoError(t, err)
	require.Len(t, artifacts, 1)
	assert.Equal(t, "backup.tar.gz", artifacts[0].FileName)
	assert.Equal(t, "archive", artifacts[0].Type)
	assert.Equal(t, "application/gzip", artifacts[0].MIMEType)
}

func TestSnapshotWatcher_TC_WAT_08_RootDirNamedExcluded(t *testing.T) {
	// If rootDir itself is named "build" or "bin", it must not be pruned
	parentDir := t.TempDir()
	buildRootDir := filepath.Join(parentDir, "build")
	require.NoError(t, os.MkdirAll(buildRootDir, 0755))

	watcher := agy.NewSnapshotWatcher()
	beforeSnap, err := watcher.TakeSnapshot(buildRootDir)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(buildRootDir, "metric.png"), []byte("data"), 0644))

	artifacts, err := watcher.DetectArtifacts(buildRootDir, beforeSnap)
	require.NoError(t, err)
	require.Len(t, artifacts, 1)
	assert.Equal(t, "metric.png", artifacts[0].FileName)
}

func TestSnapshotWatcher_ModifiedExistingFile(t *testing.T) {
	tempDir := t.TempDir()
	watcher := agy.NewSnapshotWatcher()

	imgFile := filepath.Join(tempDir, "dynamic_chart.png")
	require.NoError(t, os.WriteFile(imgFile, []byte("initial data"), 0644))

	beforeSnap, err := watcher.TakeSnapshot(tempDir)
	require.NoError(t, err)
	assert.Len(t, beforeSnap, 1)

	time.Sleep(10 * time.Millisecond)

	// Modify existing file content and size
	require.NoError(t, os.WriteFile(imgFile, []byte("updated larger dynamic chart data"), 0644))

	artifacts, err := watcher.DetectArtifacts(tempDir, beforeSnap)
	require.NoError(t, err)
	require.Len(t, artifacts, 1)
	assert.Equal(t, "dynamic_chart.png", artifacts[0].FileName)
}
