package tarutil

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createTarGz builds a temporary .tar.gz containing the given files
// (key = archive path, value = file content) and returns its path.
// The file lives under t.TempDir() so cleanup is automatic.
func createTarGz(t *testing.T, files map[string][]byte) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "archive.tar.gz")

	f, err := os.Create(path)
	require.NoError(t, err)

	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	for name, content := range files {
		err := tw.WriteHeader(&tar.Header{
			Name:     name,
			Size:     int64(len(content)),
			Mode:     0o644,
			Typeflag: tar.TypeReg,
		})
		require.NoError(t, err)

		_, err = tw.Write(content)
		require.NoError(t, err)
	}

	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())
	require.NoError(t, f.Close())

	return path
}

// createTarGzWithEntries builds a .tar.gz from explicit tar headers, giving
// the caller control over directories and other entry types.
func createTarGzWithEntries(t *testing.T, entries []tar.Header, contents map[string][]byte) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "archive.tar.gz")

	f, err := os.Create(path)
	require.NoError(t, err)

	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	for _, hdr := range entries {
		h := hdr // copy
		require.NoError(t, tw.WriteHeader(&h))

		if h.Typeflag == tar.TypeReg {
			if data, ok := contents[h.Name]; ok {
				_, err := tw.Write(data)
				require.NoError(t, err)
			}
		}
	}

	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())
	require.NoError(t, f.Close())

	return path
}

func TestExtractTarGz_Basic(t *testing.T) {
	archive := createTarGz(t, map[string][]byte{
		"hello.txt":     []byte("hello"),
		"sub/world.txt": []byte("world"),
	})

	dst := t.TempDir()
	err := ExtractTarGz(archive, dst)
	require.NoError(t, err)

	hello, err := os.ReadFile(filepath.Join(dst, "hello.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello", string(hello))

	world, err := os.ReadFile(filepath.Join(dst, "sub", "world.txt"))
	require.NoError(t, err)
	assert.Equal(t, "world", string(world))
}

func TestExtractTarGz_ZipSlip(t *testing.T) {
	// Manually craft a tar.gz with a path-traversal entry.
	entries := []tar.Header{
		{
			Name:     "../../etc/evil.txt",
			Size:     int64(len("pwned")),
			Mode:     0o644,
			Typeflag: tar.TypeReg,
		},
	}
	contents := map[string][]byte{
		"../../etc/evil.txt": []byte("pwned"),
	}
	archive := createTarGzWithEntries(t, entries, contents)

	dst := t.TempDir()
	err := ExtractTarGz(archive, dst)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid tar path")
}

func TestExtractTarGz_EmptyArchive(t *testing.T) {
	archive := createTarGz(t, map[string][]byte{})

	dst := filepath.Join(t.TempDir(), "output")
	err := ExtractTarGz(archive, dst)
	require.NoError(t, err)

	info, err := os.Stat(dst)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestExtractTarGz_NonExistentSource(t *testing.T) {
	dst := t.TempDir()
	err := ExtractTarGz("/tmp/does-not-exist-ever.tar.gz", dst)
	require.Error(t, err)
}

func TestExtractTarGz_Directories(t *testing.T) {
	entries := []tar.Header{
		{
			Name:     "mydir/",
			Typeflag: tar.TypeDir,
			Mode:     0o755,
		},
		{
			Name:     "mydir/file.txt",
			Size:     int64(len("inside")),
			Typeflag: tar.TypeReg,
			Mode:     0o644,
		},
	}
	contents := map[string][]byte{
		"mydir/file.txt": []byte("inside"),
	}
	archive := createTarGzWithEntries(t, entries, contents)

	dst := t.TempDir()
	err := ExtractTarGz(archive, dst)
	require.NoError(t, err)

	dirInfo, err := os.Stat(filepath.Join(dst, "mydir"))
	require.NoError(t, err)
	assert.True(t, dirInfo.IsDir())

	data, err := os.ReadFile(filepath.Join(dst, "mydir", "file.txt"))
	require.NoError(t, err)
	assert.Equal(t, "inside", string(data))
}

func TestExtractTarGzReader_Basic(t *testing.T) {
	archive := createTarGz(t, map[string][]byte{
		"hello.txt":     []byte("hello"),
		"sub/world.txt": []byte("world"),
	})

	f, err := os.Open(archive)
	require.NoError(t, err)
	defer f.Close()

	dst := t.TempDir()
	err = ExtractTarGzReader(f, dst)
	require.NoError(t, err)

	hello, err := os.ReadFile(filepath.Join(dst, "hello.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello", string(hello))

	world, err := os.ReadFile(filepath.Join(dst, "sub", "world.txt"))
	require.NoError(t, err)
	assert.Equal(t, "world", string(world))
}

// createTarGzWithSize builds a tar.gz containing a single file of the given
// name and size. The file content is all zeros. The archive is written to a
// temporary directory and its path is returned.
func createTarGzWithSize(t *testing.T, name string, size int64) string {
	t.Helper()

	dir := t.TempDir()
	archivePath := filepath.Join(dir, "archive.tar.gz")

	f, err := os.Create(archivePath)
	require.NoError(t, err)

	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	err = tw.WriteHeader(&tar.Header{
		Name:     name,
		Size:     size,
		Mode:     0o644,
		Typeflag: tar.TypeReg,
	})
	require.NoError(t, err)

	// Write zeros in chunks to avoid allocating the full size in memory.
	written := int64(0)
	chunk := make([]byte, 32*1024) // 32 KB chunks of zeros
	for written < size {
		toWrite := size - written
		if toWrite > int64(len(chunk)) {
			toWrite = int64(len(chunk))
		}
		n, werr := tw.Write(chunk[:toWrite])
		require.NoError(t, werr)
		written += int64(n)
	}

	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())
	require.NoError(t, f.Close())

	return archivePath
}

func TestExtractTarGz_PerFileSizeLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("skips in short mode — allocates >100MB tar.gz")
	}

	// Verify the constant has the expected value.
	assert.Equal(t, int64(100<<20), MaxFileSize)

	archive := createTarGzWithSize(t, "bigfile.bin", MaxFileSize+1)

	dst := t.TempDir()
	err := ExtractTarGz(archive, dst)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds max size")
}

