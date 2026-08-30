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
// edit_file/write_file formats staged content before the single atomic
// publication, so the model's output always lands in project-canonical style
// and never fails a CI format check it cannot see. Off by default (set
// ZERO_FORMAT_ON_WRITE=1): auto-reformatting changes bytes the model did not
// write, which strict workflows may not want.
//
// Ordering matters: formatting runs on a sibling temporary file BEFORE
// publication and BEFORE the FileTracker re-baseline. The caller records the
// POST-format content that was actually published. Formatting the destination
// in place after publication would reintroduce partial-file writes.

// formatOnWriteTimeout bounds one formatter run; a wedged formatter must never
// hang a tool call. On timeout the unformatted staged bytes are published.
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

// maybeFormatWrittenFile runs the configured formatter on a sibling copy of
// writtenContent (when enabled and on PATH) and returns the bytes to publish.
// The destination path is never opened or rewritten here. Best-effort
// throughout: any failure — no formatter, formatter error, timeout, unreadable
// result — returns writtenContent so the caller can atomically publish the
// unformatted staged bytes.
func maybeFormatWrittenFile(ctx context.Context, absolutePath string, writtenContent string) string {
	if !formatOnWriteEnabled() {
		return writtenContent
	}
	ext := strings.ToLower(filepath.Ext(absolutePath))
	command, ok := formatterCommands[ext]
	if !ok {
		return writtenContent
	}
	binaryPath, err := exec.LookPath(command[0])
	if err != nil {
		return writtenContent
	}
	dir := filepath.Dir(absolutePath)
	staging, err := os.CreateTemp(dir, ".zero-fmt-*"+ext)
	if err != nil {
		return writtenContent
	}
	stagingName := staging.Name()
	defer func() { _ = os.Remove(stagingName) }()
	if _, err := staging.WriteString(writtenContent); err != nil {
		_ = staging.Close()
		return writtenContent
	}
	if err := staging.Close(); err != nil {
		return writtenContent
	}
	formatCtx, cancel := context.WithTimeout(ctx, formatOnWriteTimeout)
	defer cancel()
	arguments := append(append([]string(nil), command[1:]...), stagingName)
	formatter := exec.CommandContext(formatCtx, binaryPath, arguments...)
	formatter.Dir = dir
	formatter.Stdin = strings.NewReader("")
	if err := formatter.Run(); err != nil {
		return writtenContent
	}
	formatted, err := os.ReadFile(stagingName)
	if err != nil {
		return writtenContent
	}
	return string(formatted)
}
