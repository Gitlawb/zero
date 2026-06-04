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
