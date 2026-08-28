//go:build unix

package sandbox

import (
	"path/filepath"
	"syscall"
	"testing"
)

func TestGitMetadataWriteCarveoutsProtectsSpecialGitEntry(t *testing.T) {
	root := t.TempDir()
	gitPath := filepath.Join(root, ".git")
	if err := syscall.Mkfifo(gitPath, 0o644); err != nil {
		t.Skip(err)
	}
	mustEqualCarveouts(t, gitMetadataWriteCarveouts(root), []string{gitPath})
}
