package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

func TestModelSupportsVisionTUI(t *testing.T) {
	cases := []struct {
		modelName string
		want      bool
	}{
		{modelName: "gpt-4.1", want: true},                 // vision model in the catalog
		{modelName: "claude-sonnet-4.5", want: true},       // vision model in the catalog
		{modelName: "claude-haiku-3.5", want: false},       // claude-3-5-haiku has no image input support
		{modelName: "totally-unknown-custom", want: false}, // not in catalog -> can't confirm
		{modelName: "", want: false},
	}
	for _, tc := range cases {
		got := modelSupportsVisionTUI(tc.modelName)
		if got != tc.want {
			t.Fatalf("modelSupportsVisionTUI(%q) = %v, want %v", tc.modelName, got, tc.want)
		}
	}
}

func writeTestPNG(t *testing.T, dir, name string) string {
	t.Helper()
	png := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4,
		0x89, 0x00, 0x00, 0x00, 0x0A, 0x49, 0x44, 0x41,
		0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
		0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00,
		0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE,
		0x42, 0x60, 0x82,
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, png, 0o644); err != nil {
		t.Fatalf("write png: %v", err)
	}
	return path
}

func lastTranscriptText(m model) string {
	if len(m.transcript) == 0 {
		return ""
	}
	return m.transcript[len(m.transcript)-1].text
}

func TestImageCommandAttachRendersChip(t *testing.T) {
	root := t.TempDir()
	writeTestPNG(t, root, "photo.png")

	m := newModel(context.Background(), Options{Cwd: root, ModelName: "gpt-4.1"})
	m.input.SetValue("/image photo.png")
	updated, _ := m.handleSubmit()
	next := updated.(model)

	if len(next.pendingImages) != 1 {
		t.Fatalf("expected 1 pending image, got %d", len(next.pendingImages))
	}
	if next.pendingImages[0].MediaType != "image/png" {
		t.Fatalf("MediaType = %q, want image/png", next.pendingImages[0].MediaType)
	}
	if len(next.pendingImageLabels) != 1 || next.pendingImageLabels[0] != "photo.png" {
		t.Fatalf("labels = %v, want [photo.png]", next.pendingImageLabels)
	}
	if chips := renderImageChips(next.pendingImageLabels); chips == "" {
		t.Fatal("expected a chip row for pending images")
	} else if !strings.Contains(chips, "photo.png") {
		t.Fatalf("chip row %q should name the image", chips)
	}
}

func TestImageCommandClear(t *testing.T) {
	root := t.TempDir()
	writeTestPNG(t, root, "photo.png")

	m := newModel(context.Background(), Options{Cwd: root, ModelName: "gpt-4.1"})
	m.input.SetValue("/image photo.png")
	updated, _ := m.handleSubmit()
	m = updated.(model)

	m.input.SetValue("/image clear")
	updated, _ = m.handleSubmit()
	next := updated.(model)

	if len(next.pendingImages) != 0 || len(next.pendingImageLabels) != 0 {
		t.Fatalf("expected cleared pending images, got %d/%d", len(next.pendingImages), len(next.pendingImageLabels))
	}
}

func TestImageCommandNonVisionRefuses(t *testing.T) {
	root := t.TempDir()
	writeTestPNG(t, root, "photo.png")

	// A custom/unknown model id is treated as non-vision (can't confirm).
	m := newModel(context.Background(), Options{Cwd: root, ModelName: "totally-unknown-custom"})
	m.input.SetValue("/image photo.png")
	updated, _ := m.handleSubmit()
	next := updated.(model)

	if len(next.pendingImages) != 0 {
		t.Fatalf("non-vision model must refuse: got %d pending images", len(next.pendingImages))
	}
	notice := lastTranscriptText(next)
	if !strings.Contains(notice, "does not support image input") {
		t.Fatalf("expected an inline refusal notice, got %q", notice)
	}
}

func TestImageCommandMissingFileNotice(t *testing.T) {
	root := t.TempDir()
	m := newModel(context.Background(), Options{Cwd: root, ModelName: "gpt-4.1"})
	m.input.SetValue("/image nope.png")
	updated, _ := m.handleSubmit()
	next := updated.(model)

	if len(next.pendingImages) != 0 {
		t.Fatal("a missing file must not attach")
	}
	if notice := lastTranscriptText(next); !strings.Contains(notice, "nope.png") {
		t.Fatalf("expected a notice naming the missing file, got %q", notice)
	}
}

