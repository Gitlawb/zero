package tui

import (
	"encoding/json"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gitlawb/zero/internal/sessions"
	"github.com/Gitlawb/zero/internal/specialist"
)

func durableBridge(t *testing.T) (*PlanProgressBridge, *sessions.Store, string) {
	t.Helper()
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir()})
	session, err := store.Create(sessions.CreateInput{Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	bridge := NewPlanProgressBridge()
	bridge.Attach(func(tea.Msg) {}, 1, store, session.SessionID)
	return bridge, store, session.SessionID
}

func recordedTypes(t *testing.T, store *sessions.Store, sessionID string) []sessions.EventType {
	t.Helper()
	events, err := store.ReadEvents(sessionID)
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	var types []sessions.EventType
	for _, event := range events {
		types = append(types, event.Type)
	}
	return types
}

func samplePlan(t *testing.T) specialist.Plan {
	t.Helper()
	plan, err := specialist.ParsePlan(map[string]any{
		"name": "durable",
		"tasks": []any{
			map[string]any{"id": "a", "prompt": "one"},
			map[string]any{"id": "b", "prompt": "two", "depends_on": []any{"a"}},
		},
		"budget": map[string]any{"max_workers": float64(1)},
	}, specialist.Limits{MaxTasks: 20, ParentTools: []string{"read_file"}})
	if err != nil {
		t.Fatalf("ParsePlan: %v", err)
	}
	return plan
}

// A PLAN RUN IN THE TUI MUST LEAVE A RECORD. It drove the panel and wrote
// nothing: the same plan under `zero exec` wrote five events per task, so a
// plan was durable or not depending on which surface you ran it from.
func TestATuiPlanIsRecordedDurably(t *testing.T) {
	bridge, store, sessionID := durableBridge(t)
	plan := samplePlan(t)

	bridge.PlanAdmitted(plan)
	bridge.TaskDispatched(specialist.Task{ID: "a"})
	bridge.TaskCompleted(specialist.TaskResult{ID: "a", Outcome: specialist.TaskSucceeded, SessionID: "specialist_aaa", Tokens: 150})
	bridge.TaskFailed(specialist.TaskResult{ID: "b", Outcome: specialist.TaskFailed, Err: "boom"})
	bridge.PlanCompleted(plan, specialist.PlanReport{Status: specialist.PlanPartial, Succeeded: 1, Failed: 1})

	want := []sessions.EventType{
		sessions.EventPlanAdmitted, sessions.EventTaskDispatched,
		sessions.EventTaskCompleted, sessions.EventTaskFailed, sessions.EventPlanCompleted,
	}
	got := recordedTypes(t, store, sessionID)
	if len(got) != len(want) {
		t.Fatalf("recorded %v, want the five lifecycle events", got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("event %d = %q, want %q", index, got[index], want[index])
		}
	}
}

// Dispatch is written BEFORE the child runs, so a task that was in flight when
// the process died is distinguishable on resume from one that never started.
// That ordering IS the durability guarantee.
func TestDispatchIsRecordedBeforeTheTaskFinishes(t *testing.T) {
	bridge, store, sessionID := durableBridge(t)
	bridge.TaskDispatched(specialist.Task{ID: "a"})

	got := recordedTypes(t, store, sessionID)
	if len(got) != 1 || got[0] != sessions.EventTaskDispatched {
		t.Fatalf("recorded %v, want a dispatch already on disk before the task ends", got)
	}
}

// THE PARITY OBLIGATION. Resume is a deterministic reduction over these events,
// and a reducer cannot be written against two shapes. The two recorders must
// emit byte-identical payloads for the same plan.
//
// Compared by round-tripping both through JSON — the form they are actually
// stored in — rather than by eyeballing two builders.
func TestBothRecordersEmitTheSamePayloads(t *testing.T) {
	plan := samplePlan(t)
	result := specialist.TaskResult{
		ID: "a", Outcome: specialist.TaskSucceeded,
		Duration: 1500 * time.Millisecond, SessionID: "specialist_aaa", Tokens: 150,
	}
	failed := specialist.TaskResult{
		ID: "b", Outcome: specialist.TaskSkippedDependency,
		Err: "skipped", Duration: time.Second,
	}
	report := specialist.PlanReport{Status: specialist.PlanPartial, Succeeded: 1, Skipped: 1, TokensUsed: 150}

	// The TUI's recorder, straight to a store.
	tuiBridge, store, sessionID := durableBridge(t)
	tuiBridge.PlanAdmitted(plan)
	tuiBridge.TaskDispatched(specialist.Task{ID: "a", DependsOn: nil})
	tuiBridge.TaskCompleted(result)
	tuiBridge.TaskFailed(failed)
	tuiBridge.PlanCompleted(plan, report)

	events, err := store.ReadEvents(sessionID)
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	if len(events) != 5 {
		t.Fatalf("expected five events, got %d", len(events))
	}

	// The shared builders are what the headless recorder appends, so comparing
	// against them compares the two surfaces.
	builders := []func() (sessions.EventType, map[string]any){
		func() (sessions.EventType, map[string]any) { return specialist.PlanAdmittedEvent(plan) },
		func() (sessions.EventType, map[string]any) {
			return specialist.TaskDispatchedEvent(specialist.Task{ID: "a"})
		},
		func() (sessions.EventType, map[string]any) { return specialist.TaskCompletedEvent(result) },
		func() (sessions.EventType, map[string]any) { return specialist.TaskFailedEvent(failed) },
		func() (sessions.EventType, map[string]any) { return specialist.PlanCompletedEvent(plan, report) },
	}
	for index, build := range builders {
		wantType, wantPayload := build()
		if events[index].Type != wantType {
			t.Fatalf("event %d type = %q, want %q", index, events[index].Type, wantType)
		}
		got, _ := json.Marshal(events[index].Payload)
		want, _ := json.Marshal(wantPayload)
		if string(got) != string(want) {
			t.Fatalf("event %d payload differs between the surfaces:\n TUI: %s\nexec: %s", index, got, want)
		}
	}
}

// Recording is BEST EFFORT: no store, no session, or a nil bridge and the plan
// still runs. Recording must never be the thing that fails a plan.
func TestRecordingFailuresNeverStopAPlan(t *testing.T) {
	plan := samplePlan(t)

	unattached := NewPlanProgressBridge()
	unattached.PlanAdmitted(plan)
	unattached.TaskDispatched(specialist.Task{ID: "a"})
	unattached.PlanCompleted(plan, specialist.PlanReport{})
	if err := unattached.RecordingError(); err != nil {
		t.Fatalf("an unattached bridge must not latch an error: %v", err)
	}

	sinkOnly := NewPlanProgressBridge()
	sinkOnly.Attach(func(tea.Msg) {}, 1, nil, "")
	sinkOnly.PlanAdmitted(plan)
	if err := sinkOnly.RecordingError(); err != nil {
		t.Fatalf("a bridge with no store must not latch an error: %v", err)
	}

	var nilBridge *PlanProgressBridge
	nilBridge.PlanAdmitted(plan)
	nilBridge.TaskCompleted(specialist.TaskResult{})
	if err := nilBridge.RecordingError(); err != nil {
		t.Fatalf("a nil bridge must be inert: %v", err)
	}
}

// A failed append is LATCHED and surfaced once, not silently dropped — a user
// must not believe a plan was persisted when it was not.
func TestAFailedAppendIsLatched(t *testing.T) {
	bridge := NewPlanProgressBridge()
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir()})
	// A session id that does not exist: appends fail.
	bridge.Attach(func(tea.Msg) {}, 1, store, "zero_00000000000000000000000000000000_1")

	bridge.PlanAdmitted(samplePlan(t))
	if bridge.RecordingError() == nil {
		t.Fatal("a failed append must be latched so it can be reported once")
	}
}

// THE WIRING. Everything above drives the bridge directly; this drives the
// model's own run-start path, which is the only thing that ever binds the
// bridge to a session. A bridge that records perfectly and is never bound
// records nothing — the shape this feature keeps producing.
func TestTheBridgeIsBoundToTheActiveSession(t *testing.T) {
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir()})
	session, err := store.Create(sessions.CreateInput{Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	m := model{now: func() time.Time { return time.Unix(1000, 0) }}
	m.planProgress = NewPlanProgressBridge()
	m.sessionStore = store
	m.activeSession = session
	m.runtimeMessageSink = func(tea.Msg) {}

	m = m.beginRun(func() {})

	// If beginRun bound the bridge, an event recorded now lands in the store.
	m.planProgress.PlanAdmitted(samplePlan(t))
	if err := m.planProgress.RecordingError(); err != nil {
		t.Fatalf("recording failed after beginRun: %v", err)
	}
	if got := recordedTypes(t, store, session.SessionID); len(got) != 1 || got[0] != sessions.EventPlanAdmitted {
		t.Fatalf("recorded %v; beginRun did not bind the bridge to the active session", got)
	}
}
