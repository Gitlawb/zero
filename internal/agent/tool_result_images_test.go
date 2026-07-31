package agent

import (
	"context"
	"testing"

	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// imageTool returns a tool result carrying an image, the way a screenshot tool
// should be able to.
type imageTool struct {
	media string
	data  []byte
}

func (imageTool) Name() string        { return "capture" }
func (imageTool) Description() string { return "Captures an image." }
func (imageTool) Parameters() tools.Schema {
	return tools.Schema{Type: "object", Properties: map[string]tools.PropertySchema{}}
}
func (imageTool) Safety() tools.Safety {
	return tools.Safety{Permission: tools.PermissionAllow}
}
func (t imageTool) Run(context.Context, map[string]any) tools.Result {
	return tools.Result{
		Status: tools.StatusOK,
		Output: "Captured a screenshot.",
		Images: []zeroruntime.ImageBlock{{MediaType: t.media, Data: t.data}},
	}
}

// A tool that produces an image must get that image in front of the model.
//
// It cannot ride the tool-result message: every provider drops images there.
// Anthropic maps a tool result to a tool_result block with string content,
// Gemini to a functionResponse, and OpenAI guards its image parts to the user
// role explicitly. So the image has to arrive as a following user message,
// which is also the only shape that keeps one tool result per tool call.
func TestRunDeliversToolResultImagesToTheModel(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(imageTool{media: "image/png", data: []byte("\x89PNG\r\n\x1a\nfake")})

	provider := &mockProvider{
		turns: [][]zeroruntime.StreamEvent{
			{
				{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "call_1", ToolName: "capture"},
				{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "call_1", ArgumentsFragment: `{}`},
				{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "call_1"},
				{Type: zeroruntime.StreamEventDone},
			},
			{
				{Type: zeroruntime.StreamEventText, Content: "I can see it."},
				{Type: zeroruntime.StreamEventDone},
			},
		},
	}

	result, err := Run(context.Background(), "screenshot please", provider, Options{
		Registry: registry,
		MaxTurns: 2,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var carrier *zeroruntime.Message
	for index := range result.Messages {
		if len(result.Messages[index].Images) > 0 {
			carrier = &result.Messages[index]
			break
		}
	}
	if carrier == nil {
		t.Fatal("no message carried the tool's image; the model never saw it")
	}
	if carrier.Role != zeroruntime.MessageRoleUser {
		t.Errorf("image rode a %q message; every provider only serializes images on the user role", carrier.Role)
	}
	if got := carrier.Images[0].MediaType; got != "image/png" {
		t.Errorf("media type = %q, want image/png", got)
	}

	// The tool-result pairing must be untouched: one tool result per tool call.
	toolResults := 0
	for _, message := range result.Messages {
		if message.Role == zeroruntime.MessageRoleTool {
			toolResults++
		}
	}
	if toolResults != 1 {
		t.Errorf("got %d tool-result messages for 1 tool call", toolResults)
	}
}

// textTool is a second, image-free tool so a turn can carry two calls.
type textTool struct{}

func (textTool) Name() string        { return "note" }
func (textTool) Description() string { return "Notes something." }
func (textTool) Parameters() tools.Schema {
	return tools.Schema{Type: "object", Properties: map[string]tools.PropertySchema{}}
}
func (textTool) Safety() tools.Safety { return tools.Safety{Permission: tools.PermissionAllow} }
func (textTool) Run(context.Context, map[string]any) tools.Result {
	return tools.Result{Status: tools.StatusOK, Output: "noted"}
}

// Tool results for one assistant turn must stay contiguous. The image messages
// are user-role, and a user message between two tool_results breaks strict
// provider replay: Anthropic coalesces consecutive user content into one block
// list and requires tool_result blocks first, so interleaving produces
// [tool_result, text, image, tool_result] and the request is rejected.
//
// The first version of this feature appended each image immediately after its
// own tool result, which is exactly that shape. A single-tool-call test passed
// and said nothing about it.
func TestRunKeepsToolResultsContiguousWhenAToolReturnsAnImage(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(imageTool{media: "image/png", data: []byte("\x89PNG\r\n\x1a\nfake")})
	registry.Register(textTool{})

	provider := &mockProvider{turns: [][]zeroruntime.StreamEvent{
		{
			{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "c1", ToolName: "capture"},
			{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "c1", ArgumentsFragment: `{}`},
			{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "c1"},
			{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "c2", ToolName: "note"},
			{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "c2", ArgumentsFragment: `{}`},
			{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "c2"},
			{Type: zeroruntime.StreamEventDone},
		},
		{{Type: zeroruntime.StreamEventText, Content: "done"}, {Type: zeroruntime.StreamEventDone}},
	}}

	result, err := Run(context.Background(), "two calls", provider, Options{Registry: registry, MaxTurns: 2})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	firstTool, lastTool, imageIndex := -1, -1, -1
	for index, message := range result.Messages {
		switch {
		case message.Role == zeroruntime.MessageRoleTool:
			if firstTool < 0 {
				firstTool = index
			}
			lastTool = index
		case len(message.Images) > 0:
			imageIndex = index
		}
	}
	if firstTool < 0 || lastTool == firstTool {
		t.Fatalf("expected two tool results, got messages %#v", result.Messages)
	}
	if imageIndex < 0 {
		t.Fatal("the image never reached the model")
	}
	if imageIndex > firstTool && imageIndex < lastTool {
		t.Errorf("image message at %d splits the tool results at %d and %d", imageIndex, firstTool, lastTool)
	}
	if imageIndex < lastTool {
		t.Errorf("image message at %d precedes the last tool result at %d", imageIndex, lastTool)
	}
}