func TestTranscriptViewShowsImageChips(t *testing.T) {
	m := newModel(context.Background(), Options{ModelName: "gpt-4.1"})
	m.width = 100
	m.height = 30
	m.pendingImageLabels = []string{"photo.png", "diagram.gif"}

	view := m.transcriptView()
	if !strings.Contains(view, "photo.png") || !strings.Contains(view, "diagram.gif") {
		t.Fatalf("transcript view should show pending image chips, got:\n%s", view)
	}
}

func TestSubmitThreadsImagesThenClears(t *testing.T) {
	root := t.TempDir()
	writeTestPNG(t, root, "photo.png")

	captured := make(chan []zeroruntime.ImageBlock, 1)
	provider := &fakeProvider{events: []zeroruntime.StreamEvent{
		{Type: zeroruntime.StreamEventText, Content: "ok"},
		{Type: zeroruntime.StreamEventDone},
	}}

	m := newModel(context.Background(), Options{
		Cwd:          root,
		ProviderName: "openai",
		ModelName:    "gpt-4.1",
		Provider:     provider,
		Registry:     tools.NewRegistry(),
		SessionStore: testSessionStore(t),
	})
	// Capture the Images the agent run is launched with.
	m.agentOptions.OnText = func(string) {}
	m.captureRunImages = func(imgs []zeroruntime.ImageBlock) { captured <- imgs }

	m.input.SetValue("/image photo.png")
	updated, _ := m.handleSubmit()
	m = updated.(model)
	if len(m.pendingImages) != 1 {
		t.Fatalf("setup: expected 1 staged image, got %d", len(m.pendingImages))
	}

	m.input.SetValue("describe this")
	updated, cmd := m.handleSubmit()
	next := updated.(model)
	if cmd == nil {
		t.Fatal("expected a prompt submit to start a run")
	}
	if len(next.pendingImages) != 0 || len(next.pendingImageLabels) != 0 {
		t.Fatalf("submit must clear pending images, got %d/%d", len(next.pendingImages), len(next.pendingImageLabels))
	}

	execCmd(cmd) // run the agent goroutine; it invokes captureRunImages

	select {
	case imgs := <-captured:
		if len(imgs) != 1 || imgs[0].MediaType != "image/png" {
			t.Fatalf("agent run should receive 1 png image, got %#v", imgs)
		}
	default:
		t.Fatal("expected captureRunImages to be invoked with the staged image")
	}
}

// TestSubmitDropsImagesWhenModelSwitchedToNonVision attaches an image on a vision
// model, then simulates a /model switch to a non-vision id before submit. The
// submit-time re-check must drop the images (the agent run receives none), append
// an inline notice mirroring exec's drop+warn wording, and still clear pending
// state.
func TestSubmitDropsImagesWhenModelSwitchedToNonVision(t *testing.T) {
	root := t.TempDir()
	writeTestPNG(t, root, "photo.png")

	captured := make(chan []zeroruntime.ImageBlock, 1)
	provider := &fakeProvider{events: []zeroruntime.StreamEvent{
		{Type: zeroruntime.StreamEventText, Content: "ok"},
		{Type: zeroruntime.StreamEventDone},
	}}

	m := newModel(context.Background(), Options{
		Cwd:          root,
		ProviderName: "openai",
		ModelName:    "gpt-4.1",
		Provider:     provider,
		Registry:     tools.NewRegistry(),
		SessionStore: testSessionStore(t),
	})
	m.agentOptions.OnText = func(string) {}
	m.captureRunImages = func(imgs []zeroruntime.ImageBlock) { captured <- imgs }

	// Attach on the vision model.
	m.input.SetValue("/image photo.png")
	updated, _ := m.handleSubmit()
	m = updated.(model)
	if len(m.pendingImages) != 1 {
		t.Fatalf("setup: expected 1 staged image, got %d", len(m.pendingImages))
	}

	// Simulate a /model switch to a non-vision (catalog-unknown) model.
	m.modelName = "totally-unknown-custom"

	m.input.SetValue("describe this")
	updated, cmd := m.handleSubmit()
	next := updated.(model)
	if cmd == nil {
		t.Fatal("expected a prompt submit to start a run")
	}
	if len(next.pendingImages) != 0 || len(next.pendingImageLabels) != 0 {
		t.Fatalf("submit must clear pending images, got %d/%d", len(next.pendingImages), len(next.pendingImageLabels))
	}
	if notice := lastTranscriptText(next); !strings.Contains(notice, "does not support image input") {
		t.Fatalf("expected an inline drop notice, got %q", notice)
	}

	execCmd(cmd) // run the agent goroutine; it invokes captureRunImages

	select {
	case imgs := <-captured:
		if len(imgs) != 0 {
			t.Fatalf("non-vision model must receive no images, got %#v", imgs)
		}
	default:
		t.Fatal("expected captureRunImages to be invoked (with no images)")
	}
}
