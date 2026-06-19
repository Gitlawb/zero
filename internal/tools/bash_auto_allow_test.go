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

func sandboxedBashPolicy() sandbox.Policy {
	policy := sandbox.DefaultPolicy()
	// Network deny so no proxy is started for these gate-only tests.
	return policy
}

const permissionRequiredFragment = "Permission required for bash"

func TestBashAutoAllowedWhenSandboxActive(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	registry.Register(NewBashTool(root))
	engine := sandbox.NewEngine(sandbox.EngineOptions{
		WorkspaceRoot: root,
		Policy:        sandboxedBashPolicy(),
		Backend:       nativeBackendStub(),
	})

	result := registry.RunWithOptions(context.Background(), "bash", map[string]any{
		"command": "echo hi",
	}, RunOptions{
		PermissionGranted: false,
		Sandbox:           engine,
		PermissionMode:    string(sandbox.PermissionModeAsk),
		Autonomy:          "high",
	})

	if strings.Contains(result.Output, permissionRequiredFragment) {
		t.Fatalf("bash was gated despite auto-allow: %q", result.Output)
	}
	if result.SandboxDecision == nil || result.SandboxDecision.Action != sandbox.ActionAllow || !result.SandboxDecision.AutoAllowed {
		t.Fatalf("sandbox decision = %#v, want auto-allowed allow", result.SandboxDecision)
	}
}

func TestBashStillPromptsWithoutActiveSandbox(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	registry.Register(NewBashTool(root))
	engine := sandbox.NewEngine(sandbox.EngineOptions{
		WorkspaceRoot: root,
		Policy:        sandboxedBashPolicy(),
		Backend:       sandbox.Backend{Name: sandbox.BackendUnavailable},
	})

	result := registry.RunWithOptions(context.Background(), "bash", map[string]any{
		"command": "echo hi",
	}, RunOptions{
		PermissionGranted: false,
		Sandbox:           engine,
		PermissionMode:    string(sandbox.PermissionModeAsk),
		Autonomy:          "high",
	})

	if result.Status != StatusError || !strings.Contains(result.Output, "Sandbox approval required for bash") {
		t.Fatalf("expected bash to be gated when sandbox inactive, got %s: %q", result.Status, result.Output)
	}
}
