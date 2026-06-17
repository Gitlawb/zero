package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/sandbox"
)

// nativeBackendStub reports as an active wrapping sandbox so shellSandboxActive
// is true. Its executable does not exist, so the command itself fails to launch
// — that is fine: these tests assert the *permission gate*, not execution.
func nativeBackendStub() sandbox.Backend {
	return sandbox.Backend{
		Name:            sandbox.BackendLinuxBwrap,
		Available:       true,
		Executable:      "/nonexistent/zero-linux-sandbox-stub",
		CommandWrapping: true,
		NativeIsolation: true,
	}
}

func autoAllowBashPolicy(autoAllow bool) sandbox.Policy {
	policy := sandbox.DefaultPolicy()
	policy.AutoAllowBashWhenSandboxed = autoAllow
	// Network deny so no proxy is started for these gate-only tests.
	return policy
}

const permissionRequiredFragment = "Permission required for bash"

// TestBashAutoAllowedWhenSandboxActive: flag on + active sandbox => the bash
// permission gate is bypassed (no "Permission required" error). The command
// itself fails to exec (stub backend), which is the expected non-gate outcome.
func TestBashAutoAllowedWhenSandboxActive(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	registry.Register(NewBashTool(root))
	engine := sandbox.NewEngine(sandbox.EngineOptions{
		WorkspaceRoot: root,
		Policy:        autoAllowBashPolicy(true),
		Backend:       nativeBackendStub(),
	})

	result := registry.RunWithOptions(context.Background(), "bash", map[string]any{
		"command": "echo hi",
	}, RunOptions{
		PermissionGranted: false,
		Sandbox:           engine,
		PermissionMode:    string(sandbox.PermissionModeAsk),
		Autonomy:          string(sandbox.AutonomyHigh),
	})

	if strings.Contains(result.Output, permissionRequiredFragment) {
		t.Fatalf("bash was gated despite auto-allow: %q", result.Output)
	}
	if result.SandboxDecision == nil || result.SandboxDecision.Action != sandbox.ActionAllow || !result.SandboxDecision.AutoAllowed {
		t.Fatalf("sandbox decision = %#v, want auto-allowed allow", result.SandboxDecision)
	}
}

// TestBashStillPromptsWithoutAutoAllow: flag off + active sandbox => the normal
// prompt policy stands; the bash gate blocks an ungranted command.
func TestBashStillPromptsWithoutAutoAllow(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	registry.Register(NewBashTool(root))
	engine := sandbox.NewEngine(sandbox.EngineOptions{
		WorkspaceRoot: root,
		Policy:        autoAllowBashPolicy(false),
		Backend:       nativeBackendStub(),
	})

	result := registry.RunWithOptions(context.Background(), "bash", map[string]any{
		"command": "echo hi",
	}, RunOptions{
		PermissionGranted: false,
		Sandbox:           engine,
		PermissionMode:    string(sandbox.PermissionModeAsk),
		Autonomy:          string(sandbox.AutonomyHigh),
	})

	if result.Status != StatusError || !strings.Contains(result.Output, "Sandbox approval required for bash") {
		t.Fatalf("expected bash to be gated when auto-allow off, got %s: %q", result.Status, result.Output)
	}
}

// TestBashAutoAllowIgnoredWithoutActiveSandbox: flag on but the backend is
// policy-only (no native isolation), so the flag must be ignored and the bash
// gate blocks the ungranted command.
func TestBashAutoAllowIgnoredWithoutActiveSandbox(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	registry.Register(NewBashTool(root))
	engine := sandbox.NewEngine(sandbox.EngineOptions{
		WorkspaceRoot: root,
		Policy:        autoAllowBashPolicy(true),
		Backend:       sandbox.Backend{Name: sandbox.BackendPolicyOnly},
	})

	result := registry.RunWithOptions(context.Background(), "bash", map[string]any{
		"command": "echo hi",
	}, RunOptions{
		PermissionGranted: false,
		Sandbox:           engine,
		PermissionMode:    string(sandbox.PermissionModeAsk),
		Autonomy:          string(sandbox.AutonomyHigh),
	})

	if result.Status != StatusError || !strings.Contains(result.Output, "Sandbox approval required for bash") {
		t.Fatalf("expected bash to be gated when sandbox inactive, got %s: %q", result.Status, result.Output)
	}
}
