package tools

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Gitlawb/zero/internal/sandbox"
)

// Format-on-write for the mutating file tools. When enabled, a successful
// edit_file/write_file sends the written content to the language's standard
// formatter over stdin, using the destination only as a project-config filename
// hint, then publishes stdout through the protected write primitive. Off by
// default (set ZERO_FORMAT_ON_WRITE=1): auto-reformatting changes bytes the
// model did not write, which strict workflows may not want.
//
// Ordering matters: formatting runs BEFORE the FileTracker re-baseline, and
// the caller records the POST-format content. Formatting after the baseline
// would make the very next edit look like an external modification and trip
// the conflict guard.

// formatOnWriteTimeout bounds one formatter run; a wedged formatter must never
// hang a tool call. On timeout the unformatted write stands, and the caller
// says so: see formatOnWriteResult.
//
// A var rather than a const so a test can shorten it. Nothing outside a test
// assigns to it, and the deadline path was previously unreachable in a test at
// any speed, which is part of why it went unnoticed that it reported nothing.
var formatOnWriteTimeout = 10 * time.Second

// formatOnWriteResult is the content after formatting, plus whether the
// formatting that was supposed to happen actually did.
//
// A TIMEOUT IS NOT THE SAME KIND OF MISS AS THE OTHERS. Every other way this
// falls back is a standing fact about the environment: the toggle is off, the
// extension has no formatter, the binary is not installed. Those are silent on
// purpose, because nothing is wrong and saying so on every write would be
// noise. A deadline firing is different: formatting was configured, available
// and expected, and the file was written unformatted anyway, on a machine that
// was merely slow. Left silent, the caller believes it wrote canonical style
// and finds out from a CI format check it cannot see, which is the thing this
// feature exists to prevent.
type formatOnWriteResult struct {
	Content string
	// Formatter is the binary that was run, named in the notice so the user can
	// tell a slow gofmt from a slow prettier.
	Formatter string
	TimedOut  bool
}

// notice is the line appended to the tool summary when formatting was expected
// and did not happen, and empty in every other case.
func (result formatOnWriteResult) notice(relativePath string) string {
	if !result.TimedOut {
		return ""
	}
	return "\n\nNote: " + relativePath + " was written but not formatted: " +
		result.Formatter + " did not finish within " + formatOnWriteTimeout.String() +
		". The file holds exactly what was written, so a project format check may still flag it."
}

type writtenFileFormatter func(context.Context, *os.Root, string, string, string, string, os.FileMode) formatOnWriteResult

const formatterPathPlaceholder = "{zero_file_path}"

// formatterCommands maps a file extension to a formatter argv. The placeholder
// is replaced with the destination path solely as the formatter's filename hint.
// All formatters consume stdin and emit formatted content on stdout; a missing
// binary silently skips formatting.
var formatterCommands = map[string][]string{
	".go":    {"gofmt"},
	".rs":    {"rustfmt", "--emit", "stdout"},
	".py":    {"ruff", "format", "--quiet", "--stdin-filename", formatterPathPlaceholder, "-"},
	".ts":    {"prettier", "--log-level", "silent", "--stdin-filepath", formatterPathPlaceholder},
	".tsx":   {"prettier", "--log-level", "silent", "--stdin-filepath", formatterPathPlaceholder},
	".js":    {"prettier", "--log-level", "silent", "--stdin-filepath", formatterPathPlaceholder},
	".jsx":   {"prettier", "--log-level", "silent", "--stdin-filepath", formatterPathPlaceholder},
	".json":  {"prettier", "--log-level", "silent", "--stdin-filepath", formatterPathPlaceholder},
	".css":   {"prettier", "--log-level", "silent", "--stdin-filepath", formatterPathPlaceholder},
	".scss":  {"prettier", "--log-level", "silent", "--stdin-filepath", formatterPathPlaceholder},
	".html":  {"prettier", "--log-level", "silent", "--stdin-filepath", formatterPathPlaceholder},
	".md":    {"prettier", "--log-level", "silent", "--stdin-filepath", formatterPathPlaceholder},
	".yaml":  {"prettier", "--log-level", "silent", "--stdin-filepath", formatterPathPlaceholder},
	".yml":   {"prettier", "--log-level", "silent", "--stdin-filepath", formatterPathPlaceholder},
	".zig":   {"zig", "fmt", "--stdin"},
	".dart":  {"dart", "format", "--output=show", "--stdin-name", formatterPathPlaceholder},
	".tf":    {"terraform", "fmt", "-"},
	".gleam": {"gleam", "format", "--stdin"},
	".sh":    {"shfmt", "--filename", formatterPathPlaceholder},
	".bash":  {"shfmt", "--filename", formatterPathPlaceholder},
	".c":     {"clang-format", "--assume-filename=" + formatterPathPlaceholder},
	".h":     {"clang-format", "--assume-filename=" + formatterPathPlaceholder},
	".cpp":   {"clang-format", "--assume-filename=" + formatterPathPlaceholder},
	".hpp":   {"clang-format", "--assume-filename=" + formatterPathPlaceholder},
	".cc":    {"clang-format", "--assume-filename=" + formatterPathPlaceholder},
	".kt":    {"ktlint", "--format", "--stdin", "--stdin-path", formatterPathPlaceholder, "--log-level=none"},
	".swift": {"swiftformat", "--stdinpath", formatterPathPlaceholder},
	".lua":   {"stylua", "--stdin-filepath", formatterPathPlaceholder, "-"},
}

