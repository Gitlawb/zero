package imageinput

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFileReadsAndNormalizes(t *testing.T) {
	root := t.TempDir()
	// 1x1 PNG (real PNG signature so http.DetectContentType returns image/png).
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
	if err := os.WriteFile(filepath.Join(root, "pic.png"), png, 0o644); err != nil {
		t.Fatalf("write png: %v", err)
	}

	// Relative path resolves against workspaceRoot.
	block, err := LoadFile("pic.png", root)
	if err != nil {
		t.Fatalf("LoadFile relative: %v", err)
	}
	if block.MediaType != "image/png" {
		t.Fatalf("MediaType = %q, want image/png", block.MediaType)
	}
	if len(block.Data) != len(png) {
		t.Fatalf("Data length = %d, want %d", len(block.Data), len(png))
	}

	// Absolute path is used as-is.
	if _, err := LoadFile(filepath.Join(root, "pic.png"), root); err != nil {
		t.Fatalf("LoadFile absolute: %v", err)
	}
}

func TestLoadFileMissing(t *testing.T) {
	root := t.TempDir()
	_, err := LoadFile("nope.png", root)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "nope.png") {
		t.Fatalf("error %q should name the path", err.Error())
	}
}

func TestLoadFileUnsupportedType(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("just some plain text, not an image at all"), 0o644); err != nil {
		t.Fatalf("write txt: %v", err)
	}
	_, err := LoadFile("notes.txt", root)
	if err == nil {
		t.Fatal("expected error for non-image content")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("error %q should mention unsupported", err.Error())
	}
}

func TestLoadFileRejectsNonRegular(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "adir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A directory is non-regular like a FIFO/device; the guard must reject it
	// before os.Open (a writerless FIFO would otherwise block the read forever).
	_, err := LoadFile("adir", root)
	if err == nil {
		t.Fatal("expected error for a non-regular file")
	}
	if !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("error %q should mention regular file", err.Error())
	}
}

func TestLoadFileOversizeRejected(t *testing.T) {
	root := t.TempDir()
	big := make([]byte, (10<<20)+1)
	// Make it sniff as a GIF so size is the only failing condition.
	copy(big, []byte("GIF89a"))
	if err := os.WriteFile(filepath.Join(root, "big.gif"), big, 0o644); err != nil {
		t.Fatalf("write big: %v", err)
	}
	_, err := LoadFile("big.gif", root)
	if err == nil {
		t.Fatal("expected error for oversize image")
	}
	if !strings.Contains(err.Error(), "10") {
		t.Fatalf("error %q should mention the size limit", err.Error())
	}
}
