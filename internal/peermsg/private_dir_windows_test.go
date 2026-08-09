//go:build windows

package peermsg

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsurePrivateDirRejectsWindowsReparseParent(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create Windows directory symlink: %v", err)
	}
	if err := ensurePrivateDir(filepath.Join(link, "peers")); err == nil {
		t.Fatal("expected reparse-point parent to be rejected")
	}
}

func TestEnsurePrivateDirRejectsWindowsFileComponent(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensurePrivateDir(filepath.Join(file, "peers")); err == nil {
		t.Fatal("expected file path component to be rejected")
	}
}
