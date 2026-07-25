package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadMinifiedFileStripsCommentsAndLineNumbers(t *testing.T) {
	dir := t.TempDir()
	src := "package demo\n\nimport \"fmt\"\n\n// secret doc comment\nfunc F() { fmt.Println(\"x\") }\n"
	if err := os.WriteFile(filepath.Join(dir, "f.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	res := NewScopedReadMinifiedFileTool(dir, nil).Run(context.Background(), map[string]any{"path": "f.go"})
	if res.Status != StatusOK {
		t.Fatalf("status %v: %s", res.Status, res.Output)
	}
	if strings.Contains(res.Output, "secret doc comment") {
		t.Errorf("comment leaked:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "func F()") {
		t.Errorf("code missing:\n%s", res.Output)
	}
	if strings.Contains(res.Output, " | ") {
		t.Errorf("minified output should carry NO line-number prefixes:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "minified go view") {
		t.Errorf("expected a minified header note:\n%s", res.Output)
	}
	for _, key := range []string{"mode", "compacted", "raw_bytes", "emitted_bytes", "estimated_tokens_saved"} {
		if res.Meta[key] == "" {
			t.Fatalf("expected compact-read metadata key %q, got %#v", key, res.Meta)
		}
	}
}

func TestReadMinifiedFileRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	res := NewScopedReadMinifiedFileTool(dir, nil).Run(context.Background(), map[string]any{"path": "../escape.go"})
	if res.Status == StatusOK {
		t.Fatalf("expected traversal rejection, got OK:\n%s", res.Output)
	}
}

func TestReadMinifiedFileAppliesByteBudget(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "large.txt"), []byte(strings.Repeat("0123456789abcdef\n", 9000)), 0o644); err != nil {
		t.Fatal(err)
	}

	res := NewScopedReadMinifiedFileTool(dir, nil).Run(context.Background(), map[string]any{"path": "large.txt"})
	if res.Status != StatusOK || !res.Truncated {
		t.Fatalf("expected ok+truncated, got status=%s truncated=%v", res.Status, res.Truncated)
	}
	if !strings.Contains(res.Output, "output exceeded") || !strings.Contains(res.Output, "read_file") {
		t.Fatalf("expected byte-budget continuation hint, got %q", res.Output[len(res.Output)-200:])
	}
	if res.Meta["truncated"] != "true" {
		t.Fatalf("expected truncation metadata, got %#v", res.Meta)
	}
}
