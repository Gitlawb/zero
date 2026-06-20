package tui

import (
	"strings"
	"testing"
)

func TestPlanSummaryCardBodyCollapses(t *testing.T) {
	detail := "Current Plan:\n1. [completed] Explore workspace\n2. [in_progress] Create pages\n3. [pending] Build CSS\n4. [pending] Add JS\n5. [failed] Verify"
	body := planSummaryCardBody(toolBodyRequest{name: "update_plan", detail: detail})
	if len(body.lines) != 1 {
		t.Fatalf("expected one summary line, got %d: %#v", len(body.lines), body.lines)
	}
	got := body.lines[0]
	for _, want := range []string{"5 steps", "1 done", "1 in progress", "1 failed"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary missing %q: %q", want, got)
		}
	}
	if strings.Contains(got, "Explore") || strings.Contains(got, "Build CSS") {
		t.Errorf("summary must not re-dump the full plan body: %q", got)
	}
}

func TestPlanSummaryFallsBackOnNonPlan(t *testing.T) {
	body := planSummaryCardBody(toolBodyRequest{name: "update_plan", detail: "unexpected error text"})
	if len(body.lines) == 0 {
		t.Error("non-plan detail should fall back to a generic body, not collapse to nothing")
	}
}
