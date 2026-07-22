package specialist

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func TestStorageCreateWritesValidManifestAndDeleteRemovesIt(t *testing.T) {
	userDir := filepath.Join(t.TempDir(), "user")
	storage := NewStorage(Paths{UserDir: userDir})

	manifest, err := storage.Create(CreateInput{
		Name:         "triage",
		Description:  "Triage failures",
		SystemPrompt: "Find the likely failure area.",
		Tools:        []string{"read-only", "plan"},
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if manifest.FilePath != filepath.Join(userDir, "triage.md") || manifest.Location != LocationUser {
		t.Fatalf("unexpected manifest path/location: %#v", manifest)
	}
	data, err := os.ReadFile(manifest.FilePath)
	if err != nil {
		t.Fatalf("read created manifest: %v", err)
	}
	loaded, err := ParseMarkdown(string(data))
	if err != nil {
		t.Fatalf("created manifest did not parse: %v\n%s", err, string(data))
	}
	if loaded.Metadata.Name != "triage" || !contains(loaded.ResolvedTools, "update_plan") {
		t.Fatalf("unexpected loaded manifest: %#v", loaded)
	}

	deleted, err := storage.Delete(DeleteInput{Name: "triage"})
	if err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if deleted != manifest.FilePath {
		t.Fatalf("deleted path = %q, want %q", deleted, manifest.FilePath)
	}
	if _, err := os.Stat(manifest.FilePath); !os.IsNotExist(err) {
		t.Fatalf("expected file deleted, stat err=%v", err)
	}
}

func TestStorageRejectsUnsafeNamesAndDuplicates(t *testing.T) {
	storage := NewStorage(Paths{UserDir: t.TempDir()})
	if _, err := storage.Create(CreateInput{Name: "../escape", Description: "Escape"}); err == nil || !strings.Contains(err.Error(), "invalid specialist name") {
		t.Fatalf("unsafe Create error = %v", err)
	}
	if _, err := storage.Create(CreateInput{Name: "safe", Description: "Safe"}); err != nil {
		t.Fatalf("Create safe returned error: %v", err)
	}
	if _, err := storage.Create(CreateInput{Name: "safe", Description: "Safe"}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate Create error = %v", err)
	}
}

func TestStorageCreateForceRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	userDir := filepath.Join(root, "user")
	if err := os.MkdirAll(userDir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "target.md")
	if err := os.WriteFile(target, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(userDir, "safe.md")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	storage := NewStorage(Paths{UserDir: userDir})

	_, err := storage.Create(CreateInput{Name: "safe", Description: "Safe", Overwrite: true})

	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite symlink") {
		t.Fatalf("symlink overwrite error = %v", err)
	}
	data, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "outside" {
		t.Fatalf("symlink target was modified: %q", string(data))
	}
	assertNoTemporarySpecialistFiles(t, userDir)
}

func TestStorageCreateForceAtomicallyReplacesFile(t *testing.T) {
	userDir := t.TempDir()
	path := filepath.Join(userDir, "safe.md")
	if err := os.WriteFile(path, []byte("old content"), 0o644); err != nil {
		t.Fatal(err)
	}
	storage := NewStorage(Paths{UserDir: userDir})

	manifest, err := storage.Create(CreateInput{
		Name:         "safe",
		Description:  "Safe",
		SystemPrompt: "new content",
		Overwrite:    true,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), FormatMarkdown(manifest); got != want {
		t.Fatalf("file content = %q, want %q", got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); runtime.GOOS != "windows" && got != 0o600 {
		t.Fatalf("file permissions = %o, want 600", got)
	}
	assertNoTemporarySpecialistFiles(t, userDir)
}

func TestWriteSpecialistAtomicRetriesTransientWindowsRename(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("rename retries are Windows-specific")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "safe.md")
	if err := os.WriteFile(path, []byte("old content"), 0o600); err != nil {
		t.Fatal(err)
	}
	attempts := 0
	err := writeSpecialistAtomicWith(path, "new content", func(src, dst string) error {
		attempts++
		if attempts == 1 {
			return syscall.Errno(32) // ERROR_SHARING_VIOLATION
		}
		return os.Rename(src, dst)
	}, func(string) error { return nil })
	if err != nil {
		t.Fatalf("writeSpecialistAtomicWith returned error: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("rename attempts = %d, want 2", attempts)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "new content" {
		t.Fatalf("file content = %q, want %q", got, "new content")
	}
	assertNoTemporarySpecialistFiles(t, dir)
}

func TestWriteSpecialistAtomicPropagatesDirectorySyncError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "safe.md")
	syncErr := errors.New("sync failed")
	err := writeSpecialistAtomicWith(path, "new content", nil, func(got string) error {
		if got != dir {
			t.Fatalf("sync directory = %q, want %q", got, dir)
		}
		return syncErr
	})
	if !errors.Is(err, syncErr) {
		t.Fatalf("writeSpecialistAtomicWith error = %v, want %v", err, syncErr)
	}
	assertNoTemporarySpecialistFiles(t, dir)
}

func assertNoTemporarySpecialistFiles(t *testing.T, dir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".specialist-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary specialist files remain: %v", matches)
	}
}