func TestExtractTarGz_TotalSizeLimit(t *testing.T) {
	// MaxTotalSize is 2 GB — creating that much data in a unit test is
	// impractical. Instead we verify the constant value and exercise the
	// accumulation path with a small multi-file archive that stays under
	// the limit, confirming no false positives.
	assert.Equal(t, int64(2<<30), MaxTotalSize)

	archive := createTarGz(t, map[string][]byte{
		"a.txt": bytes.Repeat([]byte("A"), 1024),
		"b.txt": bytes.Repeat([]byte("B"), 1024),
		"c.txt": bytes.Repeat([]byte("C"), 1024),
	})

	dst := t.TempDir()
	err := ExtractTarGz(archive, dst)
	require.NoError(t, err, "small multi-file archive should not trigger total size limit")

	// Verify all three files were extracted.
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		_, statErr := os.Stat(filepath.Join(dst, name))
		assert.NoError(t, statErr, "expected %s to be extracted", name)
	}
}

func TestExtractTarGz_SymlinkIgnored(t *testing.T) {
	entries := []tar.Header{
		{
			Name:     "legit.txt",
			Size:     int64(len("safe")),
			Mode:     0o644,
			Typeflag: tar.TypeReg,
		},
		{
			Name:     "evil-link",
			Linkname: "/etc/passwd",
			Mode:     0o777,
			Typeflag: tar.TypeSymlink,
		},
	}
	contents := map[string][]byte{
		"legit.txt": []byte("safe"),
	}
	archive := createTarGzWithEntries(t, entries, contents)

	dst := t.TempDir()
	err := ExtractTarGz(archive, dst)
	require.NoError(t, err, "symlinks should be silently ignored, not cause an error")

	// The regular file should be extracted.
	data, err := os.ReadFile(filepath.Join(dst, "legit.txt"))
	require.NoError(t, err)
	assert.Equal(t, "safe", string(data))

	// The symlink entry should NOT have been created.
	_, err = os.Lstat(filepath.Join(dst, "evil-link"))
	assert.True(t, os.IsNotExist(err), "symlink target should not exist in output directory")
}

func TestExtractTarGz_AbsolutePath(t *testing.T) {
	entries := []tar.Header{
		{
			Name:     "/etc/shadow",
			Size:     int64(len("root::0:0:root")),
			Mode:     0o644,
			Typeflag: tar.TypeReg,
		},
	}
	contents := map[string][]byte{
		"/etc/shadow": []byte("root::0:0:root"),
	}
	archive := createTarGzWithEntries(t, entries, contents)

	dst := t.TempDir()
	err := ExtractTarGz(archive, dst)

	// Go's filepath.Join strips leading slashes, so "/etc/shadow" is safely
	// extracted as "<dst>/etc/shadow" — no escape from the destination directory.
	require.NoError(t, err)

	// Verify the file was extracted safely under dst.
	data, err := os.ReadFile(filepath.Join(dst, "etc", "shadow"))
	require.NoError(t, err)
	assert.Equal(t, "root::0:0:root", string(data))
}

func TestExtractTarGz_PlainTarRejected(t *testing.T) {
	// Create a plain .tar (no gzip compression).
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "plain.tar")

	f, err := os.Create(tarPath)
	require.NoError(t, err)

	tw := tar.NewWriter(f)
	content := []byte("not gzipped")
	err = tw.WriteHeader(&tar.Header{
		Name:     "file.txt",
		Size:     int64(len(content)),
		Mode:     0o644,
		Typeflag: tar.TypeReg,
	})
	require.NoError(t, err)
	_, err = tw.Write(content)
	require.NoError(t, err)

	require.NoError(t, tw.Close())
	require.NoError(t, f.Close())

	dst := t.TempDir()
	err = ExtractTarGz(tarPath, dst)
	require.Error(t, err, "plain tar without gzip should be rejected by gzip.NewReader")
}
