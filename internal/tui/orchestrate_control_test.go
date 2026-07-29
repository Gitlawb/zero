package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gitlawb/zero/internal/specialist"
)

// runningBridge is a bridge with a plan in flight, holding a cancel whose
// effect the test can observe.
func runningBridge(t *testing.T) (*PlanProgressBridge, context.Context) {
	t.Helper()
	bridge := NewPlanProgressBridge()
	bridge.Attach(func(tea.Msg) {}, 1, nil, "")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	bridge.PlanRunning(cancel)
	return bridge, ctx
}

// STOPPING A PLAN MUST NOT STOP THE TURN. Ctrl-C cancels the run; this cancels
// only the context the plan runs under, which is a child of it.
func TestStoppingAPlanCancelsOnlyThePlansContext(t *testing.T) {
	bridge := NewPlanProgressBridge()
	bridge.Attach(func(tea.Msg) {}, 1, nil, "")
	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	planCtx, cancelPlan := context.WithCancel(runCtx)
	defer cancelPlan()
	bridge.PlanRunning(cancelPlan)

	if !bridge.StopPlan() {
		t.Fatal("StopPlan reported no running plan")
	}
	if planCtx.Err() == nil {
		t.Fatal("the plan's context was not cancelled")
	}
	if runCtx.Err() != nil {
		t.Fatal("stopping the plan cancelled the whole run; the turn must survive")
	}
}

// A control verb with no plan running must SAY so rather than appear to work.
func TestPlanControlWithNoPlanRunningRefusesWithAReason(t *testing.T) {
	m := model{planProgress: NewPlanProgressBridge()}
	for _, verb := range []string{"stop", "pause", "resume"} {
		text := m.orchestrateControlText(verb)
		if !strings.Contains(text, "status: warning") {
			t.Errorf("/plans %s with no plan running: %q", verb, text)
		}
		if !strings.Contains(text, "No plan is running") {
			t.Errorf("/plans %s must name the reason: %q", verb, text)
		}
	}
}

// THE PAUSE ACTUALLY HOLDS THE EXECUTOR, and the release actually releases it.
// Asserting the boolean alone would pass against a flag nothing waits on.
func TestPauseHoldsTheExecutorAtATaskBoundary(t *testing.T) {
	bridge, ctx := runningBridge(t)
	if !bridge.SetPlanPaused(true) {
		t.Fatal("SetPlanPaused reported no running plan")
	}

	released := make(chan struct{})
	go func() {
		bridge.WaitWhilePaused(ctx)
		close(released)
	}()

	select {
	case <-released:
		t.Fatal("WaitWhilePaused returned while paused; nothing is holding the executor")
	case <-time.After(80 * time.Millisecond):
	}

	bridge.SetPlanPaused(false)
	select {
	case <-released:
	case <-time.After(3 * time.Second):
		t.Fatal("the executor was never released after resume")
	}
}

// STOPPING A PAUSED PLAN MUST NOT DEADLOCK — a plan cancelled on paper and
// parked forever in fact is the worst of both. Two separate things have to
// hold, and the mutation sweep showed they are not the same thing: the waiter
// has to be released (ctx does that), AND the reported pause state has to
// follow (clearing the flag does that). Asserting only the first passes against
// a bridge that goes on calling itself paused.
func TestStoppingAPausedPlanReleasesTheExecutor(t *testing.T) {
	bridge, ctx := runningBridge(t)
	bridge.SetPlanPaused(true)

	released := make(chan struct{})
	go func() {
		bridge.WaitWhilePaused(ctx)
		close(released)
	}()
	time.Sleep(30 * time.Millisecond)

	bridge.StopPlan()
	select {
	case <-released:
	case <-time.After(3 * time.Second):
		t.Fatal("a stopped plan is still parked in the pause; stop must release the waiter")
	}

	// AND THE REPORTED STATE MUST FOLLOW. The cancel alone frees the waiter —
	// WaitWhilePaused selects on ctx — so releasing it proves nothing about the
	// pause FLAG. Left set, the surface goes on offering "/plans resume" for a
	// plan that is being abandoned.
	if bridge.PlanPaused() {
		t.Fatal("a stopped plan still reports itself paused, so the surface would offer to resume it")
	}
	m := model{planProgress: bridge}
	if strings.Contains(m.planControlHint(), "resume") {
		t.Fatalf("the hint offers resume on a stopped plan: %q", m.planControlHint())
	}
}

