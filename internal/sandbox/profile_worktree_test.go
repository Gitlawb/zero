package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

// Worktree/submodule checkouts store .git as a *file* ("gitdir: <path>"). The
// hooks/config carveout must resolve the real (common) git dir instead of the
// bogus <root>/.git/hooks under that file — a --tmpfs there makes bwrap fail
// ("Can't mkdir parents ... Not a directory") and blocks every sandboxed tool.
func TestGitMetadataWriteCarveoutsResolvesWorktree(t *testing.T) {
	main := t.TempDir()
	commonGit := filepath.Join(main, ".git")
	worktreeGit := filepath.Join(commonGit, "worktrees", "wt")
	if err := os.MkdirAll(worktreeGit, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktreeGit, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+worktreeGit+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := gitMetadataWriteCarveouts(worktree)
	want := []string{
		filepath.Join(commonGit, "hooks"),
		filepath.Join(commonGit, "config"),
	}
	if len(got) != len(want) {
		t.Fatalf("carveouts = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("carveout[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// Must never point under the .git *file* — that path has no mkdir-able parent.
	bogus := filepath.Join(worktree, ".git", "hooks")
	for _, c := range got {
		if c == bogus {
			t.Fatalf("carveout points under .git file: %q", c)
		}
	}
}

// Plain checkouts (.git is a dir) and .git-absent roots keep the literal
// <root>/.git/{hooks,config} carveouts.
func TestGitMetadataWriteCarveoutsPlainCheckout(t *testing.T) {
	root := t.TempDir()
	want := []string{
		filepath.Join(root, ".git", "hooks"),
		filepath.Join(root, ".git", "config"),
	}
	got := gitMetadataWriteCarveouts(root)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("carveout[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	got = gitMetadataWriteCarveouts(root)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("dir carveout[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
