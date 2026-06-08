package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pngBytes is a minimal valid PNG (8-byte signature is enough for
// http.DetectContentType to sniff "image/png").
var pngBytes = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
	0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
}

func TestResolveExecImagesValidSingle(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "shot.png"), pngBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	images, err := resolveExecImages([]string{"shot.png"}, root)
	if err != nil {
		t.Fatalf("resolveExecImages error: %v", err)
	}
	if len(images) != 1 {
		t.Fatalf("len(images) = %d, want 1", len(images))
	}
	if images[0].MediaType != "image/png" {
		t.Fatalf("MediaType = %q, want image/png", images[0].MediaType)
	}
	if !bytes.Equal(images[0].Data, pngBytes) {
		t.Fatalf("Data = %v, want raw png bytes", images[0].Data)
	}
}

func TestResolveExecImagesRepeatedAndRelativeToRoot(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "media")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.png"), pngBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "b.png"), pngBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	images, err := resolveExecImages([]string{"a.png", "media/b.png"}, root)
	if err != nil {
		t.Fatalf("resolveExecImages error: %v", err)
	}
	if len(images) != 2 {
		t.Fatalf("len(images) = %d, want 2", len(images))
	}
}

func TestResolveExecImagesMissingFileIsUsageError(t *testing.T) {
	root := t.TempDir()
	_, err := resolveExecImages([]string{"nope.png"}, root)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if _, ok := err.(execUsageError); !ok {
		t.Fatalf("error type = %T, want execUsageError", err)
	}
	if !strings.Contains(err.Error(), "image file not found") {
		t.Fatalf("error = %q, want image-file-not-found", err.Error())
	}
}

func TestResolveExecImagesUnsupportedTypeIsUsageError(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("just some text, not an image"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := resolveExecImages([]string{"notes.txt"}, root)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if _, ok := err.(execUsageError); !ok {
		t.Fatalf("error type = %T, want execUsageError", err)
	}
	if !strings.Contains(err.Error(), "unsupported image type") {
		t.Fatalf("error = %q, want unsupported-image-type", err.Error())
	}
}

func TestResolveExecImagesOversizedIsUsageError(t *testing.T) {
	root := t.TempDir()
	big := make([]byte, (10<<20)+1)
	copy(big, pngBytes) // keep a valid png sniff at the head
	if err := os.WriteFile(filepath.Join(root, "huge.png"), big, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := resolveExecImages([]string{"huge.png"}, root)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if _, ok := err.(execUsageError); !ok {
		t.Fatalf("error type = %T, want execUsageError", err)
	}
	if !strings.Contains(err.Error(), "10 MiB") {
		t.Fatalf("error = %q, want size-cap message", err.Error())
	}
}

func TestResolveExecImagesEmptyReturnsNil(t *testing.T) {
	images, err := resolveExecImages(nil, t.TempDir())
	if err != nil {
		t.Fatalf("resolveExecImages error: %v", err)
	}
	if images != nil {
		t.Fatalf("images = %#v, want nil", images)
	}
}
