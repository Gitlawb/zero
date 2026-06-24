package tui

import (
	"testing"
	"time"
)

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
