package specialist

import (
	"context"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/tools"
)

func writeCapableArgs() map[string]any {
	return map[string]any{
		"name": "fixit",
		"tasks": []any{
			map[string]any{"id": "a", "prompt": "read it"},
			map[string]any{"id": "b", "prompt": "fix it", "depends_on": []any{"a"}, "tools": []any{"write_file"}},
		},
		"budget": map[string]any{"max_workers": float64(1)},
	}
}

func writeCapableTool(t *testing.T, isolate PlanIsolator) *OrchestrateTool {
	t.Helper()
	return &OrchestrateTool{
		PostureActive: func() bool { return true },
		RunTask: func(context.Context, PlanTaskRequest) (TaskResult, error) {
			return TaskResult{Outcome: TaskSucceeded}, nil
		},
		ParentTools: append(PlanReadOnlyToolNames(), "write_file"),
		Isolate:     isolate,
	}
}

// THE ARC, ASSERTED AS ONE THING. A write-capable plan must PROMPT (step 1 gave
// the card something to show) and must be ISOLATED (step 2 gave it somewhere to
// write). Either missing and the relaxation in step 3 was not safe to make.
func TestAWriteCapablePlanPromptsAndIsIsolated(t *testing.T) {
	tool := writeCapableTool(t, nil)

	if got := tool.PermissionForArgs(writeCapableArgs()); got != tools.PermissionPrompt {
		t.Fatalf("permission = %v; a plan that can write must ask", got)
	}
	plan, err := ParsePlan(writeCapableArgs(), Limits{MaxTasks: 20, ParentTools: tool.ParentTools})
	if err != nil {
		t.Fatalf("a write-capable plan must now admit: %v", err)
	}
	if !plan.RequiresIsolation() {
		t.Fatal("a write-capable plan does not require isolation")
	}
	// ...and with no isolator it does not run at all.
	if _, err := resolvePlanWorkspace(context.Background(), plan, nil); err == nil {
		t.Fatal("a write-capable plan ran with no isolation available")
	}
}

// A READ-ONLY PLAN IS UNCHANGED by all of it: no prompt, no worktree. The
// friction is paid only by the plans that earn it.
func TestAReadOnlyPlanStillDoesNotPromptOrIsolate(t *testing.T) {
	tool := writeCapableTool(t, nil)
	args := map[string]any{
		"tasks":  []any{map[string]any{"id": "a", "prompt": "look", "tools": []any{"grep"}}},
		"budget": map[string]any{"max_workers": float64(1)},
	}
	if got := tool.PermissionForArgs(args); got != tools.PermissionAllow {
		t.Fatalf("permission = %v; a read-only plan must not prompt", got)
	}
	plan, err := ParsePlan(args, Limits{MaxTasks: 20, ParentTools: tool.ParentTools})
	if err != nil {
		t.Fatalf("ParsePlan: %v", err)
	}
	if plan.RequiresIsolation() {
		t.Fatal("a read-only plan asked for a worktree")
	}
}

// THE POSTURE STILL GATES EVERYTHING. PermissionForArgs must deny with the
// posture off whatever the arguments say, or the args-aware permission has
// become a way around the gate.
func TestThePostureGateSurvivesTheArgsAwarePermission(t *testing.T) {
	off := &OrchestrateTool{PostureActive: func() bool { return false }}
	for _, args := range []map[string]any{writeCapableArgs(), {}, {"saved": "x"}} {
		if got := off.PermissionForArgs(args); got != tools.PermissionDeny {
			t.Fatalf("permission = %v with the posture off; it must deny", got)
		}
	}
	if !off.PermanentlyDenied() {
		t.Fatal("with the posture off the tool must report itself permanently denied")
	}
	on := &OrchestrateTool{PostureActive: func() bool { return true }}
	if on.PermanentlyDenied() {
		t.Fatal("with the posture on the tool must not report itself denied")
	}
}

// ERRING TOWARD ASKING. A saved plan's tasks are in a file the permission check
// has not opened, and an unreadable entry could be anything. A wrong guess
// toward prompting costs one prompt; the other way runs write tasks unasked.
func TestAnUnreadablePlanErrsTowardPrompting(t *testing.T) {
	tool := writeCapableTool(t, nil)
	for _, args := range []map[string]any{
		{"saved": "sweep"},
		{"tasks": []any{"not-an-object"}},
		{"tasks": []any{map[string]any{"id": "a", "tools": []any{"something_new"}}}},
	} {
		if got := tool.PermissionForArgs(args); got != tools.PermissionPrompt {
			t.Errorf("args %v gave %v; an unreadable plan must ask", args, got)
		}
	}
}

// WRITE TOOLS ARE AN ALLOW-LIST. "Anything not read-only" would hand a plan
// every future tool the moment it is registered.
func TestOnlyNamedWriteToolsArePermitted(t *testing.T) {
	limits := Limits{MaxTasks: 20, ParentTools: append(PlanGrantableToolNames(), "browser_open", "web_fetch")}
	for _, name := range []string{"browser_open", "web_fetch", "kill_shell", "made_up"} {
		args := map[string]any{
			"tasks":  []any{map[string]any{"id": "a", "prompt": "x", "tools": []any{name}}},
			"budget": map[string]any{"max_workers": float64(1)},
		}
		if _, err := ParsePlan(args, limits); err == nil {
			t.Errorf("tool %q was permitted; the write set is an allow-list", name)
		}
	}
	for _, name := range PlanWriteToolNames() {
		args := map[string]any{
			"tasks":  []any{map[string]any{"id": "a", "prompt": "x", "tools": []any{name}}},
			"budget": map[string]any{"max_workers": float64(1)},
		}
		if _, err := ParsePlan(args, limits); err != nil {
			t.Errorf("write tool %q must be permitted when the parent holds it: %v", name, err)
		}
	}
}

// The refusal NAMES what is available, both halves — a message that says "no"
// without saying "these instead" makes the caller guess.
func TestTheRefusalNamesBothToolSets(t *testing.T) {
	args := map[string]any{
		"tasks":  []any{map[string]any{"id": "a", "prompt": "x", "tools": []any{"browser_open"}}},
		"budget": map[string]any{"max_workers": float64(1)},
	}
	_, err := ParsePlan(args, Limits{MaxTasks: 20, ParentTools: []string{"browser_open", "read_file"}})
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if !strings.Contains(err.Error(), "read_file") || !strings.Contains(err.Error(), "write_file") {
		t.Fatalf("the refusal must name what IS available: %v", err)
	}
}

// A NAMED WRITE TOOL ACTUALLY REACHES THE TASK. Admission permitting what
// dispatch drops would produce a task that validated and then ran with less than
// it asked for — silently.
func TestANamedWriteToolReachesTheTask(t *testing.T) {
	plan, err := ParsePlan(writeCapableArgs(), Limits{MaxTasks: 20, ParentTools: append(PlanReadOnlyToolNames(), "write_file")})
	if err != nil {
		t.Fatalf("ParsePlan: %v", err)
	}
	var writer Task
	for _, task := range plan.Tasks() {
		if task.ID == "b" {
			writer = task
		}
	}
	granted, err := planToolGrant(writer, append(PlanReadOnlyToolNames(), "write_file"))
	if err != nil {
		t.Fatalf("planToolGrant: %v", err)
	}
	if len(granted) != 1 || granted[0] != "write_file" {
		t.Fatalf("granted %v; the named write tool must reach the task", granted)
	}
}
