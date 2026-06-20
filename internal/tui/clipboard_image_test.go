package tui

import (
	"context"
	"testing"
)

func TestClipboardImageMsgAttachesImage(t *testing.T) {
	m := newModel(context.Background(), Options{ModelName: "gpt-4o"})
	pngData := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a} // PNG header
	// gpt-4o is vision-capable in the catalog, so the gate passes.
	updated := m.attachClipboardImage(pngData, "image/png")
	m2 := updated
	if len(m2.pendingImages) != 1 {
		t.Fatalf("expected 1 pending image, got %d", len(m2.pendingImages))
	}
	if m2.pendingImages[0].MediaType != "image/png" {
		t.Errorf("mediaType = %q, want image/png", m2.pendingImages[0].MediaType)
	}
	if len(m2.pendingImageLabels) != 1 || m2.pendingImageLabels[0] != "clipboard" {
		t.Errorf("expected label 'clipboard', got %v", m2.pendingImageLabels)
	}
}

func TestClipboardImageVisionGateRefuses(t *testing.T) {
	// claude-haiku-3.5 is in the catalog and lacks ModelCapabilityVision.
	m := newModel(context.Background(), Options{ModelName: "claude-haiku-3.5"})
	pngData := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	updated := m.attachClipboardImage(pngData, "image/png")
	m2 := updated
	if len(m2.pendingImages) != 0 {
		t.Fatalf("expected 0 pending images on non-vision model, got %d", len(m2.pendingImages))
	}
}

func TestClipboardImageTooLargeRefuses(t *testing.T) {
	m := newModel(context.Background(), Options{ModelName: "gpt-4o"})
	largeData := make([]byte, 11*1024*1024) // 11 MiB > 10 MiB cap
	updated := m.attachClipboardImage(largeData, "image/png")
	m2 := updated
	if len(m2.pendingImages) != 0 {
		t.Fatalf("expected 0 pending images for oversize clipboard image, got %d", len(m2.pendingImages))
	}
}
