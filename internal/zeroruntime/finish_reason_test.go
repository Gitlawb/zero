package zeroruntime

import (
	"context"
	"testing"
)

// A terminal finish reason carried on the done event must be recorded so a
// truncated (length-capped) response is not mistaken for a normal completion.
func TestCollectStreamRecordsFinishReason(t *testing.T) {
	events := make(chan StreamEvent, 4)
	events <- StreamEvent{Type: StreamEventText, Content: "partial answer that got cut"}
	events <- StreamEvent{Type: StreamEventDone, FinishReason: FinishReasonLength}
	close(events)

	got := CollectStream(context.Background(), events)
	if got.FinishReason != FinishReasonLength {
		t.Fatalf("FinishReason = %q, want %q", got.FinishReason, FinishReasonLength)
	}
}

// A finish reason may also arrive on an earlier event (e.g. a usage event) and
// must still be captured; a normal completion leaves it empty.
func TestCollectStreamFinishReasonEmptyOnNormalCompletion(t *testing.T) {
	events := make(chan StreamEvent, 4)
	events <- StreamEvent{Type: StreamEventText, Content: "complete answer"}
	events <- StreamEvent{Type: StreamEventDone}
	close(events)

	got := CollectStream(context.Background(), events)
	if got.FinishReason != "" {
		t.Fatalf("FinishReason = %q, want empty for a normal completion", got.FinishReason)
	}
}
