package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

func TestFreeformApplyPatchUsesExistingToolExecutionPath(t *testing.T) {
	root := t.TempDir()
	registry := tools.NewRegistry()
	registry.Register(tools.NewScopedApplyPatchTool(root, nil))
	patch := "*** Begin Patch\n*** Add File: hello.txt\n+hello\n*** End Patch\n"

	result, err := executeToolCall(context.Background(), registry, zeroruntime.ToolCall{
		ID:        "call-1",
		Name:      "apply_patch",
		Arguments: patch,
		Freeform:  true,
	}, PermissionModeUnsafe, Options{Cwd: root, FileTracker: tools.NewFileTracker()})
	if err != nil {
		t.Fatalf("executeToolCall: %v", err)
	}
	if result.Status != tools.StatusOK {
		t.Fatalf("freeform apply_patch status = %s: %s", result.Status, result.Output)
	}
	content, err := os.ReadFile(filepath.Join(root, "hello.txt"))
	if err != nil {
		t.Fatalf("read patched file: %v", err)
	}
	if string(content) != "hello\n" {
		t.Fatalf("patched content = %q, want hello newline", content)
	}
}

func TestUnknownFreeformToolFailsClosed(t *testing.T) {
	result, err := executeToolCall(context.Background(), tools.NewRegistry(), zeroruntime.ToolCall{
		ID: "call-1", Name: "unknown", Arguments: "raw", Freeform: true,
	}, PermissionModeUnsafe, Options{})
	if err != nil {
		t.Fatalf("executeToolCall: %v", err)
	}
	if result.Status != tools.StatusError {
		t.Fatalf("unknown freeform status = %s, want error", result.Status)
	}
}

func TestDeniedFreeformApplyPatchDoesNotWrite(t *testing.T) {
	root := t.TempDir()
	registry := tools.NewRegistry()
	registry.Register(tools.NewScopedApplyPatchTool(root, nil))
	patch := "*** Begin Patch\n*** Add File: denied.txt\n+must not exist\n*** End Patch\n"

	result, err := executeToolCall(context.Background(), registry, zeroruntime.ToolCall{
		ID:        "call-1",
		Name:      "apply_patch",
		Arguments: patch,
		Freeform:  true,
	}, PermissionModeAsk, Options{
		Cwd: root,
		OnPermissionRequest: func(context.Context, PermissionRequest) (PermissionDecision, error) {
			return PermissionDecision{Action: PermissionDecisionDeny, Reason: "test denial"}, nil
		},
	})
	if err != nil {
		t.Fatalf("executeToolCall: %v", err)
	}
	if result.Status != tools.StatusError {
		t.Fatalf("denied freeform status = %s, want error", result.Status)
	}
	if _, err := os.Stat(filepath.Join(root, "denied.txt")); !os.IsNotExist(err) {
		t.Fatalf("denied freeform patch changed the workspace: %v", err)
	}
}

func TestHistorySafeToolCallsPreservesRawFreeformArguments(t *testing.T) {
	raw := "*** Begin Patch\n*** Update File: internal/main.go\n@@\n-old\n+new\n*** End Patch\n"
	safe := historySafeToolCalls([]ToolCall{{
		ID: "call-1", ProviderCallID: "provider-1", Name: "apply_patch", Arguments: raw, Freeform: true,
	}})
	if len(safe) != 1 || safe[0].Arguments != raw || safe[0].ProviderCallID != "provider-1" || !safe[0].Freeform {
		t.Fatalf("freeform history changed raw call: %#v", safe)
	}
}

func TestAbortedToolResultPreservesProviderCallID(t *testing.T) {
	messages := appendAbortedToolResults(nil, []ToolCall{{
		ID: "call-1", ProviderCallID: "provider-1", Name: "read_file",
	}})
	if len(messages) != 1 || messages[0].ToolCallID != "call-1" || messages[0].ToolCallProviderID != "provider-1" || !messages[0].IsError {
		t.Fatalf("aborted tool result lost provider identity: %#v", messages)
	}
}
