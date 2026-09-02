package agent

import (
	"context"
	"fmt"
	"strings"
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

	// Two separate ifs rather than a switch: a tool-role message that also
	// carried images would match only the first arm of a switch, so a regression
	// that put the image back on the tool result would read as "no image at all"
	// instead of naming what actually happened.
	type toolResult struct {
		index int
		id    string
	}
	var toolResults []toolResult
	imageIndex := -1
	for index, message := range result.Messages {
		if message.Role == zeroruntime.MessageRoleTool {
			toolResults = append(toolResults, toolResult{index: index, id: message.ToolCallID})
		}
		if len(message.Images) > 0 {
			if imageIndex >= 0 {
				t.Fatalf("image delivered twice, at %d and %d; recorded %s", imageIndex, index, messageShape(result.Messages))
			}
			imageIndex = index
		}
	}
	if imageIndex < 0 {
		t.Fatalf("the image never reached the model; recorded %s", messageShape(result.Messages))
	}
	if len(toolResults) != 2 {
		t.Fatalf("got %d tool-result messages for 2 tool calls; recorded %s", len(toolResults), messageShape(result.Messages))
	}
	if toolResults[0].id != "c1" || toolResults[1].id != "c2" {
		t.Errorf("tool results answered %q then %q, want c1 then c2", toolResults[0].id, toolResults[1].id)
	}
	// The contract Anthropic enforces: every tool_result block has to come before
	// any other block in the user message it lands in. Anything wedged between
	// the two results breaks that, which is the 400 this PR exists to avoid.
	if toolResults[1].index != toolResults[0].index+1 {
		t.Errorf("tool results at %d and %d are not adjacent; %q message at %d splits them; recorded %s",
			toolResults[0].index, toolResults[1].index, result.Messages[toolResults[0].index+1].Role,
			toolResults[0].index+1, messageShape(result.Messages))
	}
	// Follows the last tool result, deliberately NOT pinned to exactly last+1.
	// appendUserBlocks coalesces consecutive user-role content, so
	// [tool_result, tool_result, text, image] still maps to tool_result blocks
	// first and replays fine; pinning would redden that valid shape for nothing.
	if imageIndex <= toolResults[1].index {
		t.Errorf("image message at %d does not follow the last tool result at %d; recorded %s",
			imageIndex, toolResults[1].index, messageShape(result.Messages))
	}
}

// messageShape renders the conversation as a compact role list so a failure says
// what the ordering actually was instead of dumping whole messages.
func messageShape(messages []zeroruntime.Message) string {
	parts := make([]string, 0, len(messages))
	for index, message := range messages {
		part := fmt.Sprintf("%d:%s", index, message.Role)
		if message.ToolCallID != "" {
			part += "(" + message.ToolCallID + ")"
		}
		if len(message.Images) > 0 {
			part += "+image"
		}
		parts = append(parts, part)
	}
	return "[" + strings.Join(parts, " ") + "]"
}
