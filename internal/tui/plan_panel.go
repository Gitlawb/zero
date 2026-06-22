// plan_panel.go renders the sticky plan panel for the Zero TUI. The panel
// surfaces the in-progress task plan produced by the update_plan tool: a
// one-line header with a live spinner and progress count, a text progress
// bar, and (while running or expanded) the per-step list with status icons
// and timings. planPanelState tracks per-step start/completion timestamps
// across the tool's full-replacement updates so durations stay stable as
// steps transition between pending, in_progress, completed, and failed.
package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/Gitlawb/zero/internal/tools"
)

// planStep is one rendered plan item. The timestamps are preserved across
// update_plan calls (which replace the whole plan each time) by matching on
// content, so a step that flips from in_progress to completed keeps the
// startedAt it was first marked in_progress with.
type planStep struct {
	content     string
	status      string // "pending" | "in_progress" | "completed" | "failed"
	notes       string
	startedAt   time.Time
	completedAt time.Time
}

// planPanelState holds the sticky plan panel's view state. It lives on the
// model and is synced from the update_plan tool's CurrentPlan() output.
type planPanelState struct {
	steps       []planStep
	expanded    bool
	completedAt time.Time // set once all steps reach a terminal status
	startedAt   time.Time // set on the first non-empty update
	// frozenAt freezes the live plan clock while the agent is idle (between
	// turns / waiting on the user). Stamped when a run ends; cleared by clear()
	// at the next run's start. While set and idle, an in_progress step's elapsed
	// time stops ticking up instead of counting forever against a yielded turn.
	frozenAt time.Time
}

// completedHideAfter is how long a finished plan stays pinned before the
// collapsed panel hides itself to reclaim screen space.
const completedHideAfter = 30 * time.Second

// updateFromItems syncs the planStep slice from the update_plan tool. The
// tool replaces the entire plan each call, so steps are matched by content
// to preserve start/completion timestamps. Timestamps are filled in for
// newly-transitioned steps: startedAt on first in_progress, completedAt on
// first completed/failed. The panel-level startedAt is stamped on the first
// non-empty update, and completedAt when every step reaches a terminal
// status (and cleared again if the plan becomes incomplete later).
func (s *planPanelState) updateFromItems(items []tools.PlanItem, now time.Time) {
	if len(items) == 0 {
		s.steps = nil
		s.completedAt = time.Time{}
		return
	}

	if s.startedAt.IsZero() {
		s.startedAt = now
	}

	prev := s.steps
	// Consume each prior step at most once so two steps with identical content don't
	// both inherit the SAME prior entry's timestamps; positional order then breaks
	// the tie, giving duplicate-text steps their own start/complete times (L22).
	prevUsed := make([]bool, len(prev))
	next := make([]planStep, 0, len(items))
	for _, item := range items {
		step := planStep{
			content: item.Content,
			status:  item.Status,
			notes:   item.Notes,
		}
		// Carry over timestamps from the first unconsumed prior step with the same content.
		for pi := range prev {
			if !prevUsed[pi] && prev[pi].content == step.content {
				step.startedAt = prev[pi].startedAt
				step.completedAt = prev[pi].completedAt
				prevUsed[pi] = true
				break
			}
		}
		switch step.status {
		case "in_progress":
			if step.startedAt.IsZero() {
				step.startedAt = now
			}
		case "completed", "failed":
			if step.startedAt.IsZero() {
				step.startedAt = now
			}
			if step.completedAt.IsZero() {
				step.completedAt = now
			}
		}
		next = append(next, step)
	}
	s.steps = next

	if s.isComplete() {
		if s.completedAt.IsZero() {
			s.completedAt = now
		}
	} else {
		s.completedAt = time.Time{}
	}
}

// clear resets all plan panel state (steps, expansion, timestamps).
func (s *planPanelState) clear() {
	s.steps = nil
	s.expanded = false
	s.completedAt = time.Time{}
	s.startedAt = time.Time{}
	s.frozenAt = time.Time{}
}

