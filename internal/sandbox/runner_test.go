package sandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBuildCommandPlanWrapsBubblewrap(t *testing.T) {
	root := t.TempDir()
	resolvedRoot := resolvedTestPath(t, root)
	nested := filepath.Join(root, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: root,
		Policy:        DefaultPolicy(),
		Backend: Backend{
			Name:       BackendBubblewrap,
			Available:  true,
			Executable: "/usr/bin/bwrap",
			Message:    "bubblewrap sandbox available",
		},
	})

	plan, err := engine.BuildCommandPlan(CommandSpec{
		Name: "/bin/sh",
		Args: []string{"-c", "pwd"},
		Dir:  nested,
	})
	if err != nil {
		t.Fatalf("BuildCommandPlan: %v", err)
	}

	if !plan.Wrapped || plan.Name != "/usr/bin/bwrap" || plan.Backend.Name != BackendBubblewrap {
		t.Fatalf("plan backend = %#v, want wrapped bubblewrap", plan)
	}
	assertArgsContainSequence(t, plan.Args, "--bind", resolvedRoot, bubblewrapWorkspace)
	assertArgsContainSequence(t, plan.Args, "--chdir", bubblewrapWorkspace+"/nested")
	assertArgsContainSequence(t, plan.Args, "--unshare-net")
	assertArgsContainSequence(t, plan.Args, "--", "/bin/sh", "-c", "pwd")
	if plan.SandboxDir != bubblewrapWorkspace+"/nested" {
		t.Fatalf("SandboxDir = %q, want nested workspace dir", plan.SandboxDir)
	}
	if plan.Dir != "" {
		t.Fatalf("bubblewrap host Dir = %q, want empty because bwrap owns chdir", plan.Dir)
	}
}

func TestBuildCommandPlanWrapsSandboxExec(t *testing.T) {
	root := t.TempDir()
	resolvedRoot := resolvedTestPath(t, root)
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: root,
		Policy:        DefaultPolicy(),
		Backend: Backend{
			Name:       BackendSandboxExec,
			Available:  true,
			Executable: "/usr/bin/sandbox-exec",
			Message:    "sandbox-exec backend available",
		},
	})

	plan, err := engine.BuildCommandPlan(CommandSpec{
		Name: "/bin/sh",
		Args: []string{"-c", "pwd"},
		Dir:  root,
	})
	if err != nil {
		t.Fatalf("BuildCommandPlan: %v", err)
	}

	if !plan.Wrapped || plan.Name != "/usr/bin/sandbox-exec" || plan.Backend.Name != BackendSandboxExec {
		t.Fatalf("plan backend = %#v, want wrapped sandbox-exec", plan)
	}
	if len(plan.Args) < 5 || plan.Args[0] != "-p" {
		t.Fatalf("sandbox-exec args = %#v, want profile and command", plan.Args)
	}
	profile := plan.Args[1]
	for _, want := range []string{
		"(deny default)",
		"(deny network*)",
		`(subpath "` + sandboxProfileString(resolvedRoot) + `")`,
		`(literal "/dev/null")`,
		`(subpath "/private/tmp")`,
	} {
		if !strings.Contains(profile, want) {
			t.Fatalf("profile missing %q:\n%s", want, profile)
		}
	}
	assertArgsContainSequence(t, plan.Args, "/bin/sh", "-c", "pwd")
	if plan.Dir != resolvedRoot || plan.SandboxDir != resolvedRoot {
		t.Fatalf("sandbox-exec dirs = host %q sandbox %q, want %q", plan.Dir, plan.SandboxDir, resolvedRoot)
	}
}

func TestBuildCommandPlanUsesPolicyOnlyFallback(t *testing.T) {
	root := t.TempDir()
	resolvedRoot := resolvedTestPath(t, root)
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: root,
		Policy:        DefaultPolicy(),
		Backend:       Backend{Name: BackendPolicyOnly, Message: "policy-only fallback"},
	})

	plan, err := engine.BuildCommandPlan(CommandSpec{
		Name: "/bin/sh",
		Args: []string{"-c", "pwd"},
		Dir:  root,
	})
	if err != nil {
		t.Fatalf("BuildCommandPlan: %v", err)
	}

	if plan.Wrapped || plan.Name != "/bin/sh" || plan.Dir != resolvedRoot || plan.WorkspaceRoot != resolvedRoot || plan.Backend.Name != BackendPolicyOnly {
		t.Fatalf("policy-only plan = %#v, want direct command", plan)
	}
}

func TestBuildCommandPlanCanRejectPolicyOnlyFallback(t *testing.T) {
	root := t.TempDir()
	policy := DefaultPolicy()
	policy.AllowPolicyOnlyRunner = false
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: root,
		Policy:        policy,
		Backend:       Backend{Name: BackendPolicyOnly, Message: "policy-only fallback"},
	})

	_, err := engine.BuildCommandPlan(CommandSpec{Name: "/bin/sh", Dir: root})
	if !errors.Is(err, errPolicyOnlyRunnerDisabled) {
		t.Fatalf("error = %v, want policy-only disabled", err)
	}
}

func TestBuildCommandPlanRejectsOutsideDirectory(t *testing.T) {
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: t.TempDir(),
		Policy:        DefaultPolicy(),
		Backend:       Backend{Name: BackendPolicyOnly},
	})

	_, err := engine.BuildCommandPlan(CommandSpec{Name: "/bin/sh", Dir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "outside_workspace") {
		t.Fatalf("error = %v, want outside workspace violation", err)
	}
}

func assertArgsContainSequence(t *testing.T, args []string, sequence ...string) {
	t.Helper()
	if len(sequence) == 0 {
		return
	}
	for index := 0; index <= len(args)-len(sequence); index++ {
		matched := true
		for offset, want := range sequence {
			if args[index+offset] != want {
				matched = false
				break
			}
		}
		if matched {
			return
		}
	}
	t.Fatalf("args %#v do not contain sequence %#v", args, sequence)
}

// TestSandboxExecProfileAllowsDevNullAndTemp reproduces the audit finding that
// the generated sandbox-exec profile blocked `> /dev/null` and mktemp because
// only the workspace was writable. It runs real commands through sandbox-exec
// when that backend is available on the host.
func TestSandboxExecProfileAllowsDevNullAndTemp(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox-exec is macOS-only")
	}
	backend := SelectBackend(BackendOptions{})
	if !backend.Available || backend.Name != BackendSandboxExec {
		t.Skipf("sandbox-exec backend unavailable: %s", backend.Message)
	}
	root := t.TempDir()
	engine := NewEngine(EngineOptions{WorkspaceRoot: root, Policy: DefaultPolicy(), Backend: backend})

	run := func(script string) (string, error) {
		command, _, err := engine.CommandContext(context.Background(), CommandSpec{
			Name: "/bin/sh",
			Args: []string{"-c", script},
			Dir:  root,
		})
		if err != nil {
			return "", err
		}
		out, runErr := command.CombinedOutput()
		return string(out), runErr
	}

	for _, script := range []string{"echo hi > /dev/null", "mktemp"} {
		if out, err := run(script); err != nil {
			t.Fatalf("sandboxed %q failed: %v\noutput: %s", script, err, out)
		}
	}

	// The workspace remains writable; a sibling write still lands.
	if out, err := run("echo ok > probe.txt && cat probe.txt"); err != nil {
		t.Fatalf("workspace write failed: %v\noutput: %s", err, out)
	}
}

func resolvedTestPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", path, err)
	}
	return resolved
}
