package trace

import (
	"bytes"
	"strings"
	"testing"
)

func TestTaskStateTraceRoundTripContainsNoTaskContent(t *testing.T) {
	recorder := NewRecorder("session", "run", "")
	recorder.Start()
	event := TaskStateEvent{
		Revision:            4,
		Status:              "active",
		PlanPending:         2,
		PlanCompleted:       1,
		ToolsSucceeded:      3,
		ToolsFailed:         1,
		VerificationPassed:  1,
		VerificationOutcome: "passed",
		ChangedFileCount:    2,
		PlanParity:          "match",
	}
	recorder.EmitTaskState(event)

	var output bytes.Buffer
	if err := WriteNDJSON(&output, recorder.Finish()); err != nil {
		t.Fatalf("WriteNDJSON: %v", err)
	}
	if strings.Contains(output.String(), "secret objective") || strings.Contains(output.String(), "private.go") {
		t.Fatalf("task content leaked into trace: %s", output.String())
	}
	if strings.Contains(output.String(), "objective_hash") {
		t.Fatalf("trace contains a confirmable objective fingerprint: %s", output.String())
	}
	parsed, err := ReadNDJSON(strings.NewReader(output.String()))
	if err != nil {
		t.Fatalf("ReadNDJSON: %v", err)
	}
	if len(parsed.TaskStates) != 1 || parsed.TaskStates[0] != event {
		t.Fatalf("unexpected round trip: %#v", parsed.TaskStates)
	}
}

func TestTaskStateIsDocumentedAsOptionalTraceEvent(t *testing.T) {
	for _, key := range OptionalEventKeys() {
		if key == "task_state" {
			return
		}
	}
	t.Fatal("task_state missing from OptionalEventKeys")
}

func TestTaskStateTraceIsBoundedAndRetainsLatestSnapshot(t *testing.T) {
	recorder := NewRecorder("session", "run", "")
	for revision := 1; revision <= maxTaskStateEvents+20; revision++ {
		recorder.EmitTaskState(TaskStateEvent{Revision: revision})
	}
	events := recorder.Finish().TaskStates
	if len(events) != maxTaskStateEvents {
		t.Fatalf("task state events = %d, want cap %d", len(events), maxTaskStateEvents)
	}
	if got := events[len(events)-1].Revision; got != maxTaskStateEvents+20 {
		t.Fatalf("latest revision = %d, want %d", got, maxTaskStateEvents+20)
	}
}
