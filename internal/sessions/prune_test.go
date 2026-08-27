package sessions

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPruneDryRunDeletesNothing(t *testing.T) {
	store, ids := setupPruneFixture(t)
	result, err := store.Prune(PrunePolicy{RetentionDays: 7, DryRun: true})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(result.Deleted) == 0 {
		t.Fatalf("dry-run should report the unlocked old session as deletable, got %#v", result)
	}
	if !result.DryRun {
		t.Fatalf("DryRun = false, want true")
	}
	for _, id := range []string{ids.old, ids.named, ids.locked, ids.active} {
		if !sessionDirExists(store, id) {
			t.Fatalf("dry-run deleted %s", id)
		}
	}
}

func TestPruneKeepsLockedAndNamedSessions(t *testing.T) {
	store, ids := setupPruneFixture(t)
	unlock, err := store.lockSession(ids.locked)
	if err != nil {
		t.Fatalf("lockSession: %v", err)
	}
	defer unlock()

	result, err := store.Prune(PrunePolicy{RetentionDays: 7})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if !sessionDirExists(store, ids.locked) {
		t.Fatalf("locked session %s was deleted", ids.locked)
	}
	if !sessionDirExists(store, ids.named) {
		t.Fatalf("named session %s was deleted", ids.named)
	}
	if !sessionDirExists(store, ids.active) {
		t.Fatalf("active session %s was deleted", ids.active)
	}
	if sessionDirExists(store, ids.old) {
		t.Fatalf("old unlocked session %s was kept", ids.old)
	}
	if !hasSkipReason(result, ids.locked, pruneReasonLocked) {
		t.Fatalf("locked skip missing: %#v", result.Skipped)
	}
	if !hasSkipReason(result, ids.named, pruneReasonNamed) {
		t.Fatalf("named skip missing: %#v", result.Skipped)
	}
	if !hasSkipReason(result, ids.active, pruneReasonActive) {
		t.Fatalf("active skip missing: %#v", result.Skipped)
	}
}

func TestPruneRemovesOldUnlockedSessions(t *testing.T) {
	store, ids := setupPruneFixture(t)
	result, err := store.Prune(PrunePolicy{RetentionDays: 7})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if sessionDirExists(store, ids.old) {
		t.Fatalf("old unlocked session %s was not removed", ids.old)
	}
	if sessionDirExists(store, ids.locked) {
		t.Fatalf("old unlocked session %s (lock not held) was not removed", ids.locked)
	}
	if !sessionDirExists(store, ids.active) {
		t.Fatalf("active session %s was removed", ids.active)
	}
	if !sessionDirExists(store, ids.named) {
		t.Fatalf("named session %s was removed", ids.named)
	}
	deleted := map[string]string{}
	for _, item := range result.Deleted {
		deleted[item.SessionID] = item.Reason
	}
	if deleted[ids.old] != pruneReasonAge || deleted[ids.locked] != pruneReasonAge {
		t.Fatalf("deleted = %#v, want %s and %s for age", result.Deleted, ids.old, ids.locked)
	}
}

func TestPruneRetentionDaysZeroMeansOff(t *testing.T) {
	store, ids := setupPruneFixture(t)
	result, err := store.Prune(PrunePolicy{RetentionDays: 0, MaxCount: 0})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(result.Deleted) != 0 {
		t.Fatalf("retentionDays=0 deleted %#v", result.Deleted)
	}
	for _, id := range []string{ids.old, ids.named, ids.locked, ids.active} {
		if !sessionDirExists(store, id) {
			t.Fatalf("retentionDays=0 removed %s", id)
		}
	}
}

type pruneIDs struct {
	old, named, locked, active string
}

func setupPruneFixture(t *testing.T) (*Store, pruneIDs) {
	t.Helper()
	now := time.Date(2026, 8, 27, 18, 0, 0, 0, time.UTC)
	old := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	clock := old
	store := NewStore(StoreOptions{
		RootDir: t.TempDir(),
		Now:     func() time.Time { return clock },
	})
	ids := pruneIDs{
		old:    "old_unlocked",
		named:  "named_saved",
		locked: "locked_old",
		active: "active_now",
	}
	if _, err := store.Create(CreateInput{SessionID: ids.old, Title: "old run"}); err != nil {
		t.Fatalf("Create old: %v", err)
	}
	clock = old.Add(time.Second)
	if _, err := store.Create(CreateInput{SessionID: ids.named, Title: "bookmarked", Tag: "keep"}); err != nil {
		t.Fatalf("Create named: %v", err)
	}
	clock = old.Add(2 * time.Second)
	if _, err := store.Create(CreateInput{SessionID: ids.locked, Title: "held lock"}); err != nil {
		t.Fatalf("Create locked: %v", err)
	}
	clock = now
	if _, err := store.Create(CreateInput{SessionID: ids.active, Title: "current"}); err != nil {
		t.Fatalf("Create active: %v", err)
	}
	return store, ids
}

func sessionDirExists(store *Store, sessionID string) bool {
	info, err := os.Stat(filepath.Join(store.RootDir, sessionID))
	return err == nil && info.IsDir()
}

func hasSkipReason(result PruneResult, sessionID, reason string) bool {
	for _, item := range result.Skipped {
		if item.SessionID == sessionID && item.Reason == reason {
			return true
		}
	}
	return false
}
