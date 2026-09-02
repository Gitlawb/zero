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
	Content      string
	ContentKnown bool
	// Formatter is the binary that was run, named in the notice so the user can
	// tell a slow gofmt from a slow prettier.
	Formatter string
	TimedOut  bool
	// RestoreFailed means the file on disk is not known to hold Content.
	//
	// These formatters edit in place, so one that is killed or fails partway
	// can leave the target truncated or half-rewritten: what a dead
	// `prettier --write` leaves behind is not the input and not the output.
	// Returning the written bytes while disk holds something else would put the
	// tracker baseline, the diff preview and the file itself into three
	// different states, so the failure paths write the bytes back. When even
	// that fails the user has to hear about it: it is their file.
	RestoreFailed bool
}

// notice is the line appended to the tool summary when formatting was expected
// and did not happen, and empty in every other case.
func (result formatOnWriteResult) notice(relativePath string) string {
	if result.RestoreFailed {
		return "\n\nWARNING: " + relativePath + " may not hold what was written. " +
			result.Formatter + " was interrupted while rewriting it in place and the " +
			"content could not be written back. Re-read the file before trusting it."
	}
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

var runFormatOnWriteCommand = func(ctx context.Context, binaryPath string, arguments []string, directory string) error {
	formatter := exec.CommandContext(ctx, binaryPath, arguments...)
	formatter.Dir = directory
	formatter.Stdin = strings.NewReader("")
	return formatter.Run()
}

var readFormattedFile = os.ReadFile

// maybeFormatWrittenFile runs the configured formatter for absolutePath (when
// enabled and on PATH) and returns the verified file content afterwards.
// Formatter failures restore writtenContent; if either that restoration or a
// successful formatter's final read fails, ContentKnown is false so callers do
// not publish exact diff evidence for bytes they could not verify.
func maybeFormatWrittenFile(ctx context.Context, absolutePath string, writtenContent string) formatOnWriteResult {
	unformatted := formatOnWriteResult{Content: writtenContent, ContentKnown: true}
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
	if err := runFormatOnWriteCommand(formatCtx, binaryPath, arguments, filepath.Dir(absolutePath)); err != nil {
		unformatted.Formatter = command[0]
		// THE FORMATTER EDITS IN PLACE, SO A FAILED RUN CAN LEAVE THE FILE
		// NEITHER FORMATTED NOR AS WRITTEN. Killed by the deadline or by the
		// caller, or exiting partway through its own rewrite, the target can hold
		// a truncation. Returning the written bytes on top of that would leave the
		// tracker baseline and the diff preview describing a file that is not on
		// disk, which is a worse failure than the missing formatting: the next
		// edit compares against content the file does not have.
		//
		// Written back unconditionally on this path rather than only when the
		// bytes differ. Comparing first means reading the file to find out, and a
		// read that fails leaves the same ambiguity this exists to remove.
		if restoreErr := os.WriteFile(absolutePath, []byte(writtenContent), 0o644); restoreErr != nil {
			unformatted.RestoreFailed = true
			unformatted.ContentKnown = false
		}
		// OUR deadline, not the caller's cancellation and not the formatter's own
		// exit status. A cancelled tool call is already being reported as
		// cancelled, and a formatter that ran and refused the file usually means
		// content it could not parse, which the write itself does not promise to
		// fix. Neither is this notice's business; the restore above is, for all
		// three.
		if errors.Is(formatCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			unformatted.TimedOut = true
		}
		return unformatted
	}
	formatted, err := readFormattedFile(absolutePath)
	if err != nil {
		unformatted.ContentKnown = false
		return unformatted
	}
	return formatOnWriteResult{Content: string(formatted), ContentKnown: true, Formatter: command[0]}
}
