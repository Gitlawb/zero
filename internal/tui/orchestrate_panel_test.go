package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// diamondAdmitted is the plan the whole feature is measured against: a -> (b,c)
// -> d. It has to LOOK like a diamond, not like a flat list.
func diamondAdmitted() planAdmittedMsg {
	return planAdmittedMsg{
		runID:      1,
		name:       "diamond",
		taskCount:  4,
		tokenLimit: 100000,
		tasks: []planGraphTask{
			{id: "a"},
			{id: "b", dependsOn: []string{"a"}},
			{id: "c", dependsOn: []string{"a"}},
			{id: "d", dependsOn: []string{"b", "c"}},
		},
	}
}

// admittedModel returns a model with the plan admitted AND EXPANDED. The panel
// collapses by default (the header is the state; the tasks are detail), so a
// test about task rows has to open it — the collapsed default has its own test.
func admittedModel(t *testing.T, msg planAdmittedMsg) model {
	t.Helper()
	m := model{now: func() time.Time { return time.Unix(1000, 0) }}
	m.orchestrate.admit(msg, m.now())
	m.orchestrate.expanded = true
	return m
}

// THE POINT OF THE PANEL. A dependency graph rendered as a flat list is just
// the cards again; the shape is the thing cards cannot show.
func TestDiamondRendersAsADiamond(t *testing.T) {
	m := admittedModel(t, diamondAdmitted())

	depth := map[string]int{}
	for _, task := range m.orchestrate.tasks {
		depth[task.id] = task.depth
	}
	if depth["a"] != 0 {
		t.Fatalf("the root must sit at depth 0, got %d", depth["a"])
	}
	if depth["b"] != 1 || depth["c"] != 1 {
		t.Fatalf("b and c fan out from the same parent and must share a rung: b=%d c=%d", depth["b"], depth["c"])
	}
	if depth["d"] != 2 {
		t.Fatalf("the join must sit below both branches, got %d", depth["d"])
	}

	rendered := m.renderOrchestratePanel(100)
	indent := func(id string) int {
		for _, line := range strings.Split(rendered, "\n") {
			if strings.Contains(line, " "+id+" ") {
				return len(line) - len(strings.TrimLeft(line, " "))
			}
		}
		t.Fatalf("task %q missing from the panel:\n%s", id, rendered)
		return -1
	}
	if indent("b") != indent("c") {
		t.Fatalf("b and c must be drawn on the same rung:\n%s", rendered)
	}
	if indent("a") >= indent("b") || indent("d") <= indent("a") {
		t.Fatalf("the diamond is drawn flat:\n%s", rendered)
	}
}

// A chain is not a diamond: each task must step in one further, or the panel
// draws every shape the same way.
func TestAChainStepsInOncePerLink(t *testing.T) {
	m := admittedModel(t, planAdmittedMsg{
		runID: 1, name: "chain", taskCount: 3,
		tasks: []planGraphTask{
			{id: "a"},
			{id: "b", dependsOn: []string{"a"}},
			{id: "c", dependsOn: []string{"b"}},
		},
	})
	got := []int{}
	for _, task := range m.orchestrate.tasks {
		got = append(got, task.depth)
	}
	if len(got) != 3 || got[0] != 0 || got[1] != 1 || got[2] != 2 {
		t.Fatalf("chain depths = %v, want 0,1,2", got)
	}
}

// Independent tasks all sit on the same rung — a fan-out reads as a fan-out.
func TestIndependentTasksShareOneRung(t *testing.T) {
	m := admittedModel(t, planAdmittedMsg{
		runID: 1, name: "fan", taskCount: 3,
		tasks: []planGraphTask{{id: "a"}, {id: "b"}, {id: "c"}},
	})
	for _, task := range m.orchestrate.tasks {
		if task.depth != 0 {
			t.Fatalf("task %q has no dependencies but sits at depth %d", task.id, task.depth)
		}
	}
}

