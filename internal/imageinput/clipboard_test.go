package imageinput

import (
	"testing"
)

func TestReadClipboardImageNoImageReturnsNil(t *testing.T) {
	// When the clipboard has no image (only text or empty), ReadClipboardImage
	// returns nil bytes with no error. This is the common case — not an error.
	data, mediaType, err := ReadClipboardImage()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data != nil {
		t.Fatalf("expected nil data when no image in clipboard, got %d bytes", len(data))
	}
	if mediaType != "" {
		t.Fatalf("expected empty media type when no image, got %q", mediaType)
	}
}

func TestReadClipboardImageReturnsValidMediaType(t *testing.T) {
	// This test only runs meaningfully on a machine with an image in the
	// clipboard. On CI (no image), it passes as a no-op via the test above.
	// On a dev machine, copy an image to clipboard before running:
	//   go test -run TestReadClipboardImageReturnsValidMediaType -v ./internal/imageinput/
	data, mediaType, _ := ReadClipboardImage()
	if data == nil {
		t.Skip("no image in clipboard — skipping")
	}
	validTypes := map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}
	if !validTypes[mediaType] {
		t.Errorf("mediaType = %q, want one of png/jpeg/gif/webp", mediaType)
	}
}
