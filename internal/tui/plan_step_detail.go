// plan_step_detail.go makes the context-sidebar plan steps clickable: each step
// records the file mutations made while it was in_progress ("what was built"),
// and clicking a step drops a transcript card listing them. The work is captured
// from tool-result rows as they stream; the click maps a sidebar y-coordinate to
// a step, mirroring sidebarAgentSelectables' offset accounting.
package tui

import (
	"fmt"
	"strings"
	"time"

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

// captureStepNarration attributes a finalized assistant prose segment to the
// plan step that was in_progress when it streamed — the agent's own running
// account of the work, replayed as the step-detail card's explanation.
// Consecutive duplicates are collapsed so a re-emitted segment doesn't repeat.
func (m model) captureStepNarration(text string) model {
	text = strings.TrimSpace(text)
	if text == "" {
		return m
	}
	key := currentStepContent(m.plan.steps)
	if key == "" {
		return m
	}
	if m.stepNarration == nil {
		m.stepNarration = map[string][]string{}
	}
	prev := m.stepNarration[key]
	if n := len(prev); n > 0 && prev[n-1] == text {
		return m
	}
	m.stepNarration[key] = append(prev, text)
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

// openPlanStepDetail toggles a transcript card explaining a plan step. The card
// adapts to the step's status: a finished step (completed/failed) reads as "what
// we did" — the outcome, how long it took, the model's own note, and the file
// changes + commands captured while it ran; an unfinished step (pending or
// in_progress) reads as "what we will do / are doing" — its intent, the planned
// approach, and any work captured so far. Clicking the open step hides it;
// clicking a different step replaces it, so at most one card shows at a time.
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
	done := step.status == "completed" || step.status == "failed"

	// The lead section is titled with the step itself and opens with a
	// status-aware outcome line ("Done in 1m 20s." / "Not started yet…").
	sections := []commandSection{{
		Title: step.content,
		Lines: m.planStepOutcomeLines(step, len(changes), len(commands)),
	}}

	// The centerpiece: a prose explanation of the step, framed by status — "what
	// we did" for a finished step, "what we'll do" for a queued one. It replays
	// the agent's own narration when captured, else a status-aware summary.
	sections = append(sections, commandSection{
		Title: planStepExplanationTitle(step.status),
		Lines: m.planStepExplanationLines(step),
	})

	// The model's own per-step note is a second, distilled statement of intent
	// (before) or record (after); surface it verbatim under a fitting label.
	if note := strings.TrimSpace(step.notes); note != "" {
		label := "Plan"
		if done {
			label = "Notes"
		}
		sections = append(sections, commandSection{Title: label, Lines: planWrapText(note, 76)})
	}

	if len(changes) > 0 {
		sections = append(sections, commandSection{
			Title: fmt.Sprintf("Files changed (%d)", len(changes)),
			Lines: planChangeLines(changes),
		})
	}
	if len(commands) > 0 {
		sections = append(sections, commandSection{
			Title: fmt.Sprintf("Commands run (%d)", len(commands)),
			Lines: planCommandLines(commands),
		})
	}

	card := renderCommandOutput(commandOutput{
		Title:    fmt.Sprintf("Plan step %d of %d · %s", stepIndex+1, len(m.plan.steps), planStepStateLabel(step.status)),
		Status:   planStepDetailStatus(step.status),
		Sections: sections,
		Hints:    []string{planStepDetailHint(step.status)},
	})
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowSystem, tool: "plan", id: planStepDetailRowID, text: card})
	return m
}

// planStepOutcomeLines opens the lead section with a status-aware framing of the
// step: a finished step gets an outcome + duration ("Done in 1m 20s.") and a
// tally of what was built; an in_progress step gets its running clock and the
// work so far; a pending step is framed forward-looking ("what this step will
// do"), since no work has been captured yet.
func (m model) planStepOutcomeLines(step planStep, nChanges, nCommands int) []string {
	switch step.status {
	case "completed":
		head := "Done."
		if d := formatPlanStepDuration(step.completedAt.Sub(step.startedAt)); d != "" {
			head = "Done in " + d + "."
		}
		return []string{head, planWorkTally("Built", nChanges, nCommands)}
	case "failed":
		head := "Failed."
		if d := formatPlanStepDuration(step.completedAt.Sub(step.startedAt)); d != "" {
			head = "Failed after " + d + "."
		}
		return []string{head, planWorkTally("Attempted", nChanges, nCommands)}
	case "in_progress":
		head := "In progress."
		if d := formatPlanStepDuration(m.planNow().Sub(step.startedAt)); d != "" {
			head = "In progress — running for " + d + "."
		}
		return []string{head, planWorkTally("So far", nChanges, nCommands)}
	default: // pending
		return []string{"Not started yet — here's what this step will do."}
	}
}

// planStepExplanationTitle is the header for the prose explanation section,
// matching the status framing the user asked for: "what we did" for a finished
// step, "what we'll do" for a queued one.
func planStepExplanationTitle(status string) string {
	switch status {
	case "completed", "failed":
		return "What we did"
	case "in_progress":
		return "What we're doing"
	default:
		return "What we'll do"
	}
}

