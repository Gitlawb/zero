package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// orchestrateTaskStatus is a plan task's state in the panel.
type orchestrateTaskStatus int

const (
	orchestratePending orchestrateTaskStatus = iota
	orchestrateRunning
	orchestrateDone
	orchestrateFailed
	orchestrateSkipped
	orchestrateCancelled
)

func (s orchestrateTaskStatus) label() string {
	switch s {
	case orchestrateRunning:
		return "running"
	case orchestrateDone:
		return "done"
	case orchestrateFailed:
		return "failed"
	case orchestrateSkipped:
		return "skipped"
	case orchestrateCancelled:
		return "cancelled"
	default:
		return "pending"
	}
}

// orchestrateTask is one row of the panel.
type orchestrateTask struct {
	id        string
	dependsOn []string
	phase     string
	status    orchestrateTaskStatus
	summary   string
	startedAt time.Time
	endedAt   time.Time
	// depth is the task's rank in the dependency graph: 0 for a task with no
	// dependencies, otherwise one more than its deepest dependency. This is what
	// makes a diamond LOOK like a diamond — tasks at the same depth ran from the
	// same fan-out and are drawn on the same rung.
	depth int
	// attempts is how many times the task ran. A stalled task is retried, and
	// the retry happens INSIDE the executor — one dispatch, one card — so
	// without this the panel shows a task that silently took twice as long.
	attempts int
	// cardKey links this task to its live agent state in the specialist
	// tracker: the temporary key while it runs, the child's real session id
	// once it finishes. The detail pane reads tool counts, the current tool and
	// spend through it rather than duplicating that state here.
	cardKey string
}

// elapsed is the task's duration: live while running, frozen once ended.
func (t orchestrateTask) elapsed(now time.Time) time.Duration {
	if t.startedAt.IsZero() {
		return 0
	}
	if t.endedAt.IsZero() {
		return now.Sub(t.startedAt)
	}
	return t.endedAt.Sub(t.startedAt)
}

// orchestratePanelState is the live view of one orchestrate plan.
//
// Distinct from planPanelState, which belongs to the update_plan tool (the
// model's own TODO list). Two different things called "plan" is a real
// collision risk, so this type, its command and its state are named
// ORCHESTRATE throughout, and only the user-facing command says "plans".
type orchestratePanelState struct {
	name        string
	tasks       []orchestrateTask
	byID        map[string]int
	status      string
	tokensUsed  int
	tokenLimit  int
	maxSpeedup  float64
	startedAt   time.Time
	completedAt time.Time
	// frozenAt stops the header clock when the run ends without a terminal plan
	// event — an interrupt or a crash. Mirrors planPanelState.frozenAt.
	frozenAt time.Time
	expanded bool
	// sidebarCollapsed hides the task list and detail in the right column,
	// toggled by clicking the sidebar's PLAN header. Separate from `expanded`,
	// which is the inline panel above the composer: they are two surfaces and
	// collapsing one must not silently collapse the other.
	sidebarCollapsed bool
}

func (s *orchestratePanelState) clear() { *s = orchestratePanelState{} }

func (s orchestratePanelState) isEmpty() bool { return len(s.tasks) == 0 }

func (s orchestratePanelState) isComplete() bool { return s.status != "" }

// admit installs the shape. Called once per plan, before any task runs.
func (s *orchestratePanelState) admit(msg planAdmittedMsg, now time.Time) {
	s.clear()
	s.name = msg.name
	s.tokenLimit = msg.tokenLimit
	s.startedAt = now
	s.byID = make(map[string]int, len(msg.tasks))
	s.tasks = make([]orchestrateTask, 0, len(msg.tasks))
	for _, task := range msg.tasks {
		s.byID[task.id] = len(s.tasks)
		s.tasks = append(s.tasks, orchestrateTask{
			id:        task.id,
			dependsOn: task.dependsOn,
			phase:     task.phase,
			status:    orchestratePending,
		})
	}
	s.computeDepths()
}

