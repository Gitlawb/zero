package tools

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Gitlawb/zero/internal/sandbox"
)

// Format-on-write for the mutating file tools. When enabled, a successful
// edit_file/write_file runs the language's standard formatter on detached
// content and publishes it through the protected write primitive. Formatters
// retain the original working directory, but settings discovered solely from
// the input file's ancestors may differ because staging is outside the
// workspace. Off by default (set ZERO_FORMAT_ON_WRITE=1): auto-reformatting
// changes bytes the model did not write, which strict workflows may not want.
//
// Ordering matters: formatting runs BEFORE the FileTracker re-baseline, and
// the caller records the POST-format content. Formatting after the baseline
// would make the very next edit look like an external modification and trip
// the conflict guard.

// formatOnWriteTimeout bounds one formatter run; a wedged formatter must never
// hang a tool call. On timeout the unformatted write stands.
const formatOnWriteTimeout = 10 * time.Second

type writtenFileFormatter func(context.Context, *os.Root, string, string, string, string, os.FileMode) string

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

// maybeFormatWrittenFile formats a detached staging file populated only from
// writtenContent. The formatter never receives the destination path. The
// staged result is read from a rooted, credential-checked handle and published
// through the same protected handle primitive as the original write.
func maybeFormatWrittenFile(ctx context.Context, root *os.Root, relativePath, absolutePath, workspaceRoot, writtenContent string, mode os.FileMode) string {
	if !formatOnWriteEnabled() {
		return writtenContent
	}
	command, ok := formatterCommands[strings.ToLower(filepath.Ext(absolutePath))]
	if !ok {
		return writtenContent
	}
	binaryPath, err := exec.LookPath(command[0])
	if err != nil {
		return writtenContent
	}
	// Keep the formatter's input outside the mutable workspace. A stage next
	// to the destination could itself be swapped to a token alias before the
	// external formatter opens it. CWD remains the original directory for
	// formatters that discover project settings from their working directory.
	stageDir, err := os.MkdirTemp("", "zero-format-*")
	if err != nil {
		return writtenContent
	}
	defer os.RemoveAll(stageDir)
	stageRoot, err := os.OpenRoot(stageDir)
	if err != nil {
		return writtenContent
	}
	defer stageRoot.Close()
	stageRelative := filepath.Base(absolutePath)
	stagePath := filepath.Join(stageDir, stageRelative)
	stage, err := stageRoot.OpenFile(stageRelative, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return writtenContent
	}
	if _, err := io.WriteString(stage, writtenContent); err != nil {
		stage.Close()
		return writtenContent
	}
	if err := stage.Close(); err != nil {
		return writtenContent
	}
	formatCtx, cancel := context.WithTimeout(ctx, formatOnWriteTimeout)
	defer cancel()
	arguments := append(append([]string(nil), command[1:]...), stagePath)
	formatter := exec.CommandContext(formatCtx, binaryPath, arguments...)
	formatter.Dir = filepath.Dir(absolutePath)
	formatter.Stdin = strings.NewReader("")
	formatter.Env = sandbox.ScrubSensitiveEnv(os.Environ())
	if err := formatter.Run(); err != nil {
		return writtenContent
	}
	formattedFile, _, err := protectedRootRead(stageRoot, stageRelative, stagePath, workspaceRoot)
	if err != nil {
		return writtenContent
	}
	formatted, readErr := io.ReadAll(formattedFile)
	closeErr := formattedFile.Close()
	if readErr != nil || closeErr != nil {
		return writtenContent
	}
	if _, err := writeRootedFile(root, relativePath, absolutePath, workspaceRoot, formatted, mode, false); err != nil {
		return writtenContent
	}
	return string(formatted)
}

// readPublishedContent binds all post-write consumers (tracker, preview and
// diagnostics) to a newly opened, protected handle. In particular, a pathname
// swapped after the initial rooted write cannot make those consumers ingest a
// credential even when formatting is disabled or best-effort formatting stops.
func readPublishedContent(root *os.Root, relativePath, absolutePath, workspaceRoot string) (string, error) {
	file, _, err := protectedRootRead(root, relativePath, absolutePath, workspaceRoot)
	if err != nil {
		return "", err
	}
	content, readErr := io.ReadAll(file)
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return "", errors.Join(readErr, closeErr)
	}
	return string(content), nil
}
