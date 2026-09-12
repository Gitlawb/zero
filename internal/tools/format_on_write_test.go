package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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
	formatting := maybeFormatWrittenFile(context.Background(), filepath.Join(t.TempDir(), "notes.xyz"), "raw   text")
	if formatting.Content != "raw   text" {
		t.Fatalf("unknown extension must pass through: %q", formatting.Content)
	}
	if notice := formatting.notice("notes.xyz"); notice != "" {
		t.Fatalf("an extension with no formatter is not a miss worth reporting, got %q", notice)
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
	formatting := maybeFormatWrittenFile(context.Background(), targetPath, uglyContent)
	if formatting.Content != uglyContent {
		t.Fatalf("missing formatter must return written content, got %q", formatting.Content)
	}
	if notice := formatting.notice("a.go"); notice != "" {
		t.Fatalf("an uninstalled formatter is a standing fact, not a miss worth reporting, got %q", notice)
	}
}

// requirePrettier skips when the Node-based formatter is not installed; unlike
// gofmt it is not guaranteed by the Go toolchain.
func requirePrettier(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("prettier"); err != nil {
		t.Skip("prettier not on PATH")
	}
}

func TestFormatOnWritePrettierUsesDestinationFileName(t *testing.T) {
	requirePrettier(t)
	t.Setenv("ZERO_FORMAT_ON_WRITE", "1")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".prettierrc"), []byte(`{"overrides":[{"files":"special.js","options":{"singleQuote":true}}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".prettierignore"), []byte("ignored.js\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The override matches "special.js" only. If Prettier saw the staging name
	// ".zero-fmt-*.js" instead of the destination, it would use the default
	// double quotes and this assertion would fail.
	specialPath := filepath.Join(dir, "special.js")
	special := maybeFormatWrittenFile(context.Background(), specialPath, "const x = \"a\";\n")
	if !strings.Contains(special.Content, "const x = 'a';") {
		t.Fatalf("prettier must resolve .prettierrc against the destination name, got %q", special.Content)
	}

	// Default Prettier would reformat this to `const x = "a";`. Because the
	// destination name is ignored, Prettier echoes the input and the fallback
	// keeps the written bytes intact. Against the staging name it would not be
	// ignored and the reformatted bytes would win.
	ignoredPath := filepath.Join(dir, "ignored.js")
	ignoredInput := "const x=\"a\";\n"
	ignored := maybeFormatWrittenFile(context.Background(), ignoredPath, ignoredInput)
	if ignored.Content != ignoredInput {
		t.Fatalf("prettier must honour .prettierignore for the destination name, got %q", ignored.Content)
	}
}

// TestFormatOnWritePrettierUsesDestinationFilename exercises the same
// destination-name resolution end-to-end through write_file: the bytes actually
// published to disk, not just the formatter helper's return value, must reflect
// the .prettierrc override and the .prettierignore rule. Resolving either
// against the ".zero-fmt-*.js" staging name would flip both assertions.
func TestFormatOnWritePrettierUsesDestinationFilename(t *testing.T) {
	requirePrettier(t)
	t.Setenv("ZERO_FORMAT_ON_WRITE", "1")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".prettierrc"), []byte(`{"overrides":[{"files":"special.js","options":{"singleQuote":true}}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".prettierignore"), []byte("ignored.js\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	specialPath := filepath.Join(dir, "special.js")
	if err := os.WriteFile(specialPath, []byte("const x = \"a\";\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	write := NewScopedWriteFileTool(dir, nil).(optionsAwareTool).RunWithOptions(context.Background(), map[string]any{
		"path":      "special.js",
		"content":   "const x = \"a\";\n",
		"overwrite": true,
	}, RunOptions{})
	if write.Status != StatusOK {
		t.Fatalf("write_file failed: %q", write.Output)
	}
	onDisk, err := os.ReadFile(specialPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(onDisk), "const x = 'a';") {
		t.Fatalf("write_file must resolve .prettierrc against the destination filename, got %q", onDisk)
	}
	if strings.Contains(string(onDisk), "\"a\"") {
		t.Fatalf("write_file published the unformatted double-quoted content %q", onDisk)
	}

	// The input has no spaces around "=", so default Prettier would rewrite it
	// to `const x = "a";`. Honouring .prettierignore means those bytes are
	// published unchanged; against the staging name they would not be ignored.
	ignoredInput := "const x=\"a\";\n"
	ignored := NewScopedWriteFileTool(dir, nil).(optionsAwareTool).RunWithOptions(context.Background(), map[string]any{
		"path":    "ignored.js",
		"content": ignoredInput,
	}, RunOptions{})
	if ignored.Status != StatusOK {
		t.Fatalf("write_file ignored.js failed: %q", ignored.Output)
	}
	ignoredOnDisk, err := os.ReadFile(filepath.Join(dir, "ignored.js"))
	if err != nil {
		t.Fatal(err)
	}
	if string(ignoredOnDisk) != ignoredInput {
		t.Fatalf("write_file must honour .prettierignore for the destination filename, got %q", ignoredOnDisk)
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
path=
for a in "$@"; do path="$a"; done
printf 'PARTIAL' > "$path"
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
			wantPublished := ""
			switch toolName {
			case "write_file":
				wantPublished = uglyGoSource
				result = NewScopedWriteFileTool(dir, nil).(optionsAwareTool).RunWithOptions(context.Background(), map[string]any{
					"path":      "a.go",
					"content":   uglyGoSource,
					"overwrite": true,
				}, RunOptions{FileTracker: tracker})
			default:
				wantPublished = "package a\n\nfunc  B( ) {   }\n"
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
			// The failed formatter must not have scribbled on the destination:
			// the fallback staged bytes are what gets published.
			if string(onDisk) != wantPublished {
				t.Fatalf("%s destination = %q, want the fallback staged bytes %q", toolName, onDisk, wantPublished)
			}
			assertTrackerMatchesDisk(t, tracker, target)
		})
	}
}

func TestFormatOnWriteRefusesWhenDestinationChangesDuringFormat(t *testing.T) {
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
			ready := filepath.Join(dir, "fmt-ready")
			release := filepath.Join(dir, "fmt-release")
			installFakeGofmt(t, `#!/bin/sh
: > "$ZERO_FORMAT_READY"
while [ ! -f "$ZERO_FORMAT_RELEASE" ]; do sleep 0.01; done
exit 0
`)
			t.Setenv("ZERO_FORMAT_READY", ready)
			t.Setenv("ZERO_FORMAT_RELEASE", release)

			tracker := NewFileTracker()
			read := NewScopedReadFileTool(dir, nil).(optionsAwareTool).RunWithOptions(context.Background(), map[string]any{
				"path": "a.go",
			}, RunOptions{FileTracker: tracker})
			if read.Status != StatusOK {
				t.Fatalf("read_file failed: %q", read.Output)
			}

			resultCh := make(chan Result, 1)
			go func() {
				switch toolName {
				case "write_file":
					resultCh <- NewScopedWriteFileTool(dir, nil).(optionsAwareTool).RunWithOptions(context.Background(), map[string]any{
						"path":      "a.go",
						"content":   uglyGoSource,
						"overwrite": true,
					}, RunOptions{FileTracker: tracker})
				default:
					resultCh <- NewScopedEditFileTool(dir, nil).(optionsAwareTool).RunWithOptions(context.Background(), map[string]any{
						"path":       "a.go",
						"old_string": "func Original() {}",
						"new_string": "func  B( ) {   }",
					}, RunOptions{FileTracker: tracker})
				}
			}()

			waitForFile(t, ready)
			external := "package a\n\nfunc External() {}\n"
			if err := os.WriteFile(target, []byte(external), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(release, nil, 0o644); err != nil {
				t.Fatal(err)
			}

			var result Result
			select {
			case result = <-resultCh:
			case <-time.After(15 * time.Second):
				t.Fatal("tool did not return after the formatter was released")
			}
			if result.Status != StatusError || !strings.Contains(result.Output, "changed on disk") {
				t.Fatalf("%s must refuse when the destination changed during formatting, got %q", toolName, result.Output)
			}
			onDisk, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			if string(onDisk) != external {
				t.Fatalf("external bytes were clobbered: got %q, want %q", onDisk, external)
			}
		})
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", path)
		}
		time.Sleep(10 * time.Millisecond)
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
