package installtxn

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCommitDirRestoresPreviousInstallWhenPublishFails(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "demo")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "version"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	staged, cleanup, err := StageDir(root)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := os.MkdirAll(staged, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staged, "version"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	publishErr := errors.New("publish failed")
	err = CommitDir(target, staged, func() error { return publishErr })
	if !errors.Is(err, publishErr) {
		t.Fatalf("CommitDir error = %v, want publish failure", err)
	}
	data, err := os.ReadFile(filepath.Join(target, "version"))
	if err != nil {
		t.Fatalf("read restored install: %v", err)
	}
	if string(data) != "old" {
		t.Fatalf("restored content = %q, want old", data)
	}
}

func TestCommitDirRemovesFirstInstallWhenPublishFails(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "demo")
	staged, cleanup, err := StageDir(root)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := os.MkdirAll(staged, 0o755); err != nil {
		t.Fatal(err)
	}

	err = CommitDir(target, staged, func() error { return errors.New("publish failed") })
	if err == nil {
		t.Fatal("CommitDir unexpectedly succeeded")
	}
	if _, statErr := os.Stat(target); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed first install remains at target: %v", statErr)
	}
}

func TestCleanupWorkspacePreservesRetainedPreviousInstall(t *testing.T) {
	workspace := t.TempDir()
	previous := filepath.Join(workspace, "previous")
	if err := os.MkdirAll(previous, 0o755); err != nil {
		t.Fatal(err)
	}

	cleanupWorkspace(workspace)

	if _, err := os.Stat(previous); err != nil {
		t.Fatalf("cleanup removed retained previous install: %v", err)
	}
}