// THE MOUNT-POINT INVARIANT. With no plan the panel contributes nothing —
// not an empty box, not a blank line. That is what keeps a posture-off session
// unchanged.
func TestPanelRendersNothingWithoutAPlan(t *testing.T) {
	m := model{now: func() time.Time { return time.Unix(1000, 0) }}
	if got := m.renderOrchestratePanel(100); got != "" {
		t.Fatalf("with no plan the panel must render nothing, got %q", got)
	}
	if m.orchestrate.visible(m.now()) {
		t.Fatal("an empty plan must not be visible")
	}
}

// A finished plan stops pinning itself, matching the update_plan panel, so a
// completed plan does not occupy the footer forever.
func TestCompletedPlanStopsPinningItself(t *testing.T) {
	m := admittedModel(t, diamondAdmitted())
	m.orchestrate.complete(planCompletedMsg{status: "completed", maxSpeedup: 1.33}, m.now())

	if !m.orchestrate.visible(m.now()) {
		t.Fatal("a just-completed plan should still be visible")
	}
	later := m.now().Add(completedHideAfter + time.Second)
	m.orchestrate.expanded = false
	if m.orchestrate.visible(later) {
		t.Fatal("a long-finished plan must stop pinning itself")
	}
	// An expanded plan is one the user deliberately opened, so it stays.
	m.orchestrate.expanded = true
	if !m.orchestrate.visible(later) {
		t.Fatal("an expanded plan stays visible however long ago it finished")
	}
}

// Status transitions must survive to the panel, and a skipped or cancelled task
// must not be shown as a failure.
func TestPanelTracksEveryOutcome(t *testing.T) {
	m := admittedModel(t, diamondAdmitted())
	now := m.now()

	m.orchestrate.markStarted("a", "root", now)
	m.orchestrate.markDone("a", "succeeded", 0, now.Add(time.Second))
	m.orchestrate.markStarted("b", "left", now.Add(time.Second))
	m.orchestrate.markDone("b", "failed", 0, now.Add(2*time.Second))
	m.orchestrate.markDone("c", "cancelled", 0, now.Add(2*time.Second))
	m.orchestrate.markDone("d", "dependency_failed", 0, now.Add(2*time.Second))

	want := map[string]orchestrateTaskStatus{
		"a": orchestrateDone, "b": orchestrateFailed,
		"c": orchestrateCancelled, "d": orchestrateSkipped,
	}
	for _, task := range m.orchestrate.tasks {
		if task.status != want[task.id] {
			t.Errorf("task %q status = %v, want %v", task.id, task.status, want[task.id])
		}
	}
	done, failed, skipped, cancelled, running := m.orchestrate.counts()
	if done != 1 || failed != 1 || skipped != 1 || cancelled != 1 || running != 0 {
		t.Fatalf("counts = done %d failed %d skipped %d cancelled %d running %d", done, failed, skipped, cancelled, running)
	}
}

// A cancelled plan must not read as a wall of failures — the whole reason
// TaskCancelled exists.
func TestCancelledPlanIsNotShownAsFailures(t *testing.T) {
	m := admittedModel(t, diamondAdmitted())
	now := m.now()
	m.orchestrate.markStarted("a", "root", now)
	m.orchestrate.markDone("a", "succeeded", 0, now)
	for _, id := range []string{"b", "c", "d"} {
		m.orchestrate.markDone(id, "cancelled", 0, now)
	}
	m.orchestrate.complete(planCompletedMsg{status: "partial", succeeded: 1, cancelled: 3}, now)

	_, failed, _, cancelled, _ := m.orchestrate.counts()
	if failed != 0 {
		t.Fatalf("a cancelled plan reported %d failures", failed)
	}
	if cancelled != 3 {
		t.Fatalf("cancelled = %d, want 3", cancelled)
	}
	rendered := m.renderOrchestratePanel(100)
	if strings.Contains(rendered, "failed") {
		t.Fatalf("the panel calls a cancelled plan failed:\n%s", rendered)
	}
	if !strings.Contains(rendered, "cancelled") {
		t.Fatalf("the panel must say what actually happened:\n%s", rendered)
	}
}

