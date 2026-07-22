//go:build windows

package fscopy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenRegularRejectsFinalSymlink(t *testing.T) {
	dir := t.TempDir()
	target := writeFile(t, dir, "target.txt", "preserved\n")
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("creating symlink requires privileges on this Windows host: %v", err)
	}

	if f, err := openRegularRead(link); err == nil {
		_ = f.Close()
		t.Fatal("openRegularRead opened a final-component symlink; want error")
	}
	if f, err := openRegularWrite(link, 0o644); err == nil {
		_ = f.Close()
		t.Fatal("openRegularWrite opened a final-component symlink; want error")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != "preserved\n" {
		t.Fatalf("symlink target was modified: %q", got)
	}
}
