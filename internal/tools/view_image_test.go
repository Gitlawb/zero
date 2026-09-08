package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A 1x1 PNG. Small enough to inline, real enough to pass media-type sniffing.
var onePixelPNG = []byte{
	0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89, 0x00, 0x00, 0x00, 0x0a, 'I', 'D', 'A', 'T',
	0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00, 0x05,
	0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00,
	0x00, 0x00, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82,
}

func TestViewImageReturnsTheImage(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "shot.png"), onePixelPNG, 0o600); err != nil {
		t.Fatal(err)
	}
	result := NewViewImageTool(root).Run(context.Background(), map[string]any{"path": "shot.png"})

	if result.Status != StatusOK {
		t.Fatalf("status = %v: %s", result.Status, result.Output)
	}
	if len(result.Images) != 1 {
		t.Fatalf("got %d images, want 1 — the text alone is useless to the model", len(result.Images))
	}
	if result.Images[0].MediaType != "image/png" {
		t.Errorf("media type = %q, want image/png", result.Images[0].MediaType)
	}
	if len(result.Images[0].Data) != len(onePixelPNG) {
		t.Errorf("image data is %d bytes, want %d", len(result.Images[0].Data), len(onePixelPNG))
	}
}

// The tool must not reach outside the workspace. imageinput.LoadFile does no
// containment check of its own, so the scoping is entirely this tool's job.
func TestViewImageRefusesPathsOutsideTheWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "outside.png")
	if err := os.WriteFile(secret, onePixelPNG, 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewViewImageTool(root)

	for name, path := range map[string]string{
		"absolute path outside": secret,
		"parent traversal":      filepath.Join("..", filepath.Base(outside), "outside.png"),
	} {
		t.Run(name, func(t *testing.T) {
			result := tool.Run(context.Background(), map[string]any{"path": path})
			if result.Status == StatusOK {
				t.Fatalf("read an image outside the workspace: %s", result.Output)
			}
			if len(result.Images) != 0 {
				t.Error("a refused read still returned image bytes")
			}
		})
	}
}

func TestViewImageRejectsNonImages(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("not an image"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := NewViewImageTool(root).Run(context.Background(), map[string]any{"path": "notes.txt"})
	if result.Status == StatusOK {
		t.Fatalf("accepted a text file as an image: %s", result.Output)
	}
	if len(result.Images) != 0 {
		t.Error("a rejected file still returned image bytes")
	}
}

func TestViewImageReportsAMissingFileClearly(t *testing.T) {
	result := NewViewImageTool(t.TempDir()).Run(context.Background(), map[string]any{"path": "nope.png"})
	if result.Status == StatusOK {
		t.Fatal("a missing file reported success")
	}
	// The error names the path the caller asked for, matching read_file. Both
	// also carry the resolved absolute path from the underlying syscall error;
	// that is pre-existing behaviour shared with read_file, not something this
	// tool introduces, so it is not asserted either way here.
	if !strings.Contains(result.Output, "nope.png") {
		t.Errorf("error should name the file, got %q", result.Output)
	}
	if len(result.Images) != 0 {
		t.Error("a failed read still returned image bytes")
	}
}