// isEmpty reports whether the panel has no steps to show.
func (s planPanelState) isEmpty() bool {
	return len(s.steps) == 0
}

// isComplete reports whether every step has reached a terminal status
// (completed or failed). An empty plan is not complete.
func (s planPanelState) isComplete() bool {
	if len(s.steps) == 0 {
		return false
	}
	for _, step := range s.steps {
		if step.status != "completed" && step.status != "failed" {
			return false
		}
	}
	return true
}

// visible reports whether renderPlanPanel should emit anything. A finished
// plan hides itself once completedHideAfter has elapsed, unless expanded.
func (s planPanelState) visible(now time.Time) bool {
	if s.isEmpty() {
		return false
	}
	if s.isComplete() && !s.expanded && !s.completedAt.IsZero() && now.Sub(s.completedAt) > completedHideAfter {
		return false
	}
	return true
}

// height returns the number of terminal lines renderPlanPanel will occupy at
// the given width (0 when the panel is not visible). The step list is shown
// when the panel is expanded or still running; a collapsed, finished plan is
// just the header and progress bar.
func (s planPanelState) height(width int, now time.Time) int {
	if !s.visible(now) {
		return 0
	}
	if s.expanded || !s.isComplete() {
		return 2 + len(s.steps)
	}
	return 2
}

// planNow returns the clock used for live plan durations. While the agent is
// idle (no run in flight, activeRunID == 0) it freezes at the moment the last
// run ended, so an in_progress step left mid-plan when the agent yields (e.g.
// after ask_user) stops ticking up instead of counting forever against a turn
// that is no longer running. During a run it tracks the live clock as before.
func (m model) planNow() time.Time {
	if m.activeRunID == 0 && !m.plan.frozenAt.IsZero() {
		return m.plan.frozenAt
	}
	return m.now()
}

// renderPlanPanel renders the full sticky plan panel. It returns an empty
// string when the plan is empty or when a finished plan has been collapsed
// past completedHideAfter without being expanded.
func (m model) renderPlanPanel(width int) string {
	if width < 20 {
		width = 20
	}

	state := m.plan
	now := m.planNow()
	if !state.visible(now) {
		return ""
	}

	total := len(state.steps)
	done := 0
	for _, step := range state.steps {
		if step.status == "completed" || step.status == "failed" {
			done++
		}
	}

	elapsed := time.Duration(0)
	if !state.startedAt.IsZero() {
		elapsed = now.Sub(state.startedAt)
	}

	// The header already carries done/total and the per-step ✓/•/✗ icons convey
	// progress; a separate filled █/░ bar restates it and reads heavier than the
	// reference agents, so it's dropped.
	header := renderPlanHeader(state, m.spinnerGlyph(), done, total, elapsed)

	lines := []string{header}

	showSteps := state.expanded || !state.isComplete()
	if showSteps {
		maxContent := width - 15
		if maxContent < 4 {
			maxContent = 4
		}
		for _, step := range state.steps {
			lines = append(lines, renderPlanStepLine(step, now, maxContent))
		}
	}

	return strings.Join(lines, "\n")
}

// renderPinnedPlanPanel renders the plan for the pinned slot above the
// composer. It returns the full panel when it fits within maxHeight, otherwise
// a one-line summary (so a long plan can't crowd out the transcript or the
// input). maxHeight <= 0 means "no budget" and always uses the summary line.
// Returns "" when the plan is not visible. Ctrl+P (expand) still forces the
// full list via m.plan.expanded, but the height budget always wins to keep the
// composer on screen.
func (m model) renderPinnedPlanPanel(width int, maxHeight int) string {
	// When the two-column layout is active the plan lives in the context
	// sidebar (FILES / PLAN / tokens), so suppress the pinned panel above the
	// composer to avoid showing it twice. Both the real model (sidebarActive)
	// and the narrow chat-column copy (hidePinnedPlan) suppress it, so the
	// view and the mouse-geometry frame stay aligned.
	if m.hidePinnedPlan || m.sidebarActive() {
		return ""
	}
	if !m.plan.visible(m.now()) {
		return ""
	}
	full := m.renderPlanPanel(width)
	if full == "" {
		return ""
	}
	if maxHeight > 0 && strings.Count(full, "\n")+1 <= maxHeight {
		return full
	}
	return m.renderPlanSummaryLine(width)
}

