package tui

import (
	"fmt"
	"strings"
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

// runningPlanModel builds a model with an n-step running plan for the pinned
// panel tests.
func runningPlanModel(t *testing.T, steps int) model {
	t.Helper()
	m := newModel(t.Context(), Options{ModelName: "gpt-4"})
	m.width = 100
	base := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return base.Add(15 * time.Second) }
	items := make([]tools.PlanItem, steps)
	for i := range items {
		status := "pending"
		if i == 0 {
			status = "in_progress"
		}
		items[i] = tools.PlanItem{Content: fmt.Sprintf("Step number %d here", i+1), Status: status}
	}
	m.plan.updateFromItems(items, base)
	return m
}

func TestPinnedPlanFullWhenItFits(t *testing.T) {
	m := runningPlanModel(t, 3)
	// 3 steps => header + bar + 3 lines = 5; budget 10 fits.
	got := m.renderPinnedPlanPanel(80, 10)
	if strings.Count(got, "\n")+1 < 5 {
		t.Fatalf("expected full multi-line panel within budget, got:\n%s", got)
	}
	if !strings.Contains(got, "Step number 1") || !strings.Contains(got, "Step number 3") {
		t.Fatalf("full panel should list every step, got:\n%s", got)
	}
}

func TestPinnedPlanCollapsesToSummaryWhenTooTall(t *testing.T) {
	m := runningPlanModel(t, 12)
	// 12 steps would be 14 lines; budget 5 forces the one-line summary.
	got := m.renderPinnedPlanPanel(80, 5)
	if strings.Contains(got, "\n") {
		t.Fatalf("over-budget plan should collapse to one line, got:\n%s", got)
	}
	if !strings.Contains(got, "PLAN") {
		t.Fatalf("summary line should still show PLAN, got:\n%s", got)
	}
}

func TestPinnedPlanHiddenWhenEmpty(t *testing.T) {
	m := newModel(t.Context(), Options{ModelName: "gpt-4"})
	m.width = 100
	if got := m.renderPinnedPlanPanel(80, 10); got != "" {
		t.Fatalf("no plan => empty pinned render, got %q", got)
	}
}

func TestFooterIncludesPinnedPlanAboveComposer(t *testing.T) {
	m := runningPlanModel(t, 3)
	m.height = 40
	footer := plainRender(t, m.footerView(chatWidth(m.width)))
	planIdx := strings.Index(footer, "PLAN")
	composerIdx := strings.Index(footer, "describe a task")
	if planIdx < 0 {
		t.Fatalf("footer should contain the pinned plan, got:\n%s", footer)
	}
	if composerIdx >= 0 && planIdx > composerIdx {
		t.Fatalf("pinned plan should render ABOVE the composer, got:\n%s", footer)
	}
}
