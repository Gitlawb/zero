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
// a file mutation or command run while that step was in_progress.
type planStepWork struct {
	tool    string
	summary string
	detail  string // the tool's full output — the diff for edits, stdout/stderr for commands
}

// isPlanWorkTool reports whether a tool's result counts as implementation worth
// surfacing under a plan step — the file mutations and the commands run.
func isPlanWorkTool(name string) bool {
	switch name {
	case "write_file", "edit_file", "apply_patch", "bash", "exec_command":
		return true
	}
	return false
}

// isPlanCommandTool reports whether a captured work item is a command run (vs a
// file change), so the detail view can group them.
func isPlanCommandTool(name string) bool {
	return name == "bash" || name == "exec_command"
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
	m.stepWork[key] = append(m.stepWork[key], planStepWork{tool: row.tool, summary: planStepWorkSummary(row), detail: row.detail})
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

// planStepDetailRowID is the stable transcript id for the single plan-step
// detail card, so re-clicking toggles it instead of stacking duplicates.
const planStepDetailRowID = "plan/step-detail"

// dropTranscriptRowsByID returns the transcript with any rows carrying id removed.
func dropTranscriptRowsByID(rows []transcriptRow, id string) []transcriptRow {
	if id == "" {
		return rows
	}
	out := make([]transcriptRow, 0, len(rows))
	for _, r := range rows {
		if r.id != id {
			out = append(out, r)
		}
	}
	return out
}

// openPlanStepDetail toggles a transcript card showing what was built for the
// given plan step. Clicking the open step hides it; clicking a different step
// replaces it — so at most one detail card is shown and re-clicks never stack.
func (m model) openPlanStepDetail(stepIndex int) model {
	if stepIndex < 0 || stepIndex >= len(m.plan.steps) {
		return m
	}
	wasOpen := m.planDetailOpen && m.planDetailStep == stepIndex
	m.transcript = dropTranscriptRowsByID(m.transcript, planStepDetailRowID)
	if wasOpen {
		m.planDetailOpen = false
		return m
	}
	m.planDetailOpen = true
	m.planDetailStep = stepIndex

	step := m.plan.steps[stepIndex]
	work := m.stepWork[step.content]

	var changes, commands []planStepWork
	for _, w := range work {
		if isPlanCommandTool(w.tool) {
			commands = append(commands, w)
		} else {
			changes = append(changes, w)
		}
	}

	var lines []string
	if len(changes) > 0 {
		lines = append(lines, "Changes:")
		lines = append(lines, planWorkLines(changes)...)
	}
	if len(commands) > 0 {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, "Commands:")
		lines = append(lines, planWorkLines(commands)...)
	}
	if len(lines) == 0 {
		lines = append(lines, "No file changes or commands recorded for this step yet.")
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
		Hints: []string{"diffs + commands captured while this step was in progress"},
	})
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowSystem, tool: "plan", id: planStepDetailRowID, text: card})
	return m
}

// planWorkLines renders each work item as a summary line plus a short, indented
// excerpt of its diff/output, truncated so one step's card can't flood the chat.
func planWorkLines(items []planStepWork) []string {
	const maxDetailLines = 6
	var out []string
	for _, w := range items {
		out = append(out, "  • "+w.summary)
		detail := strings.TrimRight(w.detail, "\n")
		if strings.TrimSpace(detail) == "" {
			continue
		}
		dl := strings.Split(detail, "\n")
		more := 0
		if len(dl) > maxDetailLines {
			more = len(dl) - maxDetailLines
			dl = dl[:maxDetailLines]
		}
		for _, line := range dl {
			out = append(out, "      "+line)
		}
		if more > 0 {
			out = append(out, fmt.Sprintf("      … (%d more lines)", more))
		}
	}
	return out
}
