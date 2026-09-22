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

// PATH-KEYED RULES MUST RESOLVE AGAINST THE FILE THE CALLER NAMED.
//
// A stdin adapter receives the logical destination through its filename flag,
// so a .clang-format-ignore pattern, an .editorconfig section, or rustfmt's
// ignore still matches "vendor/lib.hintfmt" rather than a random staging name.
func TestFormatOnWritePassesLogicalDestinationToStdinFormatter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake formatter shim is a POSIX script")
	}
	t.Setenv("ZERO_FORMAT_ON_WRITE", "1")
	record := filepath.Join(t.TempDir(), "formatter-args")
	installFakeStdinFormatter(t, ".hintfmt", "hintfmt", "--assume-filename", record)

	dir := t.TempDir()
	target := filepath.Join(dir, "vendor", "lib.hintfmt")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	formatting := maybeFormatWrittenFile(context.Background(), target, "written\n")
	if formatting.Content != "written\n" {
		t.Fatalf("stdin adapter echoed %q, want the written bytes", formatting.Content)
	}
	recorded, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("formatter never recorded its arguments: %v", err)
	}
	args := string(recorded)
	want := "--assume-filename=" + target
	if !strings.Contains(args, want) {
		t.Fatalf("formatter args %q do not carry the logical destination %q", args, want)
	}
	if strings.Contains(args, ".zero-fmt-") {
		t.Fatalf("formatter args %q leaked a staging path instead of the destination", args)
	}
}

// THE PRODUCTION TABLE MUST CARRY THE HINTS, NOT ONLY THE TEST SEAM.
func TestFormatterFilenameHints(t *testing.T) {
	want := map[string]string{
		".c":     "--assume-filename",
		".h":     "--assume-filename",
		".cpp":   "--assume-filename",
		".hpp":   "--assume-filename",
		".cc":    "--assume-filename",
		".sh":    "--filename",
		".bash":  "--filename",
		".lua":   "--stdin-filepath",
		".swift": "--stdinpath",
		".kt":    "--stdin-path",
		".dart":  "--stdin-name",
	}
	for ext, flag := range want {
		adapter, ok := formatterCommands[ext]
		if !ok {
			t.Fatalf("no formatter adapter for %s", ext)
		}
		if !adapter.stdin {
			t.Errorf("%s adapter is not on the stdin route, so its filename hint cannot apply", ext)
		}
		if adapter.filenameFlag != flag {
			t.Errorf("%s filenameFlag = %q, want %q", ext, adapter.filenameFlag, flag)
		}
	}
}

