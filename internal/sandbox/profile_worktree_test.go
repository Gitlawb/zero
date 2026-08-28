package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

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

// Worktree/submodule checkouts store .git as a pointer file. Protect the file
// itself and never create a bwrap mount target beneath it.
func TestGitMetadataWriteCarveoutsProtectsWorktreePointer(t *testing.T) {
	root := t.TempDir()
	gitPath := filepath.Join(root, ".git")
	if err := os.WriteFile(gitPath, []byte("gitdir: ../outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := gitMetadataWriteCarveouts(root)
	mustEqualCarveouts(t, got, []string{gitPath})
	for _, carveout := range got {
		if carveout == filepath.Join(gitPath, "hooks") || carveout == filepath.Join(gitPath, "config") {
			t.Fatalf("carveout points beneath .git file: %q", carveout)
		}
	}
}

func TestLinuxBwrapPlanDoesNotMountBelowWorktreePointer(t *testing.T) {
	root := t.TempDir()
	gitPath := filepath.Join(root, ".git")
	if err := os.WriteFile(gitPath, []byte("gitdir: ../outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	profile := PermissionProfile{FileSystem: FileSystemPolicy{
		Kind: FileSystemRestricted,
		WriteRoots: []WritableRoot{{
			Root:             root,
			ReadOnlySubpaths: gitMetadataWriteCarveouts(root),
		}},
	}}
	args := linuxBwrapFilesystemArgs(profile)
	assertArgsContainSequence(t, args, "--ro-bind", gitPath, gitPath)
	for _, child := range []string{"hooks", "config"} {
		bogus := filepath.Join(gitPath, child)
		if argsContainSequence(args, "--tmpfs", bogus) || argsContainSequence(args, "--ro-bind", bogus, bogus) {
			t.Fatalf("bwrap args contain mount below .git file %q: %v", bogus, args)
		}
	}
}

func TestGitMetadataWriteCarveoutsPlainCheckout(t *testing.T) {
	root := t.TempDir()
	want := []string{
		filepath.Join(root, ".git", "hooks"),
		filepath.Join(root, ".git", "config"),
	}
	mustEqualCarveouts(t, gitMetadataWriteCarveouts(root), want)

	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustEqualCarveouts(t, gitMetadataWriteCarveouts(root), want)
}

func TestGitMetadataWriteCarveoutsPreservesDirectorySymlinkBehavior(t *testing.T) {
	root := t.TempDir()
	realGit := filepath.Join(root, "real.git")
	if err := os.MkdirAll(realGit, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realGit, filepath.Join(root, ".git")); err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join(root, ".git", "hooks"),
		filepath.Join(root, ".git", "config"),
	}
	mustEqualCarveouts(t, gitMetadataWriteCarveouts(root), want)
}