// A plan at the task cap must not push the composer off screen, and what it
// leaves out has to be stated — a silently truncated list reads as a complete
// one.
func TestALargePlanIsBoundedAndSaysWhatItHid(t *testing.T) {
	msg := planAdmittedMsg{runID: 1, name: "big", taskCount: 20}
	for index := 0; index < 20; index++ {
		msg.tasks = append(msg.tasks, planGraphTask{id: string(rune('a' + index))})
	}
	msg.taskCount = len(msg.tasks)
	m := admittedModel(t, msg)

	rendered := m.renderOrchestratePanel(100)
	lines := strings.Count(rendered, "\n") + 1
	if lines > orchestrateMaxRows+3 {
		t.Fatalf("the panel drew %d lines for a 20-task plan; it must stay bounded", lines)
	}
	if !strings.Contains(rendered, "more task(s) not shown") {
		t.Fatalf("a truncated panel must say so:\n%s", rendered)
	}
	// /plans is where the whole plan can still be read.
	full := m.orchestratePlansText()
	for _, task := range msg.tasks {
		if !strings.Contains(full, task.id) {
			t.Fatalf("/plans omitted task %q; it is the surface that shows everything", task.id)
		}
	}
}

// Narrow terminals must not panic or produce garbage.
func TestPanelSurvivesNarrowWidths(t *testing.T) {
	m := admittedModel(t, diamondAdmitted())
	m.orchestrate.markStarted("a", strings.Repeat("verbose ", 40), m.now())
	for _, width := range []int{0, 1, 10, 24, 40, 200} {
		rendered := m.renderOrchestratePanel(width)
		if rendered == "" {
			t.Fatalf("width %d rendered nothing for a live plan", width)
		}
		for _, r := range rendered {
			if r == '�' {
				t.Fatalf("width %d cut mid-rune:\n%s", width, rendered)
			}
		}
	}
}

// A task with no output must still render — the panel shows status, not results.
func TestATaskWithNoSummaryStillRenders(t *testing.T) {
	m := admittedModel(t, diamondAdmitted())
	m.orchestrate.markStarted("a", "", m.now())
	rendered := m.renderOrchestratePanel(100)
	if !strings.Contains(rendered, " a ") {
		t.Fatalf("a task with no summary vanished from the panel:\n%s", rendered)
	}
}

// The panel SUMMARISES. The full result stays in the tool output; a display
// formatter on the data path is how a 583-rune work product became 200 mangled
// runes.
func TestPanelTruncatesSummariesOnRuneBoundaries(t *testing.T) {
	m := admittedModel(t, diamondAdmitted())
	m.orchestrate.markStarted("a", strings.Repeat("日", 300), m.now())
	rendered := m.renderOrchestratePanel(100)
	for _, line := range strings.Split(rendered, "\n") {
		if len([]rune(line)) > 140 {
			t.Fatalf("a panel line ran to %d runes:\n%s", len([]rune(line)), line)
		}
	}
	if strings.Contains(rendered, "�") {
		t.Fatalf("summary was cut mid-rune:\n%s", rendered)
	}
}

// /plans answers even with no plan, and never claims one ran.
func TestPlansCommandWithNoPlan(t *testing.T) {
	m := model{now: func() time.Time { return time.Unix(1000, 0) }}
	text := m.orchestratePlansText()
	if !strings.Contains(text, "No plan has run") {
		t.Fatalf("/plans must say plainly that nothing ran: %q", text)
	}
}

// /plans shows the dependency edges explicitly, so the shape survives even
// where indentation does not (copied text, narrow terminals, screen readers).
func TestPlansCommandNamesTheEdges(t *testing.T) {
	m := admittedModel(t, diamondAdmitted())
	text := m.orchestratePlansText()
	if !strings.Contains(text, "d [pending] ← b, c") {
		t.Fatalf("/plans must name each task's dependencies:\n%s", text)
	}
}

