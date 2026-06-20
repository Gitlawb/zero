package imageinput

import (
	"testing"
)

func TestReadClipboardImage(t *testing.T) {
	// ReadClipboardImage either returns nil (no image) or valid image bytes.
	// On CI there's no image → nil. On a dev machine with a screenshot copied,
	// it returns real bytes. Both paths are valid — the test verifies whichever
	// one the clipboard produces.
	data, mediaType, err := ReadClipboardImage()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data == nil {
		// No image in clipboard — valid, mediaType must be empty.
		if mediaType != "" {
			t.Fatalf("expected empty media type when no image, got %q", mediaType)
		}
		return
	}
	// Image found — mediaType must be a supported type.
	validTypes := map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}
	if !validTypes[mediaType] {
		t.Errorf("mediaType = %q, want one of png/jpeg/gif/webp", mediaType)
	}
}
