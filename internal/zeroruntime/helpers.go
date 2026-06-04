package zeroruntime

import "context"

// CollectedStream is the non-streaming summary of provider events.
type CollectedStream struct {
	Text      string
	ToolCalls []ToolCall
	Usage     Usage
	Error     string
}

// CollectOptions provides callbacks for consumers that need live stream updates.
type CollectOptions struct {
	OnText  func(string)
	OnUsage func(Usage)
}

// SeedMessages creates the initial system and user turns for a request.
func SeedMessages(systemPrompt string, userPrompt string) []Message {
	return []Message{
		{Role: MessageRoleSystem, Content: systemPrompt},
		{Role: MessageRoleUser, Content: userPrompt},
	}
}

// CollectStream drains provider events into text, tool calls, usage, and error state.
func CollectStream(ctx context.Context, events <-chan StreamEvent) CollectedStream {
	return CollectStreamWithOptions(ctx, events, CollectOptions{})
}

// CollectStreamWithOptions drains provider events and emits optional live callbacks.
func CollectStreamWithOptions(ctx context.Context, events <-chan StreamEvent, options CollectOptions) CollectedStream {
	collected := CollectedStream{}
	pendingToolCalls := make(map[string]*ToolCall)
	toolCallOrder := []string{}

	for {
		select {
		case <-ctx.Done():
			appendOpenToolCalls(&collected, toolCallOrder, pendingToolCalls)
			return collected
		case event, ok := <-events:
			if !ok {
				appendOpenToolCalls(&collected, toolCallOrder, pendingToolCalls)
				return collected
			}

			switch event.Type {
			case StreamEventText:
				collected.Text += event.Content
				if options.OnText != nil {
					options.OnText(event.Content)
				}
			case StreamEventToolCallStart:
				toolCall := ensurePendingToolCall(event.ToolCallID, pendingToolCalls, &toolCallOrder)
				toolCall.Name = event.ToolName
			case StreamEventToolCallDelta:
				toolCall := ensurePendingToolCall(event.ToolCallID, pendingToolCalls, &toolCallOrder)
				toolCall.Arguments += event.ArgumentsFragment
			case StreamEventToolCallEnd:
				if toolCall, ok := pendingToolCalls[event.ToolCallID]; ok {
					collected.ToolCalls = append(collected.ToolCalls, *toolCall)
					delete(pendingToolCalls, event.ToolCallID)
				}
			case StreamEventUsage:
				collected.Usage.PromptTokens += event.Usage.PromptTokens
				collected.Usage.CompletionTokens += event.Usage.CompletionTokens
				collected.Usage.CachedInputTokens += event.Usage.CachedInputTokens
				if options.OnUsage != nil {
					options.OnUsage(event.Usage)
				}
			case StreamEventError:
				collected.Error = event.Error
				appendOpenToolCalls(&collected, toolCallOrder, pendingToolCalls)
				return collected
			case StreamEventDone:
				appendOpenToolCalls(&collected, toolCallOrder, pendingToolCalls)
				return collected
			}
		}
	}
}

func ensurePendingToolCall(
	toolCallID string,
	pendingToolCalls map[string]*ToolCall,
	toolCallOrder *[]string,
) *ToolCall {
	toolCall, ok := pendingToolCalls[toolCallID]
	if ok {
		return toolCall
	}

	toolCall = &ToolCall{ID: toolCallID}
	pendingToolCalls[toolCallID] = toolCall
	*toolCallOrder = append(*toolCallOrder, toolCallID)
	return toolCall
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
