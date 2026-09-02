package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
	content, known := maybeFormatWrittenFile(context.Background(), filepath.Join(t.TempDir(), "notes.xyz"), "raw   text")
	if content != "raw   text" || !known {
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
	content, known := maybeFormatWrittenFile(context.Background(), targetPath, uglyContent)
	if content != uglyContent || !known {
		t.Fatalf("missing formatter must return written content, got %q", content)
	}
}

func TestFormatOnWriteReadsMutatedFileAfterFormatterFailure(t *testing.T) {
	requireGofmt(t)
	t.Setenv("ZERO_FORMAT_ON_WRITE", "1")
	targetPath := filepath.Join(t.TempDir(), "a.go")
	if err := os.WriteFile(targetPath, []byte("requested"), 0o644); err != nil {
		t.Fatal(err)
	}
	priorRunner := runFormatOnWriteCommand
	runFormatOnWriteCommand = func(_ context.Context, _ string, _ []string, _ string) error {
		if err := os.WriteFile(targetPath, []byte("formatter-mutated"), 0o644); err != nil {
			t.Fatal(err)
		}
		return exec.ErrNotFound
	}
	t.Cleanup(func() { runFormatOnWriteCommand = priorRunner })

	content, known := maybeFormatWrittenFile(context.Background(), targetPath, "requested")
	if !known || content != "formatter-mutated" {
		t.Fatalf("formatter failure content = %q, known=%t", content, known)
	}
}

func TestFormatOnWriteMarksUnreadableFinalStateUnknown(t *testing.T) {
	requireGofmt(t)
	t.Setenv("ZERO_FORMAT_ON_WRITE", "1")
	targetPath := filepath.Join(t.TempDir(), "a.go")
	if err := os.WriteFile(targetPath, []byte("requested"), 0o644); err != nil {
		t.Fatal(err)
	}
	priorRunner := runFormatOnWriteCommand
	priorReader := readFormattedFile
	runFormatOnWriteCommand = func(context.Context, string, []string, string) error { return nil }
	readFormattedFile = func(string) ([]byte, error) { return nil, os.ErrPermission }
	t.Cleanup(func() {
		runFormatOnWriteCommand = priorRunner
		readFormattedFile = priorReader
	})

	content, known := maybeFormatWrittenFile(context.Background(), targetPath, "requested")
	if known || content != "requested" {
		t.Fatalf("unreadable formatter result = %q, known=%t", content, known)
	}
}

func TestWriteFileUsesVerifiedBytesAfterFormatterFailure(t *testing.T) {
	requireGofmt(t)
	t.Setenv("ZERO_FORMAT_ON_WRITE", "1")
	root := t.TempDir()
	targetPath := filepath.Join(root, "a.go")
	priorRunner := runFormatOnWriteCommand
	runFormatOnWriteCommand = func(_ context.Context, _ string, _ []string, _ string) error {
		if err := os.WriteFile(targetPath, []byte("formatter-mutated\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return exec.ErrNotFound
	}
	t.Cleanup(func() { runFormatOnWriteCommand = priorRunner })

	result := NewScopedWriteFileTool(root, nil).Run(context.Background(), map[string]any{
		"path": "a.go", "content": "requested\n",
	})
	if result.Status != StatusOK {
		t.Fatalf("write status = %s: %s", result.Status, result.Output)
	}
	if got := result.FileDiffs; len(got) != 1 || got[0].NewText != "formatter-mutated\n" {
		t.Fatalf("formatter-failure FileDiff = %#v", got)
	}
}

func TestEditFileUsesVerifiedBytesAfterFormatterFailure(t *testing.T) {
	requireGofmt(t)
	t.Setenv("ZERO_FORMAT_ON_WRITE", "1")
	root := t.TempDir()
	targetPath := filepath.Join(root, "a.go")
	if err := os.WriteFile(targetPath, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	priorRunner := runFormatOnWriteCommand
	runFormatOnWriteCommand = func(_ context.Context, _ string, _ []string, _ string) error {
		if err := os.WriteFile(targetPath, []byte("formatter-mutated\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return exec.ErrNotFound
	}
	t.Cleanup(func() { runFormatOnWriteCommand = priorRunner })

	result := NewScopedEditFileTool(root, nil).Run(context.Background(), map[string]any{
		"path": "a.go", "old_string": "before", "new_string": "requested",
	})
	if result.Status != StatusOK {
		t.Fatalf("edit status = %s: %s", result.Status, result.Output)
	}
	if got := result.FileDiffs; len(got) != 1 || got[0].OldText != "before\n" || got[0].NewText != "formatter-mutated\n" {
		t.Fatalf("formatter-failure edit FileDiff = %#v", got)
	}
}

func TestWriteFileOmitsRichDiffWhenFormatterFinalReadFails(t *testing.T) {
	requireGofmt(t)
	t.Setenv("ZERO_FORMAT_ON_WRITE", "1")
	root := t.TempDir()
	priorRunner := runFormatOnWriteCommand
	priorReader := readFormattedFile
	runFormatOnWriteCommand = func(context.Context, string, []string, string) error { return exec.ErrNotFound }
	readFormattedFile = func(string) ([]byte, error) { return nil, os.ErrPermission }
	t.Cleanup(func() {
		runFormatOnWriteCommand = priorRunner
		readFormattedFile = priorReader
	})

	result := NewScopedWriteFileTool(root, nil).Run(context.Background(), map[string]any{
		"path": "a.go", "content": "requested\n",
	})
	if result.Status != StatusOK || len(result.ChangedFiles) != 1 || len(result.FileDiffs) != 0 {
		t.Fatalf("unverified formatter result = status=%s changed=%#v diffs=%#v", result.Status, result.ChangedFiles, result.FileDiffs)
	}
}
