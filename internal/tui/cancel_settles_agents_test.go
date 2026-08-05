package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// "RUN CANCELLED." MUST NOT SIT BESIDE AGENTS THAT LOOK ALIVE. Cancelling the
// run kills the children with the run context, but the trackers were never
// told: a specialist row stayed specialistRunning — spinner on, clock ticking,
// "live" in MODELS — and an orchestrate task stayed orchestrateRunning in the
// PLAN bar, indefinitely, over processes that no longer existed. A real session
// showed exactly that: the cancellation marker in the transcript, and the
// sidebar still "working" a minute later.
func TestCancelSettlesEveryRunningAgentAndTask(t *testing.T) {
	start := time.Unix(1000, 0)
	m := sidebarTestModel()
	m.pending = true
	m.now = func() time.Time { return start }

	m.specialists.start("audit", "Audit specialist", "sess-1", start)
	m.specialists.setModel("sess-1", "grok-4.5")
	m.specialists.setCurrentTool("sess-1", "read_file", "internal/tui/model.go")
	m.specialists.start("done-before", "finished earlier", "sess-2", start)
	m.specialists.complete("sess-2", specialistCompleted, 0, "", start)

	m.orchestrate.tasks = []orchestrateTask{
		{id: "a", status: orchestrateRunning, startedAt: start},
		{id: "b", status: orchestrateDone},
	}

	m.cancelRun()

	for _, info := range m.specialists.specialists {
		if info.childSessionID == "sess-1" {
			if info.status != specialistCancelled {
				t.Fatalf("the running specialist stayed %v after cancel", info.status)
			}
			if info.currentTool != "" {
				t.Fatalf("a cancelled specialist still claims to run %q", info.currentTool)
			}
			if info.completedAt.IsZero() {
				t.Fatal("a cancelled specialist has no end time — its clock would tick forever")
			}
		}
		if info.childSessionID == "sess-2" && info.status != specialistCompleted {
			t.Fatalf("cancel rewrote a specialist that had already finished: %v", info.status)
		}
	}

	done, failed, _, cancelled, running := m.orchestrate.counts()
	if running != 0 {
		t.Fatalf("%d orchestrate task(s) still running after cancel", running)
	}
	if cancelled != 1 || done != 1 || failed != 0 {
		t.Fatalf("counts after cancel: done=%d failed=%d cancelled=%d, want 1/0/1", done, failed, cancelled)
	}

	// And the MODELS section agrees: nothing is "live" any more.
	for _, entry := range modelMixEntries(m.sidebarSpecialists(), "glm-5.2") {
		if entry.working > 0 {
			t.Fatalf("MODELS still counts %d live agent(s) on %s after cancel", entry.working, entry.model)
		}
	}
	if joined := strings.Join(m.renderContextSidebar(44, 40), "\n"); strings.Contains(ansi.Strip(joined), "live") {
		t.Fatalf("the sidebar still says live after cancel:\n%s", ansi.Strip(joined))
	}
}
