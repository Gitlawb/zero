package specialist

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func mustParsePlan(t *testing.T, args map[string]any, limits Limits) Plan {
	t.Helper()
	plan, err := ParsePlan(args, limits)
	if err != nil {
		t.Fatalf("ParsePlan: %v", err)
	}
	return plan
}

func singleTaskPlanArgs(task map[string]any) map[string]any {
	return map[string]any{
		"name":   "p",
		"tasks":  []any{task},
		"budget": map[string]any{"max_workers": 1, "max_tokens": 1000},
	}
}

// THE EXPRESSIBILITY FIX. An empty ResolvedTools used to be indistinguishable
// from an unset one, so a deliberately-empty grant expanded to the default
// read-only category — the narrower the parent, the wider the child.
func TestResolvedToolAllowlistTreatsADeliberatelyEmptyGrantAsEmpty(t *testing.T) {
	resolved, err := resolvedToolAllowlist(Manifest{ResolvedTools: []string{}, ToolsResolved: true})
	if err != nil {
		t.Fatalf("resolvedToolAllowlist: %v", err)
	}
	if len(resolved) != 0 {
		t.Fatalf("an authoritative empty tool list expanded to %v; empty must mean empty", resolved)
	}
}

// The other side of the same flag: a manifest that never resolved its tools
// still gets the default expansion, so no existing caller changes behaviour.
func TestResolvedToolAllowlistStillExpandsAnUnresolvedManifest(t *testing.T) {
	resolved, err := resolvedToolAllowlist(Manifest{Metadata: Metadata{Name: "x"}})
	if err != nil {
		t.Fatalf("resolvedToolAllowlist: %v", err)
	}
	if len(resolved) == 0 {
		t.Fatal("an unresolved manifest must still expand to the default selection")
	}
}

