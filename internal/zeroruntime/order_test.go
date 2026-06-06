package zeroruntime

import (
	"context"
	"testing"
)

// Collected tool calls must follow model/start order, even when an
// earlier-started call ends after a later-started one. Previously calls were
// appended at End-event time, so a call that ended first jumped ahead of a
// call that started first.
func TestCollectStreamPreservesStartOrderRegardlessOfEndOrder(t *testing.T) {
	events := make(chan StreamEvent, 16)
	// call "a" starts first, "b" starts second, but "b" ends before "a".
	events <- StreamEvent{Type: StreamEventToolCallStart, ToolCallID: "a", ToolName: "first"}
	events <- StreamEvent{Type: StreamEventToolCallStart, ToolCallID: "b", ToolName: "second"}
	events <- StreamEvent{Type: StreamEventToolCallDelta, ToolCallID: "b", ArgumentsFragment: `{"k":"b"}`}
	events <- StreamEvent{Type: StreamEventToolCallEnd, ToolCallID: "b"}
	events <- StreamEvent{Type: StreamEventToolCallDelta, ToolCallID: "a", ArgumentsFragment: `{"k":"a"}`}
	events <- StreamEvent{Type: StreamEventToolCallEnd, ToolCallID: "a"}
	events <- StreamEvent{Type: StreamEventDone}
	close(events)

	got := CollectStream(context.Background(), events)
	if len(got.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d: %+v", len(got.ToolCalls), got.ToolCalls)
	}
	if got.ToolCalls[0].Name != "first" || got.ToolCalls[1].Name != "second" {
		t.Fatalf("tool calls out of start order: %+v", got.ToolCalls)
	}
	if got.ToolCalls[0].Arguments != `{"k":"a"}` || got.ToolCalls[1].Arguments != `{"k":"b"}` {
		t.Fatalf("arguments mismatched to calls: %+v", got.ToolCalls)
	}
}

// Two distinct simultaneous calls that both arrive with an empty ToolCallID
// must NOT collapse into one. Each start begins a new call; its delta/end map
// to the in-flight (most recently started, not-yet-ended) empty-id call.
func TestCollectStreamDoesNotMergeDistinctEmptyIDCalls(t *testing.T) {
	events := make(chan StreamEvent, 16)
	events <- StreamEvent{Type: StreamEventToolCallStart, ToolName: "alpha"}
	events <- StreamEvent{Type: StreamEventToolCallDelta, ArgumentsFragment: `{"n":1}`}
	events <- StreamEvent{Type: StreamEventToolCallEnd}
	events <- StreamEvent{Type: StreamEventToolCallStart, ToolName: "beta"}
	events <- StreamEvent{Type: StreamEventToolCallDelta, ArgumentsFragment: `{"n":2}`}
	events <- StreamEvent{Type: StreamEventToolCallEnd}
	events <- StreamEvent{Type: StreamEventDone}
	close(events)

	got := CollectStream(context.Background(), events)
	if len(got.ToolCalls) != 2 {
		t.Fatalf("distinct empty-id calls collapsed: got %d: %+v", len(got.ToolCalls), got.ToolCalls)
	}
	if got.ToolCalls[0].Name != "alpha" || got.ToolCalls[0].Arguments != `{"n":1}` {
		t.Fatalf("first empty-id call wrong: %+v", got.ToolCalls[0])
	}
	if got.ToolCalls[1].Name != "beta" || got.ToolCalls[1].Arguments != `{"n":2}` {
		t.Fatalf("second empty-id call wrong: %+v", got.ToolCalls[1])
	}
}

// A duplicate non-empty start for an already-open call must not overwrite its
// name once set (some OpenAI-compatible backends re-emit the same index).
func TestCollectStreamKeepsFirstNameOnDuplicateStart(t *testing.T) {
	events := make(chan StreamEvent, 16)
	events <- StreamEvent{Type: StreamEventToolCallStart, ToolCallID: "a", ToolName: "read_file"}
	events <- StreamEvent{Type: StreamEventToolCallStart, ToolCallID: "a", ToolName: ""}
	events <- StreamEvent{Type: StreamEventToolCallDelta, ToolCallID: "a", ArgumentsFragment: `{"path":"x"}`}
	events <- StreamEvent{Type: StreamEventToolCallEnd, ToolCallID: "a"}
	events <- StreamEvent{Type: StreamEventDone}
	close(events)

	got := CollectStream(context.Background(), events)
	if len(got.ToolCalls) != 1 {
		t.Fatalf("expected 1 call, got %d: %+v", len(got.ToolCalls), got.ToolCalls)
	}
	if got.ToolCalls[0].Name != "read_file" {
		t.Fatalf("duplicate start overwrote name: %+v", got.ToolCalls[0])
	}
}
