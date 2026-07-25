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
	if data, err := os.ReadFile(path); err != nil || string(data) != "token" {
		t.Fatalf("restored lock content = %q, err %v; want %q intact", data, err, "token")
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

	// The sidelined lock must still exist on failure, and the competing lock
	// must be untouched.
	if _, err := os.Stat(reclaimed); err != nil {
		t.Fatalf("expected sidelined lock to still exist: %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "competing" {
		t.Fatalf("competing lock was clobbered: %q", data)
	}
}

func TestRestoreByCopy(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "lock")
	reclaimed := filepath.Join(tempDir, "lock.stale.token")

	// 1. Restores content and cleans up the sidelined file when the target is
	// free (the fallback taken when the platform's no-replace primitive fails
	// for a reason other than the destination existing).
	if err := os.WriteFile(reclaimed, []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := restoreByCopy(reclaimed, path, os.Link); err != nil {
		t.Fatalf("restoreByCopy failed: %v", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "token" {
		t.Fatalf("restored lock content = %q, err %v; want %q intact", data, err, "token")
	}
	if _, err := os.Stat(reclaimed); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected sidelined lock to be cleaned up after copy: %v", err)
	}
	if _, err := os.Stat(reclaimed + ".copy"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected staged copy to be cleaned up: %v", err)
	}

	// 2. Keeps the no-overwrite guarantee: a competing lock at the target wins
	// and the sidelined lock survives for the caller's ErrExist cleanup.
	if err := os.WriteFile(reclaimed, []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("competing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := restoreByCopy(reclaimed, path, os.Link); !errors.Is(err, os.ErrExist) {
		t.Fatalf("expected os.ErrExist when target exists, got: %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "competing" {
		t.Fatalf("competing lock was clobbered: %q", data)
	}
	if _, err := os.Stat(reclaimed); err != nil {
		t.Fatalf("expected sidelined lock to still exist: %v", err)
	}
	if _, err := os.Stat(reclaimed + ".copy"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected staged copy to be cleaned up after a failed publish: %v", err)
	}
}

func TestRemoveLockFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	if err := os.WriteFile(path, []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveLockFile(path); err != nil {
		t.Fatalf("RemoveLockFile failed: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected lock file to be removed: %v", err)
	}
	// Removing an already-missing lock file is a no-op on every platform.
	if err := RemoveLockFile(path); err != nil {
		t.Fatalf("RemoveLockFile on a missing file = %v, want nil", err)
	}
}
