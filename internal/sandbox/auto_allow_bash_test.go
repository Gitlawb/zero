package sandbox

import (
	"context"
	"testing"
)

func sandboxedShellEngine(t *testing.T, backend Backend) *Engine {
	t.Helper()
	return NewEngine(EngineOptions{
		WorkspaceRoot: t.TempDir(),
		Policy:        DefaultPolicy(),
		Backend:       backend,
	})
}

func bashRequest() Request {
	return Request{
		ToolName:       "bash",
		SideEffect:     SideEffectShell,
		Permission:     PermissionPrompt,
		PermissionMode: PermissionModeAsk,
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

func TestSandboxedBashAutoAllowedWhenSandboxActive(t *testing.T) {
	engine := sandboxedShellEngine(t, nativeWrappingBackend)
	decision := engine.Evaluate(context.Background(), bashRequest())
	if decision.Action != ActionAllow {
		t.Fatalf("decision = %#v, want allow (sandbox active)", decision)
	}
}

func TestSandboxedBashRequireEscalatedStillPromptsWhenSandboxActive(t *testing.T) {
	engine := sandboxedShellEngine(t, nativeWrappingBackend)
	request := bashRequest()
	request.Args["sandbox_permissions"] = "require_escalated"

	decision := engine.Evaluate(context.Background(), request)
	if decision.Action != ActionPrompt || decision.Reason != ReasonEscalatedSandboxRequired {
		t.Fatalf("decision = %#v, want require_escalated prompt", decision)
	}
}

func TestSandboxedBashStillPromptsWithoutSandbox(t *testing.T) {
	engine := sandboxedShellEngine(t, Backend{Name: BackendUnavailable})
	decision := engine.Evaluate(context.Background(), bashRequest())
	if decision.Action != ActionPrompt {
		t.Fatalf("decision = %#v, want prompt (no active sandbox)", decision)
	}
}

func TestSandboxedBashAutoAllowDoesNotAffectNonShell(t *testing.T) {
	engine := sandboxedShellEngine(t, nativeWrappingBackend)
	// A non-shell prompt tool must still prompt; auto-allow is shell-only. Use a
	// generic write tool name so the workspace file-tool auto-allow does not
	// apply.
	decision := engine.Evaluate(context.Background(), Request{
		ToolName:       "custom_writer",
		SideEffect:     SideEffectWrite,
		Permission:     PermissionPrompt,
		PermissionMode: PermissionModeAsk,
		Args:           map[string]any{"path": "notes.txt"},
	})
	if decision.Action != ActionPrompt {
		t.Fatalf("decision = %#v, want prompt (auto-allow is shell-only)", decision)
	}
}

// TestShellSandboxActive reports correctly across backends and policy modes.
func TestShellSandboxActive(t *testing.T) {
	root := t.TempDir()

	native := NewEngine(EngineOptions{WorkspaceRoot: root, Policy: DefaultPolicy(), Backend: nativeWrappingBackend})
	if !native.shellSandboxActive(DefaultPolicy()) {
		t.Fatal("native wrapping backend must be sandbox-active")
	}

	unavailable := NewEngine(EngineOptions{WorkspaceRoot: root, Policy: DefaultPolicy(), Backend: Backend{Name: BackendUnavailable}})
	if unavailable.shellSandboxActive(DefaultPolicy()) {
		t.Fatal("unavailable backend must NOT be sandbox-active")
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
