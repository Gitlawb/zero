package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLSPNavigateRejectsBadArgs(t *testing.T) {
	tool := NewLSPNavigateTool(t.TempDir())
	cases := []map[string]any{
		{},                   // missing op + path
		{"op": "definition"}, // missing path
		{"op": "workspace_symbol", "path": "x.go"},                 // missing query
		{"op": "definition", "path": "x.go"},                       // missing line
		{"op": "bogus", "path": "x.go", "line": 1, "character": 1}, // unknown op
	}
	for i, args := range cases {
		if got := tool.Run(context.Background(), args); got.Status != StatusError {
			t.Fatalf("case %d: expected StatusError for %#v, got %s: %s", i, args, got.Status, got.Output)
		}
	}
}

func TestLSPNavigateUnsupportedFileDegrades(t *testing.T) {
	root := t.TempDir()
	// A file type with no language server → the tool returns OK with a clear
	// "unavailable, fall back to grep" message rather than an error.
	path := filepath.Join(root, "notes.unknownext")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewLSPNavigateTool(root)
	got := tool.Run(context.Background(), map[string]any{
		"op": "definition", "path": "notes.unknownext", "line": 1, "character": 1,
	})
	if got.Status != StatusOK {
		t.Fatalf("unsupported file should degrade to StatusOK, got %s: %s", got.Status, got.Output)
	}
	if !strings.Contains(got.Output, "unavailable") {
		t.Fatalf("expected an 'unavailable' message, got %q", got.Output)
	}
}

func TestLSPNavigateIsReadOnly(t *testing.T) {
	tool := NewLSPNavigateTool(t.TempDir())
	if s := tool.Safety(); s.SideEffect != SideEffectRead || s.Permission != PermissionAllow {
		t.Fatalf("lsp_navigate should be read-only/allow, got %+v", s)
	}
	if tool.Name() != "lsp_navigate" {
		t.Fatalf("name = %q", tool.Name())
	}
}
