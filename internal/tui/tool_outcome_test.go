package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/tools"
)

type tuiOutcomeErrorTool struct {
	output string
}

func (tool tuiOutcomeErrorTool) Name() string             { return tools.ExecCommandToolName }
func (tool tuiOutcomeErrorTool) Description() string      { return "test tool" }
func (tool tuiOutcomeErrorTool) Parameters() tools.Schema { return tools.Schema{Type: "object"} }
func (tool tuiOutcomeErrorTool) Safety() tools.Safety {
	return tools.Safety{Permission: tools.PermissionAllow}
}
func (tool tuiOutcomeErrorTool) Run(context.Context, map[string]any) tools.Result {
	return tools.Result{Status: tools.StatusError, Output: tool.output}
}

func TestToolResultDetailUsesFinalizedHumanEvidenceForReducedError(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	raw := "output:\n" + strings.Repeat("ok  \texample.test/package\t0.01s\n", 24) +
		"--- FAIL: TestImportant\nexpected 7, got 9\nFAIL\nexit_code: 1"
	registry := tools.NewRegistry()
	registry.Register(tuiOutcomeErrorTool{output: raw})
	result := registry.Run(context.Background(), tools.ExecCommandToolName, map[string]any{"cmd": "go test ./..."})

	detail := toolResultDetail(agent.ToolResult{
		Name:    tools.ExecCommandToolName,
		Status:  result.Status,
		Output:  result.Output,
		Display: result.Display,
		Outcome: result.Outcome,
	})
	for _, want := range []string{"ok  \texample.test/package", "TestImportant", "expected 7, got 9"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("human error detail missing %q: %q", want, detail)
		}
	}
	if detail == result.ModelOutput() {
		t.Fatal("human detail collapsed back to the reduced model view")
	}
}
