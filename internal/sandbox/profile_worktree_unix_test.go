//go:build unix

package sandbox

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestGitMetadataWriteCarveoutsRejectsFIFOGitdir(t *testing.T) {
	root := t.TempDir()
	gitPath := filepath.Join(root, ".git")
	if err := syscall.Mkfifo(gitPath, 0o644); err != nil {
		t.Skip(err)
	}
	done := make(chan []string, 1)
	go func() {
		done <- gitMetadataWriteCarveouts(root)
	}()
	select {
	case got := <-done:
		mustEqualCarveouts(t, got, []string{gitPath})
	case <-time.After(2 * time.Second):
		t.Fatal("blocked reading fifo .git")
	}
}

func TestGitMetadataWriteCarveoutsRejectsFIFOCommondir(t *testing.T) {
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git-real")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(gitDir, "commondir"), 0o644); err != nil {
		t.Skip(err)
	}
	writeGitFile(t, filepath.Join(root, ".git"), "gitdir: "+gitDir+"\n")
	done := make(chan []string, 1)
	go func() {
		done <- gitMetadataWriteCarveouts(root)
	}()
	select {
	case got := <-done:
		want := []string{
			filepath.Join(root, ".git"),
			filepath.Join(gitDir, "hooks"),
			filepath.Join(gitDir, "config"),
		}
		mustEqualCarveouts(t, got, want)
	case <-time.After(2 * time.Second):
		t.Fatal("blocked reading fifo commondir")
	}
}