// A finished plan's clock stops. It used to keep counting while the turn that
// produced it carried on, so a plan that took 20 seconds read as minutes.
func TestTheHeaderClockStopsWhenThePlanEnds(t *testing.T) {
	m := admittedModel(t, diamondAdmitted())
	start := m.now()
	m.activeRunID = 7 // the run continues after the plan finishes
	m.orchestrate.complete(planCompletedMsg{status: "completed"}, start.Add(20*time.Second))

	m.now = func() time.Time { return start.Add(10 * time.Minute) }
	if got := m.orchestrateNow(); !got.Equal(start.Add(20 * time.Second)) {
		t.Fatalf("the clock kept running after the plan ended: %v", got.Sub(start))
	}
}

// A plan left mid-flight by an interrupt stops counting when the run ends,
// rather than ticking against a turn that is gone.
func TestAnInterruptedPlansClockFreezesWithTheRun(t *testing.T) {
	m := admittedModel(t, diamondAdmitted())
	start := m.now()
	m.orchestrate.markStarted("a", "root", start)
	m.orchestrate.frozenAt = start.Add(5 * time.Second)
	m.activeRunID = 0

	m.now = func() time.Time { return start.Add(10 * time.Minute) }
	if got := m.orchestrateNow(); !got.Equal(start.Add(5 * time.Second)) {
		t.Fatalf("an interrupted plan kept counting: %v", got.Sub(start))
	}
}

// THE WIRING, not the state machine. Every test above drives
// orchestratePanelState directly; this one pushes the four plan messages
// through model.Update, which is the only path a real run takes. A panel whose
// state machine is perfect and whose handlers never call it is exactly the
// defect class this feature keeps producing.
func TestPlanMessagesDriveThePanel(t *testing.T) {
	m := model{now: func() time.Time { return time.Unix(1000, 0) }}
	m.activeRunID = 1

	step := func(msg tea.Msg) {
		t.Helper()
		updated, _ := m.Update(msg)
		next, ok := updated.(model)
		if !ok {
			t.Fatalf("Update returned %T", updated)
		}
		m = next
	}

	step(diamondAdmitted())
	if len(m.orchestrate.tasks) != 4 {
		t.Fatalf("planAdmittedMsg did not reach the panel: %d tasks", len(m.orchestrate.tasks))
	}
	if m.orchestrate.tokenLimit != 100000 {
		t.Fatalf("the budget limit did not reach the panel: %d", m.orchestrate.tokenLimit)
	}

	step(planTaskStartMsg{runID: 1, taskID: "a", summary: "root", cardKey: "plantask_1"})
	if m.orchestrate.tasks[0].status != orchestrateRunning {
		t.Fatal("planTaskStartMsg did not mark the task running in the panel")
	}

	step(planTaskDoneMsg{runID: 1, taskID: "a", cardKey: "plantask_1", dispatched: true,
		status: specialistCompleted, outcome: "succeeded"})
	if m.orchestrate.tasks[0].status != orchestrateDone {
		t.Fatal("planTaskDoneMsg did not mark the task done in the panel")
	}

	step(planCompletedMsg{runID: 1, name: "diamond", status: "partial",
		succeeded: 1, tokensUsed: 150, tokenLimit: 100000, maxSpeedup: 1.33})
	if m.orchestrate.status != "partial" || m.orchestrate.tokensUsed != 150 {
		t.Fatalf("planCompletedMsg did not reach the panel: %+v", m.orchestrate.status)
	}
	if m.orchestrate.maxSpeedup != 1.33 {
		t.Fatalf("max_speedup did not reach the panel: %v", m.orchestrate.maxSpeedup)
	}
}

// A message from a superseded run must not touch the panel, or a cancelled
// turn's plan overwrites the current one.
func TestStalePlanMessagesAreDropped(t *testing.T) {
	m := model{now: func() time.Time { return time.Unix(1000, 0) }}
	m.activeRunID = 2

	stale := diamondAdmitted() // runID 1
	updated, _ := m.Update(stale)
	next := updated.(model)
	if !next.orchestrate.isEmpty() {
		t.Fatal("a plan from a superseded run reached the panel")
	}
}

