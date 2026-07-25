package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// safetyRegistry wires the read/write tools the conflict-detection feature spans,
// so tests exercise the real RunWithOptions dispatch (including optionsAwareTool
// routing) rather than calling tool methods directly.
func safetyRegistry(t *testing.T, dir string) *Registry {
	t.Helper()
	registry := NewRegistry()
	for _, tool := range []Tool{NewScopedReadFileTool(dir, nil), NewScopedEditFileTool(dir, nil), NewScopedWriteFileTool(dir, nil)} {
		registry.Register(tool)
	}
	return registry
}

func grantedOpts(tracker *FileTracker) RunOptions {
	return RunOptions{PermissionGranted: true, FileTracker: tracker}
}

func TestEditFileRefusesAfterFileChangedOnDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := safetyRegistry(t, dir)
	tracker := NewFileTracker()

	// Model reads the file → baseline recorded.
	if res := registry.RunWithOptions(context.Background(), "read_file", map[string]any{"path": "f.txt"}, grantedOpts(tracker)); res.Status != StatusOK {
		t.Fatalf("read_file failed: %s", res.Output)
	}
	// Something outside Zero rewrites the file.
	if err := os.WriteFile(path, []byte("alpha changed externally\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The edit (anchored on the stale view) must be refused, not applied.
	res := registry.RunWithOptions(context.Background(), "edit_file", map[string]any{
		"path": "f.txt", "old_string": "alpha", "new_string": "beta",
	}, grantedOpts(tracker))
	if res.Status != StatusError || !strings.Contains(res.Output, "changed on disk") {
		t.Fatalf("expected an on-disk-change refusal, got status=%s output=%q", res.Status, res.Output)
	}
	if got, _ := os.ReadFile(path); string(got) != "alpha changed externally\n" {
		t.Fatalf("file must be untouched after a refused edit, got %q", got)
	}
}

func TestEditFileSucceedsWhenUnchangedAndRebaselines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := safetyRegistry(t, dir)
	tracker := NewFileTracker()

	registry.RunWithOptions(context.Background(), "read_file", map[string]any{"path": "f.txt"}, grantedOpts(tracker))
	if res := registry.RunWithOptions(context.Background(), "edit_file", map[string]any{
		"path": "f.txt", "old_string": "alpha", "new_string": "beta",
	}, grantedOpts(tracker)); res.Status != StatusOK {
		t.Fatalf("edit on unchanged file should succeed, got %q", res.Output)
	}
	// A second edit must not false-conflict: the write re-baselined the tracker.
	if res := registry.RunWithOptions(context.Background(), "edit_file", map[string]any{
		"path": "f.txt", "old_string": "beta", "new_string": "gamma",
	}, grantedOpts(tracker)); res.Status != StatusOK {
		t.Fatalf("second consecutive edit should succeed (re-baselined), got %q", res.Output)
	}
	if got, _ := os.ReadFile(path); string(got) != "gamma\n" {
		t.Fatalf("file = %q, want gamma", got)
	}
}

func TestWriteFileOverwriteRefusedWhenChangedOnDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := safetyRegistry(t, dir)
	tracker := NewFileTracker()

	registry.RunWithOptions(context.Background(), "read_file", map[string]any{"path": "f.txt"}, grantedOpts(tracker))
	if err := os.WriteFile(path, []byte("v2 external\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := registry.RunWithOptions(context.Background(), "write_file", map[string]any{
		"path": "f.txt", "content": "v3 stale\n", "overwrite": true,
	}, grantedOpts(tracker))
	if res.Status != StatusError || !strings.Contains(res.Output, "changed on disk") {
		t.Fatalf("expected overwrite refusal, got status=%s output=%q", res.Status, res.Output)
	}
	if got, _ := os.ReadFile(path); string(got) != "v2 external\n" {
		t.Fatalf("file must be untouched after a refused overwrite, got %q", got)
	}
}

func TestWriteFileOverwriteAllowedForUntrackedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := safetyRegistry(t, dir)
	tracker := NewFileTracker()

	// Never read through a tool → no baseline → overwrite is the model's call.
	res := registry.RunWithOptions(context.Background(), "write_file", map[string]any{
		"path": "f.txt", "content": "v3\n", "overwrite": true,
	}, grantedOpts(tracker))
	if res.Status != StatusOK {
		t.Fatalf("untracked overwrite should be allowed, got %q", res.Output)
	}
	if got, _ := os.ReadFile(path); string(got) != "v3\n" {
		t.Fatalf("file = %q, want v3", got)
	}
}

func TestWriteAndEditUnaffectedWithoutTracker(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := safetyRegistry(t, dir)

	// nil tracker (RunOptions without FileTracker) preserves the old behavior:
	// read, external change, then edit all proceed with no conflict gate.
	opts := RunOptions{PermissionGranted: true}
	registry.RunWithOptions(context.Background(), "read_file", map[string]any{"path": "f.txt"}, opts)
	if err := os.WriteFile(path, []byte("alpha changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res := registry.RunWithOptions(context.Background(), "edit_file", map[string]any{
		"path": "f.txt", "old_string": "alpha changed", "new_string": "beta",
	}, opts); res.Status != StatusOK {
		t.Fatalf("without a tracker, edit should proceed, got %q", res.Output)
	}
}
