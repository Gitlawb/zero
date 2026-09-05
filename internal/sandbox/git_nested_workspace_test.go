package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

// ZERO MUST NOT SYNTHESIZE A CONTROL DIRECTORY IN SOMEBODY ELSE'S REPOSITORY.
//
// A missing .git used to mean "this directory may become a repository", so the
// Windows plan materialized .git/config and .git/hooks to get the deny ACE in
// place before git first ran. That is right for a standalone directory. It is
// wrong when the workspace is a subdirectory of an existing repository: the
// created .git competes with the ancestor's for git's discovery walk, inside a
// repository Zero does not own.
//
// The metadata for such a workspace belongs to the ancestor, sits outside this
// write root, and the sandboxed principal has no inherited access to it, which
// is the same reason the linked-worktree branch denies only the pointer file.
func TestCarveoutsAreNotSynthesizedInsideAnAncestorRepository(t *testing.T) {
	parent := t.TempDir()
	if err := os.MkdirAll(filepath.Join(parent, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(parent, "sub", "project")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}

	// SETUP: the workspace itself has no .git, or this is a different case.
	if _, err := os.Lstat(filepath.Join(workspace, ".git")); err == nil {
		t.Fatal("SETUP INVALID: the workspace already carries .git")
	}

	specs := gitMetadataWriteCarveoutSpecs(workspace)
	for _, spec := range specs {
		if spec.Path == filepath.Join(workspace, ".git", "config") || spec.Path == filepath.Join(workspace, ".git", "hooks") {
			t.Fatalf("a nested workspace asks to materialize %s, which creates a control directory competing with the ancestor repository at %s", spec.Path, parent)
		}
	}
}

// And a standalone directory keeps the materialized carveouts, or the guard
// above would be satisfied by returning nothing everywhere and the protection
// would be gone for the case it was written for.
func TestCarveoutsStillCoverAStandaloneDirectory(t *testing.T) {
	workspace := t.TempDir()
	if gitMetadataGovernedByAncestor(workspace) {
		t.Skip("this temp directory sits inside a repository, so it is not the standalone case")
	}

	specs := gitMetadataWriteCarveoutSpecs(workspace)
	wantConfig := filepath.Join(workspace, ".git", "config")
	wantHooks := filepath.Join(workspace, ".git", "hooks")
	var sawConfig, sawHooks bool
	for _, spec := range specs {
		switch spec.Path {
		case wantConfig:
			sawConfig = true
			if !spec.IsFile {
				t.Errorf("%s is planned as a directory; creating .git/config as a directory makes git init fail", spec.Path)
			}
		case wantHooks:
			sawHooks = true
		}
	}
	if !sawConfig || !sawHooks {
		t.Fatalf("a standalone directory lost its carveouts: config=%t hooks=%t specs=%+v", sawConfig, sawHooks, specs)
	}
}

// A linked worktree is unchanged: the pointer file is denied and nothing beneath
// it is named, which is what keeps elevated setup from descending through a
// regular file.
func TestCarveoutsStillDenyTheLinkedWorktreePointer(t *testing.T) {
	workspace := t.TempDir()
	pointer := filepath.Join(workspace, ".git")
	if err := os.WriteFile(pointer, []byte("gitdir: ../real/.git/worktrees/w\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	specs := gitMetadataWriteCarveoutSpecs(workspace)
	if len(specs) != 1 || specs[0].Path != pointer || !specs[0].IsFile {
		t.Fatalf("linked worktree carveouts = %+v, want exactly the file-shaped pointer %s", specs, pointer)
	}
}

// The ancestor test itself has to distinguish shapes, because a linked worktree
// or submodule ancestor carries .git as a FILE and still owns this directory.
func TestAncestorDetectionAcceptsBothGitShapes(t *testing.T) {
	for _, shape := range []struct {
		name string
		make func(t *testing.T, parent string)
	}{
		{name: "directory", make: func(t *testing.T, parent string) {
			if err := os.MkdirAll(filepath.Join(parent, ".git"), 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "pointer file", make: func(t *testing.T, parent string) {
			if err := os.WriteFile(filepath.Join(parent, ".git"), []byte("gitdir: elsewhere\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(shape.name, func(t *testing.T) {
			parent := t.TempDir()
			shape.make(t, parent)
			workspace := filepath.Join(parent, "nested", "deep")
			if err := os.MkdirAll(workspace, 0o700); err != nil {
				t.Fatal(err)
			}
			if !gitMetadataGovernedByAncestor(workspace) {
				t.Fatalf("an ancestor carrying .git as a %s was not recognised as governing %s", shape.name, workspace)
			}
		})
	}
}
