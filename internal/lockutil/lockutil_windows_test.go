//go:build windows

package lockutil

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestMoveFileNoReplaceMapsAlreadyExistsToErrExist pins the errno coupling
// every caller keys its cleanup on: with no flags, MoveFileEx fails with
// ERROR_ALREADY_EXISTS when the destination exists, and that errno must
// satisfy errors.Is(err, os.ErrExist).
func TestMoveFileNoReplaceMapsAlreadyExistsToErrExist(t *testing.T) {
	dir := t.TempDir()
	from := filepath.Join(dir, "from")
	to := filepath.Join(dir, "to")
	if err := os.WriteFile(from, []byte("sidelined"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, []byte("competing"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := moveFileNoReplace(from, to)
	if !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		t.Fatalf("MoveFileEx with an existing destination = %v, want ERROR_ALREADY_EXISTS", err)
	}
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("ERROR_ALREADY_EXISTS must satisfy errors.Is(err, os.ErrExist), got: %v", err)
	}
	if data, _ := os.ReadFile(to); string(data) != "competing" {
		t.Fatalf("destination was overwritten: %q", data)
	}
}

// TestRemoveLockFileRetriesSharingViolation holds the file open briefly (Go
// opens files without FILE_SHARE_DELETE on Windows, so a concurrent DeleteFile
// fails with ERROR_SHARING_VIOLATION while the handle is open); RemoveLockFile
// must ride its retry loop (15 x 5ms) past the window and succeed once the
// handle closes.
func TestRemoveLockFileRetriesSharingViolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	if err := os.WriteFile(path, []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(10 * time.Millisecond)
		_ = f.Close()
	}()
	if err := RemoveLockFile(path); err != nil {
		t.Fatalf("RemoveLockFile should succeed once the contending handle closes: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected lock file to be removed: %v", err)
	}
}

// TestRemoveLockFileSharingViolationExhausts verifies the retry loop is
// bounded: with the contending handle held for the whole call, RemoveLockFile
// surfaces the sharing violation instead of retrying forever.
func TestRemoveLockFileSharingViolationExhausts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	if err := os.WriteFile(path, []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := RemoveLockFile(path); !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("RemoveLockFile with a held handle = %v, want ERROR_SHARING_VIOLATION", err)
	}
}
