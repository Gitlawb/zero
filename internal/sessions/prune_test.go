package sessions

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// pruneNow is "now" for every prune in these tests.
const pruneNow = "2026-09-26T00:00:00Z"

const thirtyDays = 30 * 24 * time.Hour

func pruneStore(root string) *Store {
	return NewStore(StoreOptions{RootDir: root, Now: fixedClock(pruneNow)})
}

// createFinishedSession leaves a session the way a run that has ended leaves
// it: created and written at `at` by a Store that no longer holds it open.
func createFinishedSession(t *testing.T, root, id, at, parent string) {
	t.Helper()
	store := NewStore(StoreOptions{RootDir: root, Now: fixedClock(at)})
	var err error
	if parent == "" {
		_, err = store.Create(CreateInput{SessionID: id, Title: "title " + id})
	} else {
		_, err = store.Fork(parent, ForkInput{SessionID: id, Title: "title " + id})
	}
	if err != nil {
		t.Fatalf("create %s: %v", id, err)
	}
	if _, err := store.AppendEvent(id, AppendEventInput{Type: EventMessage, Payload: map[string]string{"content": "hello from " + id}}); err != nil {
		t.Fatalf("append to %s: %v", id, err)
	}
	store.Release(id)
	if parent != "" {
		store.Release(parent)
	}
}

func sessionDirExists(t *testing.T, root, id string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(root, id))
	if err == nil {
		return true
	}
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	t.Fatalf("stat %s: %v", id, err)
	return false
}

func pruneIDs(entries []PruneEntry) []string {
	ids := []string{}
	for _, entry := range entries {
		ids = append(ids, entry.SessionID)
	}
	return ids
}

func keptReason(report PruneReport, id string) string {
	for _, entry := range report.Kept {
		if entry.SessionID == id {
			return entry.Reason
		}
	}
	return ""
}

// rewriteUpdatedAt changes a session's recorded last update behind the store's
// back, the way a writer that takes no lease would.
func rewriteUpdatedAt(t *testing.T, root, id, updatedAt string) {
	t.Helper()
	path := filepath.Join(root, id, "metadata.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s metadata: %v", id, err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("decode %s metadata: %v", id, err)
	}
	fields["updatedAt"] = updatedAt
	data, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s metadata: %v", id, err)
	}
}

func TestPruneRemovesOnlySessionsOlderThanTheCutoff(t *testing.T) {
	root := t.TempDir()
	createFinishedSession(t, root, "old-a", "2026-07-01T00:00:00Z", "")
	createFinishedSession(t, root, "old-b", "2026-07-15T00:00:00Z", "")
	createFinishedSession(t, root, "recent", "2026-09-25T12:00:00Z", "")

	// A checkpoint blob, which has to go with its session.
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "a.txt"), []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := NewStore(StoreOptions{RootDir: root, Now: fixedClock("2026-07-01T00:00:00Z")})
	if _, err := old.CaptureToolCheckpoint("old-a", workspace, "edit_file", []string{"a.txt"}); err != nil {
		t.Fatalf("capture a checkpoint: %v", err)
	}
	old.Release("old-a")
	if blobs, err := os.ReadDir(filepath.Join(root, "old-a", CheckpointsDir, "blobs")); err != nil || len(blobs) == 0 {
		t.Fatalf("SETUP INVALID: no checkpoint blob to remove (%v)", err)
	}

	report, err := pruneStore(root).Prune(PruneOptions{OlderThan: thirtyDays})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if got := strings.Join(pruneIDs(report.Removed), ","); got != "old-a,old-b" {
		t.Fatalf("removed %q, want old-a,old-b", got)
	}
	if len(report.Kept) != 0 || len(report.Failed) != 0 {
		t.Fatalf("kept %v and failed %v, want neither", report.Kept, report.Failed)
	}
	for _, entry := range report.Removed {
		if entry.Bytes <= 0 {
			t.Errorf("%s was reported at %d bytes", entry.SessionID, entry.Bytes)
		}
		if sessionDirExists(t, root, entry.SessionID) {
			t.Errorf("%s was reported removed and its directory is still there", entry.SessionID)
		}
	}
	if got, err := pruneStore(root).Get("recent"); err != nil || got == nil {
		t.Fatalf("the recent session is gone or unreadable: %v %v", got, err)
	}
}

