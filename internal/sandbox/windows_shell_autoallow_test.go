package sandbox

import (
	"context"
	"testing"
)

// The engine's shell auto-allow must track the ACTUAL runtime enforcement, not
// just backend capability: on Windows the command is only wrapped once setup has
// run. Degraded (no marker) -> an ordinary shell command must PROMPT (per-command
// approval is the floor); native (marker present) -> auto-allow (the OS sandbox
// is the boundary). Regression guard for the #295 degrade gap.
func TestEngineWindowsShellAutoAllowTracksSetup(t *testing.T) {
	restore := windowsSandboxInitialized
	t.Cleanup(func() { windowsSandboxInitialized = restore })

	eng := NewEngine(EngineOptions{
		WorkspaceRoot: `C:\ws`,
		Policy:        DefaultPolicy(),
		Backend: Backend{
			Name: BackendWindowsRestrictedToken, Available: true, Executable: "zero.exe",
			Platform: "windows", CommandWrapping: true, NativeIsolation: true,
		},
	})
	req := Request{
		ToolName: "bash", SideEffect: SideEffectShell,
		Permission: PermissionPrompt, PermissionMode: PermissionModeAsk,
		Args: map[string]any{"command": "echo hi"},
	}

	// Degraded: setup not run -> not actually sandboxed -> must prompt.
	windowsSandboxInitialized = func() bool { return false }
	if eng.shellSandboxActive(DefaultPolicy()) {
		t.Fatal("degraded: shellSandboxActive must be false (command runs unwrapped)")
	}
	if d := eng.Evaluate(context.Background(), req); d.Action != ActionPrompt {
		t.Fatalf("degraded ordinary shell command must PROMPT, got action=%q reason=%q", d.Action, d.Reason)
	}

	// Native: setup done -> the sandbox is the boundary -> auto-allow.
	windowsSandboxInitialized = func() bool { return true }
	if !eng.shellSandboxActive(DefaultPolicy()) {
		t.Fatal("native: shellSandboxActive must be true")
	}
	if d := eng.Evaluate(context.Background(), req); d.Action != ActionAllow || !d.AutoAllowed {
		t.Fatalf("native ordinary shell command must AUTO-ALLOW, got action=%q autoAllowed=%v", d.Action, d.AutoAllowed)
	}
}
