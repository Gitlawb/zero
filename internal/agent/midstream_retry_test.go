package agent

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// midStreamAbortProvider connects successfully but emits a transport-abort
// StreamEventError on the first abortBefore calls, then succeeds with "done".
// Mirrors stallProvider shapes so mid-stream abort retries stay in parity with
// the idle/stall path (#973).
type midStreamAbortProvider struct {
	calls           int32
	abortBefore     int32
	abortError      string
	partialText     string
	partialToolCall string
}

func (p *midStreamAbortProvider) StreamCompletion(_ context.Context, _ zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	n := atomic.AddInt32(&p.calls, 1)
	ch := make(chan zeroruntime.StreamEvent, 5)
	if n <= p.abortBefore {
		if p.partialText != "" {
			ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventText, Content: p.partialText}
		}
		if p.partialToolCall != "" {
			ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "tc_1", ToolName: p.partialToolCall}
			ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "tc_1", ArgumentsFragment: `{"path":"x.html","content":"<!doctype`}
		}
		errMsg := p.abortError
		if errMsg == "" {
			errMsg = "provider stream error: read: connection reset by peer"
		}
		ch <- zeroruntime.StreamEvent{
			Type:  zeroruntime.StreamEventError,
			Error: errMsg,
		}
		close(ch)
		return ch, nil
	}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventText, Content: "done"}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventDone}
	close(ch)
	return ch, nil
}

func TestRunRetriesMidStreamConnectionReset(t *testing.T) {
	p := &midStreamAbortProvider{abortBefore: 1}
	result, err := Run(context.Background(), "go", p, Options{Registry: tools.NewRegistry()})
	if err != nil {
		t.Fatalf("a mid-stream connection reset should retry to success, got %v", err)
	}
	if result.FinalAnswer != "done" {
		t.Fatalf("final answer = %q, want %q", result.FinalAnswer, "done")
	}
	if got := atomic.LoadInt32(&p.calls); got != 2 {
		t.Fatalf("want 2 calls (1 abort + 1 retry), got %d", got)
	}
}

func TestRunRetriesMidStreamWindowsAbort(t *testing.T) {
	p := &midStreamAbortProvider{
		abortBefore: 1,
		abortError:  "provider stream error: read: wsarecv: An established connection was aborted by the software in your host machine",
	}
	result, err := Run(context.Background(), "go", p, Options{Registry: tools.NewRegistry()})
	if err != nil {
		t.Fatalf("a Windows mid-stream abort should retry to success, got %v", err)
	}
	if result.FinalAnswer != "done" {
		t.Fatalf("final answer = %q, want %q", result.FinalAnswer, "done")
	}
	if got := atomic.LoadInt32(&p.calls); got != 2 {
		t.Fatalf("want 2 calls (1 abort + 1 retry), got %d", got)
	}
}

func TestRunDoesNotRetryMidStreamAbortAfterPartialOutput(t *testing.T) {
	p := &midStreamAbortProvider{abortBefore: 1, partialText: "partial"}
	_, err := Run(context.Background(), "go", p, Options{Registry: tools.NewRegistry()})
	if err == nil {
		t.Fatal("a mid-stream abort after partial output must NOT be retried; want an error")
	}
	if got := atomic.LoadInt32(&p.calls); got != 1 {
		t.Fatalf("partial-then-abort must not retry, got %d calls", got)
	}
}

// Incomplete tool call then abort SHOULD retry (parity with stall path): the
// incomplete call is never executed or committed before the error return.
func TestRunRetriesMidStreamAbortAfterIncompleteToolCall(t *testing.T) {
	starts := 0
	p := &midStreamAbortProvider{abortBefore: 1, partialToolCall: "write_file"}
	result, err := Run(context.Background(), "go", p, Options{
		Registry:        tools.NewRegistry(),
		OnToolCallStart: func(string, string) { starts++ },
		OnToolCallDelta: func(string, string) {},
	})
	if err != nil {
		t.Fatalf("a mid-stream abort mid-incomplete-tool-call should retry to success, got %v", err)
	}
	if result.FinalAnswer != "done" {
		t.Fatalf("final answer = %q, want %q", result.FinalAnswer, "done")
	}
	if got := atomic.LoadInt32(&p.calls); got != 2 {
		t.Fatalf("want 2 calls (1 abort + 1 retry), got %d", got)
	}
	if starts != 1 {
		t.Fatalf("the incomplete tool call should have been forwarded once before the abort, got %d starts", starts)
	}
}

func TestIsMidStreamTransportAbort(t *testing.T) {
	aborts := []string{
		"provider stream error: read: connection reset by peer",
		"provider stream error: read: wsarecv: An established connection was aborted by the software in your host machine",
		"An existing connection was forcibly closed by the remote host",
	}
	for _, m := range aborts {
		if !isMidStreamTransportAbort(m) {
			t.Fatalf("want mid-stream transport abort: %q", m)
		}
	}
	notAborts := []string{"", "context length exceeded", "rate limit error: slow down", "model not found"}
	for _, m := range notAborts {
		if isMidStreamTransportAbort(m) {
			t.Fatalf("must NOT classify as mid-stream transport abort: %q", m)
		}
	}
}
