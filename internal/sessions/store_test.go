package sessions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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
		Tag:       "specialist",
		Depth:     1,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if session.EventCount != 0 || session.CreatedAt != "2026-06-04T10:00:00Z" {
		t.Fatalf("unexpected session metadata: %#v", session)
	}
	if session.Tag != "specialist" || session.Depth != 1 {
		t.Fatalf("session specialist metadata = %#v", session)
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
	if loaded.Tag != "specialist" || loaded.Depth != 1 {
		t.Fatalf("loaded specialist metadata = %#v", loaded)
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
	got := []EventType{events[0].Type, events[1].Type, events[2].Type}
	want := []EventType{EventMessage, EventMessage, EventSessionFork}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fork event types = %#v, want %#v", got, want)
	}
}

func TestStoreCreatesChildSessionsAndRecordsLineageEvents(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir(), Now: fixedClock("2026-06-04T11:30:00Z")})
	parent, err := store.Create(CreateInput{
		SessionID: "parent",
		Title:     "Parent",
		Cwd:       "/repo",
		ModelID:   "gpt-4.1",
		Provider:  "openai",
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if _, err := store.AppendEvent(parent.SessionID, AppendEventInput{Type: EventMessage, Payload: map[string]string{"content": "plan"}}); err != nil {
		t.Fatalf("AppendEvent returned error: %v", err)
	}

	child, err := store.CreateChild(parent.SessionID, ChildInput{
		SessionID: "child",
		Title:     "Review agent",
		Tag:       "specialist",
		Depth:     1,
		AgentName: "code-review",
		TaskID:    "task-1",
		Prompt:    "Review the diff",
	})
	if err != nil {
		t.Fatalf("CreateChild returned error: %v", err)
	}
	if child.SessionKind != SessionKindChild || child.ParentSessionID != parent.SessionID || child.RootSessionID != parent.SessionID {
		t.Fatalf("child lineage metadata = %#v", child)
	}
	if child.Cwd != parent.Cwd || child.ModelID != parent.ModelID || child.Provider != parent.Provider {
		t.Fatalf("child did not inherit parent runtime context: %#v", child)
	}
	if child.AgentName != "code-review" || child.TaskID != "task-1" || child.SpawnedFromEventID != "parent:1" || child.SpawnedFromSequence != 1 {
		t.Fatalf("child agent metadata = %#v", child)
	}
	if child.Tag != "specialist" || child.Depth != 1 {
		t.Fatalf("child specialist metadata = %#v", child)
	}

	childEvents, err := store.ReadEvents(child.SessionID)
	if err != nil {
		t.Fatalf("ReadEvents child returned error: %v", err)
	}
	if len(childEvents) != 1 || childEvents[0].Type != EventSessionChild {
		t.Fatalf("child events = %#v, want one session_child event", childEvents)
	}
	var childPayload map[string]any
	if err := json.Unmarshal(childEvents[0].Payload, &childPayload); err != nil {
		t.Fatalf("decode child payload: %v", err)
	}
	if childPayload["parentSessionId"] != parent.SessionID || childPayload["prompt"] != "Review the diff" {
		t.Fatalf("child payload = %#v, want parent and prompt", childPayload)
	}

	parentEvents, err := store.ReadEvents(parent.SessionID)
	if err != nil {
		t.Fatalf("ReadEvents parent returned error: %v", err)
	}
	if len(parentEvents) != 2 || parentEvents[1].Type != EventSessionChild {
		t.Fatalf("parent events = %#v, want child linkage event", parentEvents)
	}
	var parentPayload map[string]any
	if err := json.Unmarshal(parentEvents[1].Payload, &parentPayload); err != nil {
		t.Fatalf("decode parent payload: %v", err)
	}
	if parentPayload["childSessionId"] != child.SessionID || parentPayload["agentName"] != "code-review" || parentPayload["tag"] != "specialist" || parentPayload["depth"] != float64(1) {
		t.Fatalf("parent payload = %#v, want child linkage", parentPayload)
	}
}

func TestStoreRejectsNegativeSessionDepth(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir()})

	_, err := store.Create(CreateInput{SessionID: "bad_depth", Depth: -1})

	if err == nil || !strings.Contains(err.Error(), "invalid zero session depth") {
		t.Fatalf("expected invalid depth error, got %v", err)
	}
}

