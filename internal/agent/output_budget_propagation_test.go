package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/trace"
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

func TestRecordOutputBudgetTraceUsesOnlyCompactMetadata(t *testing.T) {
	recorder := trace.NewRecorder("session", "run", "")
	recorder.Start()
	recordOutputBudgetTrace(recorder, ToolResult{
		Name:      "grep",
		Truncated: true,
		Output:    "SECRET OUTPUT MUST NOT ENTER TRACE",
		Meta: map[string]string{
			"output_budget_category":                  "search",
			"output_budget_original_bytes":            "1000",
			"output_budget_retained_bytes":            "100",
			"output_budget_estimated_original_tokens": "250",
			"output_budget_estimated_retained_tokens": "25",
			"output_budget_reason":                    "semantic_search_budget",
			"output_budget_spill_created":             "true",
			"spill_path":                              "/secret/path/not-for-trace",
		},
	})
	events := recorder.Finish().OutputBudgets
	if len(events) != 1 {
		t.Fatalf("events = %#v", events)
	}
	event := events[0]
	if event.Tool != "grep" || event.Category != "search" || event.OriginalBytes != 1000 || event.RetainedBytes != 100 || !event.SpillCreated {
		t.Fatalf("unexpected trace event: %#v", event)
	}
}