// A bare []string cannot carry this distinction across a round trip: the field
// is omitempty, so a deliberately-empty list marshals away entirely and comes
// back nil. This is why the fix is a flag and not a nil check.
func TestToolsResolvedSurvivesAJSONRoundTrip(t *testing.T) {
	manifest := Manifest{ResolvedTools: []string{}, ToolsResolved: true}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var round Manifest
	if err := json.Unmarshal(encoded, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if round.ResolvedTools != nil {
		t.Fatalf("omitempty was expected to erase the empty slice, got %v", round.ResolvedTools)
	}
	if !round.ToolsResolved {
		t.Fatalf("the authoritative flag did not survive the round trip: %s", encoded)
	}
	resolved, err := resolvedToolAllowlist(round)
	if err != nil {
		t.Fatalf("resolvedToolAllowlist: %v", err)
	}
	if len(resolved) != 0 {
		t.Fatalf("after a round trip the empty grant expanded to %v", resolved)
	}
}

// THE SECOND LAYER. ExecutePlan refuses an empty grant before a manifest is
// ever built, but if that check were bypassed the manifest itself must still
// not expand. This is the belt to planToolGrant's braces.
func TestPlanTaskManifestMarksItsGrantAuthoritative(t *testing.T) {
	manifest := planTaskManifest("explorer", []string{})
	if !manifest.ToolsResolved {
		t.Fatal("a plan task's manifest carries an already-intersected grant; it must be marked authoritative")
	}
	resolved, err := resolvedToolAllowlist(manifest)
	if err != nil {
		t.Fatalf("resolvedToolAllowlist: %v", err)
	}
	if len(resolved) != 0 {
		t.Fatalf("an empty plan grant expanded to %v instead of refusing", resolved)
	}
}

// planToolGrant refuses rather than returning an empty slice, so the empty case
// never reaches a caller that has to guess what it meant.
func TestPlanToolGrantRefusesWhenTheParentHoldsNothing(t *testing.T) {
	_, err := planToolGrant(Task{ID: "a"}, nil)
	if err == nil {
		t.Fatal("an empty parent grant must be refused, not returned as an empty list")
	}
	if !strings.Contains(err.Error(), "parent grant: none") {
		t.Fatalf("the refusal must name the empty grant explicitly, got: %v", err)
	}
}

// The intersection is unconditional on both sides. Guarding it on a non-empty
// parent list is what made the rule inert.
func TestPlanToolGrantNarrowsToTheParentsHoldings(t *testing.T) {
	granted, err := planToolGrant(Task{ID: "a", Tools: []string{"read_file", "grep"}}, []string{"grep"})
	if err != nil {
		t.Fatalf("planToolGrant: %v", err)
	}
	if strings.Join(granted, ",") != "grep" {
		t.Fatalf("granted %v, want [grep]: read_file is not held by the parent", granted)
	}
}

// An empty request inherits the parent's grant — narrowed, never widened.
func TestPlanToolGrantInheritsOnlyWhatTheParentHolds(t *testing.T) {
	granted, err := planToolGrant(Task{ID: "a"}, []string{"grep", "write_file"})
	if err != nil {
		t.Fatalf("planToolGrant: %v", err)
	}
	if strings.Join(granted, ",") != "grep" {
		t.Fatalf("granted %v, want [grep]: write_file is not a read-only plan tool", granted)
	}
}

// Validation rejects a widening even when the caller supplied a grant that does
// not contain the requested tool. Previously this only fired when the grant was
// non-empty, which no production caller ever made it.
func TestParsePlanRejectsAToolTheParentDoesNotHold(t *testing.T) {
	_, err := ParsePlan(
		singleTaskPlanArgs(map[string]any{"id": "a", "prompt": "p", "tools": []any{"read_file"}}),
		Limits{MaxTasks: 20, MaxTokens: 200000, ParentTools: []string{"grep"}},
	)
	if err == nil {
		t.Fatal("a task requesting a tool the run does not hold must be rejected")
	}
	if !strings.Contains(err.Error(), "never widen it") {
		t.Fatalf("unexpected rejection reason: %v", err)
	}
}

// With no grant supplied at all, every request is rejected. Fail closed: an
// unsupplied grant is a wiring bug, and the run must stop rather than assume
// authority — which is exactly what the old escape hatch did.
func TestParsePlanRejectsEveryToolWhenNoGrantWasSupplied(t *testing.T) {
	_, err := ParsePlan(
		singleTaskPlanArgs(map[string]any{"id": "a", "prompt": "p", "tools": []any{"read_file"}}),
		Limits{MaxTasks: 20, MaxTokens: 200000},
	)
	if err == nil {
		t.Fatal("an unwired parent grant must reject, not silently allow")
	}
}

// End to end through ExecutePlan: an ungrantable task is a recorded FAILURE
// with a reason, never a dispatch. A dispatched-then-failed task would put a
// task_dispatched event in the log for work that never started.
func TestExecutePlanFailsAnUngrantableTaskWithoutDispatchingIt(t *testing.T) {
	plan := mustParsePlan(t,
		singleTaskPlanArgs(map[string]any{"id": "a", "prompt": "p"}),
		Limits{MaxTasks: 20, MaxTokens: 200000, ParentTools: []string{"grep"}},
	)
	dispatched := 0
	report := ExecutePlan(context.Background(), plan, nil,
		func(context.Context, Task, []string) (TaskResult, error) {
			dispatched++
			return TaskResult{Outcome: TaskSucceeded}, nil
		}, nil)

	if dispatched != 0 {
		t.Fatalf("the runner was invoked %d times for a task that could be granted nothing", dispatched)
	}
	if report.Status != PlanFailed {
		t.Fatalf("status = %q, want %q", report.Status, PlanFailed)
	}
	if len(report.Tasks) != 1 || report.Tasks[0].Outcome != TaskFailed {
		t.Fatalf("outcome = %+v, want a recorded failure", report.Tasks)
	}
	if !strings.Contains(report.Tasks[0].Err, "resolved no tools") {
		t.Fatalf("the failure must say why, got %q", report.Tasks[0].Err)
	}
}
