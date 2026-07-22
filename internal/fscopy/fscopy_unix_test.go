//go:build unix

package fscopy

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// TestCopyTreePreservesExecutableBit verifies CopyFile preserves the source
// executable bit on the copy (it stats the opened descriptor, not the Lstat
// entry, so the mode round-trips even with a TOCTOU-hardened open). Real POSIX
// permission bits only exist on unix; on Windows os.Chmod toggles only the
// read-only bit, so this assertion lives in the unix build.
func TestCopyTreePreservesExecutableBit(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	exe := writeFile(t, src, "bin/run.sh", "#!/bin/sh\n")
	if err := os.Chmod(exe, 0o755); err != nil {
		t.Fatalf("chmod %s: %v", exe, err)
	}

	if err := CopyTree(src, dst); err != nil {
		t.Fatalf("CopyTree: %v", err)
	}

	info, err := os.Stat(filepath.Join(dst, "bin", "run.sh"))
	if err != nil {
		t.Fatalf("stat exe: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("exec perm = %o, want 0o755", info.Mode().Perm())
	}
}

// TestHashTreeSensitiveToExecBit verifies the content hash encodes the
// executable bit: a 0644 -> 0755 flip with identical content and paths must
// produce a different hash. (Unix only — see the Windows note on the shared
// size/content sensitivity test.)
func TestHashTreeSensitiveToExecBit(t *testing.T) {
	base := t.TempDir()
	writeFile(t, base, "a.txt", "aaa")
	writeFile(t, base, "b.txt", "bbb")
	writeFile(t, base, "dir/c.txt", "ccc")

	want, err := HashTree(base)
	if err != nil {
		t.Fatalf("HashTree: %v", err)
	}

	flipped := t.TempDir()
	writeFile(t, flipped, "a.txt", "aaa")
	exe := writeFile(t, flipped, "b.txt", "bbb")
	if err := os.Chmod(exe, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	writeFile(t, flipped, "dir/c.txt", "ccc")

	got, err := HashTree(flipped)
	if err != nil {
		t.Fatalf("HashTree flipped: %v", err)
	}
	if got == want {
		t.Fatalf("exec bit flip did not change hash: got=%s want=%s", got, want)
	}
}

// TestCopyFileRejectsFIFOSource verifies openRegularRead does not block when
// the source path is a FIFO: O_NONBLOCK makes the open return immediately and
// the fstat rejection refuses it before any blocking read. Copying a skill
// file that has been swapped for a FIFO would otherwise hang skill/plugin
// installation indefinitely.
func TestCopyFileRejectsFIFOSource(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	fifoPath := filepath.Join(src, "fifo")
	if err := unix.Mkfifo(fifoPath, 0o644); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	err := CopyFile(fifoPath, filepath.Join(dst, "fifo"))
	if err == nil {
		t.Fatalf("CopyFile unexpectedly succeeded against a FIFO source")
	}
	var perr *os.PathError
	if !errors.As(err, &perr) {
		t.Fatalf("CopyFile returned %T, want *os.PathError", err)
	}
}

// TestOpenRegularReadAtRejectsFIFO verifies the fd-held open guard: an entry
// classified as regular by readDirAt's fstatat can be swapped for a FIFO at
// the openat below, and O_NOFOLLOW does not refuse FIFOs. openRegularReadAt
// must open nonblocking, fstat the descriptor, and refuse the FIFO before any
// blocking read — otherwise skill/plugin installation hangs. This drives the
// real At-based open used by CopyTree and HashTree.
func TestOpenRegularReadAtRejectsFIFO(t *testing.T) {
	src := t.TempDir()
	if err := unix.Mkfifo(filepath.Join(src, "fifo"), 0o644); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	dir, err := noFollowOpenDir(src)
	if err != nil {
		t.Fatalf("noFollowOpenDir: %v", err)
	}
	defer func() { _ = dir.Close() }()
	f, err := openRegularReadAt(dir, "fifo")
	if err == nil {
		_ = f.Close()
		t.Fatalf("openRegularReadAt unexpectedly opened a FIFO")
	}
	var perr *os.PathError
	if !errors.As(err, &perr) {
		t.Fatalf("err = %T, want *os.PathError", err)
	}
}
