package sandbox

import (
	"context"
	"os"
	"testing"
)

func autoAllowBashEngine(t *testing.T, autoAllow bool, backend Backend) *Engine {
	t.Helper()
	policy := DefaultPolicy()
	policy.AutoAllowBashWhenSandboxed = autoAllow
	return NewEngine(EngineOptions{
		WorkspaceRoot: t.TempDir(),
		Policy:        policy,
		Backend:       backend,
	})
}

func bashRequest() Request {
	return Request{
		ToolName:       "bash",
		SideEffect:     SideEffectShell,
		Permission:     PermissionPrompt,
		PermissionMode: PermissionModeAsk,
		Autonomy:       AutonomyHigh,
		Args:           map[string]any{"command": "echo hi"},
	}
}

var nativeWrappingBackend = Backend{
	Name:            BackendLinuxBwrap,
	Available:       true,
	Executable:      "/usr/bin/zero-linux-sandbox",
	CommandWrapping: true,
	NativeIsolation: true,
}

// TestAutoAllowBashWhenSandboxedActive: flag on + a real wrapping backend means
// the bash prompt is skipped (auto-allow), because the sandbox is the safety
// boundary.
func TestAutoAllowBashWhenSandboxedActive(t *testing.T) {
	engine := autoAllowBashEngine(t, true, nativeWrappingBackend)
	decision := engine.Evaluate(context.Background(), bashRequest())
	if decision.Action != ActionAllow {
		t.Fatalf("decision = %#v, want allow (sandbox active + flag on)", decision)
	}
}

// TestAutoAllowBashIgnoredWithoutSandbox: flag on but NO active sandbox
// (policy-only backend) must NOT auto-allow; the normal prompt policy stands.
func TestAutoAllowBashIgnoredWithoutSandbox(t *testing.T) {
	engine := autoAllowBashEngine(t, true, Backend{Name: BackendPolicyOnly})
	decision := engine.Evaluate(context.Background(), bashRequest())
	if decision.Action != ActionPrompt {
		t.Fatalf("decision = %#v, want prompt (no active sandbox, flag must be ignored)", decision)
	}
}

// TestAutoAllowBashOffStillPrompts: flag off + active sandbox keeps the normal
// prompt policy.
func TestAutoAllowBashOffStillPrompts(t *testing.T) {
	engine := autoAllowBashEngine(t, false, nativeWrappingBackend)
	decision := engine.Evaluate(context.Background(), bashRequest())
	if decision.Action != ActionPrompt {
		t.Fatalf("decision = %#v, want prompt (flag off)", decision)
	}
}

// TestAutoAllowBashDisabledPolicyMode: when the policy is disabled the sandbox
// is not enforcing, so the flag must not change the (already-allow) outcome and
// must not error.
func TestAutoAllowBashDoesNotAffectNonShell(t *testing.T) {
	engine := autoAllowBashEngine(t, true, nativeWrappingBackend)
	// A non-shell prompt tool must still prompt; auto-allow is shell-only.
	decision := engine.Evaluate(context.Background(), Request{
		ToolName:       "write_file",
		SideEffect:     SideEffectWrite,
		Permission:     PermissionPrompt,
		PermissionMode: PermissionModeAsk,
		Autonomy:       AutonomyHigh,
		Args:           map[string]any{"path": "notes.txt"},
	})
	if decision.Action != ActionPrompt {
		t.Fatalf("decision = %#v, want prompt (auto-allow is shell-only)", decision)
	}
}

// TestAutoAllowBashEnvSurface verifies the ZERO_SANDBOX_AUTO_ALLOW_BASH env var
// is off by default and only enables on a truthy value, and that the overlay
// never disables an already-enabled config field.
func TestAutoAllowBashEnvSurface(t *testing.T) {
	cases := []struct {
		value string
		set   bool
		want  bool
	}{
		{set: false, want: false},
		{value: "", set: true, want: false},
		{value: "0", set: true, want: false},
		{value: "false", set: true, want: false},
		{value: "nonsense", set: true, want: false},
		{value: "1", set: true, want: true},
		{value: "true", set: true, want: true},
		{value: "TRUE", set: true, want: true},
	}
	for _, tc := range cases {
		if tc.set {
			t.Setenv(EnvAutoAllowBash, tc.value)
		} else {
			os.Unsetenv(EnvAutoAllowBash)
		}
		if got := AutoAllowBashEnvEnabled(); got != tc.want {
			t.Fatalf("AutoAllowBashEnvEnabled() with %q (set=%v) = %v, want %v", tc.value, tc.set, got, tc.want)
		}
		overlaid := ApplyAutoAllowBashEnv(DefaultPolicy())
		if overlaid.AutoAllowBashWhenSandboxed != tc.want {
			t.Fatalf("ApplyAutoAllowBashEnv with %q = %v, want %v", tc.value, overlaid.AutoAllowBashWhenSandboxed, tc.want)
		}
	}

	// The overlay must not clear an already-enabled config opt-in when the env is
	// unset.
	os.Unsetenv(EnvAutoAllowBash)
	enabled := DefaultPolicy()
	enabled.AutoAllowBashWhenSandboxed = true
	if got := ApplyAutoAllowBashEnv(enabled); !got.AutoAllowBashWhenSandboxed {
		t.Fatal("ApplyAutoAllowBashEnv cleared an already-enabled config opt-in")
	}
}

// TestShellSandboxActive reports correctly across backends and policy modes.
func TestShellSandboxActive(t *testing.T) {
	root := t.TempDir()

	native := NewEngine(EngineOptions{WorkspaceRoot: root, Policy: DefaultPolicy(), Backend: nativeWrappingBackend})
	if !native.shellSandboxActive(DefaultPolicy()) {
		t.Fatal("native wrapping backend must be sandbox-active")
	}

	policyOnly := NewEngine(EngineOptions{WorkspaceRoot: root, Policy: DefaultPolicy(), Backend: Backend{Name: BackendPolicyOnly}})
	if policyOnly.shellSandboxActive(DefaultPolicy()) {
		t.Fatal("policy-only backend must NOT be sandbox-active")
	}

	disabled := DefaultPolicy()
	disabled.Mode = ModeDisabled
	if native.shellSandboxActive(disabled) {
		t.Fatal("disabled policy must NOT be sandbox-active")
	}

	var nilEngine *Engine
	if nilEngine.shellSandboxActive(DefaultPolicy()) {
		t.Fatal("nil engine must NOT be sandbox-active")
	}
}
