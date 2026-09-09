package tools

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Format-on-write for the mutating file tools. When enabled, a successful
// edit_file/write_file runs the language's standard formatter on the file it
// just wrote, so the model's output always lands in project-canonical style
// and never fails a CI format check it cannot see. Off by default (set
// ZERO_FORMAT_ON_WRITE=1): auto-reformatting changes bytes the model did not
// write, which strict workflows may not want.
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

// formatterCommands maps a file extension to the formatter argv; the file path
// is appended as the final argument. Only in-place, config-respecting,
// community-standard formatters — a missing binary silently skips formatting.
var formatterCommands = map[string][]string{
	".go":    {"gofmt", "-w"},
	".rs":    {"rustfmt"},
	".py":    {"ruff", "format", "--quiet"},
	".ts":    {"prettier", "--log-level", "silent", "--write"},
	".tsx":   {"prettier", "--log-level", "silent", "--write"},
	".js":    {"prettier", "--log-level", "silent", "--write"},
	".jsx":   {"prettier", "--log-level", "silent", "--write"},
	".json":  {"prettier", "--log-level", "silent", "--write"},
	".css":   {"prettier", "--log-level", "silent", "--write"},
	".scss":  {"prettier", "--log-level", "silent", "--write"},
	".html":  {"prettier", "--log-level", "silent", "--write"},
	".md":    {"prettier", "--log-level", "silent", "--write"},
	".yaml":  {"prettier", "--log-level", "silent", "--write"},
	".yml":   {"prettier", "--log-level", "silent", "--write"},
	".zig":   {"zig", "fmt"},
	".dart":  {"dart", "format"},
	".tf":    {"terraform", "fmt"},
	".gleam": {"gleam", "format"},
	".sh":    {"shfmt", "-w"},
	".bash":  {"shfmt", "-w"},
	".c":     {"clang-format", "-i"},
	".h":     {"clang-format", "-i"},
	".cpp":   {"clang-format", "-i"},
	".hpp":   {"clang-format", "-i"},
	".cc":    {"clang-format", "-i"},
	".kt":    {"ktlint", "-F"},
	".swift": {"swiftformat"},
	".lua":   {"stylua"},
}

// formatOnWriteEnabled reports whether the opt-in env toggle is set.
func formatOnWriteEnabled() bool {
	value := strings.TrimSpace(os.Getenv("ZERO_FORMAT_ON_WRITE"))
	return value != "" && value != "0" && !strings.EqualFold(value, "false")
}

// maybeFormatWrittenFile runs the configured formatter for absolutePath (when
// enabled and on PATH) and returns the file's content afterwards. Best-effort
// throughout: any failure — no formatter, formatter error, timeout, unreadable
// result — returns writtenContent so the caller's state matches the last write
// it performed itself. Only the timeout is reported back, for the reason on
// formatOnWriteResult.
func maybeFormatWrittenFile(ctx context.Context, absolutePath string, writtenContent string) formatOnWriteResult {
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
	formatCtx, cancel := context.WithTimeout(ctx, formatOnWriteTimeout)
	defer cancel()
	arguments := append(append([]string(nil), command[1:]...), absolutePath)
	formatter := exec.CommandContext(formatCtx, binaryPath, arguments...)
	formatter.Dir = filepath.Dir(absolutePath)
	formatter.Stdin = strings.NewReader("")
	if err := formatter.Run(); err != nil {
		// OUR deadline, not the caller's cancellation and not the formatter's own
		// exit status. A cancelled tool call is already being reported as
		// cancelled, and a formatter that ran and refused the file usually means
		// content it could not parse, which the write itself does not promise to
		// fix. Neither is this notice's business.
		if errors.Is(formatCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			unformatted.Formatter = command[0]
			unformatted.TimedOut = true
		}
		return unformatted
	}
	formatted, err := os.ReadFile(absolutePath)
	if err != nil {
		return unformatted
	}
	return formatOnWriteResult{Content: string(formatted), Formatter: command[0]}
}
