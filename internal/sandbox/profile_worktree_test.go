package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func writeGitFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustEqualCarveouts(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("carveouts = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("carveout[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// Worktree/submodule checkouts store .git as a *file* ("gitdir: <path>"). The
// hooks/config carveout must resolve the real (common) git dir instead of the
// bogus <root>/.git/hooks under that file — a --tmpfs there makes bwrap fail
// ("Can't mkdir parents ... Not a directory") and blocks every sandboxed tool.
func TestGitMetadataWriteCarveoutsResolvesWorktree(t *testing.T) {
	root := t.TempDir()
	commonGit := filepath.Join(root, ".git-common")
	worktreeGit := filepath.Join(root, ".git-wt")
	if err := os.MkdirAll(worktreeGit, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(commonGit, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGitFile(t, filepath.Join(worktreeGit, "commondir"), filepath.Join("..", ".git-common")+"\n")
	writeGitFile(t, filepath.Join(root, ".git"), "gitdir: "+worktreeGit+"\n")

	got := gitMetadataWriteCarveouts(root)
	want := []string{
		filepath.Join(commonGit, "hooks"),
		filepath.Join(commonGit, "config"),
	}
	mustEqualCarveouts(t, got, want)
	bogus := filepath.Join(root, ".git", "hooks")
	for _, c := range got {
		if c == bogus {
			t.Fatalf("carveout points under .git file: %q", c)
		}
	}
}

func TestGitMetadataWriteCarveoutsOutsideWorktreeGitdirReturnsNone(t *testing.T) {
	main := t.TempDir()
	commonGit := filepath.Join(main, ".git")
	worktreeGit := filepath.Join(commonGit, "worktrees", "wt")
	if err := os.MkdirAll(worktreeGit, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGitFile(t, filepath.Join(worktreeGit, "commondir"), "../..\n")
	worktree := t.TempDir()
	writeGitFile(t, filepath.Join(worktree, ".git"), "gitdir: "+worktreeGit+"\n")
	got := gitMetadataWriteCarveouts(worktree)
	if len(got) != 0 {
		t.Fatalf("outside-workspace gitdir carveouts = %v, want none", got)
	}
}

func TestGitMetadataWriteCarveoutsPlainCheckout(t *testing.T) {
	root := t.TempDir()
	want := []string{
		filepath.Join(root, ".git", "hooks"),
		filepath.Join(root, ".git", "config"),
	}
	got := gitMetadataWriteCarveouts(root)
	mustEqualCarveouts(t, got, want)

	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	got = gitMetadataWriteCarveouts(root)
	mustEqualCarveouts(t, got, want)
}

func TestGitMetadataWriteCarveoutsRejectsEscapingGitdir(t *testing.T) {
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeGitFile(t, filepath.Join(root, ".git"), "gitdir: "+filepath.Join(outside, "git")+"\n")
	got := gitMetadataWriteCarveouts(root)
	if len(got) != 0 {
		t.Fatalf("escaping absolute gitdir carveouts = %v, want none", got)
	}
}

func TestGitMetadataWriteCarveoutsRejectsRelativeEscapingGitdir(t *testing.T) {
	parent := t.TempDir()
	outside := filepath.Join(parent, "elsewhere")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "ws")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGitFile(t, filepath.Join(root, ".git"), "gitdir: ../elsewhere\n")
	got := gitMetadataWriteCarveouts(root)
	if len(got) != 0 {
		t.Fatalf("escaping relative gitdir carveouts = %v, want none", got)
	}
}

func TestGitMetadataWriteCarveoutsRejectsEscapingCommondir(t *testing.T) {
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git-real")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	writeGitFile(t, filepath.Join(gitDir, "commondir"), outside+"\n")
	writeGitFile(t, filepath.Join(root, ".git"), "gitdir: "+gitDir+"\n")
	got := gitMetadataWriteCarveouts(root)
	if len(got) != 0 {
		t.Fatalf("escaping commondir carveouts = %v, want none", got)
	}
}

func TestGitMetadataWriteCarveoutsCommondirReadFailureKeepsGitDir(t *testing.T) {
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git-real")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGitFile(t, filepath.Join(root, ".git"), "gitdir: "+gitDir+"\n")
	got := gitMetadataWriteCarveouts(root)
	want := []string{
		filepath.Join(gitDir, "hooks"),
		filepath.Join(gitDir, "config"),
	}
	mustEqualCarveouts(t, got, want)
}

func TestGitMetadataWriteCarveoutsStaleGitdirReturnsNone(t *testing.T) {
	root := t.TempDir()
	writeGitFile(t, filepath.Join(root, ".git"), "gitdir: missing-target\n")
	got := gitMetadataWriteCarveouts(root)
	if len(got) != 0 {
		t.Fatalf("stale gitdir carveouts = %v, want none", got)
	}
}

func TestGitMetadataWriteCarveoutsRejectsNonDirectoryTarget(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "not-a-dir")
	writeGitFile(t, target, "nope\n")
	writeGitFile(t, filepath.Join(root, ".git"), "gitdir: "+target+"\n")
	got := gitMetadataWriteCarveouts(root)
	if len(got) != 0 {
		t.Fatalf("non-directory gitdir carveouts = %v, want none", got)
	}
}

func TestGitMetadataWriteCarveoutsRejectsSymlinkGitdir(t *testing.T) {
	outside := t.TempDir()
	root := t.TempDir()
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(outside, alias); err != nil {
		t.Fatal(err)
	}
	writeGitFile(t, filepath.Join(root, ".git"), "gitdir: "+alias+"\n")
	got := gitMetadataWriteCarveouts(root)
	if len(got) != 0 {
		t.Fatalf("symlink gitdir carveouts = %v, want none", got)
	}
}