// THE MOUNT. The panel has to be reachable from the real View, not just
// renderable in isolation — a renderer nothing calls is the same defect as a
// state machine nothing feeds.
func TestOrchestratePanelMountsInTheFooter(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.width, m.height = 96, 30
	m.activeRunID = 1

	before := plainRender(t, m.View())
	if strings.Contains(before, "PLAN diamond") {
		t.Fatal("the panel must not appear before a plan is admitted")
	}

	updated, _ := m.Update(diamondAdmitted())
	m = updated.(model)
	m.width, m.height = 96, 30

	// Collapsed by default: the header is there, the tasks are not.
	collapsed := plainRender(t, m.View())
	if !strings.Contains(collapsed, "PLAN diamond") {
		t.Fatalf("the collapsed panel must still show the plan header:\n%s", collapsed)
	}
	if strings.Contains(collapsed, "ctrl+g") == false {
		t.Fatalf("the collapsed panel must say how to open it:\n%s", collapsed)
	}
	m.orchestrate.expanded = true
	after := plainRender(t, m.View())

	if !strings.Contains(after, "PLAN diamond") {
		t.Fatalf("the panel is not mounted in the view:\n%s", after)
	}
	for _, id := range []string{"a", "b", "c", "d"} {
		if !strings.Contains(after, " "+id+" ") {
			t.Fatalf("task %q missing from the rendered view:\n%s", id, after)
		}
	}
}

// /plans reaches orchestratePlansText through the real command dispatch.
func TestPlansCommandIsWired(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.width, m.height = 96, 30
	m.activeRunID = 1 // set BEFORE the message, or the stale-run guard drops it
	updated, _ := m.Update(diamondAdmitted())
	m = updated.(model)

	result, _ := m.executeSlash("/plans")
	m = result.(model)
	if !transcriptContains(m.transcript, "PLAN diamond") {
		t.Fatal("/plans did not report the admitted plan")
	}
	if !transcriptContains(m.transcript, "← b, c") {
		t.Fatal("/plans did not report the dependency shape")
	}
}

// COLLAPSED BY DEFAULT, and the header says how to open it. A six-task chain
// otherwise takes seven footer lines for the whole run, pushing the
// conversation up for detail that is one keypress away.
func TestThePanelIsCollapsedUntilAsked(t *testing.T) {
	m := model{now: func() time.Time { return time.Unix(1000, 0) }}
	m.orchestrate.admit(diamondAdmitted(), m.now())

	collapsed := m.renderOrchestratePanel(100)
	if strings.Count(collapsed, "\n") != 0 {
		t.Fatalf("the collapsed panel must be exactly one line:\n%s", collapsed)
	}
	if !strings.Contains(collapsed, "PLAN diamond") {
		t.Fatalf("the collapsed panel must still name the plan:\n%s", collapsed)
	}
	if !strings.Contains(collapsed, "ctrl+g") {
		t.Fatalf("a collapsed panel that does not say how to open it reads as the whole panel:\n%s", collapsed)
	}
	for _, id := range []string{" b ", " c ", " d "} {
		if strings.Contains(collapsed, id) {
			t.Fatalf("task%q leaked into the collapsed panel:\n%s", id, collapsed)
		}
	}

	m.orchestrate.expanded = true
	expanded := m.renderOrchestratePanel(100)
	if strings.Count(expanded, "\n") < 4 {
		t.Fatalf("the expanded panel must show every task:\n%s", expanded)
	}
	if strings.Contains(expanded, "to expand") {
		t.Fatal("an already-open panel must not still offer to open")
	}
}

