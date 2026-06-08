package agent

import (
	"context"
	"testing"

	"github.com/Gitlawb/zero/internal/sandbox"
	"github.com/Gitlawb/zero/internal/tools"
)

func TestExecuteToolCallCategorizesFilteredDenial(t *testing.T) {
	root := t.TempDir()
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(root))

	result, _ := executeToolCall(context.Background(), registry, ToolCall{ID: "c1", Name: "read_file", Arguments: `{"path":"x"}`}, PermissionModeAuto, Options{
		DisabledTools: []string{"read_file"},
	})
	if result.DenialReason != DenialFiltered {
		t.Fatalf("DenialReason = %q, want %q", result.DenialReason, DenialFiltered)
	}
	if result.Status != tools.StatusError {
		t.Fatalf("status = %q, want error", result.Status)
	}
}

func TestDeniedPermissionResultCategories(t *testing.T) {
	// Plain approval-declined.
	plain := deniedPermissionResult(ToolCall{ID: "c1", Name: "bash"}, "needs approval", PermissionEvent{ToolName: "bash"})
	if plain.DenialReason != DenialPermissionDenied {
		t.Fatalf("plain denial = %q, want %q", plain.DenialReason, DenialPermissionDenied)
	}
	// Sandbox-violation-driven denial.
	sandboxDenied := deniedPermissionResult(ToolCall{ID: "c2", Name: "bash"}, "outside workspace", PermissionEvent{
		ToolName:  "bash",
		Violation: &sandbox.Violation{Code: "outside_workspace"},
	})
	if sandboxDenied.DenialReason != DenialSandboxViolation {
		t.Fatalf("sandbox denial = %q, want %q", sandboxDenied.DenialReason, DenialSandboxViolation)
	}
}
