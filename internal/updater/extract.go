package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// ExtractBinaryFromZip searches for binaryName within a zip archive and returns its content bytes.
func ExtractBinaryFromZip(zipBytes []byte, binaryName string) ([]byte, error) {
	r, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, fmt.Errorf("failed to read zip archive: %w", err)
	}

	targetName := strings.ToLower(binaryName)
	for _, f := range r.File {
		base := strings.ToLower(filepath.Base(f.Name))
		if base == targetName || base == targetName+".exe" {
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("failed to open zip file entry %s: %w", f.Name, err)
			}
			defer rc.Close()

			return io.ReadAll(rc)
		}
	}

	return nil, fmt.Errorf("binary %q not found in zip archive", binaryName)
}

// ExtractBinaryFromTarGz searches for binaryName within a .tar.gz archive and returns its content bytes.
func ExtractBinaryFromTarGz(tarGzBytes []byte, binaryName string) ([]byte, error) {
	gzr, err := gzip.NewReader(bytes.NewReader(tarGzBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to read gzip stream: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	targetName := strings.ToLower(binaryName)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed reading tar entry: %w", err)
		}

		if header.Typeflag == tar.TypeReg || header.Typeflag == tar.TypeRegA {
			base := strings.ToLower(filepath.Base(header.Name))
			if base == targetName || base == targetName+".exe" {
				return io.ReadAll(tr)
			}
		}
	}

	return nil, fmt.Errorf("binary %q not found in tar.gz archive", binaryName)
}
