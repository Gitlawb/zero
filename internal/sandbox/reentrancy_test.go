package sandbox

import (
	"strings"
	"testing"
)

func TestIsAlreadySandboxed(t *testing.T) {
	// A lone ZERO_SANDBOXED=1 (no corroborating backend marker) must NOT count as
	// sandboxed — stronger provenance than a single ambient flag.
	t.Setenv(EnvSandboxBackend, "")
	t.Setenv(EnvSandboxed, "1")
	if IsAlreadySandboxed() {
		t.Fatalf("IsAlreadySandboxed must be false when only %s=1 is set without %s", EnvSandboxed, EnvSandboxBackend)
	}
	// The backend marker alone (no ZERO_SANDBOXED=1) must not count either.
	t.Setenv(EnvSandboxBackend, string(BackendLinuxBwrap))
	t.Setenv(EnvSandboxed, "")
	if IsAlreadySandboxed() {
		t.Fatalf("IsAlreadySandboxed must be false when only %s is set", EnvSandboxBackend)
	}
	// Both correlated markers present (as sandboxEnvironment sets them) → sandboxed.
	t.Setenv(EnvSandboxed, "1")
	if !IsAlreadySandboxed() {
		t.Fatalf("IsAlreadySandboxed must be true when both %s=1 and %s are set", EnvSandboxed, EnvSandboxBackend)
	}
	t.Setenv(EnvSandboxed, "0")
	if IsAlreadySandboxed() {
		t.Fatalf("IsAlreadySandboxed must be false when %s=0", EnvSandboxed)
	}
}

func TestSandboxEnvironmentMarksSandboxed(t *testing.T) {
	env := sandboxEnvironment(DefaultPolicy(), BackendLinuxBwrap, "/home/agent")
	// Both correlated markers must be set: IsAlreadySandboxed requires BOTH, so a
	// regression that drops either one would silently break re-entrancy detection.
	wantSandboxed := EnvSandboxed + "=1"
	wantBackend := EnvSandboxBackend + "=" + string(BackendLinuxBwrap)
	var gotSandboxed, gotBackend bool
	for _, entry := range env {
		switch entry {
		case wantSandboxed:
			gotSandboxed = true
		case wantBackend:
			gotBackend = true
		}
	}
	if !gotSandboxed || !gotBackend {
		t.Fatalf("sandboxEnvironment must set %q and %q so a wrapped command is detectable; got %#v", wantSandboxed, wantBackend, env)
	}
}

func TestSandboxEnvironmentPreservesCallerEnv(t *testing.T) {
	env := sandboxEnvironmentForCommand(
		[]string{
			"PATH=/custom/bin",
			"TERM=xterm-256color",
			"HOME=/home/user",
			EnvSandboxed + "=0",
			EnvSandboxBackend + "=other",
		},
		DefaultPolicy(),
		BackendMacOSSeatbelt,
	)

	for _, want := range []string{
		"TERM=xterm-256color",
		"HOME=/home/user",
		EnvSandboxed + "=1",
		EnvSandboxBackend + "=" + string(BackendMacOSSeatbelt),
		"ZERO_SANDBOX_NETWORK=deny",
	} {
		if !stringSliceContains(env, want) {
			t.Fatalf("sandbox env = %#v, missing %q", env, want)
		}
	}
	path := envListValue(env, "PATH", "")
	if !pathListContains(path, "/custom/bin") {
		t.Fatalf("sandbox PATH = %q, want it to preserve caller path segment /custom/bin", path)
	}
	if stringSliceContains(env, EnvSandboxed+"=0") || stringSliceContains(env, EnvSandboxBackend+"=other") {
		t.Fatalf("sandbox env did not replace stale sandbox markers: %#v", env)
	}
}

func pathListContains(path string, want string) bool {
	for _, entry := range strings.Split(path, ":") {
		if entry == want {
			return true
		}
	}
	return false
}

func TestBuildCommandPlanWrapsWhenNotAlreadySandboxed(t *testing.T) {
	// Ensure neither re-entrancy marker is set so we are NOT seen as sandboxed.
	t.Setenv(EnvSandboxed, "")
	t.Setenv(EnvSandboxBackend, "")
	root := t.TempDir()
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: root,
		Policy:        DefaultPolicy(),
		Backend:       Backend{Name: BackendLinuxBwrap, Available: true, Executable: "/usr/bin/zero-linux-sandbox"},
	})
	plan, err := engine.BuildCommandPlan(CommandSpec{Name: "/bin/sh", Args: []string{"-c", "pwd"}, Dir: root})
	if err != nil {
		t.Fatalf("BuildCommandPlan: %v", err)
	}
	if !plan.Wrapped || plan.Name != "/usr/bin/zero-linux-sandbox" {
		t.Fatalf("expected a wrapped Linux helper plan, got wrapped=%v name=%q", plan.Wrapped, plan.Name)
	}
}

func TestBuildCommandPlanReEntrancyGuardReturnsPassThrough(t *testing.T) {
	// Simulate running inside an already-wrapped process: BOTH correlated markers
	// that sandboxEnvironment sets together must be present for the guard to fire.
	t.Setenv(EnvSandboxed, "1")
	t.Setenv(EnvSandboxBackend, string(BackendLinuxBwrap))
	root := t.TempDir()
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: root,
		Policy:        DefaultPolicy(),
		Backend:       Backend{Name: BackendLinuxBwrap, Available: true, Executable: "/usr/bin/zero-linux-sandbox"},
	})
	plan, err := engine.BuildCommandPlan(CommandSpec{Name: "/bin/sh", Args: []string{"-c", "pwd"}, Dir: root})
	if err != nil {
		t.Fatalf("BuildCommandPlan: %v", err)
	}
	if plan.Wrapped {
		t.Fatalf("re-entrancy guard must return an unwrapped pass-through plan, got wrapped=%v name=%q args=%v", plan.Wrapped, plan.Name, plan.Args)
	}
	if plan.Name != "/bin/sh" {
		t.Fatalf("pass-through plan must run the command directly, got name=%q", plan.Name)
	}
}
