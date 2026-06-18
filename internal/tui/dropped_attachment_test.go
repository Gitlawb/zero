package tui

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDroppedAttachmentPath(t *testing.T) {
	dir := t.TempDir()
	// A screenshot-style name with spaces.
	img := filepath.Join(dir, "Screenshot 2026-06-17 at 5.22.03 PM.png")
	if err := os.WriteFile(img, []byte("\x89PNG\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	txt := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(txt, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Positives — each must resolve to the image path.
	positives := map[string]string{
		"double-quoted drop":  `"` + img + `"`,
		"single-quoted drop":  `'` + img + `'`,
		"plain absolute path": img,
	}
	if runtime.GOOS != "windows" {
		// Backslash-escaping of spaces is a Unix terminal drag-drop convention; on
		// Windows the backslash is the path separator, so this form does not occur.
		positives["backslash-escaped drop"] = strings.ReplaceAll(img, " ", `\ `)
	}
	for name, in := range positives {
		got, ok := droppedAttachmentPath(in, dir)
		if !ok || got != img {
			t.Fatalf("%s: got %q ok=%v, want %q true", name, got, ok, img)
		}
	}

	// Negatives — must NOT be treated as a dropped attachment (so text paste and
	// real slash-commands are untouched).
	for name, in := range map[string]string{
		"real slash command": "/help",
		"non-existent image": filepath.Join(dir, "nope.png"),
		"plain text":         "what is this image?",
		"non-image file":     txt,
		"multi-line":         strings.ReplaceAll(img, " ", `\ `) + "\nmore text",
		"empty":              "   ",
	} {
		if got, ok := droppedAttachmentPath(in, dir); ok {
			t.Fatalf("%s: should NOT attach, got %q", name, got)
		}
	}
}