// formatOnWriteEnabled reports whether the opt-in env toggle is set.
func formatOnWriteEnabled() bool {
	value := strings.TrimSpace(os.Getenv("ZERO_FORMAT_ON_WRITE"))
	return value != "" && value != "0" && !strings.EqualFold(value, "false")
}

// maybeFormatWrittenFile formats writtenContent over stdin and publishes stdout
// through the rooted, credential-checked write path. The destination pathname is
// passed only through each formatter's filename-hint option so project settings
// resolve from the same ancestors as the real file.
func maybeFormatWrittenFile(ctx context.Context, root *os.Root, relativePath, absolutePath, workspaceRoot, writtenContent string, mode os.FileMode) formatOnWriteResult {
	unformatted := formatOnWriteResult{Content: writtenContent}
	if !formatOnWriteEnabled() {
		return unformatted
	}
	command, ok := formatterCommands[strings.ToLower(filepath.Ext(absolutePath))]
	if !ok {
		return unformatted
	}
	binaryPath, err := exec.LookPath(command[0])
	if err != nil {
		return unformatted
	}
	arguments := make([]string, len(command)-1)
	for index, argument := range command[1:] {
		arguments[index] = strings.ReplaceAll(argument, formatterPathPlaceholder, absolutePath)
	}
	formatCtx, cancel := context.WithTimeout(ctx, formatOnWriteTimeout)
	defer cancel()
	formatter := exec.CommandContext(formatCtx, binaryPath, arguments...)
	// Stdout is a pipe, so Run waits for every holder of it, not only the
	// process the deadline kills. A formatter launched through a shim (every
	// npm-installed one on Windows is a .cmd) leaves the real formatter holding
	// that pipe after the shim dies. Kill the whole tree and bound the wait.
	hardenProcessLifetime(formatter)
	formatter.Dir = filepath.Dir(absolutePath)
	formatter.Stdin = strings.NewReader(writtenContent)
	var stdout bytes.Buffer
	formatter.Stdout = &stdout
	formatter.Env = sandbox.ScrubSensitiveEnv(os.Environ())
	if err := formatter.Run(); err != nil {
		unformatted.Formatter = command[0]
		if errors.Is(formatCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			unformatted.TimedOut = true
		}
		return unformatted
	}
	formatted := stdout.Bytes()
	// A formatter that declines a file can answer with silence and exit 0:
	// clang-format does for a path matched by .clang-format-ignore. Publishing
	// that would empty the file while the write reports success. Nothing went
	// wrong, so there is no notice; the written bytes simply stand.
	if len(bytes.TrimSpace(formatted)) == 0 && strings.TrimSpace(writtenContent) != "" {
		return unformatted
	}
	if _, err := writeRootedFile(root, relativePath, absolutePath, workspaceRoot, formatted, mode, false); err != nil {
		return unformatted
	}
	return formatOnWriteResult{Content: string(formatted), Formatter: command[0]}
}
