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
		filepath.Join(root, ".git"),
		filepath.Join(commonGit, "hooks"),
		filepath.Join(commonGit, "config"),
		filepath.Join(worktreeGit, "hooks"),
		filepath.Join(worktreeGit, "config"),
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
	want := []string{filepath.Join(worktree, ".git")}
	mustEqualCarveouts(t, got, want)
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
	mustEqualCarveouts(t, got, []string{filepath.Join(root, ".git")})
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
	mustEqualCarveouts(t, got, []string{filepath.Join(root, ".git")})
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
	mustEqualCarveouts(t, got, []string{filepath.Join(root, ".git")})
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
		filepath.Join(root, ".git"),
		filepath.Join(gitDir, "hooks"),
		filepath.Join(gitDir, "config"),
	}
	mustEqualCarveouts(t, got, want)
}

func TestGitMetadataWriteCarveoutsStaleGitdirReturnsNone(t *testing.T) {
	root := t.TempDir()
	writeGitFile(t, filepath.Join(root, ".git"), "gitdir: missing-target\n")
	got := gitMetadataWriteCarveouts(root)
	mustEqualCarveouts(t, got, []string{filepath.Join(root, ".git")})
}

func TestGitMetadataWriteCarveoutsRejectsNonDirectoryTarget(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "not-a-dir")
	writeGitFile(t, target, "nope\n")
	writeGitFile(t, filepath.Join(root, ".git"), "gitdir: "+target+"\n")
	got := gitMetadataWriteCarveouts(root)
	mustEqualCarveouts(t, got, []string{filepath.Join(root, ".git")})
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
	mustEqualCarveouts(t, got, []string{filepath.Join(root, ".git")})
}

func TestGitMetadataWriteCarveoutsResolvesWorktreeUnderSymlinkRoot(t *testing.T) {
	realRoot := t.TempDir()
	commonGit := filepath.Join(realRoot, ".git-common")
	worktreeGit := filepath.Join(realRoot, ".git-wt")
	if err := os.MkdirAll(worktreeGit, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(commonGit, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGitFile(t, filepath.Join(worktreeGit, "commondir"), filepath.Join("..", ".git-common")+"\n")
	writeGitFile(t, filepath.Join(realRoot, ".git"), "gitdir: "+worktreeGit+"\n")
	parent := t.TempDir()
	alias := filepath.Join(parent, "ws")
	if err := os.Symlink(realRoot, alias); err != nil {
		t.Fatal(err)
	}
	got := gitMetadataWriteCarveouts(alias)
	want := []string{
		filepath.Join(alias, ".git"),
		filepath.Join(alias, ".git-common", "hooks"),
		filepath.Join(alias, ".git-common", "config"),
		filepath.Join(alias, ".git-wt", "hooks"),
		filepath.Join(alias, ".git-wt", "config"),
	}
	mustEqualCarveouts(t, got, want)
}

func TestGitMetadataWriteCarveoutsParsesFirstGitdirLine(t *testing.T) {
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git-real")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGitFile(t, filepath.Join(root, ".git"), "gitdir: "+gitDir+"\n# comment\n")
	got := gitMetadataWriteCarveouts(root)
	want := []string{
		filepath.Join(root, ".git"),
		filepath.Join(gitDir, "hooks"),
		filepath.Join(gitDir, "config"),
	}
	mustEqualCarveouts(t, got, want)
}

func TestGitMetadataWriteCarveoutsFollowsSymlinkGitDir(t *testing.T) {
	root := t.TempDir()
	realGit := filepath.Join(root, "real.git")
	if err := os.MkdirAll(realGit, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realGit, filepath.Join(root, ".git")); err != nil {
		t.Fatal(err)
	}
	got := gitMetadataWriteCarveouts(root)
	want := []string{
		filepath.Join(root, ".git", "hooks"),
		filepath.Join(root, ".git", "config"),
	}
	mustEqualCarveouts(t, got, want)
}