// computeDepths ranks tasks by dependency depth. The message carries them in
// the validated topological order, so every dependency is already ranked when a
// task is reached — the same property criticalPath relies on.
func (s *orchestratePanelState) computeDepths() {
	for index := range s.tasks {
		depth := 0
		for _, dep := range s.tasks[index].dependsOn {
			position, ok := s.byID[dep]
			if !ok || position >= index {
				continue
			}
			if next := s.tasks[position].depth + 1; next > depth {
				depth = next
			}
		}
		s.tasks[index].depth = depth
	}
}

func (s *orchestratePanelState) markStarted(taskID, summary, cardKey string, now time.Time) {
	index, ok := s.byID[taskID]
	if !ok {
		return
	}
	s.tasks[index].status = orchestrateRunning
	s.tasks[index].summary = summary
	s.tasks[index].startedAt = now
	if cardKey != "" {
		s.tasks[index].cardKey = cardKey
	}
}

// linkCard repoints a task at its child's real session id, so the detail pane
// keeps finding it after the tracker reconciles the temporary key.
func (s *orchestratePanelState) linkCard(taskID, cardKey string) {
	if index, ok := s.byID[taskID]; ok && cardKey != "" {
		s.tasks[index].cardKey = cardKey
	}
}

func (s *orchestratePanelState) markDone(taskID, outcome string, tokens, attempts int, now time.Time) {
	index, ok := s.byID[taskID]
	if !ok {
		return
	}
	// ACCUMULATE AS TASKS FINISH. tokensUsed was only ever assigned from the
	// plan_completed event, which arrives when the whole plan ends — so the
	// footer read "budget 0/200000" for the entire run while the cards above it
	// showed tens of thousands of tokens spent. A budget line that reports zero
	// while spending is the one number a user is watching it for.
	s.tokensUsed += tokens
	task := &s.tasks[index]
	task.status = orchestrateStatusFromOutcome(outcome)
	task.attempts = attempts
	task.endedAt = now
	if task.startedAt.IsZero() {
		// Never dispatched: it has no duration, and pretending otherwise would
		// put time against work that did not happen.
		task.startedAt = now
	}
}

// orchestrateStatusFromOutcome maps the executor's terminal outcomes onto panel
// statuses. The string values are specialist's TaskOutcome constants; matching
// on them here rather than importing keeps the panel free of the executor.
func orchestrateStatusFromOutcome(outcome string) orchestrateTaskStatus {
	switch outcome {
	case "succeeded":
		return orchestrateDone
	case "failed":
		return orchestrateFailed
	case "cancelled":
		return orchestrateCancelled
	case "dependency_failed", "budget_exhausted":
		return orchestrateSkipped
	default:
		return orchestrateFailed
	}
}

func (s *orchestratePanelState) complete(msg planCompletedMsg, now time.Time) {
	s.status = msg.status
	// The executor's total is AUTHORITATIVE and replaces what the panel
	// accumulated: it counts every task, including any whose message the panel
	// dropped as stale.
	if msg.tokensUsed > 0 {
		s.tokensUsed = msg.tokensUsed
	}
	if msg.tokenLimit > 0 {
		s.tokenLimit = msg.tokenLimit
	}
	s.maxSpeedup = msg.maxSpeedup
	s.completedAt = now
}

// visible mirrors planPanelState.visible: nothing to show when empty, and a
// finished plan stops pinning itself after a grace period unless expanded.
//
// THIS IS THE MOUNT-POINT ANSWER. With no plan admitted the state is empty and
// this returns false, so the panel contributes nothing to the view — the
// posture-off, no-plan TUI is unchanged because the panel never renders, not
// because it renders something empty.
func (s orchestratePanelState) visible(now time.Time) bool {
	if s.isEmpty() {
		return false
	}
	if s.isComplete() && !s.expanded && !s.completedAt.IsZero() && now.Sub(s.completedAt) > completedHideAfter {
		return false
	}
	return true
}

func (s orchestratePanelState) counts() (done, failed, skipped, cancelled, running int) {
	for _, task := range s.tasks {
		switch task.status {
		case orchestrateDone:
			done++
		case orchestrateFailed:
			failed++
		case orchestrateSkipped:
			skipped++
		case orchestrateCancelled:
			cancelled++
		case orchestrateRunning:
			running++
		}
	}
	return
}

