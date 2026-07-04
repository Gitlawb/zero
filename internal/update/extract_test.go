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
