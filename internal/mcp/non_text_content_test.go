package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/tools"
)

// A server that returns only an image currently reports "(empty MCP tool
// result)": TextContent keeps text blocks and drops the rest, so a successful
// call looks like it produced nothing. The model then usually retries, which is
// the worst outcome, and the user is never told an image existed (#823).
//
// Carrying the payload is a separate change. Naming what was dropped is what
// stops the retry loop, and it has to be true of the DELIVERED result, so this
// drives registryTool.Run rather than the helper alone.
func TestAnImageOnlyResultSaysWhatItReturned(t *testing.T) {
	tool := registryTool{
		client: &nonTextClient{content: []Content{
			{Type: "image", MimeType: "image/png"},
		}},
		server: Server{Name: "shots"},
		remote: RemoteTool{Name: "screenshot"},
	}

	result := tool.Run(context.Background(), map[string]any{})

	if strings.Contains(result.Output, "(empty MCP tool result)") {
		t.Fatalf("an image-only result still reports empty, so the model will retry:\n%s", result.Output)
	}
	if !strings.Contains(result.Output, "image/png") {
		t.Errorf("the output does not name what the server returned:\n%s", result.Output)
	}
	if result.Status != tools.StatusOK {
		t.Errorf("status = %v, want OK: the call succeeded, we just cannot forward the payload", result.Status)
	}
}

// The quieter half of the same bug: when a result carries text AND an image,
// the text arrives and the image vanishes with no mention at all.
func TestTextAlongsideAnImageStillReportsTheImage(t *testing.T) {
	tool := registryTool{
		client: &nonTextClient{content: []Content{
			{Type: "text", Text: "captured the page"},
			{Type: "image", MimeType: "image/png"},
		}},
		server: Server{Name: "shots"},
		remote: RemoteTool{Name: "screenshot"},
	}

	result := tool.Run(context.Background(), map[string]any{})

	if !strings.Contains(result.Output, "captured the page") {
		t.Errorf("the text block was lost:\n%s", result.Output)
	}
	if !strings.Contains(result.Output, "image/png") {
		t.Errorf("the dropped image was not mentioned:\n%s", result.Output)
	}
}

// A text-only result must be byte-for-byte what it was before: this change adds
// a line only when something was actually dropped.
func TestATextOnlyResultIsUnchanged(t *testing.T) {
	tool := registryTool{
		client: &nonTextClient{content: []Content{{Type: "text", Text: "plain answer"}}},
		server: Server{Name: "shots"},
		remote: RemoteTool{Name: "lookup"},
	}

	if got := tool.Run(context.Background(), map[string]any{}).Output; got != "plain answer" {
		t.Fatalf("output = %q, want exactly %q", got, "plain answer")
	}
}

func TestDroppedContentSummaryNamesTheBlocks(t *testing.T) {
	tests := []struct {
		name    string
		content []Content
		want    string
	}{
		{
			name:    "nothing dropped",
			content: []Content{{Type: "text", Text: "hi"}},
			want:    "",
		},
		{
			name:    "no content at all",
			content: nil,
			want:    "",
		},
		{
			name:    "one image with a mime type",
			content: []Content{{Type: "image", MimeType: "image/png"}},
			want:    "1 image/png block",
		},
		{
			// A server may omit mimeType. Fall back to the block type rather than
			// inventing one or printing an empty pair of slashes.
			name:    "one image without a mime type",
			content: []Content{{Type: "image"}},
			want:    "1 image block",
		},
		{
			name: "several of the same kind are counted, not repeated",
			content: []Content{
				{Type: "image", MimeType: "image/png"},
				{Type: "image", MimeType: "image/png"},
			},
			want: "2 image/png blocks",
		},
		{
			// Order follows first appearance so the message is stable to read and
			// to assert on.
			name: "mixed kinds",
			content: []Content{
				{Type: "text", Text: "ignored here"},
				{Type: "resource"},
				{Type: "audio", MimeType: "audio/wav"},
				{Type: "resource"},
			},
			want: "2 resource blocks, 1 audio/wav block",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := DroppedContentSummary(test.content); got != test.want {
				t.Fatalf("DroppedContentSummary() = %q, want %q", got, test.want)
			}
		})
	}
}

type nonTextClient struct {
	content []Content
}

func (client *nonTextClient) ListTools(context.Context) ([]RemoteTool, error) { return nil, nil }

func (client *nonTextClient) CallTool(context.Context, string, map[string]any) (CallToolResult, error) {
	return CallToolResult{Content: client.content}, nil
}

func (client *nonTextClient) Close() error { return nil }
