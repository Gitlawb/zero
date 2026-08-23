package fsutil

import (
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

func TestRenameWithRetryRetriesOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("RenameWithRetry only retries on Windows")
	}
	var attempts int
	err := RenameWithRetry("src", "dst", func(src, dst string) error {
		attempts++
		switch attempts {
		case 1:
			return syscall.Errno(32) // ERROR_SHARING_VIOLATION
		case 2:
			return syscall.Errno(33) // ERROR_LOCK_VIOLATION
		default:
			return nil
		}
	})
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
}

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "sub", "test.txt")
	content := []byte("hello atomic world")

	if err := WriteFileAtomic(target, content, 0o644); err != nil {
		t.Fatalf("WriteFileAtomic failed: %v", err)
	}

	read, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if string(read) != string(content) {
		t.Fatalf("read content mismatch: got %q, want %q", string(read), string(content))
	}

	// Overwrite test
	newContent := []byte("overwritten atomic content")
	if err := WriteFileAtomic(target, newContent, 0o644); err != nil {
		t.Fatalf("WriteFileAtomic overwrite failed: %v", err)
	}
	read2, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile after overwrite failed: %v", err)
	}
	if string(read2) != string(newContent) {
		t.Fatalf("read content mismatch: got %q, want %q", string(read2), string(newContent))
	}
}