// planStepExplanationLines is the elaborate, prose explanation of a step. When
// the agent narrated its work while the step was in_progress, those segments are
// replayed verbatim (the most faithful account of what happened); otherwise a
// status-aware summary stands in, pointing at the structured sections below.
func (m model) planStepExplanationLines(step planStep) []string {
	if segs := m.stepNarration[step.content]; len(segs) > 0 {
		return planWrapText(strings.Join(segs, "\n"), 76)
	}
	switch step.status {
	case "completed":
		return planWrapText("This step finished. The agent didn't post a written summary, so the files and commands it touched are listed below.", 76)
	case "failed":
		return planWrapText("This step failed. The agent didn't post a written summary; what it attempted is listed below.", 76)
	case "in_progress":
		return planWrapText("Work is underway — the agent hasn't summarized this step yet. Changes and commands appear below as they happen.", 76)
	default: // pending
		return planWrapText("This step is queued and hasn't started. Once the agent reaches it, its explanation, file changes, and commands will appear here.", 76)
	}
}

// planWorkTally summarizes how much work a step captured, or notes that none was
// recorded yet. verb frames it for the step's state ("Built", "So far", …).
func planWorkTally(verb string, nChanges, nCommands int) string {
	if nChanges == 0 && nCommands == 0 {
		return "No file changes or commands recorded yet."
	}
	var parts []string
	if nChanges > 0 {
		parts = append(parts, pluralCount(nChanges, "file change"))
	}
	if nCommands > 0 {
		parts = append(parts, pluralCount(nCommands, "command"))
	}
	return verb + " " + strings.Join(parts, " and ") + "."
}

// planChangeLines lists each file mutation as a bullet with a +added/−removed
// diffstat and a short excerpt of the changed lines.
func planChangeLines(items []planStepWork) []string {
	var out []string
	for _, w := range items {
		head := "• " + w.summary
		if add, del := planDiffStat(w.detail); add+del > 0 {
			head += fmt.Sprintf("  (+%d −%d)", add, del)
		}
		out = append(out, head)
		out = append(out, planDetailExcerpt(w.detail, 3)...)
	}
	return out
}

// planCommandLines lists each command run as a bullet with a short excerpt of
// its output.
func planCommandLines(items []planStepWork) []string {
	var out []string
	for _, w := range items {
		out = append(out, "• "+w.summary)
		out = append(out, planDetailExcerpt(w.detail, 3)...)
	}
	return out
}

// planDiffStat counts added/removed lines in a unified diff, ignoring the
// +++/--- file headers. Output that isn't a diff (e.g. a written file's body or
// command stdout) yields no counts.
func planDiffStat(detail string) (added, removed int) {
	for _, line := range strings.Split(detail, "\n") {
		switch {
		case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
			continue
		case strings.HasPrefix(line, "+"):
			added++
		case strings.HasPrefix(line, "-"):
			removed++
		}
	}
	return added, removed
}

// planDetailExcerpt returns up to max non-blank lines of a tool's output for the
// card, trimmed, with a trailing "… (N more lines)" when truncated. The card
// renderer collapses indentation, so this only needs to keep each line short.
func planDetailExcerpt(detail string, max int) []string {
	if strings.TrimSpace(detail) == "" {
		return nil
	}
	var kept []string
	for _, line := range strings.Split(detail, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		kept = append(kept, strings.TrimSpace(line))
	}
	more := 0
	if len(kept) > max {
		more = len(kept) - max
		kept = kept[:max]
	}
	if more > 0 {
		kept = append(kept, fmt.Sprintf("… (%d more line%s)", more, map[bool]string{true: "s", false: ""}[more != 1]))
	}
	return kept
}

// planStepStateLabel is the short human label after "Plan step N of M ·" in the
// card title.
func planStepStateLabel(status string) string {
	switch status {
	case "completed":
		return "done"
	case "failed":
		return "failed"
	case "in_progress":
		return "in progress"
	default:
		return "up next"
	}
}

// planStepDetailStatus maps a step's status to the card's status banner: a
// completed step reads ok, a failed step blocked, everything else neutral info.
func planStepDetailStatus(status string) commandStatus {
	switch status {
	case "completed":
		return commandStatusOK
	case "failed":
		return commandStatusBlocked
	default:
		return commandStatusInfo
	}
}

// planStepDetailHint is the trailing one-line hint, framing the card as a record
// ("what was done") or a preview ("what this step will do").
func planStepDetailHint(status string) string {
	switch status {
	case "completed", "failed":
		return "what was done in this step"
	case "in_progress":
		return "what this step is doing now"
	default:
		return "what this step will do"
	}
}

// formatPlanStepDuration renders a step's span in a friendlier form than the raw
// footer clock: seconds under a minute, "Nm Ns" above. A zero or negative span
// renders empty so callers can drop the clause.
func formatPlanStepDuration(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()+0.5))
	}
	mins := int(d / time.Minute)
	secs := int((d % time.Minute) / time.Second)
	if secs == 0 {
		return fmt.Sprintf("%dm", mins)
	}
	return fmt.Sprintf("%dm %ds", mins, secs)
}

// planWrapText word-wraps s to width columns, returning one line per wrapped
// row, so a long note isn't truncated by the card's single-line fitter.
func planWrapText(s string, width int) []string {
	if width < 8 {
		width = 8
	}
	var out []string
	for _, para := range strings.Split(strings.TrimSpace(s), "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			continue
		}
		line := words[0]
		for _, w := range words[1:] {
			if len(line)+1+len(w) > width {
				out = append(out, line)
				line = w
				continue
			}
			line += " " + w
		}
		out = append(out, line)
	}
	return out
}