func TestStoreListsChildrenLineageAndTree(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir(), Now: sequenceClock([]time.Time{
		time.Date(2026, 6, 4, 11, 45, 0, 0, time.UTC),
		time.Date(2026, 6, 4, 11, 45, 1, 0, time.UTC),
		time.Date(2026, 6, 4, 11, 45, 2, 0, time.UTC),
		time.Date(2026, 6, 4, 11, 45, 3, 0, time.UTC),
		time.Date(2026, 6, 4, 11, 45, 4, 0, time.UTC),
		time.Date(2026, 6, 4, 11, 45, 5, 0, time.UTC),
		time.Date(2026, 6, 4, 11, 45, 6, 0, time.UTC),
	})})
	root, err := store.Create(CreateInput{SessionID: "root", Title: "Root"})
	if err != nil {
		t.Fatalf("Create root returned error: %v", err)
	}
	first, err := store.CreateChild(root.SessionID, ChildInput{SessionID: "first", AgentName: "review"})
	if err != nil {
		t.Fatalf("CreateChild first returned error: %v", err)
	}
	second, err := store.CreateChild(root.SessionID, ChildInput{SessionID: "second", AgentName: "test-gen"})
	if err != nil {
		t.Fatalf("CreateChild second returned error: %v", err)
	}
	grandchild, err := store.CreateChild(first.SessionID, ChildInput{SessionID: "grandchild", AgentName: "security"})
	if err != nil {
		t.Fatalf("CreateChild grandchild returned error: %v", err)
	}

	children, err := store.ListChildren(root.SessionID)
	if err != nil {
		t.Fatalf("ListChildren returned error: %v", err)
	}
	if got := []string{children[0].SessionID, children[1].SessionID}; !reflect.DeepEqual(got, []string{second.SessionID, first.SessionID}) {
		t.Fatalf("children order = %#v, want newest direct children first", got)
	}

	lineage, err := store.Lineage(grandchild.SessionID)
	if err != nil {
		t.Fatalf("Lineage returned error: %v", err)
	}
	if got := []string{lineage[0].SessionID, lineage[1].SessionID, lineage[2].SessionID}; !reflect.DeepEqual(got, []string{root.SessionID, first.SessionID, grandchild.SessionID}) {
		t.Fatalf("lineage = %#v, want root to child path", got)
	}

	tree, err := store.Tree(root.SessionID)
	if err != nil {
		t.Fatalf("Tree returned error: %v", err)
	}
	if tree.Session.SessionID != root.SessionID || len(tree.Children) != 2 {
		t.Fatalf("tree root = %#v, want two children", tree)
	}
	if tree.Children[1].Session.SessionID != first.SessionID || len(tree.Children[1].Children) != 1 {
		t.Fatalf("tree nested children = %#v, want grandchild under first", tree.Children)
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

func TestPrepareExecPersistsSpecialistMetadataForNewSession(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir(), Now: fixedClock("2026-06-04T14:30:00Z")})

	prepared, err := PrepareExec(PrepareExecOptions{
		Store:     store,
		SessionID: "child_session",
		Title:     "Explorer child",
		Tag:       "specialist",
		Depth:     2,
	})
	if err != nil {
		t.Fatalf("PrepareExec returned error: %v", err)
	}
	if prepared.Mode != ModeNew || prepared.Session.Tag != "specialist" || prepared.Session.Depth != 2 {
		t.Fatalf("prepared specialist metadata = %#v", prepared)
	}
	loaded, err := store.Get("child_session")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if loaded == nil || loaded.Tag != "specialist" || loaded.Depth != 2 {
		t.Fatalf("loaded specialist metadata = %#v", loaded)
	}
}

func TestPrepareExecWhitespaceResumeDoesNotFallbackToLatest(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir(), Now: fixedClock("2026-06-04T14:00:00Z")})
	if _, err := store.Create(CreateInput{SessionID: "latest"}); err != nil {
		t.Fatal(err)
	}

	prepared, err := PrepareExec(PrepareExecOptions{Store: store, Resume: "   ", SessionID: "new_session"})
	if err != nil {
		t.Fatalf("PrepareExec returned error: %v", err)
	}
	if prepared.Mode != ModeNew || prepared.Session.SessionID != "new_session" {
		t.Fatalf("expected whitespace resume to create a new session, got %#v", prepared)
	}
}

func fixedClock(value string) func() time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return func() time.Time { return parsed }
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