func TestPruneRefusesACutoffShorterThanADay(t *testing.T) {
	root := t.TempDir()
	createFinishedSession(t, root, "old", "2026-07-01T00:00:00Z", "")
	_, err := pruneStore(root).Prune(PruneOptions{OlderThan: 23 * time.Hour})
	if err == nil || !strings.Contains(err.Error(), "minimum") {
		t.Fatalf("a 23h cutoff was not refused for being under the minimum: %v", err)
	}
	if !sessionDirExists(t, root, "old") {
		t.Fatal("a refused prune removed a session")
	}
}

func TestPruneDryRunDecidesTheSameAndRemovesNothing(t *testing.T) {
	root := t.TempDir()
	createFinishedSession(t, root, "old-a", "2026-07-01T00:00:00Z", "")
	createFinishedSession(t, root, "old-b", "2026-07-15T00:00:00Z", "")

	report, err := pruneStore(root).Prune(PruneOptions{OlderThan: thirtyDays, DryRun: true})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if !report.DryRun || strings.Join(pruneIDs(report.Removed), ",") != "old-a,old-b" {
		t.Fatalf("dry run report = %+v, want both sessions listed as would-be removals", report)
	}
	for _, id := range []string{"old-a", "old-b"} {
		if !sessionDirExists(t, root, id) {
			t.Errorf("a dry run removed %s", id)
		}
	}
}

// A SESSION ANOTHER PROCESS HAS OPEN STAYS, HOWEVER OLD. The other Store stands
// in for the other process: lease locks conflict between handles, not only
// between processes. Each way of having a session open is covered: holding it
// outright, writing to it, and loading it to resume it.
func TestPruneLeavesASessionAnotherProcessHasOpen(t *testing.T) {
	for _, open := range []struct {
		name string
		do   func(t *testing.T, other *Store, id string)
	}{
		{"held", func(t *testing.T, other *Store, id string) { other.Hold(id) }},
		{"written", func(t *testing.T, other *Store, id string) {
			if _, err := other.AppendEvent(id, AppendEventInput{Type: EventMessage, Payload: map[string]string{"content": "still here"}}); err != nil {
				t.Fatalf("append: %v", err)
			}
		}},
		{"resumed", func(t *testing.T, other *Store, id string) {
			if _, err := other.ReadRehydratedEvents(id); err != nil {
				t.Fatalf("resume: %v", err)
			}
		}},
	} {
		t.Run(open.name, func(t *testing.T) {
			root := t.TempDir()
			createFinishedSession(t, root, "old", "2026-07-01T00:00:00Z", "")
			// The other process's clock is as old as the session, so a write
			// leaves it just as old and only the lease can keep it.
			other := NewStore(StoreOptions{RootDir: root, Now: fixedClock("2026-07-01T00:00:00Z")})
			open.do(t, other, "old")

			// A dry run has to say so too, not list it as removable.
			preview, err := pruneStore(root).Prune(PruneOptions{OlderThan: thirtyDays, DryRun: true})
			if err != nil {
				t.Fatalf("dry run: %v", err)
			}
			if reason := keptReason(preview, "old"); reason != PruneKeptOpen || len(preview.Removed) != 0 {
				t.Fatalf("dry run kept reason %q and would remove %v, want it kept as open", reason, pruneIDs(preview.Removed))
			}

			report, err := pruneStore(root).Prune(PruneOptions{OlderThan: thirtyDays})
			if err != nil {
				t.Fatalf("Prune: %v", err)
			}
			if reason := keptReason(report, "old"); reason != PruneKeptOpen {
				t.Fatalf("kept reason %q, want %q (report %+v)", reason, PruneKeptOpen, report)
			}
			if !sessionDirExists(t, root, "old") {
				t.Fatal("a session another process had open was removed")
			}

			// The lease was the only thing keeping it.
			other.Release("old")
			report, err = pruneStore(root).Prune(PruneOptions{OlderThan: thirtyDays})
			if err != nil {
				t.Fatalf("Prune after release: %v", err)
			}
			if strings.Join(pruneIDs(report.Removed), ",") != "old" {
				t.Fatalf("after the other process let go, removed %v", pruneIDs(report.Removed))
			}
		})
	}
}

