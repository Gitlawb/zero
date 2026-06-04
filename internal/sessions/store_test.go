package sessions

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDefaultRootUsesXDGDataHome(t *testing.T) {
	root, err := DefaultRoot(DefaultRootOptions{
		Env: map[string]string{
			"XDG_DATA_HOME": "/tmp/zero-data",
			"HOME":          "/tmp/home",
		},
	})

	if err != nil {
		t.Fatalf("DefaultRoot returned error: %v", err)
	}
	if root != filepath.Join("/tmp/zero-data", "zero", "sessions") {
		t.Fatalf("root = %q", root)
	}
}

func TestStoreCreatesAppendsListsAndReadsSessions(t *testing.T) {
	store := NewStore(StoreOptions{
		RootDir: t.TempDir(),
		Now: sequenceClock([]time.Time{
			time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC),
			time.Date(2026, 6, 4, 10, 0, 1, 0, time.UTC),
			time.Date(2026, 6, 4, 10, 0, 2, 0, time.UTC),
		}),
	})

	session, err := store.Create(CreateInput{
		SessionID: "session_one",
		Title:     "Build headless",
		Cwd:       "/repo/zero",
		ModelID:   "gpt-4.1",
		Provider:  "openai",
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if session.EventCount != 0 || session.CreatedAt != "2026-06-04T10:00:00Z" {
		t.Fatalf("session metadata = %#v", session)
	}

	event, err := store.AppendEvent(session.SessionID, AppendEventInput{
		Type:    EventMessage,
		Payload: map[string]any{"role": "user", "content": "hello"},
	})
	if err != nil {
		t.Fatalf("AppendEvent returned error: %v", err)
	}
	if event.ID != "session_one:1" || event.Sequence != 1 {
		t.Fatalf("event = %#v", event)
	}

	events, err := store.ReadEvents(session.SessionID)
	if err != nil {
		t.Fatalf("ReadEvents returned error: %v", err)
	}
	if len(events) != 1 || events[0].Type != EventMessage {
		t.Fatalf("events = %#v", events)
	}

	latest, err := store.Latest()
	if err != nil {
		t.Fatalf("Latest returned error: %v", err)
	}
	if latest == nil || latest.SessionID != session.SessionID || latest.EventCount != 1 {
		t.Fatalf("latest = %#v", latest)
	}

	metadataPath := filepath.Join(store.RootDir, session.SessionID, MetadataFile)
	if _, err := os.Stat(metadataPath); err != nil {
		t.Fatalf("expected metadata file at %s: %v", metadataPath, err)
	}
}

func TestStoreForkCopiesEventsAndRecordsLineage(t *testing.T) {
	store := NewStore(StoreOptions{
		RootDir: t.TempDir(),
		Now: sequenceClock([]time.Time{
			time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC),
			time.Date(2026, 6, 4, 10, 0, 1, 0, time.UTC),
			time.Date(2026, 6, 4, 10, 0, 2, 0, time.UTC),
			time.Date(2026, 6, 4, 10, 0, 3, 0, time.UTC),
			time.Date(2026, 6, 4, 10, 0, 4, 0, time.UTC),
			time.Date(2026, 6, 4, 10, 0, 5, 0, time.UTC),
		}),
	})
	if _, err := store.Create(CreateInput{SessionID: "parent", Title: "Parent"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendEvent("parent", AppendEventInput{Type: EventMessage, Payload: map[string]any{"content": "first"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendEvent("parent", AppendEventInput{Type: EventToolResult, Payload: map[string]any{"output": "done"}}); err != nil {
		t.Fatal(err)
	}

	fork, err := store.Fork("parent", ForkInput{SessionID: "forked", Cwd: "/repo/new"})
	if err != nil {
		t.Fatalf("Fork returned error: %v", err)
	}
	if fork.ParentSessionID != "parent" || fork.ForkedFromEventID != "parent:2" || fork.EventCount != 3 {
		t.Fatalf("fork metadata = %#v", fork)
	}

	events, err := store.ReadEvents("forked")
	if err != nil {
		t.Fatalf("ReadEvents returned error: %v", err)
	}
	got := []EventType{events[0].Type, events[1].Type, events[2].Type}
	want := []EventType{EventMessage, EventToolResult, EventSessionFork}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fork event types = %#v, want %#v", got, want)
	}
}

func TestPrepareExecSessionResolvesResumeAndFork(t *testing.T) {
	store := NewStore(StoreOptions{
		RootDir: t.TempDir(),
		Now: sequenceClock([]time.Time{
			time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC),
			time.Date(2026, 6, 4, 10, 0, 1, 0, time.UTC),
			time.Date(2026, 6, 4, 10, 0, 2, 0, time.UTC),
			time.Date(2026, 6, 4, 10, 0, 3, 0, time.UTC),
		}),
	})
	if _, err := store.Create(CreateInput{SessionID: "older"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(CreateInput{SessionID: "latest", Title: "Latest"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendEvent("latest", AppendEventInput{Type: EventMessage, Payload: map[string]any{"content": "previous answer"}}); err != nil {
		t.Fatal(err)
	}

	prepared, err := PrepareExec(PrepareExecOptions{Store: store, ResumeLatest: true})
	if err != nil {
		t.Fatalf("PrepareExec returned error: %v", err)
	}
	if prepared.Mode != ModeResume || prepared.Session.SessionID != "latest" || len(prepared.ContextEvents) != 1 {
		t.Fatalf("prepared resume = %#v", prepared)
	}
	if got := FormatExecPrompt("continue", prepared); got == "continue" || !strings.Contains(got, "previous answer") {
		t.Fatalf("expected session context in prompt, got %q", got)
	}

	forked, err := PrepareExec(PrepareExecOptions{Store: store, Fork: "latest", SessionID: "forked"})
	if err != nil {
		t.Fatalf("PrepareExec fork returned error: %v", err)
	}
	if forked.Mode != ModeFork || forked.Session.ParentSessionID != "latest" {
		t.Fatalf("prepared fork = %#v", forked)
	}
}

func sequenceClock(values []time.Time) func() time.Time {
	index := 0
	return func() time.Time {
		if index >= len(values) {
			return values[len(values)-1]
		}
		value := values[index]
		index++
		return value
	}
}
