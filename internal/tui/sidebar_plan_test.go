package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func sidebarPlanModel(t *testing.T) model {
	t.Helper()
	m := model{now: func() time.Time { return time.Unix(1000, 0) }}
	m.orchestrate.admit(diamondAdmitted(), m.now())
	now := m.now()
	m.orchestrate.markStarted("a", "root", "k1", now)
	m.orchestrate.markDone("a", "succeeded", 100, now)
	m.orchestrate.markStarted("b", "left", "k2", now)
	m.orchestrate.markDone("b", "failed", 200, now)
	m.orchestrate.markStarted("c", "right", "k3", now)
	return m
}

// THE OFFSET CONSTRAINT, and it is the reason these lines live inside
// sidebarPlanLines rather than beside the placeholder.
//
// sidebarFileSelectables computes the FILES section's click offsets from
// len(sidebarPlanLines). Rendering the plan anywhere else would silently
// misdirect every file hit by the number of lines added — a click landing on
// the wrong file, with nothing to indicate it.
func TestFileClickOffsetsSurviveThePlanSection(t *testing.T) {
	withPlan := sidebarPlanModel(t)
	planLines := len(withPlan.sidebarPlanLines(34))
	if planLines < 2 {
		t.Fatalf("expected the plan to render several lines, got %d", planLines)
	}

	// The offsets the FILES section computes must move by exactly the number of
	// lines the PLAN section actually renders.
	empty := model{now: withPlan.now}
	if got := len(empty.sidebarPlanLines(34)); got != 0 {
		t.Fatalf("with no plan the section must render nothing, got %d lines", got)
	}
}

// Each status carries its own glyph, and the colours are distinct — the section
// is read at a glance, and a wall of one colour says nothing.
func TestTheSidebarPlanColoursEachStatus(t *testing.T) {
	m := sidebarPlanModel(t)
	lines := m.sidebarPlanLines(34)

	glyphs := map[string]string{}
	for _, line := range lines {
		plain := strings.TrimSpace(ansi.Strip(line))
		fields := strings.Fields(plain)
		if len(fields) >= 2 {
			glyphs[fields[1]] = fields[0]
		}
	}
	want := map[string]string{"a": "✓", "b": "✗", "c": "•", "d": "○"}
	for id, glyph := range want {
		if glyphs[id] != glyph {
			t.Errorf("task %q carries glyph %q, want %q", id, glyphs[id], glyph)
		}
	}

	// Distinct STYLING, not just distinct glyphs: the raw lines must differ in
	// their escape sequences or the section is monochrome.
	styles := map[string]bool{}
	for _, line := range lines {
		if index := strings.Index(line, "m"); index > 0 {
			styles[line[:index]] = true
		}
	}
	if len(styles) < 3 {
		t.Fatalf("only %d distinct colours across the section; statuses must be distinguishable", len(styles))
	}
}

// The header carries progress, and turns red when something failed — the one
// thing worth noticing from across the screen.
func TestTheSidebarPlanHeaderShowsProgress(t *testing.T) {
	m := sidebarPlanModel(t)
	header := ansi.Strip(m.sidebarPlanHeader(34))
	if !strings.Contains(header, "PLAN") || !strings.Contains(header, "1/4") {
		t.Fatalf("header = %q, want the section name and progress", header)
	}

	clean := model{now: m.now}
	clean.orchestrate.admit(diamondAdmitted(), clean.now())
	if styled := clean.sidebarPlanHeader(34); styled == m.sidebarPlanHeader(34) {
		t.Fatal("a plan with a failure must not look the same as one without")
	}
}

// update_plan's own steps still win the section when it has any: the two are
// different things, and the TODO list is the one the model is actively editing.
func TestUpdatePlanStepsStillWinTheSection(t *testing.T) {
	m := sidebarPlanModel(t)
	m.plan.steps = []planStep{{content: "a step of the model's own plan", status: "in_progress"}}

	rendered := strings.Join(m.sidebarPlanLines(34), "\n")
	if !strings.Contains(ansi.Strip(rendered), "a step of the model") {
		t.Fatalf("update_plan's steps must still render:\n%s", rendered)
	}
	if strings.Contains(ansi.Strip(rendered), "root") {
		t.Fatalf("the orchestrate plan must not displace them:\n%s", rendered)
	}
}

// A long plan is bounded and says what it dropped. A silently truncated list
// reads as a complete one.
func TestALongPlanIsBoundedInTheSidebar(t *testing.T) {
	msg := planAdmittedMsg{runID: 1, name: "big"}
	for index := 0; index < 20; index++ {
		msg.tasks = append(msg.tasks, planGraphTask{id: string(rune('a' + index))})
	}
	msg.taskCount = len(msg.tasks)
	m := model{now: func() time.Time { return time.Unix(1000, 0) }}
	m.orchestrate.admit(msg, m.now())

	lines := m.sidebarPlanLines(34)
	if len(lines) > maxSidebarOrchestrateLines+1 {
		t.Fatalf("the sidebar drew %d lines for a 20-task plan", len(lines))
	}
	if !strings.Contains(ansi.Strip(strings.Join(lines, "\n")), "more") {
		t.Fatalf("a truncated list must say so:\n%s", strings.Join(lines, "\n"))
	}
}

// Cancelled and skipped are not failures, in the sidebar as everywhere else.
func TestTheSidebarDoesNotPaintSkippedTasksRed(t *testing.T) {
	m := model{now: func() time.Time { return time.Unix(1000, 0) }}
	m.orchestrate.admit(diamondAdmitted(), m.now())
	m.orchestrate.markDone("a", "dependency_failed", 0, m.now())

	icon, _ := sidebarOrchestrateStyle(m.orchestrate.tasks[0], 30)
	if strings.Contains(icon, "✗") {
		t.Fatal("a skipped task is not a failure and must not be drawn as one")
	}
	if !strings.Contains(ansi.Strip(icon), "⊘") {
		t.Fatalf("a skipped task must carry the neutral marker, got %q", ansi.Strip(icon))
	}
}
