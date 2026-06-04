package zeroruntime

import "context"

type CollectedStream struct {
	Text      string
	ToolCalls []ToolCall
	Usage     Usage
}

func SeedMessages(systemPrompt string, userPrompt string) []Message {
	return []Message{
		{Role: MessageRoleSystem, Content: systemPrompt},
		{Role: MessageRoleUser, Content: userPrompt},
	}
}

func CollectStream(ctx context.Context, events <-chan StreamEvent) CollectedStream {
	collected := CollectedStream{}
	pendingToolCalls := make(map[string]*ToolCall)
	toolCallOrder := []string{}

	for {
		select {
		case <-ctx.Done():
			return collected
		case event, ok := <-events:
			if !ok {
				return collected
			}

			switch event.Type {
			case StreamEventText:
				collected.Text += event.Content
			case StreamEventToolCallStart:
				pendingToolCalls[event.ToolCallID] = &ToolCall{
					ID:   event.ToolCallID,
					Name: event.ToolName,
				}
				toolCallOrder = append(toolCallOrder, event.ToolCallID)
			case StreamEventToolCallDelta:
				if toolCall, ok := pendingToolCalls[event.ToolCallID]; ok {
					toolCall.Arguments += event.ArgumentsFragment
				}
			case StreamEventToolCallEnd:
				if toolCall, ok := pendingToolCalls[event.ToolCallID]; ok {
					collected.ToolCalls = append(collected.ToolCalls, *toolCall)
					delete(pendingToolCalls, event.ToolCallID)
				}
			case StreamEventUsage:
				collected.Usage.PromptTokens += event.Usage.PromptTokens
				collected.Usage.CompletionTokens += event.Usage.CompletionTokens
				collected.Usage.CachedInputTokens += event.Usage.CachedInputTokens
			case StreamEventDone:
				appendOpenToolCalls(&collected, toolCallOrder, pendingToolCalls)
				return collected
			}
		}
	}
}

func appendOpenToolCalls(
	collected *CollectedStream,
	toolCallOrder []string,
	pendingToolCalls map[string]*ToolCall,
) {
	for _, id := range toolCallOrder {
		toolCall, ok := pendingToolCalls[id]
		if !ok {
			continue
		}
		collected.ToolCalls = append(collected.ToolCalls, *toolCall)
		delete(pendingToolCalls, id)
	}
}
