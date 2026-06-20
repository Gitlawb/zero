package imageinput

import (
	"reflect"
	"testing"
)

func TestImageMIMETypesFiltersAndPreservesRaw(t *testing.T) {
	// Only image/* lines are kept, trimmed. Crucially, a hostile/odd type is
	// returned VERBATIM (not sanitized) — safety comes from never passing it
	// through a shell, so the filter must not silently mangle a real type.
	list := []byte("text/plain\nimage/png\n  image/jpeg  \nTARGETS\n" +
		"image/png; rm -rf ~\n")
	got := imageMIMETypes(list)
	want := []string{"image/png", "image/jpeg", "image/png; rm -rf ~"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("imageMIMETypes = %#v, want %#v", got, want)
	}
}

func TestImageMIMETypesEmpty(t *testing.T) {
	if got := imageMIMETypes([]byte("text/plain\nTARGETS\n")); got != nil {
		t.Fatalf("expected no image types, got %#v", got)
	}
}

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
