package specialist

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A PLAN THAT ASKED FOR NO BOUND STILL GETS ONE.
//
// With no max_tokens there is nothing to meter against — and on a provider that
// reports no usage, max_tokens would not have bounded it either: the meter sums
// the children's usage events, and a provider emitting none leaves it at zero
// forever while the work runs. Nothing then stops the plan but the work ending.
func TestAPlanWithNoBoundAtAllGetsAWallBackstop(t *testing.T) {
	if got := planWallBudget(Budget{}); got != unmeteredWallBudget {
		t.Errorf("a plan with neither bound got %v, want the wall backstop", got)
	}
	// AN EXPLICIT WALL WINS. The caller chose; the default is for the plan that
	// chose nothing.
	if got := planWallBudget(Budget{MaxWall: 90 * time.Second}); got != 90*time.Second {
		t.Errorf("an explicit wall was overridden: %v", got)
	}
	// A TOKEN BOUND IS A BOUND. Defaulting a wall over it would put a new ceiling
	// on a plan that deliberately runs long inside a budget it named.
	if got := planWallBudget(Budget{MaxTokens: 500_000}); got != 0 {
		t.Errorf("a plan with a token budget gained an unasked-for wall of %v", got)
	}
	if got := planWallBudget(Budget{MaxTokens: 500_000, MaxWall: time.Minute}); got != time.Minute {
		t.Errorf("both set: %v", got)
	}
}

// AND THE BACKSTOP MUST REACH THE RUN, not just the helper.
func TestTheWallBackstopActuallyBoundsAnUnboundedPlan(t *testing.T) {
	budget := map[string]any{"max_workers": float64(1)}
	plan := mustPlan(t, []any{task("a", "x")}, budget, readOnlyLimits())
	if plan.Budget().MaxWall != 0 {
		t.Fatal("setup: the fixture already carries a wall bound")
	}

	var sawDeadline bool
	ExecutePlan(context.Background(), plan, []string{"read_file"},
		func(ctx context.Context, _ PlanTaskRequest) (TaskResult, error) {
			if _, ok := ctx.Deadline(); ok {
				sawDeadline = true
			}
			return TaskResult{Outcome: TaskSucceeded, Output: "ok"}, nil
		}, nil)

	if !sawDeadline {
		t.Error("a plan with no bound of any kind ran with no deadline on its context")
	}
}

// SPEND THAT COULD NOT BE MEASURED IS NOT SPEND THAT DID NOT HAPPEN. A provider
// emitting no usage reports zero however much it billed, and a max_tokens set
// against that number bounds nothing while reading like a guarantee.
func TestTheReportSaysWhenSpendCouldNotBeMeasured(t *testing.T) {
	unmetered := PlanReport{
		Status: PlanCompleted, Succeeded: 2, TokensUsed: 0,
		Tasks: []TaskResult{{ID: "a", Outcome: TaskSucceeded}, {ID: "b", Outcome: TaskSucceeded}},
	}.Summary()
	if !strings.Contains(unmetered, "spend could not be measured") {
		t.Errorf("a plan that reported no usage said nothing about it:\n%s", unmetered)
	}
	if !strings.Contains(unmetered, "max_wall_seconds") {
		t.Errorf("the note does not say what to use instead:\n%s", unmetered)
	}

	// A metered plan must not gain the line.
	metered := PlanReport{
		Status: PlanCompleted, Succeeded: 1, TokensUsed: 12_000,
		Tasks: []TaskResult{{ID: "a", Outcome: TaskSucceeded}},
	}.Summary()
	if strings.Contains(metered, "could not be measured") {
		t.Errorf("a metered plan claimed its spend was unmeasurable:\n%s", metered)
	}
	// Nor a plan where nothing ran — zero tokens is honest there.
	empty := PlanReport{Status: PlanFailed, Succeeded: 0, TokensUsed: 0}.Summary()
	if strings.Contains(empty, "could not be measured") {
		t.Errorf("a plan that ran nothing reported an unmeasurable spend:\n%s", empty)
	}
}
