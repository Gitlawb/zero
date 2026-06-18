package tui

import (
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/tools"
)

func TestPlanPanelUpdateFromItems(t *testing.T) {
	var s planPanelState
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)

	s.updateFromItems([]tools.PlanItem{
		{Content: "Read file", Status: "in_progress"},
		{Content: "Edit file", Status: "pending"},
	}, now)

	if len(s.steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(s.steps))
	}
	if s.steps[0].status != "in_progress" {
		t.Errorf("step 0 status = %q, want in_progress", s.steps[0].status)
	}
	if s.steps[0].startedAt.IsZero() {
		t.Error("in_progress step should have startedAt set")
	}
	if !s.startedAt.IsZero() == false {
		t.Error("panel startedAt should be set on first update")
	}
	if s.isComplete() {
		t.Error("plan should not be complete with a pending step")
	}
}

func TestPlanPanelPreservesTimestamps(t *testing.T) {
	var s planPanelState
	t0 := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)

	s.updateFromItems([]tools.PlanItem{
		{Content: "Step A", Status: "in_progress"},
	}, t0)

	t1 := t0.Add(10 * time.Second)
	s.updateFromItems([]tools.PlanItem{
		{Content: "Step A", Status: "completed"},
	}, t1)

	if s.steps[0].startedAt != t0 {
		t.Errorf("startedAt not preserved: got %v, want %v", s.steps[0].startedAt, t0)
	}
	if s.steps[0].completedAt != t1 {
		t.Errorf("completedAt not set: got %v, want %v", s.steps[0].completedAt, t1)
	}
}

func TestPlanPanelIsComplete(t *testing.T) {
	var s planPanelState
	now := time.Now()

	s.updateFromItems([]tools.PlanItem{
		{Content: "A", Status: "completed"},
		{Content: "B", Status: "failed"},
	}, now)

	if !s.isComplete() {
		t.Error("plan with all completed/failed should be complete")
	}
	if s.completedAt.IsZero() {
		t.Error("completedAt should be set when plan is complete")
	}
}

func TestPlanPanelClear(t *testing.T) {
	var s planPanelState
	s.updateFromItems([]tools.PlanItem{{Content: "A", Status: "pending"}}, time.Now())
	s.clear()

	if !s.isEmpty() {
		t.Error("plan should be empty after clear")
	}
	if len(s.steps) != 0 {
		t.Errorf("expected 0 steps after clear, got %d", len(s.steps))
	}
}

func TestPlanPanelHeight(t *testing.T) {
	var s planPanelState
	now := time.Now()

	// Empty plan: height 0
	if h := s.height(80); h != 0 {
		t.Errorf("empty plan height = %d, want 0", h)
	}

	// Running plan with 3 steps: 2 (header+bar) + 3 (steps) = 5
	s.updateFromItems([]tools.PlanItem{
		{Content: "A", Status: "completed"},
		{Content: "B", Status: "in_progress"},
		{Content: "C", Status: "pending"},
	}, now)
	if h := s.height(80); h != 5 {
		t.Errorf("running plan height = %d, want 5", h)
	}

	// Completed plan collapsed: 2 (header+bar only)
	s.updateFromItems([]tools.PlanItem{
		{Content: "A", Status: "completed"},
		{Content: "B", Status: "completed"},
		{Content: "C", Status: "completed"},
	}, now)
	if h := s.height(80); h != 2 {
		t.Errorf("completed collapsed plan height = %d, want 2", h)
	}

	// Expanded: 2 + 3 = 5
	s.expanded = true
	if h := s.height(80); h != 5 {
		t.Errorf("expanded plan height = %d, want 5", h)
	}
}

func TestPlanPanelVisibleHidesAfterTimeout(t *testing.T) {
	var s planPanelState
	t0 := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)

	s.updateFromItems([]tools.PlanItem{
		{Content: "A", Status: "completed"},
	}, t0)

	// Visible right after completion
	if !s.visible(t0.Add(10 * time.Second)) {
		t.Error("plan should be visible within 30s of completion")
	}

	// Hidden after 30s
	if s.visible(t0.Add(31 * time.Second)) {
		t.Error("plan should hide after 30s of completion when not expanded")
	}

	// Expanded keeps it visible
	s.expanded = true
	if !s.visible(t0.Add(31 * time.Second)) {
		t.Error("expanded plan should stay visible")
	}
}

func TestPlanPanelRenderEmpty(t *testing.T) {
	m := newModel(t.Context(), Options{ModelName: "gpt-4"})
	if got := m.renderPlanPanel(80); got != "" {
		t.Errorf("empty plan should render empty string, got %q", got)
	}
}

func TestPlanPanelRenderRunning(t *testing.T) {
	m := newModel(t.Context(), Options{ModelName: "gpt-4"})
	m.width = 100
	base := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return base.Add(15 * time.Second) }

	m.plan.updateFromItems([]tools.PlanItem{
		{Content: "Read encrypt.go", Status: "completed"},
		{Content: "Fix retry loop", Status: "in_progress"},
		{Content: "Run tests", Status: "pending"},
	}, base)

	got := m.renderPlanPanel(80)
	if got == "" {
		t.Fatal("expected non-empty plan panel render")
	}
}
