package sessions

import (
	"sync"
	"sync/atomic"
	"testing"
)

// AppendEventUnlessExists must perform the existence check and append atomically:
// under concurrency, exactly ONE caller appends even though all see the event
// absent at first. This is the dedup the specialist accounting paths rely on.
func TestAppendEventUnlessExistsIsAtomic(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir()})
	if _, err := store.Create(CreateInput{SessionID: "s"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	const once EventType = "test_record_once"
	exists := func(events []Event) bool {
		for _, e := range events {
			if e.Type == once {
				return true
			}
		}
		return false
	}

	const goroutines = 24
	var wg sync.WaitGroup
	var appended int32
	start := make(chan struct{})
	for range goroutines {
		wg.Go(func() {
			<-start
			_, ok, err := store.AppendEventUnlessExists("s", AppendEventInput{Type: once, Payload: map[string]any{"n": 1}}, exists)
			if err != nil {
				t.Errorf("AppendEventUnlessExists: %v", err)
				return
			}
			if ok {
				atomic.AddInt32(&appended, 1)
			}
		})
	}
	close(start)
	wg.Wait()

	if appended != 1 {
		t.Fatalf("expected exactly 1 append under concurrency, got %d", appended)
	}
	events, err := store.ReadEvents("s")
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	count := 0
	for _, e := range events {
		if e.Type == once {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 %q event persisted, got %d", once, count)
	}
}

// A nil predicate always appends (parity with AppendEvent).
func TestAppendEventUnlessExistsNilPredicateAppends(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir()})
	if _, err := store.Create(CreateInput{SessionID: "s"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, ok, err := store.AppendEventUnlessExists("s", AppendEventInput{Type: "e", Payload: map[string]any{}}, nil)
	if err != nil || !ok {
		t.Fatalf("nil predicate should append: ok=%v err=%v", ok, err)
	}
}
