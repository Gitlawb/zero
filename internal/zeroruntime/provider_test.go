package zeroruntime

import (
	"context"
	"testing"
)

type mockProvider struct {
	events []StreamEvent
}

func (provider mockProvider) StreamCompletion(
	ctx context.Context,
	request CompletionRequest,
) (<-chan StreamEvent, error) {
	events := make(chan StreamEvent)
	go func() {
		defer close(events)
		for _, event := range provider.events {
			select {
			case <-ctx.Done():
				return
			case events <- event:
			}
		}
	}()
	return events, nil
}

func TestSeedMessagesProducesSystemAndUserTurns(t *testing.T) {
	messages := SeedMessages("you are a helper", "inspect this repo")

	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}
	if messages[0].Role != MessageRoleSystem || messages[0].Content != "you are a helper" {
		t.Fatalf("unexpected system message: %#v", messages[0])
	}
	if messages[1].Role != MessageRoleUser || messages[1].Content != "inspect this repo" {
		t.Fatalf("unexpected user message: %#v", messages[1])
	}
}

func TestStreamEventNamesMatchProviderContract(t *testing.T) {
	cases := map[StreamEventType]string{
		StreamEventText:          "text",
		StreamEventToolCallStart: "tool-call-start",
		StreamEventToolCallDelta: "tool-call-delta",
		StreamEventToolCallEnd:   "tool-call-end",
		StreamEventUsage:         "usage",
		StreamEventDone:          "done",
		StreamEventError:         "error",
	}

	for eventType, want := range cases {
		if string(eventType) != want {
			t.Fatalf("event type %s = %q, want %q", eventType, string(eventType), want)
		}
	}
}

func TestCollectStreamAccumulatesTextToolCallsAndUsage(t *testing.T) {
	events := make(chan StreamEvent)
	go func() {
		defer close(events)
		events <- StreamEvent{Type: StreamEventText, Content: "Hello "}
		events <- StreamEvent{Type: StreamEventText, Content: "world"}
		events <- StreamEvent{Type: StreamEventToolCallStart, ToolCallID: "call_1", ToolName: "read_file"}
		events <- StreamEvent{Type: StreamEventToolCallDelta, ToolCallID: "call_1", ArgumentsFragment: `{"pa`}
		events <- StreamEvent{Type: StreamEventToolCallDelta, ToolCallID: "call_1", ArgumentsFragment: `th":"README.md"}`}
		events <- StreamEvent{Type: StreamEventToolCallEnd, ToolCallID: "call_1"}
		events <- StreamEvent{Type: StreamEventUsage, Usage: Usage{PromptTokens: 12, CompletionTokens: 8, CachedInputTokens: 3}}
		events <- StreamEvent{Type: StreamEventDone}
	}()

	collected := CollectStream(context.Background(), events)

	if collected.Text != "Hello world" {
		t.Fatalf("text = %q, want %q", collected.Text, "Hello world")
	}
	if len(collected.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %d", len(collected.ToolCalls))
	}
	toolCall := collected.ToolCalls[0]
	if toolCall.ID != "call_1" || toolCall.Name != "read_file" || toolCall.Arguments != `{"path":"README.md"}` {
		t.Fatalf("unexpected tool call: %#v", toolCall)
	}
	if collected.Usage.PromptTokens != 12 || collected.Usage.CompletionTokens != 8 || collected.Usage.CachedInputTokens != 3 {
		t.Fatalf("unexpected usage: %#v", collected.Usage)
	}
	if collected.Usage.TotalTokens() != 20 {
		t.Fatalf("total tokens = %d, want 20", collected.Usage.TotalTokens())
	}
}

func TestCollectStreamWithOptionsEmitsTextAndUsageCallbacks(t *testing.T) {
	events := make(chan StreamEvent)
	go func() {
		defer close(events)
		events <- StreamEvent{Type: StreamEventText, Content: "Hello "}
		events <- StreamEvent{Type: StreamEventUsage, Usage: Usage{PromptTokens: 12, CompletionTokens: 5, CachedInputTokens: 2}}
		events <- StreamEvent{Type: StreamEventText, Content: "zero"}
		events <- StreamEvent{Type: StreamEventDone}
	}()

	var textDeltas []string
	var usageEvents []Usage
	collected := CollectStreamWithOptions(context.Background(), events, CollectOptions{
		OnText:  func(delta string) { textDeltas = append(textDeltas, delta) },
		OnUsage: func(usage Usage) { usageEvents = append(usageEvents, usage) },
	})

	if collected.Text != "Hello zero" {
		t.Fatalf("text = %q, want Hello zero", collected.Text)
	}
	if len(textDeltas) != 2 || textDeltas[0] != "Hello " || textDeltas[1] != "zero" {
		t.Fatalf("unexpected text callbacks: %#v", textDeltas)
	}
	if len(usageEvents) != 1 {
		t.Fatalf("expected one usage callback, got %#v", usageEvents)
	}
	if usageEvents[0].PromptTokens != 12 || usageEvents[0].CompletionTokens != 5 || usageEvents[0].CachedInputTokens != 2 {
		t.Fatalf("unexpected usage callback: %#v", usageEvents[0])
	}
}

