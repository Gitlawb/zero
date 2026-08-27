package sessions

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// PrunePolicy is the on-disk session retention policy. Unset or 0 values
// disable the corresponding limit so existing users are not surprised: Zero
// never deletes session directories unless a positive RetentionDays,
// MaxCount, or OlderThan is set.
type PrunePolicy struct {
	// RetentionDays deletes unprotected sessions whose UpdatedAt is older than
	// this many days. 0 means off.
	RetentionDays int
	// MaxCount keeps at most this many sessions (newest UpdatedAt first),
	// never deleting protected ones. 0 means off.
	MaxCount int
	// OlderThan, when > 0, is an explicit age cutoff that replaces
	// RetentionDays (used by `zero sessions prune --older-than`).
	OlderThan time.Duration
	// DryRun reports what would be deleted without removing anything.
	DryRun bool
}

// Enabled reports whether the policy would prune anything. 0 / unset is off.
func (p PrunePolicy) Enabled() bool {
	return p.RetentionDays > 0 || p.MaxCount > 0 || p.OlderThan > 0
}

func (p PrunePolicy) cutoff(now time.Time) (time.Time, bool) {
	if p.OlderThan > 0 {
		return now.Add(-p.OlderThan), true
	}
	if p.RetentionDays > 0 {
		return now.AddDate(0, 0, -p.RetentionDays), true
	}
	return time.Time{}, false
}

// PruneItem is one session directory considered by Prune.
type PruneItem struct {
	SessionID string
	Title     string
	UpdatedAt string
	Reason    string
}

// PruneResult summarizes a prune pass.
type PruneResult struct {
	Deleted []PruneItem
	Skipped []PruneItem
	DryRun  bool
}

const (
	pruneReasonAge    = "older than retention"
	pruneReasonCount  = "over max_count"
	pruneReasonLocked = "locked"
	pruneReasonNamed  = "named"
	pruneReasonActive = "active"
)

// Prune removes unprotected session directories according to policy.
//
// A session is never deleted when it:
//   - holds session.lock (another process has the exclusive lock),
//   - is the active resumable session (LatestResumable), or
//   - is explicitly saved/named/bookmarked (non-empty Tag).
//
// Checkpoint/rewind blobs live under the session directory (checkpoints/blobs),
// so removing the directory also drops unreferenced blobs for that session.
func (store *Store) Prune(policy PrunePolicy) (PruneResult, error) {
	result := PruneResult{DryRun: policy.DryRun}
	if !policy.Enabled() {
		return result, nil
	}

	sessions, err := store.List()
	if err != nil {
		return result, err
	}
	if len(sessions) == 0 {
		return result, nil
	}

	activeID := ""
	if active, err := store.LatestResumable(); err != nil {
		return result, err
	} else if active != nil {
		activeID = active.SessionID
	} else if latest, err := store.Latest(); err != nil {
		return result, err
	} else if latest != nil {
		activeID = latest.SessionID
	}

	now := store.now().UTC()
	cutoff, hasCutoff := policy.cutoff(now)

	type ranked struct {
		meta      Metadata
		updatedAt time.Time
		protected string
	}
	items := make([]ranked, 0, len(sessions))
	protectedCount := 0
	for _, session := range sessions {
		updatedAt, ok := parseSessionTime(session.UpdatedAt)
		if !ok {
			updatedAt, ok = parseSessionTime(session.CreatedAt)
		}
		if !ok {
			// Unparseable timestamps are treated as current so we never
			// delete a session we cannot date.
			updatedAt = now
		}
		item := ranked{meta: session, updatedAt: updatedAt}
		switch {
		case strings.TrimSpace(session.SessionID) == activeID:
			item.protected = pruneReasonActive
		case sessionIsNamed(session):
			item.protected = pruneReasonNamed
		}
		if item.protected != "" {
			protectedCount++
		}
		items = append(items, item)
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].updatedAt.Equal(items[j].updatedAt) {
			return items[i].meta.SessionID < items[j].meta.SessionID
		}
		return items[i].updatedAt.After(items[j].updatedAt)
	})

	unprotectedSlots := 0
	if policy.MaxCount > 0 {
		unprotectedSlots = policy.MaxCount - protectedCount
		if unprotectedSlots < 0 {
			unprotectedSlots = 0
		}
	}

	type candidate struct {
		ranked
		reason string
	}
	var deleteList []candidate
	keptUnprotected := 0
	for _, item := range items {
		if item.protected != "" {
			result.Skipped = append(result.Skipped, pruneItemFrom(item.meta, item.protected))
			continue
		}
		if hasCutoff && item.updatedAt.Before(cutoff) {
			deleteList = append(deleteList, candidate{ranked: item, reason: pruneReasonAge})
			continue
		}
		if policy.MaxCount > 0 && keptUnprotected >= unprotectedSlots {
			deleteList = append(deleteList, candidate{ranked: item, reason: pruneReasonCount})
			continue
		}
		keptUnprotected++
	}

	for _, cand := range deleteList {
		unlock, ok, err := store.tryLockSession(cand.meta.SessionID)
		if err != nil {
			result.Skipped = append(result.Skipped, pruneItemFrom(cand.meta, err.Error()))
			continue
		}
		if !ok {
			result.Skipped = append(result.Skipped, pruneItemFrom(cand.meta, pruneReasonLocked))
			continue
		}
		if policy.DryRun {
			unlock()
			result.Deleted = append(result.Deleted, pruneItemFrom(cand.meta, cand.reason))
			continue
		}
		// Release session.lock before RemoveAll so Windows can delete the lock
		// file. tryLockSession already confirmed no other holder.
		unlock()
		if err := os.RemoveAll(store.sessionPath(cand.meta.SessionID)); err != nil {
			result.Skipped = append(result.Skipped, pruneItemFrom(cand.meta, err.Error()))
			continue
		}
		result.Deleted = append(result.Deleted, pruneItemFrom(cand.meta, cand.reason))
	}
	return result, nil
}

func sessionIsNamed(session Metadata) bool {
	return strings.TrimSpace(session.Tag) != ""
}

func pruneItemFrom(session Metadata, reason string) PruneItem {
	return PruneItem{
		SessionID: session.SessionID,
		Title:     session.Title,
		UpdatedAt: session.UpdatedAt,
		Reason:    reason,
	}
}

func parseSessionTime(raw string) (time.Time, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}, false
	}
	if ts, err := time.Parse(time.RFC3339, trimmed); err == nil {
		return ts.UTC(), true
	}
	if ts, err := time.Parse(time.RFC3339Nano, trimmed); err == nil {
		return ts.UTC(), true
	}
	return time.Time{}, false
}

// FormatPruneSummary is a stable one-line description used by tests and logs.
func FormatPruneSummary(result PruneResult) string {
	verb := "Deleted"
	if result.DryRun {
		verb = "Would delete"
	}
	return fmt.Sprintf("%s %d session(s), skipped %d", verb, len(result.Deleted), len(result.Skipped))
}
