//go:build !windows

package credstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func secureTestDirectory(t *testing.T, dir string) {
	t.Helper()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestFileLockRejectsSymlinkRedirection(t *testing.T) {
	dir := t.TempDir()
	store := fileStore(t, dir)
	target := filepath.Join(t.TempDir(), "outside.lock")
	const sentinel = "outside"
	if err := os.WriteFile(target, []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, store.lockPath()); err != nil {
		t.Fatal(err)
	}

	if err := store.Set("openai", "secret"); err == nil || !strings.Contains(err.Error(), "unsafe lock path") {
		t.Fatalf("Set error = %v, want unsafe lock path rejection", err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != sentinel {
		t.Fatalf("redirect target = %q, err %v; want unchanged", data, err)
	}
	if _, err := os.Stat(store.file); !os.IsNotExist(err) {
		t.Fatalf("credential file exists after rejected lock: %v", err)
	}
}

func TestFileLockRejectsUnsafeParentPermissions(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	store := fileStore(t, dir)

	if err := store.Set("openai", "secret"); err == nil || !strings.Contains(err.Error(), "unsafe permissions") {
		t.Fatalf("Set error = %v, want unsafe permissions rejection", err)
	}
	if _, err := os.Stat(store.file); !os.IsNotExist(err) {
		t.Fatalf("credential file exists after rejected parent: %v", err)
	}
}