// Ctrl+O toggles it through the real key handler, and does nothing when there
// is no plan — a keypress that silently mutates hidden state is worse than one
// that does nothing.
func TestCtrlGTogglesTheOrchestratePanel(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.width, m.height = 96, 30

	press := func(m model) model {
		t.Helper()
		updated, _ := m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
		return updated.(model)
	}

	m = press(m)
	if m.orchestrate.expanded {
		t.Fatal("with no plan admitted the toggle must do nothing")
	}

	m.activeRunID = 1
	updated, _ := m.Update(diamondAdmitted())
	m = updated.(model)
	if m.orchestrate.expanded {
		t.Fatal("a freshly admitted plan starts collapsed")
	}
	m = press(m)
	if !m.orchestrate.expanded {
		t.Fatal("ctrl+g did not expand the panel")
	}
	m = press(m)
	if m.orchestrate.expanded {
		t.Fatal("ctrl+g did not collapse the panel again")
	}
}

// An unbounded plan still reports what it spent. Removing the cap must not also
// remove the measurement.
func TestAnUnboundedPlanStillReportsItsSpend(t *testing.T) {
	m := admittedModel(t, planAdmittedMsg{runID: 1, name: "p", taskCount: 1,
		tasks: []planGraphTask{{id: "a"}}})
	m.orchestrate.complete(planCompletedMsg{status: "completed", tokensUsed: 469555}, m.now())

	rendered := m.renderOrchestratePanel(100)
	if !strings.Contains(rendered, "469555 tokens") {
		t.Fatalf("an unbounded plan must still report its spend:\n%s", rendered)
	}
	if strings.Contains(rendered, "/0 tokens") {
		t.Fatalf("no bound was asked for, so there is no denominator to show:\n%s", rendered)
	}
}

// A long chain must not indent itself off the screen. A twenty-task chain adds
// a rung per link, which would be forty columns of indent — the shape stops
// being legible long before that.
func TestADeepChainStopsIndenting(t *testing.T) {
	msg := planAdmittedMsg{runID: 1, name: "chain"}
	for index := 0; index < 20; index++ {
		task := planGraphTask{id: string(rune('a' + index))}
		if index > 0 {
			task.dependsOn = []string{string(rune('a' + index - 1))}
		}
		msg.tasks = append(msg.tasks, task)
	}
	msg.taskCount = len(msg.tasks)
	m := admittedModel(t, msg)

	// The DEPTHS still reflect the real graph — only the drawing is capped.
	last := m.orchestrate.tasks[len(m.orchestrate.tasks)-1]
	if last.depth != 19 {
		t.Fatalf("the last link of a 20-task chain is at depth %d, want 19", last.depth)
	}

	rendered := m.renderOrchestratePanel(60)
	for _, styled := range strings.Split(rendered, "\n") {
		// VISIBLE width: the rendered line carries colour escapes, and counting
		// those would measure the wrong thing entirely.
		line := ansi.Strip(styled)
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent > 2*(orchestrateMaxIndentDepth+1) {
			t.Fatalf("indent ran to %d columns:\n%s", indent, rendered)
		}
		if len([]rune(line)) > 60 {
			t.Fatalf("a line ran to %d visible columns past a 60-wide panel:\n%s", len([]rune(line)), line)
		}
	}
}

// FINISHED TASKS FADE OUT. The panel tracks live work rather than accumulating
// a transcript of it.
func TestFinishedTasksFadeOutOfThePanel(t *testing.T) {
	m := admittedModel(t, diamondAdmitted())
	start := m.now()

	m.orchestrate.markStarted("a", "root", start)
	m.orchestrate.markDone("a", "succeeded", 0, start)
	m.orchestrate.markStarted("b", "left", start)

	// Immediately after finishing, a is still there — you have to be able to
	// SEE it land.
	if !strings.Contains(m.renderOrchestratePanel(90), " a ") {
		t.Fatal("a task that just finished must linger long enough to be seen")
	}

	m.now = func() time.Time { return start.Add(orchestrateTaskLinger + time.Second) }
	faded := m.renderOrchestratePanel(90)
	if strings.Contains(faded, " a ") {
		t.Fatalf("a long-finished task must drop out of the panel:\n%s", faded)
	}
	for _, id := range []string{" b ", " c ", " d "} {
		if !strings.Contains(faded, id) {
			t.Fatalf("task%q is not finished and must stay:\n%s", id, faded)
		}
	}
}