const (
	// orchestrateMaxRows bounds what the pinned panel draws. A plan may hold up
	// to defaultPlanMaxTasks tasks and the panel must never push the composer
	// off screen; beyond this the panel says how many it is not showing rather
	// than silently dropping them.
	orchestrateMaxRows = 12
	// orchestrateSummaryWidth bounds a task's one-line summary.
	orchestrateSummaryWidth = 48
	// orchestrateMinWidth is the narrowest sensible render.
	orchestrateMinWidth = 24
	// orchestrateMaxIndentDepth bounds the dependency indent.
	orchestrateMaxIndentDepth = 5
)

// orchestrateTaskLinger is how long a finished task stays on screen before it
// drops out. Long enough to see it land, short enough that the panel tracks
// what is actually happening rather than accumulating history.
//
// Matches the AGENTS sidebar's treatment of finished agents, which retires them
// the same way and for the same reason.
const orchestrateTaskLinger = 5 * time.Second

// liveTasks returns the tasks the panel should draw: everything not yet
// finished, plus anything that finished within the linger window.
//
// The header's counts still cover EVERY task — a faded task is hidden, not
// forgotten — and /plans still lists the whole plan. This is the panel choosing
// to show live work rather than accumulate a transcript.
//
// TRADE-OFF, stated because it cost something real: the dependency shape goes
// with them. A diamond stops looking like a diamond once its first task fades.
// /plans is where the shape stays readable for the whole run.
func (s orchestratePanelState) liveTasks(now time.Time) []orchestrateTask {
	live := make([]orchestrateTask, 0, len(s.tasks))
	for _, task := range s.tasks {
		if task.endedAt.IsZero() || now.Sub(task.endedAt) < orchestrateTaskLinger {
			live = append(live, task)
		}
	}
	return live
}

// renderOrchestratePanel draws the plan: one row per task, indented by
// dependency depth so the shape is legible.
func (m model) renderOrchestratePanel(width int) string {
	state := m.orchestrate
	now := m.orchestrateNow()
	if !state.visible(now) {
		return ""
	}
	if width < orchestrateMinWidth {
		width = orchestrateMinWidth
	}

	var b strings.Builder
	b.WriteString(zeroTheme.ink.Render(orchestrateHeaderLine(state, now)))

	// COLLAPSED BY DEFAULT. A plan's tasks are detail; the header is the state.
	// A six-task chain otherwise takes seven lines of the footer for the whole
	// run, pushing the conversation up for information that is one keypress
	// away. Ctrl+O expands.
	if !state.expanded {
		return b.String()
	}

	rows := state.liveTasks(now)
	// Hidden by the ROW CAP is worth saying; hidden by the linger is not. A
	// truncated list reads as a complete one, but a faded task is already
	// accounted for in the header's done count.
	capped := 0
	if len(rows) > orchestrateMaxRows {
		capped = len(rows) - orchestrateMaxRows
		rows = rows[:orchestrateMaxRows]
	}
	for _, task := range rows {
		b.WriteString("\n")
		b.WriteString(m.renderOrchestrateTaskLine(task, now, width))
	}
	if capped > 0 {
		b.WriteString("\n")
		b.WriteString(zeroTheme.faint.Render(fmt.Sprintf("  … %d more task(s) not shown", capped)))
	}
	if footer := orchestrateFooterLine(state); footer != "" {
		b.WriteString("\n")
		b.WriteString(zeroTheme.faint.Render(footer))
	}
	return b.String()
}

func orchestrateHeaderLine(state orchestratePanelState, now time.Time) string {
	done, failed, skipped, cancelled, running := state.counts()
	var b strings.Builder
	// The affordance says which way the keypress goes, so the collapsed state
	// does not look like the whole panel.
	if state.expanded {
		b.WriteString("▾ ")
	} else {
		b.WriteString("▸ ")
	}
	b.WriteString("PLAN")
	if name := strings.TrimSpace(state.name); name != "" {
		fmt.Fprintf(&b, " %s", truncateRunes(name, 24))
	}
	fmt.Fprintf(&b, "  %d/%d done", done, len(state.tasks))
	if running > 0 {
		fmt.Fprintf(&b, " · %d running", running)
	}
	if failed > 0 {
		fmt.Fprintf(&b, " · %d failed", failed)
	}
	if skipped > 0 {
		fmt.Fprintf(&b, " · %d skipped", skipped)
	}
	if cancelled > 0 {
		fmt.Fprintf(&b, " · %d cancelled", cancelled)
	}
	if !state.startedAt.IsZero() {
		fmt.Fprintf(&b, " · %s", formatElapsedSeconds(now.Sub(state.startedAt)))
	}
	if !state.expanded {
		b.WriteString("  ")
		b.WriteString("click to open · ctrl+g to expand")
	}
	return b.String()
}

