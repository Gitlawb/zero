//go:build windows

package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Setup grants the principal a runtime tree; every command derives that tree
// again from the workspace root. If the two normalize differently the grant
// lands somewhere nothing reads, and the only symptom is a bare ACCESS_DENIED
// on the first cache write.
//
// This needs no symlink and no privilege. Windows opens a path whatever its
// casing, and Engine.resolveCommandDir runs EvalSymlinks (runner.go) which
// canonicalizes it, while setup used to only Clean.
func TestSetupRuntimeRootMatchesTheCommandPathForADifferentlyCasedRoot(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "MyWorkspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	lowered := strings.ToLower(workspace)
	if _, err := os.Stat(lowered); err != nil {
		t.Skipf("filesystem is case-sensitive here: %v", err)
	}
	if lowered == workspace {
		t.Skip("temp path has no case to differ on")
	}

	// What the command path ends up keyed to, per resolveCommandDir.
	commandRoot := lowered
	if resolved, err := filepath.EvalSymlinks(filepath.Clean(lowered)); err == nil {
		commandRoot = resolved
	}
	if commandRoot == lowered {
		t.Skip("EvalSymlinks changed nothing on this host; no divergence to assert")
	}

	// Drive the PRODUCTION derivation, not the helper. A test that called
	// canonicalWindowsSandboxWorkspaceRoot directly would pass just as happily
	// with setup still doing filepath.Clean, which is exactly the bug.
	fromSetup, err := windowsSandboxRuntimeRootPath(WindowsSandboxCommandConfig{
		WorkspaceRoots: []string{lowered},
		CommandCWD:     lowered,
	})
	if err != nil {
		t.Fatalf("windowsSandboxRuntimeRootPath: %v", err)
	}
	cacheRoot, err := sandboxUserCacheDir()
	if err != nil {
		t.Fatalf("sandboxUserCacheDir: %v", err)
	}
	fromCommand, err := sandboxRuntimeRootFor(commandRoot, filepath.Clean(cacheRoot))
	if err != nil {
		t.Fatalf("sandboxRuntimeRootFor(command): %v", err)
	}
	if fromSetup != fromCommand {
		t.Errorf("setup grants a runtime tree commands never use:\n  setup:   %s\n  command: %s", fromSetup, fromCommand)
	}
}

// An unresolvable root still needs a stable key rather than an empty one.
func TestCanonicalWorkspaceRootFallsBackWhenResolutionFails(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "never-created", "deeper")
	if got := canonicalWindowsSandboxWorkspaceRoot(missing); got != filepath.Clean(missing) {
		t.Errorf("canonical(%q) = %q, want the cleaned path", missing, got)
	}
	if canonicalWindowsSandboxWorkspaceRoot("   ") != "" {
		t.Error("a blank root should stay blank, not become the process directory")
	}
}
