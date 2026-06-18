package sessions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func seqTestStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(StoreOptions{RootDir: t.TempDir(), Now: fixedClock("2026-06-04T10:00:00Z")})
}

func appendN(t *testing.T, store *Store, sid string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := store.AppendEvent(sid, AppendEventInput{Type: EventMessage, Payload: map[string]any{"content": "ok"}}); err != nil {
			t.Fatal(err)
		}
	}
}

// The reported repro: a crash between the event append and the metadata write
// leaves EventCount BEHIND the log; the next append must derive the sequence from
// the log and NOT reuse a number (which would mis-target /rewind).
func TestAppendDerivesSequenceFromLogAfterStaleMetadata(t *testing.T) {
	store := seqTestStore(t)
	s, err := store.Create(CreateInput{SessionID: "zero_seq_1", Title: "t", Cwd: "/repo", ModelID: "m", Provider: "p"})
	if err != nil {
		t.Fatal(err)
	}
	appendN(t, store, s.SessionID, 3) // log + metadata both at seq 3

	// Simulate the crash: rewind metadata.EventCount to 2 while the log keeps 1,2,3.
	meta, err := store.readMetadata(s.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	meta.EventCount = 2
	if err := store.writeMetadata(meta); err != nil {
		t.Fatal(err)
	}

	ev, err := store.AppendEvent(s.SessionID, AppendEventInput{Type: EventMessage, Payload: map[string]any{"content": "after"}})
	if err != nil {
		t.Fatal(err)
	}
	if ev.Sequence != 4 {
		t.Fatalf("expected log-derived sequence 4, got %d (would have been a duplicate 3)", ev.Sequence)
	}
	events, err := store.ReadEvents(s.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int]bool{}
	for _, e := range events {
		if seen[e.Sequence] {
			t.Fatalf("duplicate sequence %d in log: %+v", e.Sequence, events)
		}
		seen[e.Sequence] = true
	}
}

// The mirror case (stale-HIGH EventCount, e.g. an interrupted truncate) must
// leave a benign gap, never a duplicate.
func TestAppendWithStaleHighMetadataLeavesGapNotDuplicate(t *testing.T) {
	store := seqTestStore(t)
	s, _ := store.Create(CreateInput{SessionID: "zero_seq_2", Title: "t", Cwd: "/repo", ModelID: "m", Provider: "p"})
	appendN(t, store, s.SessionID, 2)

	meta, _ := store.readMetadata(s.SessionID)
	meta.EventCount = 5 // stale-high; the log only has 1,2
	if err := store.writeMetadata(meta); err != nil {
		t.Fatal(err)
	}
	ev, err := store.AppendEvent(s.SessionID, AppendEventInput{Type: EventMessage, Payload: map[string]any{"content": "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if ev.Sequence != 6 {
		t.Fatalf("stale-high metadata should advance to 6 (benign gap), got %d", ev.Sequence)
	}
}

func TestLastEventSequence_EmptyAndMultiple(t *testing.T) {
	store := seqTestStore(t)
	s, _ := store.Create(CreateInput{SessionID: "zero_tail_1", Title: "t", Cwd: "/repo", ModelID: "m", Provider: "p"})
	if seq, err := store.lastEventSequence(s.SessionID); err != nil || seq != 0 {
		t.Fatalf("empty log: got %d err=%v, want 0", seq, err)
	}
	appendN(t, store, s.SessionID, 3)
	if seq, err := store.lastEventSequence(s.SessionID); err != nil || seq != 3 {
		t.Fatalf("3 events: got %d err=%v, want 3", seq, err)
	}
}

func TestLastEventSequence_IgnoresTornTail(t *testing.T) {
	store := seqTestStore(t)
	s, _ := store.Create(CreateInput{SessionID: "zero_tail_2", Title: "t", Cwd: "/repo", ModelID: "m", Provider: "p"})
	appendN(t, store, s.SessionID, 3)
	// Append a torn partial line (interrupted write: no trailing newline).
	f, err := os.OpenFile(filepath.Join(store.RootDir, s.SessionID, EventsFile), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"sequence":4,"type":"message","partial`); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if seq, err := store.lastEventSequence(s.SessionID); err != nil || seq != 3 {
		t.Fatalf("torn tail must be ignored: got %d err=%v, want 3", seq, err)
	}
}

func TestLastEventSequence_LargeLastEvent(t *testing.T) {
	store := seqTestStore(t)
	s, _ := store.Create(CreateInput{SessionID: "zero_tail_3", Title: "t", Cwd: "/repo", ModelID: "m", Provider: "p"})
	// A single event larger than the 64 KiB initial tail window forces the read
	// to grow its window; the sequence must still be found.
	big := strings.Repeat("x", 100*1024)
	if _, err := store.AppendEvent(s.SessionID, AppendEventInput{Type: EventMessage, Payload: map[string]any{"content": big}}); err != nil {
		t.Fatal(err)
	}
	if seq, err := store.lastEventSequence(s.SessionID); err != nil || seq != 1 {
		t.Fatalf("large last event: got %d err=%v, want 1", seq, err)
	}
}