func orchestrateFooterLine(state orchestratePanelState) string {
	var parts []string
	switch {
	case state.tokenLimit > 0:
		parts = append(parts, fmt.Sprintf("budget %d/%d tokens", state.tokensUsed, state.tokenLimit))
	case state.tokensUsed > 0:
		// No bound was asked for, so there is no denominator to show. The spend
		// is still reported — an unbounded plan must not also be an unmeasured
		// one.
		parts = append(parts, fmt.Sprintf("%d tokens", state.tokensUsed))
	}
	if state.isComplete() {
		parts = append(parts, "status "+state.status)
		if state.maxSpeedup > 0 {
			parts = append(parts, fmt.Sprintf("max_speedup %.2fx", state.maxSpeedup))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "  " + strings.Join(parts, " · ")
}

func (m model) renderOrchestrateTaskLine(task orchestrateTask, now time.Time, width int) string {
	// Indent by dependency depth: tasks that fan out from the same parent share
	// a rung, so a diamond reads as one node, two beside each other, one node.
	//
	// CAPPED. A strict chain adds a rung per link, so a twenty-task chain would
	// indent forty columns and run off a narrow terminal — the shape stops being
	// legible long before that. Past the cap every task sits at the same depth,
	// which is honest for a chain: they are all one-after-another anyway.
	indent := strings.Repeat("  ", minInt(task.depth, orchestrateMaxIndentDepth)+1)

	glyph := orchestrateGlyph(task.status)
	if task.status == orchestrateRunning {
		glyph = zeroTheme.accent.Render(m.spinnerGlyph())
	}

	head := fmt.Sprintf("%s%s %s", indent, glyph, truncateRunes(task.id, 24))
	meta := task.status.label()
	if elapsed := task.elapsed(now); elapsed > 0 {
		meta += " " + formatElapsedSeconds(elapsed)
	}

	// The summary is what is left after the fixed parts, and it is a SUMMARY:
	// the task's full result stays in the tool output. A display formatter on
	// the data path is how a 583-rune work product became 200 mangled runes.
	remaining := width - len([]rune(head)) - len([]rune(meta)) - 3
	line := head + " " + zeroTheme.faint.Render(meta)
	if remaining > 8 && strings.TrimSpace(task.summary) != "" {
		limit := remaining
		if limit > orchestrateSummaryWidth {
			limit = orchestrateSummaryWidth
		}
		line += " " + zeroTheme.faint.Render(truncateRunes(task.summary, limit))
	}
	return line
}

func orchestrateGlyph(status orchestrateTaskStatus) string {
	switch status {
	case orchestrateDone:
		return zeroTheme.green.Render("✓")
	case orchestrateFailed:
		return zeroTheme.red.Render("✗")
	case orchestrateSkipped, orchestrateCancelled:
		// Neutral: a skipped or stopped task is not a defect.
		return zeroTheme.faint.Render("⊘")
	default:
		return zeroTheme.faint.Render("·")
	}
}

// orchestrateNow freezes the plan clock while no run is in flight, mirroring
// planNow — a task left mid-flight when the agent yields must stop ticking up
// against a turn that is no longer running.
func (m model) orchestrateNow() time.Time {
	// The plan's own completion stops its clock, whatever the run is doing: a
	// finished plan inside a turn that continues must not keep counting.
	if !m.orchestrate.completedAt.IsZero() {
		return m.orchestrate.completedAt
	}
	// Otherwise the run ending stops it — a plan left mid-flight by an
	// interrupt would tick forever against a turn that is no longer running.
	if m.activeRunID == 0 && !m.orchestrate.frozenAt.IsZero() {
		return m.orchestrate.frozenAt
	}
	return m.now()
}

// orchestratePlansText answers /plans. It reports the plan whether or not the
// pinned panel is currently showing it — a finished plan stops pinning itself
// after a grace period, and asking for it explicitly should still work.
func (m model) orchestratePlansText() string {
	state := m.orchestrate
	if state.isEmpty() {
		return "No plan has run this session. Under the zeromaxing posture, ask for one and " +
			"the orchestrate tool will run it; tasks appear here as they go."
	}

	now := m.orchestrateNow()
	var b strings.Builder
	b.WriteString(orchestrateHeaderLine(state, now))
	for _, task := range state.tasks {
		b.WriteString("\n")
		b.WriteString(orchestratePlainTaskLine(task, now))
	}
	if footer := orchestrateFooterLine(state); footer != "" {
		b.WriteString("\n")
		b.WriteString(footer)
	}
	return b.String()
}

// orchestratePlainTaskLine is the /plans row: no spinner and no colour, since
// this is a static transcript entry rather than a live panel. Every task is
// listed — the command is where a plan larger than the panel's row budget can
// be read in full.
func orchestratePlainTaskLine(task orchestrateTask, now time.Time) string {
	indent := strings.Repeat("  ", task.depth+1)
	line := fmt.Sprintf("%s%s %s [%s]", indent, orchestratePlainGlyph(task.status), task.id, task.status.label())
	if elapsed := task.elapsed(now); elapsed > 0 {
		line += " " + formatElapsedSeconds(elapsed)
	}
	if len(task.dependsOn) > 0 {
		line += " ← " + strings.Join(task.dependsOn, ", ")
	}
	return line
}

func orchestratePlainGlyph(status orchestrateTaskStatus) string {
	switch status {
	case orchestrateDone:
		return "✓"
	case orchestrateFailed:
		return "✗"
	case orchestrateSkipped, orchestrateCancelled:
		return "⊘"
	case orchestrateRunning:
		return "▸"
	default:
		return "·"
	}
}

// orchestrateHeaderMarker identifies the panel's header line inside the footer.
// Both affordance glyphs are checked so the line is findable in either state.
const orchestrateHeaderMarker = "PLAN"

// clickedOrchestrateHeader reports whether a left-click landed on the plan
// panel's header line.
//
// Located by CONTENT rather than by a remembered row index: the footer's height
// varies with the composer, the pinned update_plan panel and the status line, so
// an index captured at render time is stale by the time a click arrives. The
// arrow glyph is what distinguishes this header from update_plan's.
func (m model) clickedOrchestrateHeader(msg tea.MouseMsg) bool {
	if m.orchestrate.isEmpty() || !m.altScreen || m.subchat.active {
		return false
	}
	width := m.chatColumnWidth()
	frame := m.scrollableTranscriptFrame(m.pinnedTitleBar(width), m.footerView(width))
	_, row, ok := frame.footerRect.local(mouseX(msg), mouseY(msg))
	if !ok || row < 0 || row >= len(frame.footerLines) {
		return false
	}
	line := ansi.Strip(frame.footerLines[row])
	return strings.HasPrefix(strings.TrimSpace(line), "▸ "+orchestrateHeaderMarker) ||
		strings.HasPrefix(strings.TrimSpace(line), "▾ "+orchestrateHeaderMarker)
}

// maxSidebarOrchestrateLines caps the sidebar's plan list. The column is 26-40
// wide and shares its height with AGENTS, FILES and ACTIVITY, so a twenty-task
// plan must not push those off — the panel and the detail view are where a
// whole plan is read.
const maxSidebarOrchestrateLines = 6

// sidebarOrchestrateLines renders the running plan for the right-hand column:
// one line per task, coloured by status, in the same shape the update_plan
// steps use so the section reads consistently whichever plan is in it.
//
// Live tasks first — the ones that have not finished — so a long plan shows
// what is happening rather than what already happened. What it drops is stated.
func (m model) sidebarOrchestrateLines(width int) []string {
	state := m.orchestrate
	if state.isEmpty() || state.sidebarCollapsed {
		return nil
	}
	room := maxInt(4, width-3)

	rows := state.liveTasks(m.orchestrateNow())
	if len(rows) == 0 {
		// Everything has finished and faded: show the tail of the plan rather
		// than an empty section.
		rows = state.tasks
		if len(rows) > maxSidebarOrchestrateLines {
			rows = rows[len(rows)-maxSidebarOrchestrateLines:]
		}
	}
	hidden := 0
	if len(rows) > maxSidebarOrchestrateLines {
		hidden = len(rows) - maxSidebarOrchestrateLines
		rows = rows[:maxSidebarOrchestrateLines]
	}

	lines := make([]string, 0, len(rows)+1)
	for _, task := range rows {
		icon, body := sidebarOrchestrateStyle(task, room)
		lines = append(lines, " "+icon+" "+body)
	}
	if hidden > 0 {
		lines = append(lines, " "+zeroTheme.faint.Render(fmt.Sprintf("  +%d more", hidden)))
	}
	return lines
}

// dependents lists the tasks that depend on the given one — the reverse of the
// declared edges, which the plan only stores forward.
func (s orchestratePanelState) dependents(taskID string) []string {
	var out []string
	for _, task := range s.tasks {
		for _, dep := range task.dependsOn {
			if dep == taskID {
				out = append(out, task.id)
				break
			}
		}
	}
	return out
}

// sidebarProgressBar draws the plan's progress across the column: done, failed
// and skipped each keep their own colour inside one bar, so a glance says both
// how far along it is AND whether it is going well.
//
// Proportional to the COLUMN, not to a fixed width — a 26-cell sidebar and a
// 40-cell one both get a bar that fills their space.
func sidebarProgressBar(state orchestratePanelState, width int) string {
	total := len(state.tasks)
	if total == 0 || width < 12 {
		return ""
	}
	done, failed, skipped, cancelled, running := state.counts()

	cells := maxInt(4, width-8)
	fill := func(count int) int {
		if count <= 0 {
			return 0
		}
		// At least one cell for anything non-zero: a failure that rounds to
		// zero cells is a failure the bar does not show.
		return maxInt(1, count*cells/total)
	}
	segments := []struct {
		count int
		style lipgloss.Style
	}{
		{done, zeroTheme.green},
		{failed, zeroTheme.red},
		{skipped + cancelled, zeroTheme.muted},
		{running, zeroTheme.accent},
	}

	var b strings.Builder
	used := 0
	for _, segment := range segments {
		n := fill(segment.count)
		if used+n > cells {
			n = cells - used
		}
		if n <= 0 {
			continue
		}
		b.WriteString(segment.style.Render(strings.Repeat("█", n)))
		used += n
	}
	if used < cells {
		b.WriteString(zeroTheme.faint.Render(strings.Repeat("░", cells-used)))
	}
	return " " + b.String() + zeroTheme.faint.Render(fmt.Sprintf(" %d/%d", done, total))
}

// sidebarOrchestrateStyle picks the glyph and text colour for one task. The
// palette matches the update_plan steps above it: green done, red failed, accent
// in flight, faint everything else — and cancelled/skipped are deliberately NOT
// red, since neither is a defect.
func sidebarOrchestrateStyle(task orchestrateTask, room int) (string, string) {
	label := task.id
	if summary := strings.TrimSpace(task.summary); summary != "" && len(label)+3 < room {
		label += " " + summary
	}
	switch task.status {
	case orchestrateDone:
		return zeroTheme.green.Render("✓"), zeroTheme.muted.Render(truncateStep(label, room))
	case orchestrateRunning:
		return zeroTheme.accent.Render("•"), zeroTheme.ink.Render(truncateStep(label, room))
	case orchestrateFailed:
		return zeroTheme.red.Render("✗"), zeroTheme.muted.Render(truncateStep(label, room))
	case orchestrateSkipped, orchestrateCancelled:
		return zeroTheme.faint.Render("⊘"), zeroTheme.faint.Render(truncateStep(label, room))
	default:
		return zeroTheme.faint.Render("○"), zeroTheme.faint.Render(truncateStep(label, room))
	}
}
