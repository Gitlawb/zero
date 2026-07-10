package lockutil

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRestoreLockFile(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "lock")
	reclaimed := filepath.Join(tempDir, "lock.stale.token")

	// 1. Successful restore when target does not exist
	if err := os.WriteFile(reclaimed, []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RestoreLockFile(reclaimed, path); err != nil {
		t.Fatalf("RestoreLockFile failed: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected lock file to exist at path: %v", err)
	}
	if _, err := os.Stat(reclaimed); !os.IsNotExist(err) {
		t.Fatalf("expected sidelined lock to be cleaned up/removed: %v", err)
	}

	// 2. Fail when target already exists (prevent overwrite)
	if err := os.WriteFile(reclaimed, []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("competing"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := RestoreLockFile(reclaimed, path)
	if err == nil {
		t.Fatal("expected RestoreLockFile to fail when target exists")
	}
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("expected os.ErrExist, got: %v", err)
	}

	// The sidelined lock must still exist on failure
	if _, err := os.Stat(reclaimed); err != nil {
		t.Fatalf("expected sidelined lock to still exist: %v", err)
	}
}
