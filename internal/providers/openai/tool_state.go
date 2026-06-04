package openai

import (
	"context"
	"sort"

	"github.com/Gitlawb/zero/internal/zeroruntime"
)

type toolState struct {
	calls map[int]*pendingToolCall
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

	if delta.ID != "" {
		call.id = delta.ID
	}
	if delta.Function.Name != "" {
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
		if call == nil || call.ended || call.id == "" {
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
