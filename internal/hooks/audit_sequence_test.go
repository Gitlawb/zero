package hooks

import (
	"path/filepath"
	"testing"
)

// The audit append must assign monotonic sequences via an O(1) tail read, and a
// second store on the same (shared, global) file must CONTINUE the sequence — an
// in-memory counter would have restarted at 1 and collided across processes.
func TestAuditStoreSequenceTailRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	store, err := NewAuditStore(AuditStoreOptions{AuditPath: path})
	if err != nil {
		t.Fatalf("NewAuditStore: %v", err)
	}
	for i := 1; i <= 50; i++ {
		ev, err := store.AppendStarted(AppendStartedInput{HookID: "h", Event: "beforeTool"})
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		if ev.Sequence != i {
			t.Fatalf("event %d Sequence = %d, want %d", i, ev.Sequence, i)
		}
	}

	// A separate store handle on the SAME file (like a concurrent process).
	store2, err := NewAuditStore(AuditStoreOptions{AuditPath: path})
	if err != nil {
		t.Fatalf("NewAuditStore #2: %v", err)
	}
	ev, err := store2.AppendStarted(AppendStartedInput{HookID: "h2", Event: "afterTool"})
	if err != nil {
		t.Fatalf("second-store append: %v", err)
	}
	if ev.Sequence != 51 {
		t.Fatalf("second store next Sequence = %d, want 51 (tail read must observe prior events)", ev.Sequence)
	}

	events, err := store2.ReadEvents()
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	if len(events) != 51 {
		t.Fatalf("expected 51 events, got %d", len(events))
	}
	for i, e := range events {
		if e.Sequence != i+1 {
			t.Fatalf("events[%d].Sequence = %d, want %d (must be unique + monotonic)", i, e.Sequence, i+1)
		}
	}
}
