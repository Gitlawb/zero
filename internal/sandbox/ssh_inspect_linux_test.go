package sandbox

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSSHInspectionReadsPinnedFileAfterAncestorReplacement(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "keys")
	path := filepath.Join(dir, "config")
	mustWriteFile(t, path, "IdentityFile ~/original-key\n")
	fd, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	pinned := os.NewFile(uintptr(fd), path)
	t.Cleanup(func() { pinned.Close() })
	if err := os.Rename(dir, filepath.Join(root, "moved")); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, path, "IdentityFile ~/replacement-key\n")
	f, err := openPinnedSSHInspectionFile(pinned)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "IdentityFile ~/original-key\n" {
		t.Fatalf("read replacement instead of pinned file: %q", data)
	}
}

func TestSSHInspectionRejectsPinnedFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fifo")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if f, err := openSSHInspectionFile(path); err == nil {
		f.Close()
		t.Fatal("opened FIFO for SSH inspection")
	}
}
