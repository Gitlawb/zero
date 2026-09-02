package tools

import (
	"context"
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
// hang a tool call. On timeout the unformatted write stands.
const formatOnWriteTimeout = 10 * time.Second

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
// enabled and on PATH) and returns the verified file content afterwards. A
// formatter may mutate the file and then fail or time out, so its process error
// never substitutes the originally requested bytes for a final read. The bool
// is false only when a formatter ran and the resulting file could not be read;
// callers keep ChangedFiles but omit exact rich evidence in that case.
func maybeFormatWrittenFile(ctx context.Context, absolutePath string, writtenContent string) (string, bool) {
	if !formatOnWriteEnabled() {
		return writtenContent, true
	}
	command, ok := formatterCommands[strings.ToLower(filepath.Ext(absolutePath))]
	if !ok {
		return writtenContent, true
	}
	binaryPath, err := exec.LookPath(command[0])
	if err != nil {
		return writtenContent, true
	}
	formatCtx, cancel := context.WithTimeout(ctx, formatOnWriteTimeout)
	defer cancel()
	arguments := append(append([]string(nil), command[1:]...), absolutePath)
	_ = runFormatOnWriteCommand(formatCtx, binaryPath, arguments, filepath.Dir(absolutePath))
	formatted, err := readFormattedFile(absolutePath)
	if err != nil {
		return writtenContent, false
	}
	return string(formatted), true
}
