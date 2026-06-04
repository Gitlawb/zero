package sessions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStoreCreatesAppendsListsAndReadsEvents(t *testing.T) {
	now := fixedClock("2026-06-04T10:00:00Z")
	store := NewStore(StoreOptions{RootDir: t.TempDir(), Now: now})

	session, err := store.Create(CreateInput{
		SessionID: "zero_test_1",
		Title:     "First run",
		Cwd:       "/repo",
		ModelID:   "gpt-4.1",
		Provider:  "openai",
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if session.EventCount != 0 || session.CreatedAt != "2026-06-04T10:00:00Z" {
		t.Fatalf("unexpected session metadata: %#v", session)
	}

	event, err := store.AppendEvent(session.SessionID, AppendEventInput{
		Type: EventMessage,
		Payload: map[string]any{
			"role":    "user",
			"content": "searchable hello",
		},
	})
	if err != nil {
		t.Fatalf("AppendEvent returned error: %v", err)
	}
	if event.ID != "zero_test_1:1" || event.Sequence != 1 {
		t.Fatalf("unexpected event identity: %#v", event)
	}

	loaded, err := store.Get(session.SessionID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if loaded == nil || loaded.EventCount != 1 || loaded.LastEventType != EventMessage {
		t.Fatalf("metadata was not updated after append: %#v", loaded)
	}

	events, err := store.ReadEvents(session.SessionID)
	if err != nil {
		t.Fatalf("ReadEvents returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one event, got %#v", events)
	}
	payload := map[string]any{}
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}
	if payload["content"] != "searchable hello" {
		t.Fatalf("unexpected payload: %#v", payload)
	}

	sessions, err := store.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(sessions) != 1 || sessions[0].SessionID != session.SessionID {
		t.Fatalf("unexpected session list: %#v", sessions)
	}
}

func TestStoreForkCopiesEventsAndLineage(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir(), Now: fixedClock("2026-06-04T11:00:00Z")})
	parent, err := store.Create(CreateInput{SessionID: "parent", Title: "Parent", Cwd: "/repo", ModelID: "gpt-4.1", Provider: "openai"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	for _, content := range []string{"first", "second"} {
		if _, err := store.AppendEvent(parent.SessionID, AppendEventInput{Type: EventMessage, Payload: map[string]string{"content": content}}); err != nil {
			t.Fatalf("AppendEvent returned error: %v", err)
		}
	}

	fork, err := store.Fork(parent.SessionID, ForkInput{SessionID: "fork"})
	if err != nil {
		t.Fatalf("Fork returned error: %v", err)
	}
	if fork.ParentSessionID != parent.SessionID || fork.ForkedFromEventID != "parent:2" || fork.ForkedFromSequence != 2 {
		t.Fatalf("fork lineage not recorded: %#v", fork)
	}
	if fork.EventCount != 3 || fork.LastEventType != EventSessionFork {
		t.Fatalf("fork event count/type wrong: %#v", fork)
	}
	events, err := store.ReadEvents(fork.SessionID)
	if err != nil {
		t.Fatalf("ReadEvents returned error: %v", err)
	}
	if len(events) != 3 || events[0].ID != "fork:1" || events[2].Type != EventSessionFork {
		t.Fatalf("fork events not copied/remapped: %#v", events)
	}
}

func TestDefaultRootHonorsXDGDataHome(t *testing.T) {
	got := DefaultRoot(map[string]string{
		"XDG_DATA_HOME": "/tmp/zero-data",
		"HOME":          "/tmp/home",
	})
	want := filepath.Join("/tmp/zero-data", "zero", "sessions")
	if got != want {
		t.Fatalf("DefaultRoot = %q, want %q", got, want)
	}
}

func TestStoreRejectsUnsafeSessionIDsAndBadJSONL(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir(), Now: fixedClock("2026-06-04T12:00:00Z")})
	if _, err := store.Create(CreateInput{SessionID: "../escape"}); err == nil {
		t.Fatal("expected unsafe session id to be rejected")
	}

	if err := os.MkdirAll(filepath.Join(store.RootDir, "bad"), 0o700); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(store.RootDir, "bad", "metadata.json"), []byte(`{"sessionId":"bad","createdAt":"x","updatedAt":"x","eventCount":1}`), 0o600); err != nil {
		t.Fatalf("metadata write failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(store.RootDir, "bad", "events.jsonl"), []byte("{not-json}\n"), 0o600); err != nil {
		t.Fatalf("events write failed: %v", err)
	}

	_, err := store.ReadEvents("bad")
	if err == nil || !strings.Contains(err.Error(), "events.jsonl at line 1") {
		t.Fatalf("expected JSONL line error, got %v", err)
	}
}

func TestStoreGeneratesUniqueIDsWithFixedClock(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir(), Now: fixedClock("2026-06-04T12:30:00Z")})
	first, err := store.Create(CreateInput{})
	if err != nil {
		t.Fatalf("Create first returned error: %v", err)
	}
	second, err := store.Create(CreateInput{})
	if err != nil {
		t.Fatalf("Create second returned error: %v", err)
	}
	if first.SessionID == second.SessionID {
		t.Fatalf("generated session ids collided: %q", first.SessionID)
	}
}

func TestStoreAppendEventSerializesConcurrentWriters(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir(), Now: fixedClock("2026-06-04T13:00:00Z")})
	session, err := store.Create(CreateInput{SessionID: "concurrent"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	const total = 24
	var wg sync.WaitGroup
	errs := make(chan error, total)
	for index := 0; index < total; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, err := store.AppendEvent(session.SessionID, AppendEventInput{
				Type:    EventMessage,
				Payload: map[string]int{"index": index},
			})
			errs <- err
		}(index)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("AppendEvent returned error: %v", err)
		}
	}

	events, err := store.ReadEvents(session.SessionID)
	if err != nil {
		t.Fatalf("ReadEvents returned error: %v", err)
	}
	if len(events) != total {
		t.Fatalf("expected %d events, got %d: %#v", total, len(events), events)
	}
	seen := map[int]bool{}
	for _, event := range events {
		if seen[event.Sequence] {
			t.Fatalf("duplicate event sequence %d in %#v", event.Sequence, events)
		}
		seen[event.Sequence] = true
		if event.ID != fmt.Sprintf("%s:%d", session.SessionID, event.Sequence) {
			t.Fatalf("event id/sequence mismatch: %#v", event)
		}
	}
	for sequence := 1; sequence <= total; sequence++ {
		if !seen[sequence] {
			t.Fatalf("missing event sequence %d in %#v", sequence, events)
		}
	}
	loaded, err := store.Get(session.SessionID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if loaded == nil || loaded.EventCount != total || loaded.LastEventType != EventMessage {
		t.Fatalf("metadata not updated after concurrent append: %#v", loaded)
	}
}

func fixedClock(value string) func() time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return func() time.Time { return parsed }
}
