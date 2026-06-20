package tui

import (
	"encoding/json"
	"testing"

	"github.com/Gitlawb/zero/internal/sessions"
)

func TestHydrationKeepsFailedTaskWithoutSpecialist(t *testing.T) {
	ev := func(typ sessions.EventType, payload string) sessions.Event {
		return sessions.Event{Type: typ, Payload: json.RawMessage(payload)}
	}

	// A Task that FAILED before a specialist started: tool call + error result, no
	// EventSpecialistStart. Its rows must survive resume (M10) — otherwise the
	// failed delegation silently vanishes.
	failed := transcriptRowsFromSessionEvents([]sessions.Event{
		ev(sessions.EventToolCall, `{"name":"Task","id":"call_fail","arguments":"{}"}`),
		ev(sessions.EventToolResult, `{"name":"Task","toolCallId":"call_fail","status":"error","output":"spawn failed"}`),
	})
	if !transcriptContains(failed, "tool call: Task") || !transcriptContains(failed, "tool result: Task") {
		t.Fatalf("a failed Task with no specialist must keep its rows on resume, got %#v", failed)
	}

	// A Task that DID start a specialist: the card renders it, so the raw Task
	// tool-call/result rows are skipped (no duplication).
	withSpecialist := transcriptRowsFromSessionEvents([]sessions.Event{
		ev(sessions.EventToolCall, `{"name":"Task","id":"call_ok","arguments":"{}"}`),
		ev(sessions.EventSpecialistStart, `{"childSessionId":"child-1","toolCallId":"call_ok","specialist":"explorer","status":"running"}`),
		ev(sessions.EventToolResult, `{"name":"Task","toolCallId":"call_ok","status":"ok","output":"done"}`),
	})
	if transcriptContains(withSpecialist, "tool call: Task") || transcriptContains(withSpecialist, "tool result: Task") {
		t.Fatalf("a Task with a specialist card must NOT also show raw Task rows, got %#v", withSpecialist)
	}
}