// renderPlanSummaryLine renders the collapsed one-line plan summary used when
// the full panel won't fit the pinned slot: a spinner/check, the done/total
// count, and the current (or first incomplete) step, truncated to width.
func (m model) renderPlanSummaryLine(width int) string {
	if width < 20 {
		width = 20
	}
	state := m.plan
	total := len(state.steps)
	done := 0
	current := ""
	for _, step := range state.steps {
		switch step.status {
		case "completed", "failed":
			done++
		case "in_progress":
			if current == "" {
				current = step.content
			}
		}
	}
	if current == "" {
		// No in-progress step: show the first not-yet-done step, else the first.
		for _, step := range state.steps {
			if step.status != "completed" && step.status != "failed" {
				current = step.content
				break
			}
		}
	}
	if state.isComplete() {
		return zeroTheme.green.Render(fmt.Sprintf("✓ PLAN · %d/%d complete", done, total))
	}
	label := fmt.Sprintf("%s PLAN · %d/%d · ", m.spinnerGlyph(), done, total)
	room := width - len([]rune(label)) - 1
	if room < 4 {
		room = 4
	}
	return zeroTheme.accent.Render(label + truncateStep(current, room))
}

// renderPlanHeader builds the single header line. While running it shows the
// live spinner, the truncated first step, the done/total count, and the
// elapsed time in the accent color; once complete it shows a green check and
// "PLAN COMPLETE".
func renderPlanHeader(state planPanelState, spinnerView string, done, total int, elapsed time.Duration) string {
	first := ""
	if total > 0 {
		first = truncateStep(state.steps[0].content, 40)
	}
	if state.isComplete() {
		return zeroTheme.green.Render(fmt.Sprintf("✓ PLAN COMPLETE · %d/%d · %s", done, total, formatElapsedSeconds(elapsed)))
	}
	return zeroTheme.accent.Render(fmt.Sprintf("%s PLAN · %s · %d/%d · %s", spinnerView, first, done, total, formatElapsedSeconds(elapsed)))
}

// renderPlanStepLine renders one step row: an indent, a status icon, the
// (truncated) content styled by status, and a duration where applicable.
// Completed/failed steps show the startedAt→completedAt span; an in_progress
// step shows the elapsed time since it started; pending steps show no time.
func renderPlanStepLine(step planStep, now time.Time, maxContent int) string {
	content := truncateStep(step.content, maxContent)
	var icon, body, timeStr string
	switch step.status {
	case "completed":
		icon = zeroTheme.green.Render("✓")
		// Quiet the completed body (muted, not saturated green) so the single
		// in-progress accent step is the obvious focus: past=quiet, now=bright,
		// future=faint. The green ✓ icon still marks success.
		body = zeroTheme.muted.Render(content)
		timeStr = formatElapsedSeconds(step.completedAt.Sub(step.startedAt))
	case "in_progress":
		icon = zeroTheme.accent.Render("•")
		body = zeroTheme.accent.Render(content)
		started := step.startedAt
		if started.IsZero() {
			started = now
		}
		timeStr = formatElapsedSeconds(now.Sub(started))
	case "failed":
		icon = zeroTheme.red.Render("✗")
		body = zeroTheme.red.Render(content)
		timeStr = formatElapsedSeconds(step.completedAt.Sub(step.startedAt))
	default: // pending
		icon = zeroTheme.faint.Render("○")
		body = zeroTheme.faint.Render(content)
	}
	line := "  " + icon + " " + body
	if timeStr != "" {
		line += " " + zeroTheme.faint.Render(timeStr)
	}
	return line
}

// truncateStep caps s to max runes, appending an ellipsis when truncated.
func truncateStep(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	return string(r[:max-1]) + "…"
}
