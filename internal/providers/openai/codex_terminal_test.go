package openai

import (
	"context"
	"testing"

	"github.com/Gitlawb/zero/internal/zeroruntime"
)

func TestToolCallKeyOutputIndexZero(t *testing.T) {
	p := &CodexProvider{}
	zero, two := 0, 2
	// output_index 0 with no item_id must produce a key (it was dropped before M1).
	if got := p.toolCallKey(&responsesEvent{OutputIndex: &zero}); got != "output-0" {
		t.Errorf("OutputIndex 0 → %q, want output-0", got)
	}
	if got := p.toolCallKey(&responsesEvent{OutputIndex: &two}); got != "output-2" {
		t.Errorf("OutputIndex 2 → %q, want output-2", got)
	}
	if got := p.toolCallKey(&responsesEvent{}); got != "" {
		t.Errorf("absent output_index + no item_id → %q, want empty", got)
	}
	if got := p.toolCallKey(&responsesEvent{ItemID: "call_x", OutputIndex: &zero}); got != "call_x" {
		t.Errorf("item_id should take precedence → %q", got)
	}
}

func TestHandleTerminalResponseNilPayload(t *testing.T) {
	p := &CodexProvider{}

	// response.failed with no Response payload must emit an error, not a silent done.
	failed := make(chan zeroruntime.StreamEvent, 4)
	st := &responsesState{}
	p.handleTerminalResponse(context.Background(), &responsesEvent{Type: responsesEventFailed}, st, failed)
	close(failed)
	sawError := false
	for ev := range failed {
		if ev.Type == zeroruntime.StreamEventError {
			sawError = true
		}
	}
	if !sawError {
		t.Error("response.failed with nil payload should emit StreamEventError (M2)")
	}
	if !st.done {
		t.Error("state.done should be set")
	}

	// response.completed with no payload is a clean (empty) done, not an error.
	completed := make(chan zeroruntime.StreamEvent, 4)
	p.handleTerminalResponse(context.Background(), &responsesEvent{Type: responsesEventCompleted}, &responsesState{}, completed)
	close(completed)
	for ev := range completed {
		if ev.Type == zeroruntime.StreamEventError {
			t.Error("response.completed with nil payload should NOT emit an error")
		}
	}
}
