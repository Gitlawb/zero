package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// nestedGitWorkspace returns an ancestor repository and a workspace inside it
// that carries no git metadata of its own, which is the shape the whole refusal
// is about.
func nestedGitWorkspace(t *testing.T) (ancestor string, workspace string) {
	t.Helper()
	ancestor = t.TempDir()
	if err := os.MkdirAll(filepath.Join(ancestor, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	workspace = filepath.Join(ancestor, "packages", "app")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(workspace, ".git")); err == nil {
		t.Fatal("SETUP INVALID: the workspace carries its own .git, which is the protected case, not this one")
	}
	return ancestor, workspace
}

func gitCommandRequest(workspace string, command string) Request {
	return Request{
		ToolName:       "bash",
		SideEffect:     SideEffectShell,
		Permission:     PermissionPrompt,
		PermissionMode: PermissionModeAsk,
		WorkspaceRoot:  workspace,
		Args:           map[string]any{"command": command},
	}
}

func gitWorkspaceEngine(t *testing.T, workspace string) *Engine {
	t.Helper()
	return NewEngine(EngineOptions{
		WorkspaceRoot: workspace,
		Policy:        DefaultPolicy(),
		Backend:       nativeWrappingBackend,
	})
}

// THE LIFECYCLE, END TO END: setup leaves this workspace with no git protection,
// so the command that would create the thing needing protection is refused.
//
// The setup guard is the load-bearing half. If gitMetadataWriteCarveouts ever
// starts returning config and hooks for a nested workspace again, the premise of
// the refusal is gone and this fails here rather than quietly guarding nothing.
func TestNestedWorkspaceRefusesGitInit(t *testing.T) {
	ancestor, workspace := nestedGitWorkspace(t)

	if carveouts := gitMetadataWriteCarveouts(workspace); len(carveouts) != 0 {
		t.Fatalf("SETUP INVALID: the nested workspace planned %v, so it does have git protection and there is nothing to refuse", carveouts)
	}

	engine := gitWorkspaceEngine(t, workspace)
	decision := engine.Evaluate(context.Background(), gitCommandRequest(workspace, "git init"))
	if decision.Action != ActionDeny {
		t.Fatalf("git init inside the repository at %s was %s, not denied; the repository it creates gets a writable config and hooks under the workspace grant", ancestor, decision.Action)
	}
	if decision.Block == nil || decision.Block.Code != BlockNestedGitInit {
		t.Fatalf("block = %#v, want code %s so the operator gets the nested-repository explanation and not a generic denial", decision.Block, BlockNestedGitInit)
	}
}

// The refusal sits ahead of every allow path in Evaluate, so a tool already
// cleared by permissions does not walk through it.
func TestNestedGitInitRefusalOutranksAnAllowPermission(t *testing.T) {
	_, workspace := nestedGitWorkspace(t)
	engine := gitWorkspaceEngine(t, workspace)

	request := gitCommandRequest(workspace, "git init")
	request.Permission = PermissionAllow
	if decision := engine.Evaluate(context.Background(), request); decision.Action != ActionDeny {
		t.Fatalf("an allow permission carried git init through: %#v", decision)
	}

	request = gitCommandRequest(workspace, "git init")
	request.PermissionMode = PermissionUnsafe
	request.PermissionGranted = true
	if decision := engine.Evaluate(context.Background(), request); decision.Action != ActionDeny {
		t.Fatalf("unsafe mode carried git init through: %#v", decision)
	}
}

// The global options that bypassed the network gate must not bypass this one.
// These are the exact spellings that classified a clone as network=false before
// gitSubcommand learned which options take a value.
func TestNestedGitInitRefusalSurvivesGitGlobalOptions(t *testing.T) {
	_, workspace := nestedGitWorkspace(t)
	engine := gitWorkspaceEngine(t, workspace)

	for _, command := range []string{
		"git init",
		"git init-db",
		"git -C packages/app init",
		"git -c core.hooksPath=/tmp/h init",
		"git --git-dir=/tmp/g init",
		"git --namespace ns init",
		"sh -c \"git init\"",
		"sudo git init",
		"git status && git init",
	} {
		t.Run(command, func(t *testing.T) {
			decision := engine.Evaluate(context.Background(), gitCommandRequest(workspace, command))
			if decision.Action != ActionDeny {
				t.Fatalf("%q was %s, not denied", command, decision.Action)
			}
		})
	}
}

// CONTROL: ordinary git in a nested workspace is untouched.
//
// Discovery still walks up to the ancestor and every command that uses it keeps
// working; only creation is refused. Without this, returning deny for anything
// containing the word git would pass the tests above.
func TestNestedWorkspaceStillRunsOrdinaryGitCommands(t *testing.T) {
	_, workspace := nestedGitWorkspace(t)
	engine := gitWorkspaceEngine(t, workspace)

	for _, command := range []string{
		"git status",
		"git add .",
		"git commit -m init",
		"git log --oneline",
		"git worktree list",
		"git submodule update --init",
	} {
		t.Run(command, func(t *testing.T) {
			decision := engine.Evaluate(context.Background(), gitCommandRequest(workspace, command))
			if decision.Action == ActionDeny {
				t.Fatalf("%q was denied in a nested workspace: %s", command, decision.Reason)
			}
		})
	}
}

// CONTROL: a standalone workspace still creates repositories.
//
// It keeps its materialized config and hooks carveouts, so the protection the
// refusal stands in for is actually there and the command is allowed.
func TestStandaloneWorkspaceStillAllowsGitInit(t *testing.T) {
	workspace := t.TempDir()
	if gitMetadataGovernedByAncestor(workspace) {
		t.Skip("this temp directory sits inside a repository, so it is not the standalone case")
	}
	if len(gitMetadataWriteCarveouts(workspace)) == 0 {
		t.Fatal("SETUP INVALID: a standalone workspace planned no carveouts, so it has no protection to allow git init against")
	}

	engine := gitWorkspaceEngine(t, workspace)
	decision := engine.Evaluate(context.Background(), gitCommandRequest(workspace, "git init"))
	if decision.Action == ActionDeny {
		t.Fatalf("git init was refused in a standalone workspace: %s", decision.Reason)
	}
}

// CONTROL: a linked worktree is a workspace whose .git is a FILE.
//
// It owns git metadata, keeps its pointer-file carveout, and is not somebody
// else's repository, so nothing here is refused. Zero's own development
// worktrees are this shape, so getting it wrong would refuse git init for the
// people writing this code.
func TestLinkedWorktreeWorkspaceStillAllowsGitInit(t *testing.T) {
	ancestor := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ancestor, ".git", "worktrees", "w"), 0o700); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(ancestor, "checkout")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	pointer := filepath.Join(workspace, ".git")
	if err := os.WriteFile(pointer, []byte("gitdir: ../.git/worktrees/w\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// SETUP: the pointer carveout is intact, or this proves nothing about
	// worktrees keeping their protection.
	specs := gitMetadataWriteCarveoutSpecs(workspace)
	if len(specs) != 1 || specs[0].Path != pointer || !specs[0].IsFile {
		t.Fatalf("SETUP INVALID: linked worktree carveouts = %+v, want the file-shaped pointer %s", specs, pointer)
	}

	engine := gitWorkspaceEngine(t, workspace)
	if decision := engine.Evaluate(context.Background(), gitCommandRequest(workspace, "git init")); decision.Action == ActionDeny {
		t.Fatalf("git init was refused in a linked worktree, which owns its git metadata: %s", decision.Reason)
	}
}

// And the workspace-level question is asked of the workspace itself, so a
// workspace that already carries .git is never treated as nested no matter what
// is above it.
func TestWorkspaceOwningGitIsNotTreatedAsNested(t *testing.T) {
	ancestor := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ancestor, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(ancestor, "vendored")
	if err := os.MkdirAll(filepath.Join(workspace, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if workspaceGovernedByAncestorRepository(workspace) {
		t.Fatal("a workspace that owns .git was reported as governed by its ancestor, so its own protection would be refused instead of applied")
	}
}
