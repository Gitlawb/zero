package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/sandbox"
	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

type customApplyPatchTool struct {
	calls int
}

func (*customApplyPatchTool) Name() string        { return "apply_patch" }
func (*customApplyPatchTool) Description() string { return "custom JSON tool" }
func (*customApplyPatchTool) Parameters() tools.Schema {
	return tools.Schema{Type: "object", Properties: map[string]tools.PropertySchema{"value": {Type: "string"}}}
}
func (*customApplyPatchTool) Safety() tools.Safety {
	return tools.Safety{Permission: tools.PermissionAllow, SideEffect: tools.SideEffectRead}
}
func (tool *customApplyPatchTool) Run(context.Context, map[string]any) tools.Result {
	tool.calls++
	return tools.Result{Status: tools.StatusOK}
}

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

func TestFreeformApplyPatchSupportsGrantedExtraRoot(t *testing.T) {
	root := t.TempDir()
	extra := t.TempDir()
	scope, err := sandbox.NewScope(root, []string{extra})
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	registry := tools.NewRegistry()
	registry.Register(tools.NewScopedApplyPatchTool(root, scope))
	target := filepath.Join(extra, "hello.txt")
	patch := "*** Begin Patch\n*** Add File: " + target + "\n+hello\n*** End Patch\n"

	result, err := executeToolCall(context.Background(), registry, zeroruntime.ToolCall{
		ID: "call-1", Name: "apply_patch", Arguments: patch, Freeform: true,
	}, PermissionModeUnsafe, Options{Cwd: root, FileTracker: tools.NewFileTracker()})
	if err != nil {
		t.Fatalf("executeToolCall: %v", err)
	}
	if result.Status != tools.StatusOK {
		t.Fatalf("freeform extra-root apply_patch status = %s: %s", result.Status, result.Output)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read extra-root patched file: %v", err)
	}
	if string(content) != "hello\n" {
		t.Fatalf("patched content = %q, want hello newline", content)
	}
}

func TestFreeformApplyPatchRejectsAbsolutePathOutsideGrantedRoots(t *testing.T) {
	root := t.TempDir()
	granted := t.TempDir()
	outside, err := os.MkdirTemp(".", ".zero-ungranted-patch-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(outside) })
	outside, err = filepath.Abs(outside)
	if err != nil {
		t.Fatalf("Abs: %v", err)
	}
	scope, err := sandbox.NewScope(root, []string{granted})
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	registry := tools.NewRegistry()
	registry.Register(tools.NewScopedApplyPatchTool(root, scope))
	target := filepath.Join(outside, "blocked.txt")
	patch := "*** Begin Patch\n*** Add File: " + target + "\n+blocked\n*** End Patch\n"

	result, err := executeToolCall(context.Background(), registry, zeroruntime.ToolCall{
		ID: "call-1", Name: "apply_patch", Arguments: patch, Freeform: true,
	}, PermissionModeUnsafe, Options{Cwd: root})
	if err != nil {
		t.Fatalf("executeToolCall: %v", err)
	}
	if result.Status != tools.StatusError {
		t.Fatalf("ungranted absolute patch status = %s, want error: %s", result.Status, result.Output)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("ungranted absolute patch created target: %v", err)
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

func TestCustomApplyPatchCannotUseFreeformContract(t *testing.T) {
	custom := &customApplyPatchTool{}
	registry := tools.NewRegistry()
	registry.Register(custom)

	result, err := executeToolCall(context.Background(), registry, zeroruntime.ToolCall{
		ID: "call-1", Name: "apply_patch", Arguments: "raw patch", Freeform: true,
	}, PermissionModeUnsafe, Options{})
	if err != nil {
		t.Fatalf("executeToolCall: %v", err)
	}
	if result.Status != tools.StatusError || !strings.Contains(result.Output, "Unsupported freeform tool call") {
		t.Fatalf("custom freeform apply_patch result = %#v, want unsupported error", result)
	}
	if custom.calls != 0 {
		t.Fatalf("custom apply_patch executed %d times", custom.calls)
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