// Lineage and Tree fail on a missing ancestor, so an ancestor of anything kept
// stays, however old.
func TestPruneKeepsEveryAncestorOfAKeptSession(t *testing.T) {
	root := t.TempDir()
	createFinishedSession(t, root, "grandparent", "2026-06-01T00:00:00Z", "")
	createFinishedSession(t, root, "parent", "2026-06-15T00:00:00Z", "grandparent")
	createFinishedSession(t, root, "child", "2026-09-25T00:00:00Z", "parent")
	createFinishedSession(t, root, "unrelated", "2026-06-01T00:00:00Z", "")

	report, err := pruneStore(root).Prune(PruneOptions{OlderThan: thirtyDays})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if got := strings.Join(pruneIDs(report.Removed), ","); got != "unrelated" {
		t.Fatalf("removed %q, want only the unrelated session", got)
	}
	for _, id := range []string{"parent", "grandparent"} {
		if reason := keptReason(report, id); reason != PruneKeptParent {
			t.Errorf("%s kept for %q, want %q", id, reason, PruneKeptParent)
		}
	}
	if lineage, err := pruneStore(root).Lineage("child"); err != nil || len(lineage) != 3 {
		t.Fatalf("the kept child's lineage is broken: %d entries, %v", len(lineage), err)
	}
}

// Descendants go first, and one that turns out to have been written since the
// plan keeps its ancestors, which are old enough themselves.
func TestPruneKeepsTheAncestorsOfASessionWrittenWhilePruning(t *testing.T) {
	root := t.TempDir()
	createFinishedSession(t, root, "parent", "2026-06-01T00:00:00Z", "")
	createFinishedSession(t, root, "child", "2026-06-15T00:00:00Z", "parent")

	var seen []string
	pruneRemoveSeam = func(id string) {
		seen = append(seen, id)
		if id == "child" {
			rewriteUpdatedAt(t, root, "child", "2026-09-25T23:00:00Z")
		}
	}
	defer func() { pruneRemoveSeam = nil }()

	report, err := pruneStore(root).Prune(PruneOptions{OlderThan: thirtyDays})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if strings.Join(seen, ",") != "child" {
		t.Errorf("removal reached %v, want the child first and the parent never", seen)
	}
	if reason := keptReason(report, "child"); reason != PruneKeptUpdated {
		t.Errorf("child kept for %q, want %q", reason, PruneKeptUpdated)
	}
	if reason := keptReason(report, "parent"); reason != PruneKeptParent {
		t.Errorf("parent kept for %q, want %q", reason, PruneKeptParent)
	}
	if len(report.Removed) != 0 || !sessionDirExists(t, root, "parent") || !sessionDirExists(t, root, "child") {
		t.Fatalf("something was removed: %+v", report)
	}
}

func TestPruneKeepsASessionWhoseLastUpdateCannotBeRead(t *testing.T) {
	root := t.TempDir()
	createFinishedSession(t, root, "undated", "2026-06-01T00:00:00Z", "")
	rewriteUpdatedAt(t, root, "undated", "sometime last spring")

	preview, err := pruneStore(root).Prune(PruneOptions{OlderThan: thirtyDays, DryRun: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if reason := keptReason(preview, "undated"); reason != PruneKeptUndated || len(preview.Removed) != 0 {
		t.Fatalf("dry run kept reason %q and would remove %v", reason, pruneIDs(preview.Removed))
	}

	report, err := pruneStore(root).Prune(PruneOptions{OlderThan: thirtyDays})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if reason := keptReason(report, "undated"); reason != PruneKeptUndated {
		t.Fatalf("kept for %q, want %q", reason, PruneKeptUndated)
	}
	if !sessionDirExists(t, root, "undated") {
		t.Fatal("a session with an unreadable update time was removed")
	}
}

// A session another process has just created, and not yet written to, is as
// open as one it has been writing for hours: a TUI creates its session before
// the first prompt.
func TestPruneLeavesASessionAnotherProcessJustCreated(t *testing.T) {
	root := t.TempDir()
	other := NewStore(StoreOptions{RootDir: root, Now: fixedClock("2026-07-01T00:00:00Z")})
	if _, err := other.Create(CreateInput{SessionID: "fresh", Title: "not written yet"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	defer other.Release("fresh")

	report, err := pruneStore(root).Prune(PruneOptions{OlderThan: thirtyDays})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if reason := keptReason(report, "fresh"); reason != PruneKeptOpen {
		t.Fatalf("kept reason %q, want %q (report %+v)", reason, PruneKeptOpen, report)
	}
	if !sessionDirExists(t, root, "fresh") {
		t.Fatal("a session another process had just created was removed")
	}
}
