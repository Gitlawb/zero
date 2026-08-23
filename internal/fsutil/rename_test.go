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

func TestWriteFileAtomicPreservesExistingMode(t *testing.T) {
	dir := t.TempDir()
	cases := []os.FileMode{0o600, 0o755}
	for _, want := range cases {
		target := filepath.Join(dir, "mode-"+want.String()+".txt")
		if err := os.WriteFile(target, []byte("old"), want); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if err := os.Chmod(target, want); err != nil {
			t.Fatalf("Chmod: %v", err)
		}
		if err := WriteFileAtomic(target, []byte("new"), 0o644); err != nil {
			t.Fatalf("WriteFileAtomic: %v", err)
		}
		info, err := os.Stat(target)
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Fatalf("mode = %04o, want %04o", got, want)
		}
		got, err := os.ReadFile(target)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if string(got) != "new" {
			t.Fatalf("content = %q, want %q", got, "new")
		}
	}
}

func TestWriteFileAtomicLeavesDestinationOnReplaceFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	marker := filepath.Join(target, "keep.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := WriteFileAtomic(target, []byte("should-not-land"), 0o644); err == nil {
		t.Fatal("expected replace failure when destination is a directory")
	}

	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("original destination was disturbed: %v", err)
	}
	if string(got) != "keep" {
		t.Fatalf("marker = %q, want keep", got)
	}
	leftovers, err := filepath.Glob(filepath.Join(dir, ".zero-tmp-*"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("temporary files left behind: %v", leftovers)
	}
}
