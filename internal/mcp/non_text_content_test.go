package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/imageinput"
	"github.com/Gitlawb/zero/internal/tools"
)

// tinyPNGBase64 is a 1x1 PNG. http.DetectContentType sniffs it as image/png.
const tinyPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

// paddedPNGBase64 is a decodable image/png of decoded length n. The 8-byte
// PNG magic is enough for http.DetectContentType; the rest is padding so
// tests can hit size caps without committing a multi-mebibyte fixture.
func paddedPNGBase64(n int) string {
	raw := make([]byte, n)
	copy(raw, "\x89PNG\r\n\x1a\n")
	return base64.StdEncoding.EncodeToString(raw)
}

// A server that returns an image WITHOUT payload still cannot be forwarded, so
// the delivered result must name the block rather than report "(empty MCP tool
// result)". The model then usually retries, which is the worst outcome (#823).
//
// Image blocks that DO carry data are forwarded on Result.Images (see the
// payload tests below). This case is the remaining drop path, and it has to be
// true of the DELIVERED result, so this drives registryTool.Run rather than
// the helper alone.
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
	// Naming the block is only half of it. Without the guidance the model still
	// retries, which is the expensive symptom, so the wording is pinned too.
	//
	// "cannot recover this payload" and not "will return the same thing": every
	// retry is a fresh call and the server may answer differently. What cannot
	// change is that Zero has nowhere to put a non-text block.
	if !strings.Contains(result.Output, "Retrying cannot recover this payload.") {
		t.Errorf("the output does not tell the model retrying is pointless:\n%s", result.Output)
	}
	if strings.Contains(result.Output, "will return the same thing") {
		t.Errorf("the output promises an identical response, which a fresh call cannot guarantee:\n%s", result.Output)
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
		{
			name:    "successfully forwarded image is not named as dropped",
			content: []Content{{Type: "image", MimeType: "image/png", Data: tinyPNGBase64}},
			want:    "",
		},
		{
			name:    "malformed image data is still named",
			content: []Content{{Type: "image", MimeType: "image/png", Data: "%%%not-base64%%%"}},
			want:    "1 image/png block",
		},
		{
			name: "forwarded image plus audio names only the audio",
			content: []Content{
				{Type: "image", MimeType: "image/png", Data: tinyPNGBase64},
				{Type: "audio", MimeType: "audio/wav"},
			},
			want: "1 audio/wav block",
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

func TestAnImageWithPayloadIsForwarded(t *testing.T) {
	tool := registryTool{
		client: &nonTextClient{content: []Content{
			{Type: "image", MimeType: "image/png", Data: tinyPNGBase64},
		}},
		server: Server{Name: "shots"},
		remote: RemoteTool{Name: "screenshot"},
	}

	result := tool.Run(context.Background(), map[string]any{})

	if result.Output != "[image forwarded]" {
		t.Fatalf("image-only Output = %q, want [image forwarded] so the tool_result is not an empty body", result.Output)
	}
	if strings.Contains(result.Output, "cannot forward") {
		t.Fatalf("a forwarded image is still described as unforwardable:\n%s", result.Output)
	}
	if len(result.Images) != 1 {
		t.Fatalf("Images len = %d, want 1", len(result.Images))
	}
	if result.Images[0].MediaType != "image/png" {
		t.Errorf("MediaType = %q, want image/png", result.Images[0].MediaType)
	}
	if len(result.Images[0].Data) == 0 {
		t.Fatal("forwarded image has empty Data")
	}
	if DroppedContentSummary([]Content{{Type: "image", MimeType: "image/png", Data: tinyPNGBase64}}) != "" {
		t.Fatal("drop summary named a successfully forwarded image")
	}
	if result.Status != tools.StatusOK {
		t.Errorf("status = %v, want OK", result.Status)
	}
}

func TestAudioIsStillDroppedAndNamed(t *testing.T) {
	tool := registryTool{
		client: &nonTextClient{content: []Content{
			{Type: "audio", MimeType: "audio/wav"},
		}},
		server: Server{Name: "shots"},
		remote: RemoteTool{Name: "clip"},
	}

	result := tool.Run(context.Background(), map[string]any{})

	if len(result.Images) != 0 {
		t.Fatalf("audio was forwarded as an image: %#v", result.Images)
	}
	if !strings.Contains(result.Output, "audio/wav") {
		t.Errorf("the output does not name the dropped audio:\n%s", result.Output)
	}
	if !strings.Contains(result.Output, "cannot forward yet") {
		t.Errorf("the output does not say the audio cannot be forwarded:\n%s", result.Output)
	}
}

func TestTextAndImageKeepsTextAndForwardsImage(t *testing.T) {
	tool := registryTool{
		client: &nonTextClient{content: []Content{
			{Type: "text", Text: "captured the page"},
			{Type: "image", MimeType: "image/png", Data: tinyPNGBase64},
		}},
		server: Server{Name: "shots"},
		remote: RemoteTool{Name: "screenshot"},
	}

	result := tool.Run(context.Background(), map[string]any{})

	if !strings.Contains(result.Output, "captured the page") {
		t.Errorf("the text block was lost:\n%s", result.Output)
	}
	if strings.Contains(result.Output, "cannot forward") {
		t.Errorf("a forwarded image is still described as unforwardable:\n%s", result.Output)
	}
	if strings.Contains(result.Output, "[image forwarded]") {
		t.Errorf("text+image result substituted a placeholder over the text:\n%s", result.Output)
	}
	if len(result.Images) != 1 {
		t.Fatalf("Images len = %d, want 1", len(result.Images))
	}
	if result.Images[0].MediaType != "image/png" {
		t.Errorf("MediaType = %q, want image/png", result.Images[0].MediaType)
	}
}

func TestMalformedImageDataDoesNotPanic(t *testing.T) {
	tool := registryTool{
		client: &nonTextClient{content: []Content{
			{Type: "image", MimeType: "image/png", Data: "%%%not-base64%%%"},
		}},
		server: Server{Name: "shots"},
		remote: RemoteTool{Name: "screenshot"},
	}

	result := tool.Run(context.Background(), map[string]any{})

	if len(result.Images) != 0 {
		t.Fatalf("malformed image was forwarded: %#v", result.Images)
	}
	if !strings.Contains(result.Output, "image/png") {
		t.Errorf("malformed image was not named in the drop summary:\n%s", result.Output)
	}
	if strings.Contains(result.Output, "(empty MCP tool result)") {
		t.Errorf("malformed image still reported as empty:\n%s", result.Output)
	}
}

func TestImageContentJSONDecodesDataAndStaysCompatibleWithoutIt(t *testing.T) {
	var withData CallToolResult
	raw := []byte(`{"content":[{"type":"image","mimeType":"image/png","data":"` + tinyPNGBase64 + `"}]}`)
	if err := json.Unmarshal(raw, &withData); err != nil {
		t.Fatalf("unmarshal image content: %v", err)
	}
	if len(withData.Content) != 1 {
		t.Fatalf("content len = %d, want 1", len(withData.Content))
	}
	if withData.Content[0].Type != "image" || withData.Content[0].MimeType != "image/png" {
		t.Fatalf("decoded fields = %+v", withData.Content[0])
	}
	if withData.Content[0].Data != tinyPNGBase64 {
		t.Fatalf("data = %q, want tiny PNG base64", withData.Content[0].Data)
	}

	var withoutData CallToolResult
	if err := json.Unmarshal([]byte(`{"content":[{"type":"image","mimeType":"image/png"}]}`), &withoutData); err != nil {
		t.Fatalf("unmarshal image content without data: %v", err)
	}
	if withoutData.Content[0].Data != "" {
		t.Fatalf("absent data decoded as %q, want empty", withoutData.Content[0].Data)
	}
}

func TestAnOversizedImageIsDroppedAndNamed(t *testing.T) {
	tool := registryTool{
		client: &nonTextClient{content: []Content{
			{Type: "image", MimeType: "image/png", Data: paddedPNGBase64(imageinput.MaxImageBytes + 1)},
		}},
		server: Server{Name: "shots"},
		remote: RemoteTool{Name: "screenshot"},
	}

	result := tool.Run(context.Background(), map[string]any{})

	if len(result.Images) != 0 {
		t.Fatalf("oversized image was forwarded: %#v", result.Images)
	}
	if !strings.Contains(result.Output, "image/png") {
		t.Errorf("oversized image was not named in the drop summary:\n%s", result.Output)
	}
	if !strings.Contains(result.Output, "cannot forward yet") {
		t.Errorf("individually oversized image should stay unforwardable, not a budget skip:\n%s", result.Output)
	}
	if strings.Contains(result.Output, "image budget") {
		t.Errorf("individually oversized image was described as a budget skip:\n%s", result.Output)
	}
}

func TestAggregateImageBudgetForwardsTheFirstAndNamesTheRest(t *testing.T) {
	// Each payload is under the per-image cap; together they exceed the
	// aggregate MaxImageBytes budget for one result. Identical bytes are
	// deliberate: DroppedContentSummary must name the second even though
	// imageBlockFromContent would accept it in isolation.
	payload := paddedPNGBase64(imageinput.MaxImageBytes/2 + 1)
	tool := registryTool{
		client: &nonTextClient{content: []Content{
			{Type: "image", MimeType: "image/png", Data: payload},
			{Type: "image", MimeType: "image/png", Data: payload},
		}},
		server: Server{Name: "shots"},
		remote: RemoteTool{Name: "screenshot"},
	}

	result := tool.Run(context.Background(), map[string]any{})

	if len(result.Images) != 1 {
		t.Fatalf("Images len = %d, want 1 (first fits the aggregate budget)", len(result.Images))
	}
	if got := len(result.Images[0].Data); got != imageinput.MaxImageBytes/2+1 {
		t.Errorf("forwarded image size = %d, want %d", got, imageinput.MaxImageBytes/2+1)
	}
	if !strings.Contains(result.Output, "[image forwarded]") {
		t.Errorf("the forwarded first image has no placeholder:\n%s", result.Output)
	}
	if !strings.Contains(result.Output, "image/png") {
		t.Errorf("the dropped second image was not named:\n%s", result.Output)
	}
	if !strings.Contains(result.Output, "which exceeded this result's image budget") {
		t.Errorf("the dropped second image is not described as a budget skip:\n%s", result.Output)
	}
	if !strings.Contains(result.Output, "Retrying with fewer images can recover this payload.") {
		t.Errorf("the output does not tell the model a retry can recover the payload:\n%s", result.Output)
	}
	if strings.Contains(result.Output, "cannot forward yet") {
		t.Errorf("a budget-skipped image is described as unforwardable:\n%s", result.Output)
	}
	if strings.Contains(result.Output, "Retrying cannot recover this payload.") {
		t.Errorf("a budget-skipped image is described as unrecoverable:\n%s", result.Output)
	}
}

func TestImagePayloadsAreDecodedOnceAndNotPastTheBudget(t *testing.T) {
	orig := decodeImageBase64
	t.Cleanup(func() { decodeImageBase64 = orig })
	var n int
	decodeImageBase64 = func(s string) ([]byte, error) {
		n++
		return orig(s)
	}

	// Two images fill the 10 MiB aggregate exactly, so remaining hits 0 and
	// the later candidates must not be decoded at all.
	half := imageinput.MaxImageBytes / 2
	payload := paddedPNGBase64(half)
	content := []Content{
		{Type: "image", MimeType: "image/png", Data: payload},
		{Type: "image", MimeType: "image/png", Data: payload},
		{Type: "image", MimeType: "image/png", Data: payload},
		{Type: "image", MimeType: "image/png", Data: payload},
		{Type: "audio", MimeType: "audio/wav"},
	}

	n = 0
	images := ImageBlocks(content)
	if n != 2 {
		t.Fatalf("ImageBlocks decoded %d payloads, want 2 (budget fills after two %d-byte images)", n, half)
	}
	if len(images) != 2 {
		t.Fatalf("ImageBlocks len = %d, want 2", len(images))
	}
	n = 0
	if got := DroppedContentSummary(content); got != "2 image/png blocks, 1 audio/wav block" {
		t.Fatalf("DroppedContentSummary() = %q, want the two skipped images and the audio", got)
	}
	if n != 2 {
		t.Fatalf("DroppedContentSummary decoded %d payloads, want 2 (same single pass as ImageBlocks)", n)
	}

	n = 0
	result := registryTool{
		client: &nonTextClient{content: content},
		server: Server{Name: "shots"},
		remote: RemoteTool{Name: "screenshot"},
	}.Run(context.Background(), map[string]any{})
	if n != 2 {
		t.Fatalf("Run decoded %d payloads, want 2 (one pass; drop note must not decode again)", n)
	}
	if len(result.Images) != 2 {
		t.Fatalf("Images len = %d, want 2", len(result.Images))
	}
	if !strings.Contains(result.Output, "[image forwarded]") {
		t.Errorf("forwarded images have no placeholder:\n%s", result.Output)
	}
	if !strings.Contains(result.Output, "image/png") {
		t.Errorf("skipped images were not named:\n%s", result.Output)
	}
	if !strings.Contains(result.Output, "which exceeded this result's image budget") {
		t.Errorf("budget-skipped images are not described as a budget drop:\n%s", result.Output)
	}
	if !strings.Contains(result.Output, "Retrying with fewer images can recover this payload.") {
		t.Errorf("budget-skipped images do not say a retry can recover them:\n%s", result.Output)
	}
	if !strings.Contains(result.Output, "audio/wav") {
		t.Errorf("audio was not named:\n%s", result.Output)
	}
	if !strings.Contains(result.Output, "cannot forward yet") {
		t.Errorf("audio is not described as unforwardable:\n%s", result.Output)
	}
	if strings.Contains(result.Output, "(empty MCP tool result)") {
		t.Errorf("forwarded images still reported as empty:\n%s", result.Output)
	}

	n = 0
	one := []Content{{Type: "image", MimeType: "image/png", Data: payload}}
	oneResult := registryTool{
		client: &nonTextClient{content: one},
		server: Server{Name: "shots"},
		remote: RemoteTool{Name: "screenshot"},
	}.Run(context.Background(), map[string]any{})
	if n != 1 {
		t.Fatalf("one image decoded %d times, want 1", n)
	}
	if len(oneResult.Images) != 1 {
		t.Fatalf("one-image Images len = %d, want 1", len(oneResult.Images))
	}
	if oneResult.Output != "[image forwarded]" {
		t.Fatalf("one-image Output = %q, want [image forwarded]", oneResult.Output)
	}
	if strings.Contains(oneResult.Output, "cannot forward") {
		t.Fatalf("a forwarded image is still described as unforwardable:\n%s", oneResult.Output)
	}
}

func BenchmarkForwardImagesFourHalfBudget(b *testing.B) {
	payload := paddedPNGBase64(imageinput.MaxImageBytes / 2)
	content := []Content{
		{Type: "image", MimeType: "image/png", Data: payload},
		{Type: "image", MimeType: "image/png", Data: payload},
		{Type: "image", MimeType: "image/png", Data: payload},
		{Type: "image", MimeType: "image/png", Data: payload},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = forwardImages(content)
	}
}
