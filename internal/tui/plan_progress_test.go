package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gitlawb/zero/internal/specialist"
)

// THE RENDER-CACHE COLLISION, asserted with N cards where one has failed.
//
// A rowSpecialist row carries no id and no text — everything that distinguishes
// one card from another lives behind row.specialistInfo, and none of it was in
// the cache key. Two specialist cards in one run therefore hashed identically,
// so the first one rendered was reused for the second: a failed sub-agent shown
// as a successful one. One child per run made that rare; a plan produces one
// card per task, which makes it certain.
func TestSpecialistCardsInOneRunDoNotShareACacheKey(t *testing.T) {
	m := model{}
	now := time.Now()
	cards := []specialistInfo{
		{name: "a", childSessionID: "s1", status: specialistCompleted, startedAt: now, completedAt: now},
		{name: "b", childSessionID: "s2", status: specialistError, errorMsg: "boom", startedAt: now, completedAt: now},
		{name: "c", childSessionID: "s3", status: specialistCancelled, startedAt: now, completedAt: now},
		{name: "d", childSessionID: "s4", status: specialistCompleted, startedAt: now, completedAt: now},
	}

	seen := map[string]string{}
	for index := range cards {
		row := transcriptRow{kind: rowSpecialist, runID: 7, specialistInfo: &cards[index]}
		key, _ := m.renderRowCacheKey(row, 80, rowContext{}, cardRenderOptions{}, false)
		if other, clash := seen[key]; clash {
			t.Fatalf("cards %q and %q share a cache key, so one renders as the other", other, cards[index].name)
		}
		seen[key] = cards[index].name
	}
}

// Every field that changes what a card shows must be in the key. Checked field
// by field so one added to specialistInfo and forgotten here fails loudly.
func TestSpecialistCacheKeyCoversEveryVaryingField(t *testing.T) {
	m := model{}
	base := specialistInfo{
		name: "a", description: "d", childSessionID: "s1", status: specialistRunning,
		exitCode: 0, errorMsg: "", toolCount: 1, currentTool: "grep", currentDetail: "x",
		startedAt: time.Unix(1, 0), completedAt: time.Unix(2, 0),
	}
	keyOf := func(info specialistInfo) string {
		key, _ := m.renderRowCacheKey(
			transcriptRow{kind: rowSpecialist, runID: 1, specialistInfo: &info}, 80, rowContext{}, cardRenderOptions{}, false)
		return key
	}
	original := keyOf(base)

	mutations := map[string]func(*specialistInfo){
		"name":           func(i *specialistInfo) { i.name = "z" },
		"description":    func(i *specialistInfo) { i.description = "z" },
		"childSessionID": func(i *specialistInfo) { i.childSessionID = "z" },
		"status":         func(i *specialistInfo) { i.status = specialistError },
		"exitCode":       func(i *specialistInfo) { i.exitCode = 3 },
		"errorMsg":       func(i *specialistInfo) { i.errorMsg = "z" },
		"toolCount":      func(i *specialistInfo) { i.toolCount = 9 },
		"currentTool":    func(i *specialistInfo) { i.currentTool = "z" },
		"currentDetail":  func(i *specialistInfo) { i.currentDetail = "z" },
		"startedAt":      func(i *specialistInfo) { i.startedAt = time.Unix(99, 0) },
		"completedAt":    func(i *specialistInfo) { i.completedAt = time.Unix(99, 0) },
	}
	for field, mutate := range mutations {
		changed := base
		mutate(&changed)
		if keyOf(changed) == original {
			t.Errorf("changing %s does not change the cache key, so a stale render survives it", field)
		}
	}
}

// The bridge must never touch the event loop and must be inert until attached.
func TestPlanProgressBridgeIsInertUntilAttached(t *testing.T) {
	bridge := NewPlanProgressBridge()
	// No sink: every method is a silent no-op rather than a panic. Recording is
	// best-effort and must never be the thing that fails a plan.
	bridge.TaskDispatched(specialist.Task{ID: "a"})
	bridge.TaskCompleted(specialist.TaskResult{ID: "a", Outcome: specialist.TaskSucceeded})
	bridge.TaskFailed(specialist.TaskResult{ID: "b", Outcome: specialist.TaskFailed})

	var nilBridge *PlanProgressBridge
	nilBridge.TaskDispatched(specialist.Task{ID: "a"})
	nilBridge.Attach(nil, 1)
}

