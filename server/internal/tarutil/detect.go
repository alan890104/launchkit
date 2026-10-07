package tarutil

import (
	"fmt"
	"os"
)

// Extract auto-detects the archive format (tar.gz or zip) by inspecting magic
// bytes, then delegates to the appropriate extractor.
// This lets callers accept both formats transparently.
func Extract(src, dst string) error {
	format, err := detectFormat(src)
	if err != nil {
		return err
	}
	switch format {
	case "tar.gz":
		return ExtractTarGz(src, dst)
	case "zip":
		return ExtractZip(src, dst)
	default:
		return fmt.Errorf("unsupported archive format: %s", format)
	}
}

// detectFormat reads the first few bytes of a file to determine whether it is
// a gzip-compressed tar (magic 1f 8b) or a zip archive (magic 50 4b 03 04).
func detectFormat(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	buf := make([]byte, 4)
	n, err := f.Read(buf)
	if err != nil {
		return "", fmt.Errorf("read magic bytes: %w", err)
	}
	if n < 2 {
		return "", fmt.Errorf("file too small to detect format")
	}

	// gzip magic: 1f 8b
	if buf[0] == 0x1f && buf[1] == 0x8b {
		return "tar.gz", nil
	}

	// zip magic: PK\x03\x04
	if n >= 4 && buf[0] == 0x50 && buf[1] == 0x4b && buf[2] == 0x03 && buf[3] == 0x04 {
		return "zip", nil
	}

	return "", fmt.Errorf("unrecognised archive format (magic: %x)", buf[:n])
}
