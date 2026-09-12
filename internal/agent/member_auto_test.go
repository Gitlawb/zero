package agent

import (
	"context"
	"testing"

	"github.com/Gitlawb/zero/internal/tools"
)

type safetyTool struct {
	name   string
	safety tools.Safety
}

func (t safetyTool) Name() string        { return t.name }
func (t safetyTool) Description() string { return "test tool" }
func (t safetyTool) Parameters() tools.Schema {
	return tools.Schema{Type: "object", AdditionalProperties: false}
}
func (t safetyTool) Safety() tools.Safety { return t.safety }
func (t safetyTool) Run(context.Context, map[string]any) tools.Result {
	return tools.Result{Status: tools.StatusOK}
}

// workspace-auto advertises the in-workspace mutators needed to build
// (write/edit + shell) that plain Auto hides, but NOT network or denied tools —
// the sandbox still gates the advertised ones at call time.
func TestToolAdvertisedWorkspaceAuto(t *testing.T) {
	write := safetyTool{name: "write_file", safety: tools.Safety{SideEffect: tools.SideEffectWrite, Permission: tools.PermissionPrompt}}
	shell := safetyTool{name: "bash", safety: tools.Safety{SideEffect: tools.SideEffectShell, Permission: tools.PermissionPrompt}}
	read := safetyTool{name: "read_file", safety: tools.Safety{SideEffect: tools.SideEffectRead, Permission: tools.PermissionAllow}}
	network := safetyTool{name: "net_tool", safety: tools.Safety{SideEffect: tools.SideEffectNetwork, Permission: tools.PermissionPrompt}}
	denied := safetyTool{name: "blocked", safety: tools.Safety{SideEffect: tools.SideEffectRead, Permission: tools.PermissionDeny}}

	// Plain Auto hides prompt-requiring mutators (the read-only member problem).
	if ToolAdvertised(write, PermissionModeAuto) || ToolAdvertised(shell, PermissionModeAuto) {
		t.Fatal("Auto must NOT advertise write/shell prompt tools")
	}

	for _, tool := range []tools.Tool{write, shell, read} {
		if !ToolAdvertised(tool, PermissionModeWorkspaceAuto) {
			t.Fatalf("workspace-auto must advertise %q", tool.Name())
		}
	}
	if ToolAdvertised(network, PermissionModeWorkspaceAuto) {
		t.Fatal("workspace-auto must NOT advertise a network prompt tool")
	}
	if ToolAdvertised(denied, PermissionModeWorkspaceAuto) {
		t.Fatal("workspace-auto must NOT advertise a denied tool")
	}
}
