package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
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
		fmt.Fprintf(&builder, "line %06d %s\n", index, strings.Repeat("x", 60))
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

func TestSingleLongLineBecomesWritableAfterExactByteReads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bundle.min.js")
	content := strings.Repeat("x", readOutputBudgetBytes*3)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve file: %v", err)
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

	full := read.RunWithOptions(ctx, map[string]any{"path": "bundle.min.js"}, options)
	if full.Meta["truncation_reason"] != "byte_budget" {
		t.Fatalf("precondition: expected byte truncation, got %#v", full.Meta)
	}
	if tracker.SeenWhole(resolvedPath) {
		t.Fatal("a truncated line read must not credit the whole file")
	}

	const chunkSize = 64 * 1024
	for offset := 0; offset < len(content); offset += chunkSize {
		result := read.RunWithOptions(ctx, map[string]any{
			"path":        "bundle.min.js",
			"byte_offset": offset,
			"byte_limit":  chunkSize,
		}, options)
		if result.Status != StatusOK || result.Truncated {
			t.Fatalf("byte read at %d was not exact: status=%s truncated=%v output=%q", offset, result.Status, result.Truncated, result.Output)
		}
	}
	if !tracker.SeenWhole(resolvedPath) {
		t.Fatal("exact byte reads covered the file but it is not seen whole")
	}

	overwrite := write.RunWithOptions(ctx, map[string]any{
		"path": "bundle.min.js", "content": "replaced\n", "overwrite": true,
	}, options)
	if overwrite.Status != StatusOK {
		t.Fatalf("overwrite refused after exact byte reads: %s", overwrite.Output)
	}
}

func TestExactByteReadsAdvanceAlongUTF8Boundaries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "unicode.min.js")
	content := strings.Repeat("😀", 40_000)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}

	tracker := NewFileTracker()
	options := RunOptions{FileTracker: tracker}
	read := NewReadFileTool(dir).(interface {
		RunWithOptions(context.Context, map[string]any, RunOptions) Result
	})
	offset := 0
	for offset < len(content) {
		result := read.RunWithOptions(context.Background(), map[string]any{
			"path": "unicode.min.js", "byte_offset": offset, "byte_limit": 65_535,
		}, options)
		if result.Status != StatusOK || result.Truncated || !utf8.ValidString(result.Output) {
			t.Fatalf("invalid exact UTF-8 read at %d: %#v", offset, result)
		}
		next, parseErr := strconv.Atoi(result.Meta["next_byte_offset"])
		if parseErr != nil || next <= offset {
			t.Fatalf("invalid continuation offset %q after %d", result.Meta["next_byte_offset"], offset)
		}
		offset = next
	}
	if !tracker.SeenWhole(resolvedPath) {
		t.Fatal("UTF-8-safe byte reads covered the file but it is not seen whole")
	}
}

func TestRegistryTruncationDoesNotCreditUndeliveredByteRanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "unicode.min.js")
	content := strings.Repeat("界", 84_000)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}

	registry := safetyRegistry(t, dir)
	tracker := NewFileTracker()
	result := registry.RunWithOptions(context.Background(), "read_file", map[string]any{
		"path": "unicode.min.js", "byte_offset": 0, "byte_limit": readFileByteChunkMax,
	}, grantedOpts(tracker))
	if !result.Truncated {
		t.Fatalf("precondition: multibyte chunk was not truncated at the registry boundary: %#v", result.Meta)
	}
	next, err := strconv.Atoi(result.Meta["next_byte_offset"])
	if err != nil || next <= 0 {
		t.Fatalf("invalid continuation offset %q", result.Meta["next_byte_offset"])
	}
	if tracker.SeenBytes(resolvedPath, 0, next) || tracker.SeenWhole(resolvedPath) {
		t.Fatal("registry-truncated bytes were credited as delivered to the model")
	}
}

func TestRegistryTruncationDoesNotCreditWholeLineRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "unicode.go")
	content := strings.Repeat("界界界界界界界界\n", 4_000)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}

	registry := safetyRegistry(t, dir)
	tracker := NewFileTracker()
	result := registry.RunWithOptions(context.Background(), "read_file", map[string]any{
		"path": "unicode.go",
	}, grantedOpts(tracker))
	if !result.Truncated {
		t.Fatalf("precondition: multibyte line read was not truncated at the registry boundary: %#v", result.Meta)
	}
	if tracker.SeenWhole(resolvedPath) {
		t.Fatal("registry-truncated line output credited the whole file")
	}
}

func TestPartialByteEditDoesNotCreditAnEntireLongLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bundle.min.js")
	content := "UNIQUE_TARGET" + strings.Repeat("x", readOutputBudgetBytes*2)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}

	registry := safetyRegistry(t, dir)
	tracker := NewFileTracker()
	opts := grantedOpts(tracker)
	read := registry.RunWithOptions(context.Background(), "read_file", map[string]any{
		"path": "bundle.min.js", "byte_offset": 0, "byte_limit": 64,
	}, opts)
	if read.Status != StatusOK || read.Truncated {
		t.Fatalf("exact byte read failed: %#v", read)
	}
	edit := registry.RunWithOptions(context.Background(), "edit_file", map[string]any{
		"path": "bundle.min.js", "old_string": "UNIQUE_TARGET", "new_string": "UPDATED_TARGET",
	}, opts)
	if edit.Status != StatusOK {
		t.Fatalf("edit of exactly observed bytes failed: %s", edit.Output)
	}
	if tracker.SeenWhole(resolvedPath) {
		t.Fatal("editing observed bytes credited the unread remainder of the long line")
	}
	overwrite := registry.RunWithOptions(context.Background(), "write_file", map[string]any{
		"path": "bundle.min.js", "content": "replacement", "overwrite": true,
	}, opts)
	if overwrite.Status != StatusError {
		t.Fatalf("whole overwrite should remain blocked after a partial byte read, got %#v", overwrite)
	}
}
