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
	res := NewReadMinifiedFileTool(dir).Run(context.Background(), map[string]any{"path": "f.go"})
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
}

func TestReadMinifiedFileRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	res := NewReadMinifiedFileTool(dir).Run(context.Background(), map[string]any{"path": "../escape.go"})
	if res.Status == StatusOK {
		t.Fatalf("expected traversal rejection, got OK:\n%s", res.Output)
	}
}
