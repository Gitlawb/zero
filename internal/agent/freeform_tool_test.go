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