func TestCollectStreamKeepsArgumentDeltasBeforeToolCallStart(t *testing.T) {
	events := make(chan StreamEvent)
	go func() {
		defer close(events)
		events <- StreamEvent{Type: StreamEventToolCallDelta, ToolCallID: "call_buffered", ArgumentsFragment: `{"path":`}
		events <- StreamEvent{Type: StreamEventToolCallStart, ToolCallID: "call_buffered", ToolName: "read_file"}
		events <- StreamEvent{Type: StreamEventToolCallDelta, ToolCallID: "call_buffered", ArgumentsFragment: `"README.md"}`}
		events <- StreamEvent{Type: StreamEventToolCallEnd, ToolCallID: "call_buffered"}
		events <- StreamEvent{Type: StreamEventDone}
	}()

	collected := CollectStream(context.Background(), events)

	if len(collected.ToolCalls) != 1 {
		t.Fatalf("expected one buffered tool call, got %d", len(collected.ToolCalls))
	}
	toolCall := collected.ToolCalls[0]
	if toolCall.ID != "call_buffered" || toolCall.Name != "read_file" || toolCall.Arguments != `{"path":"README.md"}` {
		t.Fatalf("unexpected buffered tool call: %#v", toolCall)
	}
}

func TestCollectStreamFlushesOpenToolCallsWhenChannelCloses(t *testing.T) {
	events := make(chan StreamEvent)
	go func() {
		defer close(events)
		events <- StreamEvent{Type: StreamEventToolCallStart, ToolCallID: "call_closed", ToolName: "grep"}
		events <- StreamEvent{Type: StreamEventToolCallDelta, ToolCallID: "call_closed", ArgumentsFragment: `{"query":"`}
		events <- StreamEvent{Type: StreamEventToolCallDelta, ToolCallID: "call_closed", ArgumentsFragment: `zero"}`}
	}()

	collected := CollectStream(context.Background(), events)

	if len(collected.ToolCalls) != 1 {
		t.Fatalf("expected one flushed tool call, got %d", len(collected.ToolCalls))
	}
	toolCall := collected.ToolCalls[0]
	if toolCall.ID != "call_closed" || toolCall.Name != "grep" || toolCall.Arguments != `{"query":"zero"}` {
		t.Fatalf("unexpected flushed tool call: %#v", toolCall)
	}
}

func TestCollectStreamFlushesOpenToolCallsWhenContextCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan StreamEvent)

	go func() {
		events <- StreamEvent{Type: StreamEventToolCallStart, ToolCallID: "call_cancelled", ToolName: "read_file"}
		events <- StreamEvent{Type: StreamEventToolCallDelta, ToolCallID: "call_cancelled", ArgumentsFragment: `{"path":"README`}
		cancel()
	}()

	collected := CollectStream(ctx, events)

	if len(collected.ToolCalls) != 1 {
		t.Fatalf("expected one flushed tool call, got %d", len(collected.ToolCalls))
	}
	toolCall := collected.ToolCalls[0]
	if toolCall.ID != "call_cancelled" || toolCall.Name != "read_file" || toolCall.Arguments != `{"path":"README` {
		t.Fatalf("unexpected flushed tool call after cancel: %#v", toolCall)
	}
}

func TestCollectStreamSurfacesStreamErrors(t *testing.T) {
	events := make(chan StreamEvent)
	go func() {
		defer close(events)
		events <- StreamEvent{Type: StreamEventToolCallStart, ToolCallID: "call_error", ToolName: "bash"}
		events <- StreamEvent{Type: StreamEventToolCallDelta, ToolCallID: "call_error", ArgumentsFragment: `{"command":"go test`}
		events <- StreamEvent{Type: StreamEventError, Error: "provider stream failed"}
	}()

	collected := CollectStream(context.Background(), events)

	if collected.Error != "provider stream failed" {
		t.Fatalf("error = %q, want provider stream failed", collected.Error)
	}
	if len(collected.ToolCalls) != 1 {
		t.Fatalf("expected one flushed tool call, got %d", len(collected.ToolCalls))
	}
	toolCall := collected.ToolCalls[0]
	if toolCall.ID != "call_error" || toolCall.Name != "bash" || toolCall.Arguments != `{"command":"go test` {
		t.Fatalf("unexpected flushed tool call after error: %#v", toolCall)
	}
}

func TestProviderContractCanBeImplementedByMock(t *testing.T) {
	var provider Provider = mockProvider{
		events: []StreamEvent{
			{Type: StreamEventText, Content: "ok"},
			{Type: StreamEventDone},
		},
	}

	stream, err := provider.StreamCompletion(context.Background(), CompletionRequest{
		Messages: SeedMessages("system", "user"),
		Tools: []ToolDefinition{
			{
				Name:        "read_file",
				Description: "Read a file",
				Parameters:  map[string]any{"type": "object"},
			},
		},
	})
	if err != nil {
		t.Fatalf("StreamCompletion returned error: %v", err)
	}

	collected := CollectStream(context.Background(), stream)
	if collected.Text != "ok" {
		t.Fatalf("text = %q, want ok", collected.Text)
	}
}
