package tools

import (
	"context"
	"strings"
	"testing"
)

// searchEagerTool is a minimal EAGER tool: it does NOT implement Deferred(), so
// IsDeferred returns false — it's always in the model's list (like Task/update_plan).
type searchEagerTool struct{ name string }

func (t searchEagerTool) Name() string        { return t.name }
func (t searchEagerTool) Description() string { return "eager tool " + t.name }
func (t searchEagerTool) Parameters() Schema  { return Schema{Type: "object"} }
func (t searchEagerTool) Safety() Safety {
	return Safety{SideEffect: SideEffectRead, Permission: PermissionAllow}
}
func (t searchEagerTool) Run(context.Context, map[string]any) Result { return Result{Status: StatusOK} }

// The bug from the Windows screenshot: select:Task (an eager tool) returned a
// misleading "No tools matched". It must instead redirect to "call it directly".
func TestToolSearchRedirectsEagerSelectToCallDirectly(t *testing.T) {
	reg := newDeferredFixtureRegistry() // weather_lookup + stock_quote (deferred)
	reg.Register(searchEagerTool{name: "Task"})
	reg.Register(searchEagerTool{name: "update_plan"})
	tool := NewToolSearchTool(reg).(optionsAwareTool)

	res := tool.RunWithOptions(context.Background(), map[string]any{"query": "select:Task,update_plan"}, RunOptions{})
	if strings.Contains(res.Output, "No tools matched") {
		t.Errorf("eager tools must NOT report 'No tools matched':\n%s", res.Output)
	}
	for _, want := range []string{"Task", "update_plan", "already in your tool list", "call them directly"} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("expected %q in:\n%s", want, res.Output)
		}
	}
	if res.Meta["load_tools"] != "" {
		t.Errorf("eager tools must not be 'loaded', got meta %q", res.Meta["load_tools"])
	}
}

// Mixed: load the deferred one, note the eager one.
func TestToolSearchLoadsDeferredAndNotesEager(t *testing.T) {
	reg := newDeferredFixtureRegistry()
	reg.Register(searchEagerTool{name: "Task"})
	tool := NewToolSearchTool(reg).(optionsAwareTool)

	res := tool.RunWithOptions(context.Background(), map[string]any{"query": "select:weather_lookup,Task"}, RunOptions{})
	if res.Meta["load_tools"] != "weather_lookup" {
		t.Errorf("expected weather_lookup loaded, got %q", res.Meta["load_tools"])
	}
	if !strings.Contains(res.Output, "Task is already in your tool list") {
		t.Errorf("expected the eager note for Task:\n%s", res.Output)
	}
}

// A genuinely-unknown tool must still get the honest "No tools matched".
func TestToolSearchUnknownStillReportsNoMatch(t *testing.T) {
	reg := newDeferredFixtureRegistry()
	tool := NewToolSearchTool(reg).(optionsAwareTool)
	res := tool.RunWithOptions(context.Background(), map[string]any{"query": "select:nope_not_a_tool"}, RunOptions{})
	if !strings.Contains(res.Output, "No tools matched") {
		t.Errorf("unknown tool should still report 'No tools matched':\n%s", res.Output)
	}
}

// Keyword search for an eager tool name also redirects.
func TestToolSearchKeywordEagerRedirects(t *testing.T) {
	reg := newDeferredFixtureRegistry()
	reg.Register(searchEagerTool{name: "update_plan"})
	tool := NewToolSearchTool(reg).(optionsAwareTool)
	res := tool.RunWithOptions(context.Background(), map[string]any{"query": "update_plan"}, RunOptions{})
	if !strings.Contains(res.Output, "update_plan is already in your tool list") {
		t.Errorf("keyword search for an eager tool should redirect:\n%s", res.Output)
	}
}