// A faded task is hidden, not forgotten: the header still counts it, and
// /plans still lists the whole plan with its shape intact.
func TestAFadedTaskIsStillCountedAndStillInPlans(t *testing.T) {
	m := admittedModel(t, diamondAdmitted())
	start := m.now()
	m.orchestrate.markStarted("a", "root", start)
	m.orchestrate.markDone("a", "succeeded", 0, start)
	m.now = func() time.Time { return start.Add(orchestrateTaskLinger + time.Second) }

	if done, _, _, _, _ := m.orchestrate.counts(); done != 1 {
		t.Fatalf("done count = %d, want 1: a faded task is hidden, not forgotten", done)
	}
	if !strings.Contains(m.renderOrchestratePanel(90), "1/4 done") {
		t.Fatal("the header must still report the faded task as done")
	}
	full := m.orchestratePlansText()
	if !strings.Contains(full, "a [done]") {
		t.Fatalf("/plans must still list every task, faded or not:\n%s", full)
	}
	if !strings.Contains(full, "← b, c") {
		t.Fatalf("/plans is where the dependency shape survives the fade:\n%s", full)
	}
}

// Pending tasks never fade — they have not finished, so there is nothing to
// retire.
func TestPendingTasksNeverFade(t *testing.T) {
	m := admittedModel(t, diamondAdmitted())
	m.now = func() time.Time { return time.Unix(1000, 0).Add(time.Hour) }
	live := m.orchestrate.liveTasks(m.now())
	if len(live) != 4 {
		t.Fatalf("no task has finished, so all four must still show, got %d", len(live))
	}
}

// THE BUDGET LINE MUST COUNT WHILE THE PLAN RUNS.
//
// tokensUsed was assigned only from plan_completed, which arrives when the
// whole plan ends — so the footer read "budget 0/200000" for the entire run
// while the cards above it showed tens of thousands of tokens spent. Live spend
// is the one number a user watches that line for.
func TestTheBudgetLineCountsDuringTheRun(t *testing.T) {
	m := admittedModel(t, diamondAdmitted())
	now := m.now()

	m.orchestrate.markStarted("a", "root", now)
	m.orchestrate.markDone("a", "succeeded", 16470, now)
	if m.orchestrate.tokensUsed != 16470 {
		t.Fatalf("tokensUsed = %d after one task, want it counted as it finishes", m.orchestrate.tokensUsed)
	}
	m.orchestrate.markStarted("b", "left", now)
	m.orchestrate.markDone("b", "succeeded", 68483, now)

	rendered := m.renderOrchestratePanel(100)
	if !strings.Contains(rendered, "budget 84953/100000 tokens") {
		t.Fatalf("the footer must report live spend mid-run:\n%s", rendered)
	}
}

// The executor's total is authoritative and replaces what the panel
// accumulated: it counts every task, including any whose message the panel
// dropped as stale.
func TestThePlansOwnTotalWinsAtTheEnd(t *testing.T) {
	m := admittedModel(t, diamondAdmitted())
	now := m.now()
	m.orchestrate.markStarted("a", "root", now)
	m.orchestrate.markDone("a", "succeeded", 100, now)

	m.orchestrate.complete(planCompletedMsg{status: "partial", tokensUsed: 379773}, now)
	if m.orchestrate.tokensUsed != 379773 {
		t.Fatalf("tokensUsed = %d, want the executor's authoritative total", m.orchestrate.tokensUsed)
	}
}

// ...but a plan that reports no total does not erase what was counted.
func TestAMissingFinalTotalDoesNotZeroTheCount(t *testing.T) {
	m := admittedModel(t, diamondAdmitted())
	now := m.now()
	m.orchestrate.markStarted("a", "root", now)
	m.orchestrate.markDone("a", "succeeded", 4242, now)

	m.orchestrate.complete(planCompletedMsg{status: "completed"}, now)
	if m.orchestrate.tokensUsed != 4242 {
		t.Fatalf("tokensUsed = %d, want the accumulated count kept when no total arrives", m.orchestrate.tokensUsed)
	}
}
