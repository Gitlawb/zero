package tools

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
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

func TestProtectedCredentialDoesNotDisableFormatOnWriteForOtherFiles(t *testing.T) {
	requireGofmt(t)
	dir := t.TempDir()
	token := filepath.Join(dir, "bridge-token")
	if err := os.WriteFile(token, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZERO_DAEMON_REMOTE_TOKEN", "")
	t.Setenv("ZERO_DAEMON_REMOTE_TOKEN_FILE", token)
	t.Setenv("ZERO_INTERNAL_DAEMON_REMOTE_TOKEN_FILE_RESOLVED", "")
	t.Setenv("ZERO_FORMAT_ON_WRITE", "1")

	result := NewScopedWriteFileTool(dir, nil).(optionsAwareTool).RunWithOptions(context.Background(), map[string]any{
		"path": "ordinary.go", "content": "package ordinary\n\nfunc  F( ) {   }\n",
	}, RunOptions{})
	if result.Status != StatusOK {
		t.Fatalf("write failed: %q", result.Output)
	}
	content, err := os.ReadFile(filepath.Join(dir, "ordinary.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "func F() {") {
		t.Fatalf("formatting was suppressed by unrelated token: %q", content)
	}
}

func TestFormatOnWriteSkipsUnknownExtensions(t *testing.T) {
	t.Setenv("ZERO_FORMAT_ON_WRITE", "1")
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	formatting := maybeFormatWrittenFile(context.Background(), root, "notes.xyz", filepath.Join(dir, "notes.xyz"), dir, "raw   text", 0o644)
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
	root, err := os.OpenRoot(filepath.Dir(targetPath))
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	formatting := maybeFormatWrittenFile(context.Background(), root, filepath.Base(targetPath), targetPath, filepath.Dir(targetPath), uglyContent, 0o644)
	if formatting.Content != uglyContent {
		t.Fatalf("missing formatter must return written content, got %q", formatting.Content)
	}
	if notice := formatting.notice("a.go"); notice != "" {
		t.Fatalf("an uninstalled formatter is a standing fact, not a miss worth reporting, got %q", notice)
	}
}

func TestFormatOnWriteUsesStdinAndScrubsSensitiveEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX formatter fixture")
	}
	dir := t.TempDir()
	formatter := filepath.Join(dir, "zero-test-formatter")
	script := `#!/bin/sh
[ "$#" -eq 0 ] || exit 20
if [ -n "$ZERO_DAEMON_REMOTE_TOKEN" ] || [ -n "$ZERO_DAEMON_REMOTE_TOKEN_FILE" ] || [ -n "$ZERO_INTERNAL_DAEMON_REMOTE_TOKEN_FILE_RESOLVED" ] || [ -n "$ZERO_INTERNAL_DAEMON_REMOTE_TOKEN_FILE_IDENTITY" ]; then exit 21; fi
[ "$FORMATTER_POSITIVE_CONTROL" = visible ] || exit 22
[ "$(cat)" = raw ] || exit 23
printf 'formatted\n'
`
	if err := os.WriteFile(formatter, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	old, existed := formatterCommands[".mock"]
	formatterCommands[".mock"] = []string{formatter}
	defer func() {
		if existed {
			formatterCommands[".mock"] = old
		} else {
			delete(formatterCommands, ".mock")
		}
	}()
	t.Setenv("ZERO_FORMAT_ON_WRITE", "1")
	t.Setenv("ZERO_DAEMON_REMOTE_TOKEN", "inline-secret")
	t.Setenv("ZERO_DAEMON_REMOTE_TOKEN_FILE", "/secret/token")
	t.Setenv("ZERO_INTERNAL_DAEMON_REMOTE_TOKEN_FILE_RESOLVED", "/secret/resolved")
	t.Setenv("ZERO_INTERNAL_DAEMON_REMOTE_TOKEN_FILE_IDENTITY", "startup-identity")
	t.Setenv("FORMATTER_POSITIVE_CONTROL", "visible")
	target := filepath.Join(dir, "target.mock")
	t.Setenv("FORMATTER_ORIGINAL_TARGET", target)
	if err := os.WriteFile(target, []byte("raw\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	formatting := maybeFormatWrittenFile(context.Background(), root, "target.mock", target, dir, "raw\n", 0o644)
	if formatting.Content != "formatted\n" {
		t.Fatalf("stdin formatter result = %q, want formatted content", formatting.Content)
	}
	onDisk, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != formatting.Content {
		t.Fatalf("published content = %q, want %q", onDisk, formatting.Content)
	}
}

// Keep the real argv and replace only the executable with a deterministic CLI
// contract fixture. Replacing the entire command would hide broken production
// flags (lint-only Kotlin can succeed with empty stdout; Dart rejects a dash).
func TestFormatOnWriteStdinCommandContracts(t *testing.T) {
	for _, extension := range []string{".kt", ".dart"} {
		t.Run(extension, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "source file"+extension)
			command := formatterCommands[extension]
			fixture := []string{os.Args[0], "-test.run=^TestFormatOnWriteStdinContractHelper$", "--", command[0]}
			formatterCommands[extension] = append(fixture, command[1:]...)
			t.Cleanup(func() { formatterCommands[extension] = command })
			t.Setenv("ZERO_FORMATTER_CONTRACT_TARGET", target)
			t.Setenv("ZERO_FORMAT_ON_WRITE", "1")
			result := NewScopedWriteFileTool(dir, nil).Run(context.Background(), map[string]any{
				"path": filepath.Base(target), "content": "unformatted source\n",
			})
			if result.Status != StatusOK {
				t.Fatalf("write failed: %q", result.Output)
			}
			content, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			if string(content) != "formatted source\n" {
				t.Fatalf("stdin contract did not publish non-empty formatted source: got %q", content)
			}
		})
	}
}

func TestFormatOnWriteStdinContractHelper(t *testing.T) {
	target := os.Getenv("ZERO_FORMATTER_CONTRACT_TARGET")
	if target == "" {
		return
	}
	separator := slices.Index(os.Args, "--")
	if separator < 0 || separator+1 >= len(os.Args) {
		os.Exit(2)
	}
	arguments := os.Args[separator+1:]
	var want []string
	switch arguments[0] {
	case "ktlint":
		// Lint-only mode succeeds without emitting source for valid Kotlin.
		if slices.Equal(arguments, []string{"ktlint", "--stdin"}) {
			os.Exit(0)
		}
		want = []string{"ktlint", "--format", "--stdin", "--stdin-path", target, "--log-level=none"}
	case "dart":
		// Dart uses stdin only when there are no positional paths.
		want = []string{"dart", "format", "--output=show", "--stdin-name", target}
	default:
		os.Exit(3)
	}
	if !slices.Equal(arguments, want) {
		os.Exit(4)
	}
	content, err := io.ReadAll(os.Stdin)
	if err != nil || string(content) != "unformatted source\n" {
		os.Exit(5)
	}
	if _, err := io.WriteString(os.Stdout, "formatted source\n"); err != nil {
		os.Exit(6)
	}
	os.Exit(0)
}

func TestFormatOnWriteUsesDestinationForProjectConfiguration(t *testing.T) {
	dir := t.TempDir()
	sourceDir := filepath.Join(dir, "src")
	if err := os.Mkdir(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".clang-format"), []byte("IndentWidth: 8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	previous := formatterCommands[".c"]
	formatterCommands[".c"] = []string{os.Args[0], "-test.run=^TestFormatOnWriteProjectConfigHelper$", "--", "--assume-filename=" + formatterPathPlaceholder}
	t.Cleanup(func() { formatterCommands[".c"] = previous })
	t.Setenv("ZERO_FORMATTER_CONFIG_HELPER", "1")
	t.Setenv("ZERO_FORMAT_ON_WRITE", "1")

	result := NewScopedWriteFileTool(dir, nil).(optionsAwareTool).RunWithOptions(context.Background(), map[string]any{
		"path": "src/main.c", "content": "int main() {\n  return 0;\n}\n",
	}, RunOptions{})
	if result.Status != StatusOK {
		t.Fatalf("write failed: %q", result.Output)
	}
	content, err := os.ReadFile(filepath.Join(sourceDir, "main.c"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "\n        return 0;") {
		t.Fatalf("formatter did not discover project config from destination hint: %q", content)
	}
}

func TestFormatOnWriteProjectConfigHelper(t *testing.T) {
	if os.Getenv("ZERO_FORMATTER_CONFIG_HELPER") != "1" {
		return
	}
	separator := -1
	for index, argument := range os.Args {
		if argument == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		os.Exit(2)
	}
	filename := strings.TrimPrefix(os.Args[separator+1], "--assume-filename=")
	configured := false
	for directory := filepath.Dir(filename); ; directory = filepath.Dir(directory) {
		if data, err := os.ReadFile(filepath.Join(directory, ".clang-format")); err == nil && strings.Contains(string(data), "IndentWidth: 8") {
			configured = true
			break
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			break
		}
	}
	if !configured {
		os.Exit(3)
	}
	content, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(4)
	}
	formatted := strings.ReplaceAll(string(content), "\n  return", "\n        return")
	if _, err := io.WriteString(os.Stdout, formatted); err != nil {
		os.Exit(5)
	}
	os.Exit(0)
}

func TestFormatOnWriteRejectsDestinationSwapDuringFormatter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX formatter fixture")
	}
	for _, kind := range []string{"control", "symlink", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			token, target := filepath.Join(dir, "token"), filepath.Join(dir, "ordinary.mock")
			for path, content := range map[string]string{token: "secret", target: "raw"} {
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			helper := filepath.Join(dir, "formatter")
			script := `#!/bin/sh
set -eu
case "$3" in
symlink) rm "$1"; ln -s "$2" "$1";;
hardlink) rm "$1"; ln "$2" "$1";;
esac
cat >/dev/null
printf formatted
`
			if err := os.WriteFile(helper, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			old, existed := formatterCommands[".mock"]
			formatterCommands[".mock"] = []string{helper, target, token, kind}
			t.Cleanup(func() {
				if existed {
					formatterCommands[".mock"] = old
				} else {
					delete(formatterCommands, ".mock")
				}
			})
			t.Setenv("ZERO_FORMAT_ON_WRITE", "1")
			t.Setenv("ZERO_DAEMON_REMOTE_TOKEN", "")
			t.Setenv("ZERO_DAEMON_REMOTE_TOKEN_FILE", token)
			t.Setenv("ZERO_INTERNAL_DAEMON_REMOTE_TOKEN_FILE_RESOLVED", "")
			t.Setenv("ZERO_INTERNAL_DAEMON_REMOTE_TOKEN_FILE_IDENTITY", "")
			root, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			formatting := maybeFormatWrittenFile(context.Background(), root, "ordinary.mock", target, dir, "raw", 0o600)
			want := "raw"
			if kind == "control" {
				want = "formatted"
			} else if targetInfo, err := os.Stat(target); err != nil {
				t.Fatal(err)
			} else if tokenInfo, err := os.Stat(token); err != nil || !os.SameFile(targetInfo, tokenInfo) {
				t.Fatalf("formatter did not exercise alias swap: %v", err)
			}
			if formatting.Content != want {
				t.Fatalf("formatter result = %q, want %q", formatting.Content, want)
			}
			if data, err := os.ReadFile(token); err != nil || string(data) != "secret" {
				t.Fatalf("formatter changed token: %q, %v", data, err)
			}
		})
	}
}

func TestPostWriteFormatterSwapCannotPublishOrObserveToken(t *testing.T) {
	for _, toolName := range []string{"write", "edit"} {
		for _, aliasKind := range []string{"symlink", "hardlink"} {
			t.Run(toolName+"/"+aliasKind, func(t *testing.T) {
				dir := t.TempDir()
				token := filepath.Join(dir, "bridge-token")
				target := filepath.Join(dir, "ordinary.go")
				const secret = "formatter-swap-secret"
				if err := os.WriteFile(token, []byte(secret), 0o600); err != nil {
					t.Fatal(err)
				}
				if toolName == "edit" {
					if err := os.WriteFile(target, []byte("package ordinary\n\nfunc Old() {}\n"), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				t.Setenv("ZERO_FORMAT_ON_WRITE", "1")
				t.Setenv("ZERO_DAEMON_REMOTE_TOKEN", "")
				t.Setenv("ZERO_DAEMON_REMOTE_TOKEN_FILE", token)
				t.Setenv("ZERO_INTERNAL_DAEMON_REMOTE_TOKEN_FILE_RESOLVED", "")
				tracker := NewFileTracker()
				if toolName == "edit" {
					initial := []byte("package ordinary\n\nfunc Old() {}\n")
					info, err := os.Stat(target)
					if err != nil {
						t.Fatal(err)
					}
					tracker.Record(target, initial, info)
					tracker.RecordSeenRange(target, 1, 3, 3)
				}
				diagnosticsCalled := false
				options := RunOptions{FileTracker: tracker, Diagnostics: func(context.Context, string) string {
					diagnosticsCalled = true
					return secret
				}}
				var result Result
				formatter := func(_ context.Context, _ *os.Root, _, _, _, written string, _ os.FileMode) formatOnWriteResult {
					// The injected formatter boundary is entered only after the rooted
					// write and returns immediately before publication/post-write read.
					if err := os.Remove(target); err != nil {
						t.Fatal(err)
					}
					var err error
					if aliasKind == "symlink" {
						err = os.Symlink(token, target)
					} else {
						err = os.Link(token, target)
					}
					if err != nil {
						t.Skipf("%s unavailable: %v", aliasKind, err)
					}
					return formatOnWriteResult{Content: written}
				}
				if toolName == "write" {
					tool := NewScopedWriteFileTool(dir, nil).(writeFileTool)
					tool.formatter = formatter
					result = tool.RunWithOptions(context.Background(), map[string]any{
						"path": "ordinary.go", "content": "package ordinary\n\nfunc  F( ) { }\n",
					}, options)
				} else {
					tool := NewScopedEditFileTool(dir, nil).(editFileTool)
					tool.formatter = formatter
					result = tool.RunWithOptions(context.Background(), map[string]any{
						"path": "ordinary.go", "old_string": "Old", "new_string": "F",
					}, options)
				}
				if result.Status != StatusError {
					t.Fatalf("swapped %s result status = %s, want error: %q", aliasKind, result.Status, result.Output)
				}
				if diagnosticsCalled || strings.Contains(result.Output, secret) || strings.Contains(result.Display.Preview, secret) {
					t.Fatalf("token reached a post-write consumer: diagnostics=%v output=%q preview=%q", diagnosticsCalled, result.Output, result.Display.Preview)
				}
				version, tracked := tracker.Version(target)
				if toolName == "write" && tracked {
					t.Fatal("swapped token alias was recorded by FileTracker")
				}
				if toolName == "edit" && (!tracked || version.Hash != HashContent([]byte("package ordinary\n\nfunc Old() {}\n"))) {
					t.Fatalf("FileTracker consumed swapped alias: tracked=%v version=%+v", tracked, version)
				}
				got, err := os.ReadFile(token)
				if err != nil || string(got) != secret {
					t.Fatalf("token changed: content=%q err=%v", got, err)
				}
			})
		}
	}
}
