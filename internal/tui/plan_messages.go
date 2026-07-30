package tui

import (
	"fmt"
	"strings"
)

// Plan lifecycle messages, posted from the tool goroutine by
// PlanProgressBridge and consumed on the Bubble Tea event loop. Each carries
// runID so the stale-run guard can drop messages from a superseded run, exactly
// like specialistStartMsg.

// planAdmittedMsg announces a validated plan before its first task runs, so a
// plan does not read as a frozen session until the first task finishes.
//
// It carries the SHAPE, not just the count: the panel's whole reason to exist
// beyond a stream of cards is showing that a diamond is a diamond. The shape is
// known at admission — ParsePlan already proved the graph acyclic and computed
// the order — so sending it here means the panel is complete before the first
// task starts rather than assembling itself as tasks finish.
type planAdmittedMsg struct {
	runID      int
	name       string
	taskCount  int
	tasks      []planGraphTask
	tokenLimit int
	// background marks a plan that outlives the run that launched it, so the
	// stale-run guard must not drop its progress.
	background bool
}

// planGraphTask is one node of the admitted graph, in execution order.
type planGraphTask struct {
	id        string
	dependsOn []string
	phase     string
}

// planTaskStartMsg opens a card for a dispatched task. cardKey is a temporary
// id (the child session does not exist yet) reconciled on completion.
type planTaskStartMsg struct {
	runID   int
	taskID  string
	summary string
	cardKey string
	// background marks a plan that outlives the run that launched it, so the
	// stale-run guard must not drop its progress.
	background bool
}

// planTaskDoneMsg closes a task's card.
//
// dispatched distinguishes a task that ran from one that never started
// (dependency-skipped, budget-skipped, cancelled before dispatch). A task that
// never started has no card, and closing the last dispatched task's card for it
// would mark the wrong task — the specialist-card collision defect in a new
// costume.
type planTaskDoneMsg struct {
	runID      int
	taskID     string
	cardKey    string
	dispatched bool
	sessionID  string
	status     specialistStatus
	outcome    string
	reason     string
	// tokens is what the task actually spent. The card omits the segment when
	// it is zero rather than reporting a total nobody measured.
	tokens int
	// attempts is how many times the task ran — more than one when the stall
	// watchdog fired and the executor retried it. Carried so the detail can say
	// why an apparently single run took twice as long as its siblings.
	attempts int
	// background marks a plan that outlives the run that launched it, so the
	// stale-run guard must not drop its progress.
	background bool
}

// planCompletedMsg carries the plan's terminal record.
type planCompletedMsg struct {
	runID      int
	name       string
	status     string
	succeeded  int
	failed     int
	skipped    int
	cancelled  int
	tokensUsed int
	tokenLimit int
	maxSpeedup float64
	// background marks a plan that outlives the run that launched it, so the
	// stale-run guard must not drop its progress.
	background bool
}

// planNoticeLine renders the one-line plan notices shown in the transcript.
// Kept here beside the messages so the wording and the data stay together.
func planAdmittedLine(name string, taskCount int) string {
	label := "tasks"
	if taskCount == 1 {
		label = "task"
	}
	if strings.TrimSpace(name) == "" {
		return fmt.Sprintf("plan: %d %s", taskCount, label)
	}
	return fmt.Sprintf("plan %q: %d %s", name, taskCount, label)
}

func planCompletedLine(msg planCompletedMsg) string {
	var b strings.Builder
	fmt.Fprintf(&b, "plan %s: %d succeeded", msg.status, msg.succeeded)
	if msg.failed > 0 {
		fmt.Fprintf(&b, ", %d failed", msg.failed)
	}
	if msg.skipped > 0 {
		fmt.Fprintf(&b, ", %d skipped", msg.skipped)
	}
	if msg.cancelled > 0 {
		fmt.Fprintf(&b, ", %d cancelled", msg.cancelled)
	}
	if msg.tokenLimit > 0 {
		fmt.Fprintf(&b, " · %d/%d tokens", msg.tokensUsed, msg.tokenLimit)
	}
	if msg.maxSpeedup > 0 {
		fmt.Fprintf(&b, " · max_speedup %.2fx", msg.maxSpeedup)
	}
	return b.String()
}
