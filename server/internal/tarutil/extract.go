package tarutil

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	// MaxFileSize is the maximum size of a single extracted file (100 MB).
	MaxFileSize int64 = 100 << 20
	// MaxTotalSize is the maximum total extracted size (2 GB).
	MaxTotalSize int64 = 2 << 30
)

// ExtractTarGz extracts a .tar.gz file from src into the dst directory.
// It enforces zip-slip protection and size limits to prevent decompression bombs.
func ExtractTarGz(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()

	return ExtractTarGzReader(f, dst)
}

// ExtractTarGzReader extracts a gzip-compressed tar stream into the dst directory.
func ExtractTarGzReader(r io.Reader, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}

	gr, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	var totalBytes int64

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		clean := filepath.Clean(hdr.Name)
		if clean == "." {
			continue
		}

		target := filepath.Join(dst, hdr.Name)

		// Zip-slip guard
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(dst)+string(os.PathSeparator)) {
			return fmt.Errorf("invalid tar path: %s", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("mkdir %s: %w", target, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("mkdir parent of %s: %w", target, err)
			}
			out, err := os.Create(target)
			if err != nil {
				return err
			}
			n, copyErr := io.Copy(out, io.LimitReader(tr, MaxFileSize+1))
			out.Close()
			if copyErr != nil {
				return copyErr
			}
			if n > MaxFileSize {
				return fmt.Errorf("file %s exceeds max size (%d bytes)", hdr.Name, MaxFileSize)
			}
			totalBytes += n
			if totalBytes > MaxTotalSize {
				return fmt.Errorf("total extracted size exceeds limit (%d bytes)", MaxTotalSize)
			}
		}
	}
	return nil
}

// ExtractZip extracts a .zip file from src into the dst directory.
// It enforces zip-slip protection and the same size limits as ExtractTarGz.
func ExtractZip(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}

	r, err := zip.OpenReader(src)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer r.Close()

	var totalBytes int64

	for _, f := range r.File {
		clean := filepath.Clean(f.Name)
		if clean == "." {
			continue
		}

		target := filepath.Join(dst, f.Name)

		// Zip-slip guard
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(dst)+string(os.PathSeparator)) {
			return fmt.Errorf("invalid zip path: %s", f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("mkdir %s: %w", target, err)
			}
			continue
		}

		// Ensure parent directory exists
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("mkdir parent of %s: %w", target, err)
		}

		// Skip symlinks (same policy as tar extraction)
		if f.FileInfo().Mode()&os.ModeSymlink != 0 {
			continue
		}

		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("open zip entry %s: %w", f.Name, err)
		}

		out, err := os.Create(target)
		if err != nil {
			rc.Close()
			return err
		}

		n, copyErr := io.Copy(out, io.LimitReader(rc, MaxFileSize+1))
		out.Close()
		rc.Close()
		if copyErr != nil {
			return copyErr
		}
		if n > MaxFileSize {
			return fmt.Errorf("file %s exceeds max size (%d bytes)", f.Name, MaxFileSize)
		}
		totalBytes += n
		if totalBytes > MaxTotalSize {
			return fmt.Errorf("total extracted size exceeds limit (%d bytes)", MaxTotalSize)
		}
	}
	return nil
}
