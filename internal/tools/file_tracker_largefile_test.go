package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A file past the read byte budget cannot be read whole in one call: the read is
// truncated and RecordSeenRange is skipped, which is correct — the model did not
// see those lines. But SeenWhole was a flag only a single whole-file read could
// set, so no sequence of scoped reads could ever clear it either, and write_file
// refused the overwrite forever while telling the model to read the file again.
func TestLargeFileBecomesWritableAfterScopedReadsCoverIt(t *testing.T) {
	dir := t.TempDir()
	// read_file resolves the workspace root through EvalSymlinks before it
	// records anything, so the tracker is keyed on the resolved path. On macOS
	// t.TempDir() sits under /var, a symlink to /private/var, and asserting
	// against the raw path would look up a key that never existed — passing on
	// Windows and failing there.
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	path := filepath.Join(resolvedDir, "big.go")

	const lines = 2400
	var builder strings.Builder
	for index := 0; index < lines; index++ {
		builder.WriteString(fmt.Sprintf("line %06d %s\n", index, strings.Repeat("x", 60)))
	}
	if writeErr := os.WriteFile(path, []byte(builder.String()), 0o600); writeErr != nil {
		t.Fatalf("seed file: %v", writeErr)
	}

	tracker := NewFileTracker()
	options := RunOptions{FileTracker: tracker}
	ctx := context.Background()
	read := NewReadFileTool(dir).(interface {
		RunWithOptions(context.Context, map[string]any, RunOptions) Result
	})
	write := NewScopedWriteFileTool(dir, nil).(interface {
		RunWithOptions(context.Context, map[string]any, RunOptions) Result
	})

	// One unbounded read is byte-truncated, so nothing is recorded.
	full := read.RunWithOptions(ctx, map[string]any{"path": "big.go"}, options)
	if full.Meta["truncation_reason"] != "byte_budget" {
		t.Fatalf("precondition: expected the file to exceed the read byte budget, got reason %q", full.Meta["truncation_reason"])
	}
	if tracker.SeenWhole(path) {
		t.Fatal("a truncated read must not credit the model with the whole file")
	}

	// Scoped reads that together cover it must.
	for _, span := range [][2]int{{1, 1200}, {1201, 2400}} {
		result := read.RunWithOptions(ctx, map[string]any{
			"path": "big.go", "start_line": span[0], "end_line": span[1],
		}, options)
		if result.Status != StatusOK {
			t.Fatalf("scoped read %v: %s", span, result.Output)
		}
		if result.Meta["truncation_reason"] == "byte_budget" {
			t.Fatalf("scoped read %v was itself truncated; pick smaller spans", span)
		}
	}
	if !tracker.SeenWhole(path) {
		t.Fatal("scoped reads covered every line but the file is still not seen whole")
	}

	overwrite := write.RunWithOptions(ctx, map[string]any{
		"path": "big.go", "content": "replaced\n", "overwrite": true,
	}, options)
	if overwrite.Status != StatusOK {
		t.Fatalf("overwrite refused after the file was fully read in scoped reads: %s", overwrite.Output)
	}
}
