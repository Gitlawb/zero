package sessions

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// A fork made after prune's plan, by a process that has exited since, is not in
// the plan and holds nothing. It still keeps its parent: once prune holds that
// parent exclusively it looks for children the plan did not know about.
func TestPruneKeepsAParentForkedAfterThePlan(t *testing.T) {
	root := t.TempDir()
	createFinishedSession(t, root, "parent", "2026-06-01T00:00:00Z", "")

	forked := false
	prunePlannedSeam = func() {
		other := NewStore(StoreOptions{RootDir: root, Now: fixedClock("2026-09-25T00:00:00Z")})
		if _, err := other.Fork("parent", ForkInput{SessionID: "late"}); err != nil {
			t.Errorf("fork after the plan: %v", err)
			return
		}
		forked = true
		// The forking process exits, and its leases go with it.
		other.Release("late")
		other.Release("parent")
	}
	defer func() { prunePlannedSeam = nil }()

	report, err := pruneStore(root).Prune(PruneOptions{OlderThan: thirtyDays})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if !forked {
		t.Fatal("SETUP INVALID: the fork after the plan never happened")
	}
	if reason := keptReason(report, "parent"); reason != PruneKeptParent {
		t.Errorf("parent kept for %q, want %q (removed %v)", reason, PruneKeptParent, pruneIDs(report.Removed))
	}
	if lineage, err := pruneStore(root).Lineage("late"); err != nil || len(lineage) != 2 {
		t.Fatalf("the late fork's lineage is broken: %d entries, %v", len(lineage), err)
	}
}

// Prune removes the metadata first and unlinks lease.lock after it. A process
// that picked the session a moment earlier and only now takes its lease creates
// a fresh lease.lock in the directory prune is emptying, and locks it with
// nothing to contend with. Continuing the session, or creating one under it, is
// refused all the same, and prune still removes the directory.
func TestContinuingASessionPruneIsRemovingIsRefused(t *testing.T) {
	root := t.TempDir()
	createFinishedSession(t, root, "old", "2026-06-01T00:00:00Z", "")
	other := NewStore(StoreOptions{RootDir: root})

	reached := false
	pruneRemoveDirSeam = func(id string, _ bool) {
		if id != "old" {
			return
		}
		reached = true
		if _, err := os.Stat(other.metadataPath("old")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("SETUP INVALID: the metadata is still there at the seam: %v", err)
		}
		if err := other.HoldToContinue("old"); !errors.Is(err, ErrPruning) || !strings.Contains(err.Error(), "was removed") {
			t.Errorf("continue a session prune is removing: err = %v, want it refused as removed", err)
		}
		if events, err := readExecContextEvents(other, "old"); !errors.Is(err, ErrPruning) || events != nil {
			t.Errorf("exec context read of a session prune is removing: %d events, err = %v, want it refused", len(events), err)
		}
		if _, err := other.Create(CreateInput{SessionID: "child", ParentSessionID: "old"}); !errors.Is(err, ErrPruning) {
			t.Errorf("create a session under one prune is removing: err = %v, want it refused", err)
		}
	}
	defer func() { pruneRemoveDirSeam = nil }()

	report, err := pruneStore(root).Prune(PruneOptions{OlderThan: thirtyDays})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if !reached {
		t.Fatal("SETUP INVALID: prune never reached the directory removal")
	}
	if strings.Join(pruneIDs(report.Removed), ",") != "old" || len(report.Failed) != 0 {
		t.Errorf("removed %v, failed %v: the refused continuation must not keep prune from removing the directory", pruneIDs(report.Removed), pruneIDs(report.Failed))
	}
	if sessionDirExists(t, root, "old") || sessionDirExists(t, root, "child") {
		t.Errorf("left behind: old=%v child=%v", sessionDirExists(t, root, "old"), sessionDirExists(t, root, "child"))
	}

	// Once prune has finished, the session the caller picked is simply gone, and
	// continuing it is refused the same way.
	if err := other.HoldToContinue("old"); !errors.Is(err, ErrPruning) {
		t.Errorf("continue a session prune removed: err = %v, want it refused", err)
	}
	// A caller that picked nothing still reads a missing session as empty.
	events, present, err := other.ReadRehydratedEventsWithPresence("never-existed")
	if err != nil || present || len(events) != 0 {
		t.Errorf("rehydrated read of a session that never existed = %d events, present=%v, err=%v; want empty", len(events), present, err)
	}
}
