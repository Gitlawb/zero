package hooks

import (
	"path/filepath"
	"sync"
	"testing"
)

// Multiple AuditStore handles (separate store.mu, standing in for separate
// processes) appending CONCURRENTLY to the same shared log must still get unique,
// dense sequence numbers — only the cross-process lockAudit serializes the
// read-then-append. Without it, two appends read the same last sequence and
// collide. (The sequential TestAuditStoreSequenceTailRead cannot catch this.)
func TestAuditStoreSequenceConcurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	const storeCount = 4
	const perStore = 12

	stores := make([]*AuditStore, storeCount)
	for i := range stores {
		st, err := NewAuditStore(AuditStoreOptions{AuditPath: path})
		if err != nil {
			t.Fatalf("NewAuditStore: %v", err)
		}
		stores[i] = st
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	seqs := make([]int, 0, storeCount*perStore)
	for _, st := range stores {
		for range perStore {
			wg.Add(1)
			go func(st *AuditStore) {
				defer wg.Done()
				ev, err := st.AppendStarted(AppendStartedInput{HookID: "h", Event: "beforeTool"})
				if err != nil {
					t.Errorf("append: %v", err)
					return
				}
				mu.Lock()
				seqs = append(seqs, ev.Sequence)
				mu.Unlock()
			}(st)
		}
	}
	wg.Wait()

	if len(seqs) != storeCount*perStore {
		t.Fatalf("got %d events, want %d", len(seqs), storeCount*perStore)
	}
	seen := map[int]bool{}
	for _, s := range seqs {
		if seen[s] {
			t.Fatalf("duplicate sequence %d — read-then-append is not atomic across stores", s)
		}
		seen[s] = true
	}
	for i := 1; i <= storeCount*perStore; i++ {
		if !seen[i] {
			t.Fatalf("missing sequence %d (expected dense 1..%d)", i, storeCount*perStore)
		}
	}
}
