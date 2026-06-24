// plan_step_detail.go makes the context-sidebar plan steps clickable: each step
// records the file mutations made while it was in_progress ("what was built"),
// and clicking a step drops a transcript card listing them. The work is captured
// from tool-result rows as they stream; the click maps a sidebar y-coordinate to
// a step, mirroring sidebarAgentSelectables' offset accounting.
package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// planStepWork is one captured unit of implementation attributed to a plan step:
// a file-mutating tool result recorded while that step was in_progress.
type planStepWork struct {
	tool    string
	summary string
}

// isPlanWorkTool reports whether a tool's result counts as implementation worth
// surfacing under a plan step — the file mutations.
func isPlanWorkTool(name string) bool {
	switch name {
	case "write_file", "edit_file", "apply_patch":
		return true
	}
	return false
}

// planStepWorkSummary renders a concise one-line summary of a tool-result row
// for the step-detail card (the row's first line, else the tool name).
func planStepWorkSummary(row transcriptRow) string {
	text := strings.TrimSpace(strings.SplitN(row.text, "\n", 2)[0])
	if text == "" {
		text = row.tool
	}
	return text
}

// captureStepWork attributes a finished tool-result row to the plan step that
// was in_progress when it ran. No-op for non-mutation tools or when no step is
// active. Keyed by step content (stable enough across the model's full-replace
// plan updates for this read-only view).
func (m model) captureStepWork(row transcriptRow) model {
	if row.kind != rowToolResult || !isPlanWorkTool(row.tool) {
		return m
	}
	key := currentStepContent(m.plan.steps)
	if key == "" {
		return m
	}
	if m.stepWork == nil {
		m.stepWork = map[string][]planStepWork{}
	}
	m.stepWork[key] = append(m.stepWork[key], planStepWork{tool: row.tool, summary: planStepWorkSummary(row)})
	return m
}

// planStepHit is a clickable plan-step row in the context sidebar.
type planStepHit struct {
	lineOffset int
	stepIndex  int
}

// sidebarPlanSelectables returns each plan step's clickable sidebar line. The
// PLAN section renders after AGENTS in renderContextSidebar: the AGENTS header +
// its body (the agent lines, or a 1-line placeholder), then a blank line and the
// PLAN header, then one line per step (sidebarPlanLines is one line per step).
// The offset accounting mirrors that layout exactly.
func (m model) sidebarPlanSelectables(width int) []planStepHit {
	if m.plan.isEmpty() {
		return nil
	}
	agentBody := len(m.sidebarAgentLines(width))
	if agentBody == 0 {
		agentBody = 1 // the "no agents spawned" placeholder occupies one line
	}
	base := 1 + agentBody + 2 // AGENTS header + body + (blank line + PLAN header)
	hits := make([]planStepHit, 0, len(m.plan.steps))
	for i := range m.plan.steps {
		hits = append(hits, planStepHit{lineOffset: base + i, stepIndex: i})
	}
	return hits
}

// planStepAtMouse maps a left-click in the context sidebar to a plan step index,
// mirroring sidebarLineAtMouse's column/x gate.
func (m model) planStepAtMouse(msg tea.MouseMsg) (int, bool) {
	if !m.sidebarActive() {
		return 0, false
	}
	if m.setup.visible || m.providerWizard != nil || m.mcpAddWizard != nil || m.mcpManager != nil || m.picker != nil || m.suggestionsActive() {
		return 0, false
	}
	sidebarW := sidebarWidth(m.width)
	if sidebarW <= 0 {
		return 0, false
	}
	x0 := m.chatColumnWidth() + 3 // " │ " divider between the columns
	x, y := mouseX(msg), mouseY(msg)
	if x < x0 || x >= x0+sidebarW {
		return 0, false
	}
	for _, hit := range m.sidebarPlanSelectables(sidebarW) {
		if hit.lineOffset == y {
			return hit.stepIndex, true
		}
	}
	return 0, false
}

// openPlanStepDetail appends a transcript card showing what was built for the
// given plan step: its status and the file changes captured while it ran.
func (m model) openPlanStepDetail(stepIndex int) model {
	if stepIndex < 0 || stepIndex >= len(m.plan.steps) {
		return m
	}
	step := m.plan.steps[stepIndex]
	work := m.stepWork[step.content]
	lines := make([]string, 0, len(work)+1)
	if len(work) == 0 {
		lines = append(lines, "No file changes recorded for this step yet.")
	} else {
		for _, w := range work {
			lines = append(lines, "• "+w.summary)
		}
	}
	status := step.status
	if status == "" {
		status = "pending"
	}
	card := renderCommandOutput(commandOutput{
		Title:  fmt.Sprintf("Plan step %d", stepIndex+1),
		Status: commandStatusOK,
		Sections: []commandSection{{
			Title: step.content + " · " + status,
			Lines: lines,
		}},
		Hints: []string{"file changes made while this step was in progress"},
	})
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowSystem, tool: "plan", text: card})
	return m
}
