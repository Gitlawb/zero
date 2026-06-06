package zeroruntime

import (
	"context"
	"fmt"
)

// CollectedStream is the non-streaming summary of provider events.
type CollectedStream struct {
	Text             string
	ToolCalls        []ToolCall
	Usage            Usage
	Error            string
	DroppedToolCalls int // malformed tool calls the provider could not dispatch
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
	collector := newToolCallCollector()

	for {
		select {
		case <-ctx.Done():
			collected.Error = ctx.Err().Error()
			collector.flush(&collected)
			return collected
		case event, ok := <-events:
			if !ok {
				collector.flush(&collected)
				return collected
			}

			switch event.Type {
			case StreamEventText:
				collected.Text += event.Content
				if options.OnText != nil {
					options.OnText(event.Content)
				}
			case StreamEventToolCallStart:
				collector.start(event.ToolCallID, event.ToolName)
			case StreamEventToolCallDelta:
				collector.delta(event.ToolCallID, event.ArgumentsFragment)
			case StreamEventToolCallEnd:
				collector.end(event.ToolCallID)
			case StreamEventToolCallDropped:
				collected.DroppedToolCalls++
			case StreamEventUsage:
				inputTokens := event.Usage.EffectiveInputTokens()
				outputTokens := event.Usage.EffectiveOutputTokens()
				collected.Usage.InputTokens += inputTokens
				collected.Usage.OutputTokens += outputTokens
				collected.Usage.PromptTokens += inputTokens
				collected.Usage.CompletionTokens += outputTokens
				collected.Usage.CachedInputTokens += event.Usage.CachedInputTokens
				collected.Usage.ReasoningTokens += event.Usage.ReasoningTokens
				if options.OnUsage != nil {
					options.OnUsage(event.Usage)
				}
			case StreamEventError:
				collected.Error = event.Error
				collector.flush(&collected)
				return collected
			case StreamEventDone:
				collector.flush(&collected)
				return collected
			}
		}
	}
}

// toolCallCollector accumulates streamed tool calls in start order. Calls are
// keyed by an internal key (the ToolCallID when non-empty, or a synthetic
// per-stream key for empty IDs) so distinct simultaneous calls that share an
// empty/duplicate ID never merge. Completed calls are NOT emitted at end time;
// flush emits every collected call in one ordered pass so output always follows
// model/start order regardless of the order calls finished.
type toolCallCollector struct {
	calls       map[string]*ToolCall
	order       []string
	openEmptyID []string // stack of synthetic keys for in-flight empty-id calls
	synthetic   int
}

func newToolCallCollector() *toolCallCollector {
	return &toolCallCollector{calls: make(map[string]*ToolCall)}
}

// start begins a tool call. A non-empty ID reuses any open call with that ID
// (some backends re-emit the same start); an empty ID always begins a fresh
// synthetic call so concurrent empty-id calls stay distinct.
func (collector *toolCallCollector) start(id string, name string) {
	key := id
	if id == "" {
		collector.synthetic++
		key = fmt.Sprintf("\x00synthetic-%d", collector.synthetic)
		collector.openEmptyID = append(collector.openEmptyID, key)
	}
	call := collector.ensure(key, id)
	// Only set the name when non-empty and still unset, so a duplicate or
	// nameless follow-up start cannot clobber an already-resolved name.
	if name != "" && call.Name == "" {
		call.Name = name
	}
}

func (collector *toolCallCollector) delta(id string, fragment string) {
	key, ok := collector.resolveKey(id)
	if !ok {
		key = id
		collector.ensure(key, id)
	}
	collector.calls[key].Arguments += fragment
}

// end closes an in-flight call. It does not emit anything; flush does, in start
// order. For empty IDs it pops the in-flight empty-id call off the stack so a
// following empty-id delta/end can't attach to an already-closed call.
func (collector *toolCallCollector) end(id string) {
	if id == "" {
		if len(collector.openEmptyID) > 0 {
			collector.openEmptyID = collector.openEmptyID[:len(collector.openEmptyID)-1]
		}
	}
}

// resolveKey maps an event ID to its internal key. Empty IDs route to the most
// recently started, not-yet-ended empty-id call.
func (collector *toolCallCollector) resolveKey(id string) (string, bool) {
	if id == "" {
		if len(collector.openEmptyID) == 0 {
			return "", false
		}
		return collector.openEmptyID[len(collector.openEmptyID)-1], true
	}
	if _, ok := collector.calls[id]; ok {
		return id, true
	}
	return "", false
}

func (collector *toolCallCollector) ensure(key string, id string) *ToolCall {
	if call, ok := collector.calls[key]; ok {
		return call
	}
	call := &ToolCall{ID: id}
	collector.calls[key] = call
	collector.order = append(collector.order, key)
	return call
}

// flush emits every collected call once, in start order. Malformed (nameless)
// calls are dropped so the agent never dispatches an empty tool name.
func (collector *toolCallCollector) flush(collected *CollectedStream) {
	for _, key := range collector.order {
		call, ok := collector.calls[key]
		if !ok {
			continue
		}
		delete(collector.calls, key)
		if call.Name != "" {
			collected.ToolCalls = append(collected.ToolCalls, *call)
		} else {
			collected.DroppedToolCalls++
		}
	}
	collector.order = collector.order[:0]
	collector.openEmptyID = collector.openEmptyID[:0]
}