// A waiter that arrives AFTER the resume must not block. This is why resume is
// a closed channel rather than a signal nobody is there to receive.
func TestAWaiterArrivingAfterTheResumeDoesNotBlock(t *testing.T) {
	bridge, ctx := runningBridge(t)
	bridge.SetPlanPaused(true)
	bridge.SetPlanPaused(false)

	done := make(chan struct{})
	go func() { bridge.WaitWhilePaused(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("a waiter that arrived after the resume is stuck")
	}
}

// The plan's context alone must release the waiter too: WaitWhilePaused takes
// ctx precisely so a cancellation from anywhere ends the wait.
func TestACancelledContextReleasesThePause(t *testing.T) {
	bridge := NewPlanProgressBridge()
	bridge.Attach(func(tea.Msg) {}, 1, nil, "")
	ctx, cancel := context.WithCancel(context.Background())
	bridge.PlanRunning(cancel)
	bridge.SetPlanPaused(true)

	done := make(chan struct{})
	go func() { bridge.WaitWhilePaused(ctx); close(done) }()
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("a cancelled context did not release the pause")
	}
}

// THE HANDLE IS DROPPED WHEN THE PLAN ENDS. A stale cancel would let a later
// "/plans stop" cancel a context that has since been reused — the PostureGate
// lifetime mistake in another costume.
func TestTheCancelHandleIsDroppedWhenThePlanEnds(t *testing.T) {
	bridge, _ := runningBridge(t)
	if !bridge.PlanRunningNow() {
		t.Fatal("the bridge does not consider the plan running")
	}
	bridge.PlanCompleted(samplePlan(t), specialist.PlanReport{Status: specialist.PlanCompleted, Succeeded: 1})
	if bridge.PlanRunningNow() {
		t.Fatal("the cancel handle survived the plan")
	}
	if bridge.StopPlan() {
		t.Fatal("StopPlan acted on a finished plan")
	}
}

// A NEW PLAN MUST NOT START PAUSED. The user paused the last one; carrying that
// into the next plan would suspend work nobody asked to suspend.
func TestANewPlanDoesNotInheritTheLastPlansPause(t *testing.T) {
	bridge, _ := runningBridge(t)
	bridge.SetPlanPaused(true)

	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	bridge.PlanRunning(cancel)
	if bridge.PlanPaused() {
		t.Fatal("the new plan inherited the previous plan's pause")
	}
}

// The hint is offered only while a plan is running: advertising "stop" against a
// finished plan is an offer the next line refuses.
func TestTheControlHintIsOfferedOnlyWhileAPlanRuns(t *testing.T) {
	idle := model{planProgress: NewPlanProgressBridge()}
	if strings.Contains(idle.planControlHint(), "stop") {
		t.Fatal("the hint offers stop with no plan running")
	}

	bridge, _ := runningBridge(t)
	running := model{planProgress: bridge}
	if !strings.Contains(running.planControlHint(), "/plans stop") {
		t.Fatalf("a running plan must advertise the verbs: %q", running.planControlHint())
	}
	bridge.SetPlanPaused(true)
	if !strings.Contains(running.planControlHint(), "/plans resume") {
		t.Fatalf("a paused plan must advertise resume: %q", running.planControlHint())
	}
}

// An unrecognised verb is refused by NAME, and bare /plans keeps its old
// behaviour exactly — the command that existed before this must not change.
func TestPlansKeepsItsGraphAndRefusesUnknownVerbs(t *testing.T) {
	m := model{planProgress: NewPlanProgressBridge()}
	if got := m.orchestrateControlText(""); got != m.orchestratePlansText() {
		t.Fatalf("bare /plans changed:\n%q\nvs\n%q", got, m.orchestratePlansText())
	}
	unknown := m.orchestrateControlText("halt")
	if !strings.Contains(unknown, "halt") || !strings.Contains(unknown, "status: warning") {
		t.Fatalf("an unknown verb must be refused by name: %q", unknown)
	}
}
