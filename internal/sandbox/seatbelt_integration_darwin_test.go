//go:build darwin

package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSeatbeltEnforcesExtraWriteRoots runs real /bin/sh commands under
// sandbox-exec to prove the OS kernel enforces the scope's write roots: writes
// inside the workspace and inside a user-granted extra root succeed, while a
// write outside every root is denied by seatbelt itself (not merely by the
// policy preflight). It gates on the host backend the same way
// TestBashToolRunsWithHostSandboxBackendWhenAvailable does in
// internal/tools/bash_tool_test.go.
func TestSeatbeltEnforcesExtraWriteRoots(t *testing.T) {
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		t.Skipf("sandbox-exec unavailable: %v", err)
	}
	backend := SelectBackend(BackendOptions{})
	if !backend.Available || backend.Name != BackendMacOSSeatbelt {
		t.Skipf("host sandbox backend is not sandbox-exec: %s", backend.Message)
	}

	workspace := t.TempDir()
	extra := t.TempDir()
	// The "outside" target must live OUTSIDE the sandbox's baseline-writable temp
	// trees: the profile now allows /tmp and /var/folders for parity with the
	// bubblewrap backend (--tmpfs /tmp, --dev /dev). t.TempDir() lives under
	// $TMPDIR (/var/folders on macOS), which is writable, so a write there would no
	// longer be denied. Use a directory under $HOME to prove the kernel denies a
	// write to an ungranted, non-temp location.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir for an out-of-allowlist write target: %v", err)
	}
	outside, err := os.MkdirTemp(home, ".zero-seatbelt-outside-")
	if err != nil {
		t.Fatalf("MkdirTemp under home: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(outside) })
	scope, err := NewScope(workspace, []string{extra})
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: workspace,
		Policy:        DefaultPolicy(),
		Scope:         scope,
		Backend:       backend,
	})

	// Scope roots are symlink-resolved (macOS /var -> /private/var), and the
	// seatbelt profile allows the resolved subpaths. Use resolved paths for the
	// write targets so success/denial assertions compare like with like.
	resolvedWorkspace := scope.Roots()[0]
	resolvedExtra := scope.Roots()[1]
	resolvedOutside := resolvedTestPath(t, outside)

	t.Run("WriteInsideExtraRootSucceeds", func(t *testing.T) {
		target := filepath.Join(resolvedExtra, "granted.txt")
		output, runErr := runSeatbeltShellWrite(t, engine, target, "extra-ok")
		if runErr != nil {
			t.Fatalf("write inside extra root failed: %v\noutput: %s", runErr, output)
		}
		assertSeatbeltFileContent(t, target, "extra-ok")
	})

	t.Run("WriteOutsideAllRootsIsDenied", func(t *testing.T) {
		// Denial only holds if the target isn't under a baseline-writable subpath.
		// Some environments set $HOME under $TMPDIR; skip there since the sandbox
		// legitimately allows temp writes and denial can't be demonstrated.
		for _, sub := range sandboxWritableSubpaths {
			if resolvedOutside == sub || strings.HasPrefix(resolvedOutside, sub+string(os.PathSeparator)) {
				t.Skipf("outside path %s is under writable subpath %s; cannot demonstrate denial", resolvedOutside, sub)
			}
		}
		target := filepath.Join(resolvedOutside, "denied.txt")
		output, runErr := runSeatbeltShellWrite(t, engine, target, "must-not-exist")
		if runErr == nil {
			t.Fatalf("write outside all roots succeeded, want seatbelt denial\noutput: %s", output)
		}
		if _, statErr := os.Lstat(target); !os.IsNotExist(statErr) {
			t.Fatalf("Lstat(%s) = %v, want not-exist", target, statErr)
		}
	})

	t.Run("WriteInsideWorkspaceSucceeds", func(t *testing.T) {
		target := filepath.Join(resolvedWorkspace, "workspace.txt")
		output, runErr := runSeatbeltShellWrite(t, engine, target, "workspace-ok")
		if runErr != nil {
			t.Fatalf("write inside workspace failed: %v\noutput: %s", runErr, output)
		}
		assertSeatbeltFileContent(t, target, "workspace-ok")
	})
}

// runSeatbeltShellWrite launches /bin/sh through the engine's sandbox-exec
// wrapping and asks it to write content to target. It returns the combined
// output and the run error so callers can assert success or kernel denial.
func runSeatbeltShellWrite(t *testing.T, engine *Engine, target string, content string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	script := "printf %s '" + content + "' > '" + target + "'"
	command, plan, err := engine.CommandContext(ctx, CommandSpec{
		Name: "/bin/sh",
		Args: []string{"-c", script},
	})
	if err != nil {
		t.Fatalf("CommandContext: %v", err)
	}
	if !plan.Wrapped || plan.Backend.Name != BackendMacOSSeatbelt {
		t.Fatalf("plan = %#v, want wrapped sandbox-exec", plan)
	}
	output, runErr := command.CombinedOutput()
	return string(output), runErr
}

func assertSeatbeltFileContent(t *testing.T, target string, want string) {
	t.Helper()
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read back %s: %v", target, err)
	}
	if string(content) != want {
		t.Fatalf("content of %s = %q, want %q", target, content, want)
	}
}