func TestPlanProgressBridgeEmitsACardPerTask(t *testing.T) {
	var got []tea.Msg
	bridge := NewPlanProgressBridge()
	bridge.Attach(func(msg tea.Msg) { got = append(got, msg) }, 42)

	bridge.TaskDispatched(specialist.Task{ID: "a", Prompt: "first"})
	bridge.TaskCompleted(specialist.TaskResult{ID: "a", Outcome: specialist.TaskSucceeded, SessionID: "specialist_aaa"})
	bridge.TaskDispatched(specialist.Task{ID: "b", Prompt: "second"})
	bridge.TaskFailed(specialist.TaskResult{ID: "b", Outcome: specialist.TaskFailed, Err: "boom"})

	if len(got) != 4 {
		t.Fatalf("expected one message per transition, got %d", len(got))
	}
	first, ok := got[0].(planTaskStartMsg)
	if !ok || first.taskID != "a" || first.runID != 42 {
		t.Fatalf("first message = %#v, want a start for task a on run 42", got[0])
	}
	second, ok := got[2].(planTaskStartMsg)
	if !ok || second.cardKey == first.cardKey {
		t.Fatalf("each task needs its OWN card key, got %q twice", first.cardKey)
	}
	done, ok := got[3].(planTaskDoneMsg)
	if !ok || done.status != specialistError || !done.dispatched {
		t.Fatalf("last message = %#v, want a dispatched failure", got[3])
	}
}

// A task that never started has no card, so closing the LAST dispatched task's
// card for it would mark the wrong task — the collision defect in a new shape.
func TestSkippedTaskDoesNotCloseTheDispatchedTasksCard(t *testing.T) {
	var got []tea.Msg
	bridge := NewPlanProgressBridge()
	bridge.Attach(func(msg tea.Msg) { got = append(got, msg) }, 1)

	bridge.TaskDispatched(specialist.Task{ID: "b", Prompt: "runs"})
	bridge.TaskFailed(specialist.TaskResult{ID: "b", Outcome: specialist.TaskFailed, Err: "boom"})
	bridge.TaskFailed(specialist.TaskResult{ID: "d", Outcome: specialist.TaskSkippedDependency, Err: "skipped"})

	skipped, ok := got[2].(planTaskDoneMsg)
	if !ok {
		t.Fatalf("expected a done message for the skipped task, got %#v", got[2])
	}
	if skipped.dispatched {
		t.Fatal("a dependency-skipped task never ran; marking it dispatched closes another task's card")
	}
	if skipped.status == specialistError {
		t.Fatal("a task skipped because its dependency failed is not itself an error")
	}
}

// Cancelled and skipped are not errors. A user who stopped a plan did not break
// it, and a wall of red is exactly what the outcome exists to prevent.
func TestCancelledAndSkippedRenderAsNeitherSuccessNorError(t *testing.T) {
	for _, outcome := range []specialist.TaskOutcome{
		specialist.TaskCancelled,
		specialist.TaskSkippedDependency,
		specialist.TaskSkippedBudget,
	} {
		if status := planOutcomeStatus(outcome); status != specialistCancelled {
			t.Errorf("outcome %q mapped to status %v, want the neutral cancelled status", outcome, status)
		}
	}
	if planOutcomeStatus(specialist.TaskFailed) != specialistError {
		t.Error("a real failure must still render as an error")
	}
	if planOutcomeStatus(specialist.TaskSucceeded) != specialistCompleted {
		t.Error("a success must still render as a success")
	}
}

// The card label is a SUMMARY. The full prompt stays in the tool output — a
// display formatter on the data path is how a 583-rune work product became 200
// mangled runes.
func TestPlanTaskSummaryIsShortAndCutsOnRuneBoundaries(t *testing.T) {
	multiline := specialist.Task{Prompt: "first line\nsecond line"}
	if got := planTaskSummary(multiline); got != "first line" {
		t.Fatalf("summary = %q, want only the first line", got)
	}
	long := specialist.Task{Prompt: strings.Repeat("日", 200)}
	summary := planTaskSummary(long)
	if len([]rune(summary)) > planTaskSummaryWidth {
		t.Fatalf("summary is %d runes, over the %d cap", len([]rune(summary)), planTaskSummaryWidth)
	}
	if !strings.HasSuffix(summary, "…") {
		t.Fatalf("a truncated summary must say so: %q", summary)
	}
	for _, r := range summary {
		if r == '�' {
			t.Fatalf("summary was cut mid-rune: %q", summary)
		}
	}
}
