package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// gofmt ships with the Go toolchain, so it is the one formatter guaranteed to
// exist wherever these tests run.
func requireGofmt(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("gofmt"); err != nil {
		t.Skip("gofmt not on PATH")
	}
}

func TestFormatOnWriteDisabledByDefault(t *testing.T) {
	requireGofmt(t)
	t.Setenv("ZERO_FORMAT_ON_WRITE", "")
	dir := t.TempDir()
	ugly := "package a\n\nfunc  A( ) {   }\n"

	result := NewScopedWriteFileTool(dir, nil).(optionsAwareTool).RunWithOptions(context.Background(), map[string]any{
		"path":    "a.go",
		"content": ugly,
	}, RunOptions{})
	if result.Status != StatusOK {
		t.Fatalf("write failed: %q", result.Output)
	}
	onDisk, err := os.ReadFile(filepath.Join(dir, "a.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != ugly {
		t.Fatalf("formatting must be off by default, got %q", onDisk)
	}
}

func TestFormatOnWriteFormatsAndKeepsTrackerConsistent(t *testing.T) {
	requireGofmt(t)
	t.Setenv("ZERO_FORMAT_ON_WRITE", "1")
	dir := t.TempDir()
	tracker := NewFileTracker()

	write := NewScopedWriteFileTool(dir, nil).(optionsAwareTool).RunWithOptions(context.Background(), map[string]any{
		"path":    "a.go",
		"content": "package a\n\nfunc  A( ) {   }\n",
	}, RunOptions{FileTracker: tracker})
	if write.Status != StatusOK {
		t.Fatalf("write failed: %q", write.Output)
	}
	onDisk, err := os.ReadFile(filepath.Join(dir, "a.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(onDisk), "func A() {") {
		t.Fatalf("expected gofmt-formatted content, got %q", onDisk)
	}

	// The tracker is re-baselined to the post-format bytes, but those bytes were
	// not returned exactly to the model, so an edit must require an exact read.
	edit := NewScopedEditFileTool(dir, nil).(optionsAwareTool).RunWithOptions(context.Background(), map[string]any{
		"path":       "a.go",
		"old_string": "func A() {",
		"new_string": "func B() {",
	}, RunOptions{FileTracker: tracker})
	if edit.Status != StatusError || !strings.Contains(edit.Output, "has not been read exactly") {
		t.Fatalf("formatter-modified content must require an exact read: %q", edit.Output)
	}
	read := NewScopedReadFileTool(dir, nil).(optionsAwareTool).RunWithOptions(context.Background(), map[string]any{
		"path": "a.go",
	}, RunOptions{FileTracker: tracker})
	if read.Status != StatusOK {
		t.Fatalf("exact read failed: %q", read.Output)
	}
	edit = NewScopedEditFileTool(dir, nil).(optionsAwareTool).RunWithOptions(context.Background(), map[string]any{
		"path":       "a.go",
		"old_string": "func A() {",
		"new_string": "func B( ) {",
	}, RunOptions{FileTracker: tracker})
	if edit.Status != StatusOK {
		t.Fatalf("follow-up edit after exact read failed: %q", edit.Output)
	}
	edit = NewScopedEditFileTool(dir, nil).(optionsAwareTool).RunWithOptions(context.Background(), map[string]any{
		"path":       "a.go",
		"old_string": "func B() {",
		"new_string": "func C() {",
	}, RunOptions{FileTracker: tracker})
	if edit.Status != StatusError || !strings.Contains(edit.Output, "has not been read exactly") {
		t.Fatalf("formatter-modified edit must require an exact read: %q", edit.Output)
	}
}

func TestFormatOnWriteSkipsUnknownExtensions(t *testing.T) {
	t.Setenv("ZERO_FORMAT_ON_WRITE", "1")
	content := maybeFormatWrittenFile(context.Background(), filepath.Join(t.TempDir(), "notes.xyz"), "raw   text")
	if content != "raw   text" {
		t.Fatalf("unknown extension must pass through: %q", content)
	}
}

func TestFormatOnWriteFormatterLookupFailure(t *testing.T) {
	t.Setenv("ZERO_FORMAT_ON_WRITE", "1")
	t.Setenv("PATH", t.TempDir())
	targetPath := filepath.Join(t.TempDir(), "a.go")
	uglyContent := "package a\n\nfunc  A( ) {   }\n"
	if err := os.WriteFile(targetPath, []byte(uglyContent), 0o644); err != nil {
		t.Fatal(err)
	}
	content := maybeFormatWrittenFile(context.Background(), targetPath, uglyContent)
	if content != uglyContent {
		t.Fatalf("missing formatter must return written content, got %q", content)
	}
}

const uglyGoSource = "package a\n\nfunc  A( ) {   }\n"

func TestFormatOnWritePublishesFormattedBytesForWriteAndEdit(t *testing.T) {
	requireGofmt(t)
	t.Setenv("ZERO_FORMAT_ON_WRITE", "1")
	dir := t.TempDir()
	tracker := NewFileTracker()

	write := NewScopedWriteFileTool(dir, nil).(optionsAwareTool).RunWithOptions(context.Background(), map[string]any{
		"path":    "a.go",
		"content": uglyGoSource,
	}, RunOptions{FileTracker: tracker})
	if write.Status != StatusOK {
		t.Fatalf("write_file failed: %q", write.Output)
	}
	assertFormattedOnDiskAndTracked(t, tracker, filepath.Join(dir, "a.go"), write.Display.Preview)

	read := NewScopedReadFileTool(dir, nil).(optionsAwareTool).RunWithOptions(context.Background(), map[string]any{
		"path": "a.go",
	}, RunOptions{FileTracker: tracker})
	if read.Status != StatusOK {
		t.Fatalf("read_file failed: %q", read.Output)
	}
	edit := NewScopedEditFileTool(dir, nil).(optionsAwareTool).RunWithOptions(context.Background(), map[string]any{
		"path":       "a.go",
		"old_string": "func A() {}",
		"new_string": "func  B( ) {   }",
	}, RunOptions{FileTracker: tracker})
	if edit.Status != StatusOK {
		t.Fatalf("edit_file failed: %q", edit.Output)
	}
	onDisk, err := os.ReadFile(filepath.Join(dir, "a.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(onDisk), "func B() {") {
		t.Fatalf("edit_file must publish formatted bytes, got %q", onDisk)
	}
	assertTrackerMatchesDisk(t, tracker, filepath.Join(dir, "a.go"))
	if !strings.Contains(edit.Display.Preview, "func B() {") && !strings.Contains(edit.Display.Preview, "func B()") {
		t.Fatalf("edit_file preview must reflect formatted bytes, got %q", edit.Display.Preview)
	}
}

func TestFormatOnWriteFailureLeavesDestinationIntactUntilPublish(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake formatter shim is a POSIX script")
	}
	t.Setenv("ZERO_FORMAT_ON_WRITE", "1")

	for _, toolName := range []string{"write_file", "edit_file"} {
		t.Run(toolName, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "a.go")
			original := "package a\n\nfunc Original() {}\n"
			if err := os.WriteFile(target, []byte(original), 0o644); err != nil {
				t.Fatal(err)
			}
			probeSaw := filepath.Join(dir, "probe-saw")
			installFakeGofmt(t, `#!/bin/sh
if [ -n "$ZERO_FORMAT_PROBE" ]; then
	cp "$ZERO_FORMAT_PROBE" "$ZERO_FORMAT_PROBE_SAW" || true
fi
printf 'PARTIAL' > "$1"
exit 1
`)
			t.Setenv("ZERO_FORMAT_PROBE", target)
			t.Setenv("ZERO_FORMAT_PROBE_SAW", probeSaw)

			tracker := NewFileTracker()
			read := NewScopedReadFileTool(dir, nil).(optionsAwareTool).RunWithOptions(context.Background(), map[string]any{
				"path": "a.go",
			}, RunOptions{FileTracker: tracker})
			if read.Status != StatusOK {
				t.Fatalf("read_file failed: %q", read.Output)
			}

			var result Result
			switch toolName {
			case "write_file":
				result = NewScopedWriteFileTool(dir, nil).(optionsAwareTool).RunWithOptions(context.Background(), map[string]any{
					"path":      "a.go",
					"content":   uglyGoSource,
					"overwrite": true,
				}, RunOptions{FileTracker: tracker})
			default:
				result = NewScopedEditFileTool(dir, nil).(optionsAwareTool).RunWithOptions(context.Background(), map[string]any{
					"path":       "a.go",
					"old_string": "func Original() {}",
					"new_string": "func  B( ) {   }",
				}, RunOptions{FileTracker: tracker})
			}
			if result.Status != StatusOK {
				t.Fatalf("%s failed: %q", toolName, result.Output)
			}

			saw, err := os.ReadFile(probeSaw)
			if err != nil {
				t.Fatalf("formatter never observed the destination: %v", err)
			}
			if string(saw) != original {
				t.Fatalf("formatter observed %q, want the previous destination %q", saw, original)
			}
			onDisk, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			if string(onDisk) == "PARTIAL" {
				t.Fatalf("%s left formatter-partial bytes on the destination", toolName)
			}
			if strings.Contains(string(onDisk), "PARTIAL") {
				t.Fatalf("%s published formatter-partial bytes: %q", toolName, onDisk)
			}
			assertTrackerMatchesDisk(t, tracker, target)
		})
	}
}

func assertFormattedOnDiskAndTracked(t *testing.T, tracker *FileTracker, path, preview string) {
	t.Helper()
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(onDisk), "func A() {") {
		t.Fatalf("expected gofmt-formatted content, got %q", onDisk)
	}
	assertTrackerMatchesDisk(t, tracker, path)
	if preview != "" && !strings.Contains(preview, "func A() {") && !strings.Contains(preview, "func A()") {
		t.Fatalf("preview must reflect formatted bytes, got %q", preview)
	}
}

func assertTrackerMatchesDisk(t *testing.T, tracker *FileTracker, path string) {
	t.Helper()
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	trackedPath := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		if _, ok := tracker.Version(resolved); ok {
			trackedPath = resolved
		}
	}
	version, ok := tracker.Version(trackedPath)
	if !ok {
		t.Fatalf("tracker has no version for %s", trackedPath)
	}
	if got, want := version.Hash, HashContent(onDisk); got != want {
		t.Fatalf("tracker hash %s does not match on-disk bytes (hash %s)", got, want)
	}
}

func installFakeGofmt(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "gofmt")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
