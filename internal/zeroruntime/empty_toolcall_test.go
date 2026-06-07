package zeroruntime

import (
	"context"
	"testing"
)

// A malformed (nameless) tool call must never reach the agent — it would dispatch
// an empty tool name ("Unknown tool \"\""). Valid calls still pass through.
func TestCollectStreamDropsNamelessToolCalls(t *testing.T) {
	events := make(chan StreamEvent, 8)
	// valid call
	events <- StreamEvent{Type: StreamEventToolCallStart, ToolCallID: "a", ToolName: "read_file"}
	events <- StreamEvent{Type: StreamEventToolCallDelta, ToolCallID: "a", ArgumentsFragment: `{"path":"x"}`}
	events <- StreamEvent{Type: StreamEventToolCallEnd, ToolCallID: "a"}
	// malformed: a delta/end for an id that never got a (named) start
	events <- StreamEvent{Type: StreamEventToolCallDelta, ToolCallID: "b", ArgumentsFragment: `{"path":"y"}`}
	events <- StreamEvent{Type: StreamEventToolCallEnd, ToolCallID: "b"}
	events <- StreamEvent{Type: StreamEventDone}
	close(events)

	got := CollectStream(context.Background(), events)
	if len(got.ToolCalls) != 1 {
		t.Fatalf("expected 1 valid tool call, got %d: %+v", len(got.ToolCalls), got.ToolCalls)
	}
	if got.ToolCalls[0].Name != "read_file" {
		t.Errorf("kept call name = %q, want read_file", got.ToolCalls[0].Name)
	}
	for _, c := range got.ToolCalls {
		if c.Name == "" {
			t.Error("a nameless tool call leaked to the agent")
		}
	}
}

// An empty-id DELTA that arrives BEFORE its start event must not orphan its
// buffered arguments: a following empty-id start should adopt the in-flight
// call so the name and the early-buffered arguments end up on the same call.
func TestCollectStreamEmptyIDDeltaBeforeStartIsAdopted(t *testing.T) {
	events := make(chan StreamEvent, 8)
	// delta arrives first, with an empty id and no start yet
	events <- StreamEvent{Type: StreamEventToolCallDelta, ArgumentsFragment: `{"path":`}
	// start (empty id) arrives afterwards carrying the name
	events <- StreamEvent{Type: StreamEventToolCallStart, ToolName: "read_file"}
	events <- StreamEvent{Type: StreamEventToolCallDelta, ArgumentsFragment: `"x"}`}
	events <- StreamEvent{Type: StreamEventToolCallEnd}
	events <- StreamEvent{Type: StreamEventDone}
	close(events)

	got := CollectStream(context.Background(), events)
	if len(got.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d: %+v", len(got.ToolCalls), got.ToolCalls)
	}
	if got.ToolCalls[0].Name != "read_file" {
		t.Fatalf("call name = %q, want read_file", got.ToolCalls[0].Name)
	}
	if got.ToolCalls[0].Arguments != `{"path":"x"}` {
		t.Fatalf("arguments = %q, want full buffered args (early delta orphaned?)", got.ToolCalls[0].Arguments)
	}
	if got.DroppedToolCalls != 0 {
		t.Fatalf("DroppedToolCalls = %d, want 0 (early delta should not orphan into a nameless call)", got.DroppedToolCalls)
	}
}

// A provider-signalled dropped (nameless) tool call must be counted so the agent
// can tell the model to retry instead of silently treating it as a final answer.
func TestCollectStreamCountsDroppedToolCalls(t *testing.T) {
	events := make(chan StreamEvent, 4)
	events <- StreamEvent{Type: StreamEventText, Content: "I'll write the file."}
	events <- StreamEvent{Type: StreamEventToolCallDropped}
	events <- StreamEvent{Type: StreamEventDone}
	close(events)

	got := CollectStream(context.Background(), events)
	if len(got.ToolCalls) != 0 {
		t.Fatalf("expected no usable tool calls, got %d", len(got.ToolCalls))
	}
	if got.DroppedToolCalls != 1 {
		t.Fatalf("expected DroppedToolCalls=1, got %d", got.DroppedToolCalls)
	}
}
