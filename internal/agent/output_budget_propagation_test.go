package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/tools"
)

type propagationOutputTool struct {
	output string
}

func (tool propagationOutputTool) Name() string        { return "propagation_output" }
func (tool propagationOutputTool) Description() string { return "returns output for propagation tests" }
func (tool propagationOutputTool) Parameters() tools.Schema {
	return tools.Schema{Type: "object", AdditionalProperties: false}
}
func (tool propagationOutputTool) Safety() tools.Safety {
	return tools.Safety{SideEffect: tools.SideEffectRead, Permission: tools.PermissionAllow, Reason: "test read"}
}
func (tool propagationOutputTool) Run(context.Context, map[string]any) tools.Result {
	return tools.Result{Status: tools.StatusOK, Output: tool.output}
}

func TestExecuteToolCallPropagatesOutputTruncation(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("ZERO_TOOL_OUTPUT_CEILING_TOKENS", "80")
	registry := tools.NewRegistry()
	registry.Register(propagationOutputTool{output: strings.Repeat("large output\n", 1000)})

	result, abortErr := executeToolCall(context.Background(), registry, ToolCall{
		ID:        "call-budget",
		Name:      "propagation_output",
		Arguments: `{}`,
	}, PermissionModeAuto, Options{Cwd: t.TempDir()})
	if abortErr != nil {
		t.Fatalf("executeToolCall abort error: %v", abortErr)
	}
	if !result.Truncated {
		t.Fatalf("agent ToolResult lost tools.Result.Truncated: %#v", result)
	}
	if result.Meta["spill_path"] == "" {
		t.Fatalf("agent ToolResult lost spill metadata: %#v", result.Meta)
	}
}
