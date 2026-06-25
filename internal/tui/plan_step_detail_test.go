package tui

import (
	"strings"
	"testing"
	"time"
)

// lastCardText returns the text of the most recently appended transcript row —
// the plan-step detail card built by openPlanStepDetail.
func lastCardText(m model) string {
	if len(m.transcript) == 0 {
		return ""
	}
	return m.transcript[len(m.transcript)-1].text
}

// TestPlanStepDetailByStatus: a completed step reads as "what we did" (outcome,
// duration, the model's note, and the captured changes/commands); a pending
// step reads as "what we will do" (forward framing + the planned approach).
func TestPlanStepDetailByStatus(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m := model{now: func() time.Time { return t0.Add(5 * time.Minute) }}
	m.plan.steps = []planStep{
		{content: "ship the button", status: "completed", notes: "wired the click handler", startedAt: t0, completedAt: t0.Add(80 * time.Second)},
		{content: "write the docs", status: "pending", notes: "cover the new flag"},
	}
	m.stepWork = map[string][]planStepWork{
		"ship the button": {
			{tool: "edit_file", summary: "edit button.go", detail: "+added line\n-removed line"},
			{tool: "bash", summary: "go build", detail: "exit 0"},
		},
	}
	// The agent narrated this step while it ran; the card should replay it.
	m.stepNarration = map[string][]string{
		"ship the button": {"Let me wire the click handler into the button so it dispatches the action."},
	}

	// Completed step -> "what we did", led by the agent's own narration.
	m = m.openPlanStepDetail(0)
	done := lastCardText(m)
	for _, want := range []string{"Done in 1m 20s.", "Built 1 file change and 1 command.", "What we did", "wire the click handler into the button", "wired the click handler", "Files changed (1)", "Commands run (1)", "what was done in this step"} {
		if !strings.Contains(done, want) {
			t.Errorf("completed card missing %q\n---\n%s", want, done)
		}
	}

	// Pending step -> "what we will do".
	m = m.openPlanStepDetail(1)
	pending := lastCardText(m)
	for _, want := range []string{"What we'll do", "what this step will do", "cover the new flag", "queued"} {
		if !strings.Contains(pending, want) {
			t.Errorf("pending card missing %q\n---\n%s", want, pending)
		}
	}
	if strings.Contains(pending, "Files changed") {
		t.Errorf("pending card should record no work yet:\n%s", pending)
	}
}

// TestCaptureStepNarration: assistant prose is attributed to the in_progress
// step, blank/duplicate segments are dropped, and no step swallows another's.
func TestCaptureStepNarration(t *testing.T) {
	m := model{now: time.Now}
	m.plan.steps = []planStep{{content: "build it", status: "in_progress"}}
	m = m.captureStepNarration("First I'll scaffold the file.")
	m = m.captureStepNarration("First I'll scaffold the file.") // duplicate -> collapsed
	m = m.captureStepNarration("   ")                           // blank -> ignored
	m = m.captureStepNarration("Now I'll wire it up.")
	got := m.stepNarration["build it"]
	if len(got) != 2 {
		t.Fatalf("want 2 narration segments, got %d: %v", len(got), got)
	}
	if got[0] != "First I'll scaffold the file." || got[1] != "Now I'll wire it up." {
		t.Errorf("narration wrong: %v", got)
	}
}

// TestSidebarPlanSelectablesOffsets locks the click-to-step mapping against the
// renderContextSidebar layout: with no agents the AGENTS section is header +
// placeholder (2 lines), then a blank + PLAN header (2 lines), so step 0 sits on
// sidebar line 4.
func TestSidebarPlanSelectablesOffsets(t *testing.T) {
	m := model{now: time.Now}
	m.plan.steps = []planStep{
		{content: "a", status: "completed"},
		{content: "b", status: "in_progress"},
		{content: "c", status: "pending"},
	}
	hits := m.sidebarPlanSelectables(40)
	if len(hits) != 3 {
		t.Fatalf("want 3 hits, got %d", len(hits))
	}
	for i, want := range []int{4, 5, 6} {
		if hits[i].lineOffset != want || hits[i].stepIndex != i {
			t.Errorf("hit %d: want offset %d idx %d, got offset %d idx %d", i, want, i, hits[i].lineOffset, hits[i].stepIndex)
		}
	}
	// Empty plan -> no selectables.
	if got := (model{now: time.Now}).sidebarPlanSelectables(40); got != nil {
		t.Errorf("empty plan: want nil, got %v", got)
	}
}

// TestCaptureStepWork: file mutations AND commands are attributed to the
// in_progress step with their output captured; non-work tools and non-results
// are ignored.
func TestCaptureStepWork(t *testing.T) {
	m := model{now: time.Now}
	m.plan.steps = []planStep{{content: "build it", status: "in_progress"}}
	m = m.captureStepWork(transcriptRow{kind: rowToolResult, tool: "write_file", text: "wrote style.css", detail: "+ body {}"})
	m = m.captureStepWork(transcriptRow{kind: rowToolResult, tool: "bash", text: "ran go build", detail: "exit 0"})
	m = m.captureStepWork(transcriptRow{kind: rowToolResult, tool: "read_file", text: "read x"}) // ignored: not a work tool
	m = m.captureStepWork(transcriptRow{kind: rowToolCall, tool: "write_file", text: "call"})    // ignored: not a result
	work := m.stepWork["build it"]
	if len(work) != 2 {
		t.Fatalf("want 2 captured items (write_file + bash), got %d", len(work))
	}
	if work[0].tool != "write_file" || work[0].detail != "+ body {}" {
		t.Errorf("change item wrong: %+v", work[0])
	}
	if work[1].tool != "bash" || work[1].detail != "exit 0" {
		t.Errorf("command item wrong: %+v", work[1])
	}
	if !isPlanCommandTool("bash") || !isPlanCommandTool("exec_command") || isPlanCommandTool("write_file") {
		t.Errorf("isPlanCommandTool classification wrong")
	}
}

// TestPlanStepDetailToggle: re-clicking the open step hides the card (no
// stacking); clicking a different step switches; at most one card at a time.
func TestPlanStepDetailToggle(t *testing.T) {
	m := model{now: time.Now}
	m.plan.steps = []planStep{
		{content: "a", status: "completed"},
		{content: "b", status: "in_progress"},
	}
	base := len(m.transcript)

	m = m.openPlanStepDetail(0)
	if !m.planDetailOpen || m.planDetailStep != 0 {
		t.Fatalf("first click should open step 0: open=%v step=%d", m.planDetailOpen, m.planDetailStep)
	}
	if len(m.transcript) != base+1 {
		t.Fatalf("first click should add one card: got %d, base %d", len(m.transcript), base)
	}

	m = m.openPlanStepDetail(0)
	if m.planDetailOpen {
		t.Error("re-clicking the same step should close it")
	}
	if len(m.transcript) != base {
		t.Errorf("re-click should net zero growth: got %d, base %d", len(m.transcript), base)
	}

	m = m.openPlanStepDetail(0)
	m = m.openPlanStepDetail(1)
	if !m.planDetailOpen || m.planDetailStep != 1 {
		t.Errorf("clicking a different step should switch: open=%v step=%d", m.planDetailOpen, m.planDetailStep)
	}
	if len(m.transcript) != base+1 {
		t.Errorf("switching steps should keep exactly one card: got %d, base %d", len(m.transcript), base)
	}
}
