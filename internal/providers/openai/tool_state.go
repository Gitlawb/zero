package openai

import (
	"context"
	"sort"

	"github.com/Gitlawb/zero/internal/zeroruntime"
)

type toolState struct {
	calls map[int]*pendingToolCall
	// finishReason holds the normalized terminal stop reason (zeroruntime
	// FinishReason*) when the response ended abnormally (length/content_filter),
	// so the provider can attach it to the done event. Empty for a normal finish.
	finishReason string
	// done is set once a terminal event (error) has been emitted so the post-scan
	// path does not emit a second done after the stream already ended.
	done bool
}

type pendingToolCall struct {
	id        string
	name      string
	arguments string
	started   bool
	ended     bool
}

func newToolState() *toolState {
	return &toolState{calls: make(map[int]*pendingToolCall)}
}

func (state *toolState) applyDelta(
	ctx context.Context,
	delta streamToolCallDelta,
	events chan<- zeroruntime.StreamEvent,
) {
	call := state.calls[delta.Index]
	if call == nil {
		call = &pendingToolCall{}
		state.calls[delta.Index] = call
	}

	// Set id and name once. Some OpenAI-compatible backends (e.g. minimax via
	// Ollama) occasionally stream a second tool_calls entry at the same index;
	// overwriting id/name there corrupts the in-flight call and leaks a phantom
	// nameless call into the collector ("Unknown tool \"\""). Keep the first.
	if delta.ID != "" && call.id == "" {
		call.id = delta.ID
	}
	if delta.Function.Name != "" && call.name == "" {
		call.name = delta.Function.Name
	}
	if delta.Function.Arguments != "" {
		call.arguments += delta.Function.Arguments
	}

	if call.id == "" || call.name == "" || call.ended {
		return
	}

	if !call.started {
		call.started = true
		sendEvent(ctx, events, zeroruntime.StreamEvent{
			Type:       zeroruntime.StreamEventToolCallStart,
			ToolCallID: call.id,
			ToolName:   call.name,
		})
	}
	if call.arguments != "" {
		sendEvent(ctx, events, zeroruntime.StreamEvent{
			Type:              zeroruntime.StreamEventToolCallDelta,
			ToolCallID:        call.id,
			ArgumentsFragment: call.arguments,
		})
		call.arguments = ""
	}
}

func (state *toolState) closeOpen(ctx context.Context, events chan<- zeroruntime.StreamEvent) {
	indexes := make([]int, 0, len(state.calls))
	for index := range state.calls {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)

	for _, index := range indexes {
		call := state.calls[index]
		if call == nil || call.ended {
			continue
		}
		// A call that lacks a usable name/id can't be dispatched. If the model
		// nonetheless attempted one (it streamed an id or arguments), signal a
		// drop once so the agent can ask it to retry instead of silently ending.
		if call.id == "" || call.name == "" {
			if call.id != "" || call.name != "" || call.arguments != "" {
				call.ended = true
				sendEvent(ctx, events, zeroruntime.StreamEvent{Type: zeroruntime.StreamEventToolCallDropped})
			}
			continue
		}
		if !call.started {
			call.started = true
			sendEvent(ctx, events, zeroruntime.StreamEvent{
				Type:       zeroruntime.StreamEventToolCallStart,
				ToolCallID: call.id,
				ToolName:   call.name,
			})
		}
		if call.arguments != "" {
			sendEvent(ctx, events, zeroruntime.StreamEvent{
				Type:              zeroruntime.StreamEventToolCallDelta,
				ToolCallID:        call.id,
				ArgumentsFragment: call.arguments,
			})
			call.arguments = ""
		}
		call.ended = true
		sendEvent(ctx, events, zeroruntime.StreamEvent{
			Type:       zeroruntime.StreamEventToolCallEnd,
			ToolCallID: call.id,
		})
	}
}