// installFakeStdinFormatter puts a stdin formatter on PATH and registers it for
// the test's duration. The script records its argv and echoes stdin, so a test
// can prove which logical path the adapter was handed.
func installFakeStdinFormatter(t *testing.T, extension, name, filenameFlag, record string) {
	t.Helper()
	directory := t.TempDir()
	script := "#!/bin/sh\nprintf '%s' \"$*\" > \"$ZERO_FORMAT_ARG_RECORD\"\ncat\n"
	if err := os.WriteFile(filepath.Join(directory, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ZERO_FORMAT_ARG_RECORD", record)
	previous, existed := formatterCommands[extension]
	formatterCommands[extension] = formatterAdapter{argv: []string{name}, stdin: true, filenameFlag: filenameFlag}
	t.Cleanup(func() {
		if existed {
			formatterCommands[extension] = previous
			return
		}
		delete(formatterCommands, extension)
	})
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

// A FORMATTER FAILURE ON THE STAGING COPY MUST NOT REACH THE DESTINATION.
//
// The physical formatter is handed a private staging path, so even a run that
// scribbles "PARTIAL" and exits non-zero cannot rewrite the destination. The
// tool path then publishes the fallback (unformatted) staged bytes, never the
// formatter's partial output and never through a direct destination write.
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

// Formatting must not run for a path outside the configured write roots. The
// unscoped wrapper cannot express that; the scoped entry point refuses and
// returns the written bytes unchanged, while the same call inside the root
// still formats.
func TestFormatOnWriteScopedConfinement(t *testing.T) {
	requireGofmt(t)
	t.Setenv("ZERO_FORMAT_ON_WRITE", "1")
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "a.go")
	if err := os.WriteFile(outside, []byte(uglyGoSource), 0o644); err != nil {
		t.Fatal(err)
	}
	refused := maybeFormatWrittenFileScoped(context.Background(), root, nil, outside, uglyGoSource)
	if refused.Content != uglyGoSource {
		t.Fatalf("formatting outside the write root must be refused, got %q", refused.Content)
	}

	inside := filepath.Join(root, "a.go")
	if err := os.WriteFile(inside, []byte(uglyGoSource), 0o644); err != nil {
		t.Fatal(err)
	}
	formatted := maybeFormatWrittenFileScoped(context.Background(), root, nil, inside, uglyGoSource)
	if !strings.Contains(formatted.Content, "func A() {") {
		t.Fatalf("formatting inside the write root must apply, got %q", formatted.Content)
	}
}

// A physical formatter is handed a staging copy; its working directory must
// still be pinned inside the allowed write root, not at the caller's cwd.
func TestFormatOnWriteScopedStagingRunsInsideWriteRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake formatter shim is a POSIX script")
	}
	t.Setenv("ZERO_FORMAT_ON_WRITE", "1")
	record := filepath.Join(t.TempDir(), "formatter-pwd")
	installFakePhysicalFormatter(t, ".physfmt", "physfmt", record)

	root := t.TempDir()
	target := filepath.Join(root, "sub", "a.physfmt")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("written\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	formatting := maybeFormatWrittenFileScoped(context.Background(), root, nil, target, "written\n")
	if formatting.Content != "written\n" {
		t.Fatalf("physical formatter output = %q, want the written bytes", formatting.Content)
	}
	recorded, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("formatter never recorded its working directory: %v", err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(recorded), resolvedRoot) {
		t.Fatalf("formatter cwd %q escaped the write root %q", recorded, resolvedRoot)
	}
}

// installFakePhysicalFormatter puts a non-stdin formatter on PATH and registers
// it for the test's duration. The script records its working directory and
// leaves the staging file untouched, so the published bytes are the written
// bytes.
func installFakePhysicalFormatter(t *testing.T, extension, name, record string) {
	t.Helper()
	directory := t.TempDir()
	script := "#!/bin/sh\nprintf '%s' \"$PWD\" > \"$ZERO_FORMAT_PWD_RECORD\"\nexit 0\n"
	if err := os.WriteFile(filepath.Join(directory, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ZERO_FORMAT_PWD_RECORD", record)
	previous, existed := formatterCommands[extension]
	formatterCommands[extension] = formatterAdapter{argv: []string{name}}
	t.Cleanup(func() {
		if existed {
			formatterCommands[extension] = previous
			return
		}
		delete(formatterCommands, extension)
	})
}

func TestFormatOnWriteRuffUsesLogicalDestinationForWriteAndEdit(t *testing.T) {
	if _, err := exec.LookPath("ruff"); err != nil {
		t.Skip("ruff not installed")
	}
	t.Setenv("ZERO_FORMAT_ON_WRITE", "1")
	for _, toolName := range []string{"write_file", "edit_file"} {
		t.Run(toolName, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "ruff.toml"), []byte("force-exclude = true\n[format]\nexclude = [\"special.py\"]\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"special.py", "adjacent.py"} {
				t.Run(name, func(t *testing.T) {
					original := "x=  [1,2]\n"
					target := filepath.Join(dir, name)
					if err := os.WriteFile(target, []byte(original), 0o600); err != nil {
						t.Fatal(err)
					}
					tracker := NewFileTracker()
					read := NewScopedReadFileTool(dir, nil).(optionsAwareTool).RunWithOptions(context.Background(), map[string]any{"path": name}, RunOptions{FileTracker: tracker})
					if read.Status != StatusOK {
						t.Fatalf("read_file: %s", read.Output)
					}
					input := original
					want := "x = [1, 2]\n"
					var result Result
					if toolName == "write_file" {
						result = NewScopedWriteFileTool(dir, nil).(optionsAwareTool).RunWithOptions(context.Background(), map[string]any{"path": name, "content": input, "overwrite": true}, RunOptions{FileTracker: tracker})
					} else {
						input = "y=  [3,4]\n"
						want = "y = [3, 4]\n"
						result = NewScopedEditFileTool(dir, nil).(optionsAwareTool).RunWithOptions(context.Background(), map[string]any{"path": name, "old_string": original, "new_string": input}, RunOptions{FileTracker: tracker})
					}
					if result.Status != StatusOK {
						t.Fatalf("%s: %s", toolName, result.Output)
					}
					if name == "special.py" {
						want = input
					}
					got, err := os.ReadFile(target)
					if err != nil || string(got) != want {
						t.Fatalf("%s logical filename lost: got %q, want %q, err %v", toolName, got, want, err)
					}
					assertTrackerMatchesDisk(t, tracker, target)
				})
			}
		})
	}
}
