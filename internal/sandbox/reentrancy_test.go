package sandbox

import "testing"

func TestIsAlreadySandboxed(t *testing.T) {
	t.Setenv(EnvSandboxed, "")
	if IsAlreadySandboxed() {
		t.Fatalf("IsAlreadySandboxed must be false when %s is unset", EnvSandboxed)
	}
	t.Setenv(EnvSandboxed, "1")
	if !IsAlreadySandboxed() {
		t.Fatalf("IsAlreadySandboxed must be true when %s=1", EnvSandboxed)
	}
	t.Setenv(EnvSandboxed, "0")
	if IsAlreadySandboxed() {
		t.Fatalf("IsAlreadySandboxed must be false when %s=0", EnvSandboxed)
	}
}

func TestSandboxEnvironmentMarksSandboxed(t *testing.T) {
	env := sandboxEnvironment(DefaultPolicy(), BackendBubblewrap, "/home/agent")
	want := EnvSandboxed + "=1"
	for _, entry := range env {
		if entry == want {
			return
		}
	}
	t.Fatalf("sandboxEnvironment must set %s so a wrapped command is detectable; got %#v", want, env)
}

func TestBuildCommandPlanWrapsWhenNotAlreadySandboxed(t *testing.T) {
	t.Setenv(EnvSandboxed, "") // ensure we are NOT already inside a sandbox
	root := t.TempDir()
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: root,
		Policy:        DefaultPolicy(),
		Backend:       Backend{Name: BackendBubblewrap, Available: true, Executable: "/usr/bin/bwrap"},
	})
	plan, err := engine.BuildCommandPlan(CommandSpec{Name: "/bin/sh", Args: []string{"-c", "pwd"}, Dir: root})
	if err != nil {
		t.Fatalf("BuildCommandPlan: %v", err)
	}
	if !plan.Wrapped || plan.Name != "/usr/bin/bwrap" {
		t.Fatalf("expected a wrapped bubblewrap plan, got wrapped=%v name=%q", plan.Wrapped, plan.Name)
	}
}

func TestBuildCommandPlanReEntrancyGuardReturnsPassThrough(t *testing.T) {
	t.Setenv(EnvSandboxed, "1") // simulate running inside an already-sandboxed process
	root := t.TempDir()
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: root,
		Policy:        DefaultPolicy(),
		Backend:       Backend{Name: BackendBubblewrap, Available: true, Executable: "/usr/bin/bwrap"},
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
