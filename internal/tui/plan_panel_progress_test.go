package tui

import (
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/tools"
)

// TestCurrentStepContent: the header/summary name the step actually being
// worked (in_progress, else first incomplete, else first), not always step 1.
func TestCurrentStepContent(t *testing.T) {
	steps := []planStep{
		{content: "one", status: "completed"},
		{content: "two", status: "in_progress"},
		{content: "three", status: "pending"},
	}
	if got := currentStepContent(steps); got != "two" {
		t.Errorf("in_progress: want %q, got %q", "two", got)
	}
	steps[1].status = "completed" // no in_progress -> first not-yet-terminal
	if got := currentStepContent(steps); got != "three" {
		t.Errorf("first incomplete: want %q, got %q", "three", got)
	}
	steps[2].status = "completed" // all done -> first
	if got := currentStepContent(steps); got != "one" {
		t.Errorf("all complete: want %q, got %q", "one", got)
	}
	if got := currentStepContent(nil); got != "" {
		t.Errorf("empty: want %q, got %q", "", got)
	}
}

// TestPlanRewordKeepsTimer: when the model rewords a step in place (same step
// count), its elapsed clock must NOT reset — the positional carry-over preserves
// startedAt that the content-only match used to drop.
func TestPlanRewordKeepsTimer(t *testing.T) {
	var s planPanelState
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.updateFromItems([]tools.PlanItem{
		{Content: "build the thing", Status: "in_progress"},
		{Content: "test it", Status: "pending"},
	}, t0)
	started := s.steps[0].startedAt
	if started.IsZero() {
		t.Fatal("expected startedAt set on first in_progress step")
	}

	t1 := t0.Add(30 * time.Second)
	s.updateFromItems([]tools.PlanItem{
		{Content: "build the thing (with caching)", Status: "in_progress"}, // reworded in place
		{Content: "test it", Status: "pending"},
	}, t1)
	if s.steps[0].startedAt != started {
		t.Errorf("reworded step reset its timer: want %v, got %v", started, s.steps[0].startedAt)
	}
}
