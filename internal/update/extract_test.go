package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func writeTestTarGz(t *testing.T, archivePath string, entries map[string]string) {
	t.Helper()
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatalf("Create archive: %v", err)
	}
	defer func() { _ = file.Close() }()
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	for name, content := range entries {
		header := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatalf("WriteHeader %s: %v", name, err)
		}
		if _, err := tarWriter.Write([]byte(content)); err != nil {
			t.Fatalf("Write %s: %v", name, err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
}

func writeTestZip(t *testing.T, archivePath string, entries map[string]string) {
	t.Helper()
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatalf("Create archive: %v", err)
	}
	defer func() { _ = file.Close() }()
	zipWriter := zip.NewWriter(file)
	for name, content := range entries {
		writer, err := zipWriter.Create(name)
		if err != nil {
			t.Fatalf("Create entry %s: %v", name, err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatalf("Write %s: %v", name, err)
		}
	}
	if err := zipWriter.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
}

func TestExtractTarGzRoundTrip(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "archive.tar.gz")
	writeTestTarGz(t, archivePath, map[string]string{
		"zero":                 "main-binary",
		"helpers/zero-seccomp": "helper-binary",
	})

	destDir := filepath.Join(dir, "extracted")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := extractArchive(archivePath, destDir); err != nil {
		t.Fatalf("extractArchive: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(destDir, "zero"))
	if err != nil {
		t.Fatalf("ReadFile zero: %v", err)
	}
	if string(data) != "main-binary" {
		t.Fatalf("zero content = %q", data)
	}
	data, err = os.ReadFile(filepath.Join(destDir, "helpers", "zero-seccomp"))
	if err != nil {
		t.Fatalf("ReadFile helpers/zero-seccomp: %v", err)
	}
	if string(data) != "helper-binary" {
		t.Fatalf("helpers/zero-seccomp content = %q", data)
	}
}

func TestExtractZipRoundTrip(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "archive.zip")
	writeTestZip(t, archivePath, map[string]string{
		"zero.exe": "main-binary",
	})

	destDir := filepath.Join(dir, "extracted")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := extractArchive(archivePath, destDir); err != nil {
		t.Fatalf("extractArchive: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(destDir, "zero.exe"))
	if err != nil {
		t.Fatalf("ReadFile zero.exe: %v", err)
	}
	if string(data) != "main-binary" {
		t.Fatalf("zero.exe content = %q", data)
	}
}

func TestExtractTarGzRejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "archive.tar.gz")
	writeTestTarGz(t, archivePath, map[string]string{
		"../escape": "malicious",
	})

	destDir := filepath.Join(dir, "extracted")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := extractArchive(archivePath, destDir); err == nil {
		t.Fatal("expected extractArchive to reject a path-traversal entry")
	}
	if _, err := os.Stat(filepath.Join(dir, "escape")); err == nil {
		t.Fatal("path traversal entry was written outside the destination directory")
	}
}

func TestExtractZipRejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "archive.zip")
	writeTestZip(t, archivePath, map[string]string{
		"../../escape.exe": "malicious",
	})

	destDir := filepath.Join(dir, "extracted")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := extractArchive(archivePath, destDir); err == nil {
		t.Fatal("expected extractArchive to reject a path-traversal entry")
	}
}

// A zip entry with the Unix symlink mode bit set must be rejected, not
// silently written out as an ordinary file containing the link-target
// string — mirroring extractTarGz's rejection of non-regular tar entries.
func TestExtractZipRejectsSymlinkEntry(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "archive.zip")

	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatalf("Create archive: %v", err)
	}
	zipWriter := zip.NewWriter(file)
	header := &zip.FileHeader{Name: "zero"}
	header.SetMode(os.ModeSymlink | 0o777)
	writer, err := zipWriter.CreateHeader(header)
	if err != nil {
		t.Fatalf("CreateHeader: %v", err)
	}
	if _, err := writer.Write([]byte("/some/other/path")); err != nil {
		t.Fatalf("Write symlink target: %v", err)
	}
	if err := zipWriter.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close archive file: %v", err)
	}

	destDir := filepath.Join(dir, "extracted")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := extractArchive(archivePath, destDir); err == nil {
		t.Fatal("expected extractArchive to reject a symlink entry")
	}
	if _, err := os.Lstat(filepath.Join(destDir, "zero")); err == nil {
		t.Fatal("symlink entry should not have been written to the destination")
	}
}

func TestFindByBasenameSearchesRecursively(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "helpers")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	wantPath := filepath.Join(nested, "zero-seccomp")
	if err := os.WriteFile(wantPath, []byte("helper"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	found, err := findByBasename(dir, "zero-seccomp")
	if err != nil {
		t.Fatalf("findByBasename: %v", err)
	}
	if found != wantPath {
		t.Fatalf("findByBasename = %q, want %q", found, wantPath)
	}

	notFound, err := findByBasename(dir, "does-not-exist")
	if err != nil {
		t.Fatalf("findByBasename: %v", err)
	}
	if notFound != "" {
		t.Fatalf("findByBasename = %q, want empty", notFound)
	}
}

func symlinksSupported(t *testing.T) bool {
	dir := t.TempDir()
	err := os.Symlink("target", filepath.Join(dir, "link"))
	return err == nil
}

func TestExtractTarGzAllowsSafeSymlink(t *testing.T) {
	if !symlinksSupported(t) {
		t.Skip("symlinks not supported")
	}
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "archive.tar.gz")

	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	gw := gzip.NewWriter(file)
	tw := tar.NewWriter(gw)

	h1 := &tar.Header{
		Name:     "link.txt",
		Typeflag: tar.TypeSymlink,
		Linkname: "target.txt",
	}
	if err := tw.WriteHeader(h1); err != nil {
		t.Fatalf("WriteHeader link: %v", err)
	}
	h2 := &tar.Header{
		Name:     "target.txt",
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     12,
	}
	if err := tw.WriteHeader(h2); err != nil {
		t.Fatalf("WriteHeader target: %v", err)
	}
	if _, err := tw.Write([]byte("hello symbol")); err != nil {
		t.Fatalf("Write target: %v", err)
	}

	tw.Close()
	gw.Close()
	file.Close()

	destDir := filepath.Join(dir, "extracted")
	if err := extractArchive(archivePath, destDir); err != nil {
		t.Fatalf("extractArchive: %v", err)
	}

	targetPath := filepath.Join(destDir, "link.txt")
	gotLink, err := os.Readlink(targetPath)
	if err != nil {
		t.Fatalf("Readlink: %v", err)
	}
	if gotLink != "target.txt" {
		t.Fatalf("link target = %q, want %q", gotLink, "target.txt")
	}
}

func TestExtractTarGzRejectsEscapingSymlink(t *testing.T) {
	if !symlinksSupported(t) {
		t.Skip("symlinks not supported")
	}
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "archive.tar.gz")

	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	gw := gzip.NewWriter(file)
	tw := tar.NewWriter(gw)

	h1 := &tar.Header{
		Name:     "link.txt",
		Typeflag: tar.TypeSymlink,
		Linkname: "../../../outside.txt",
	}
	if err := tw.WriteHeader(h1); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	tw.Close()
	gw.Close()
	file.Close()

	destDir := filepath.Join(dir, "extracted")
	if err := extractArchive(archivePath, destDir); err == nil {
		t.Fatal("expected error extracting escaping symlink")
	}
}
