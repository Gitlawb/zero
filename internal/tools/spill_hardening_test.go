package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func spillDirName() string {
	if uid := os.Getuid(); uid >= 0 {
		return fmt.Sprintf("zero-tool-output-%d", uid)
	}
	return "zero-tool-output"
}

func TestSpillDirRejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	elsewhere := filepath.Join(tmp, "elsewhere")
	if err := os.Mkdir(elsewhere, 0o700); err != nil {
		t.Fatal(err)
	}
	// An attacker pre-creates the spill path as a symlink to redirect spills.
	if err := os.Symlink(elsewhere, filepath.Join(tmp, spillDirName())); err != nil {
		t.Fatal(err)
	}
	if path := spillTruncatedOutput("bash", "sensitive output"); path != "" {
		t.Fatalf("spill must refuse a symlinked directory, wrote %s", path)
	}
	entries, _ := os.ReadDir(elsewhere)
	if len(entries) != 0 {
		t.Fatalf("nothing may land behind the symlink: %v", entries)
	}
}

func TestSpillDirAcceptsOwnedDirectory(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	if path := spillTruncatedOutput("bash", "ok"); path == "" {
		t.Fatal("spill must work in a clean per-user temp dir")
	}
}

func TestResolveSpillReadPathContainment(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	spillPath := spillTruncatedOutput("exec_command", "spilled body")
	if spillPath == "" {
		t.Fatal("spill must succeed in a clean temp dir")
	}
	resolved, ok := resolveSpillReadPath(spillPath)
	if !ok || resolved == "" {
		t.Fatalf("a real spill file must resolve, got ok=%v", ok)
	}
	if _, ok := resolveSpillReadPath(filepath.Join(spillRootPath(), "..", "escape.txt")); ok {
		t.Fatal("path traversal out of the spill dir must be rejected")
	}
	if _, ok := resolveSpillReadPath("/etc/hosts"); ok {
		t.Fatal("paths outside the spill dir must be rejected")
	}
	if _, ok := resolveSpillReadPath(spillRootPath()); ok {
		t.Fatal("the spill dir itself is not a readable file target")
	}
	if runtime.GOOS != "windows" {
		// A symlink planted inside the spill dir pointing outside must not leak.
		link := filepath.Join(spillRootPath(), "sneaky-link")
		if err := os.Symlink("/etc/hosts", link); err == nil {
			if _, ok := resolveSpillReadPath(link); ok {
				t.Fatal("symlink escaping the spill dir must be rejected")
			}
		}
	}
}

func TestReadFileCanReadSpillFile(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	spillPath := spillTruncatedOutput("exec_command", "line one\nline two\n")
	if spillPath == "" {
		t.Fatal("spill must succeed")
	}
	workspace := t.TempDir()
	result := NewScopedReadFileTool(workspace, nil).Run(context.Background(), map[string]any{"path": spillPath})
	if result.Status != StatusOK || !strings.Contains(result.Output, "line two") {
		t.Fatalf("read_file must be able to follow the truncation notice: %s %q", result.Status, result.Output)
	}
}
