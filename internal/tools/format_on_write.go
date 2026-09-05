package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Gitlawb/zero/internal/sandbox"
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

var readFormattedFile = readRootedFile

// maybeFormatWrittenFile runs the configured formatter for absolutePath (when
// enabled and on PATH) and returns the verified file content afterwards. A
// formatter may mutate the file and then fail or time out, so its process error
// never substitutes the originally requested bytes for a final read. The bool
// is false only when a formatter ran and the resulting file could not be read;
// callers keep ChangedFiles but omit exact rich evidence in that case.
func maybeFormatWrittenFile(ctx context.Context, workspaceRoot string, scope PathScope, absolutePath string, writtenContent string) (string, os.FileInfo, bool) {
	if !formatOnWriteEnabled() {
		return writtenContent, nil, true
	}
	command, ok := formatterCommands[strings.ToLower(filepath.Ext(absolutePath))]
	if !ok {
		return writtenContent, nil, true
	}
	binaryPath, err := exec.LookPath(command[0])
	if err != nil {
		return writtenContent, nil, true
	}
	root, relativePath, err := openFormattedFileRoot(workspaceRoot, scope, absolutePath)
	if err != nil {
		return writtenContent, nil, false
	}
	defer root.Close()
	formatCtx, cancel := context.WithTimeout(ctx, formatOnWriteTimeout)
	defer cancel()
	arguments := append(append([]string(nil), command[1:]...), absolutePath)
	_ = runFormatOnWriteCommand(formatCtx, binaryPath, arguments, filepath.Dir(absolutePath))
	formatted, info, err := readFormattedFile(root, relativePath)
	if err != nil {
		return writtenContent, nil, false
	}
	return string(formatted), info, true
}

// openFormattedFileRoot opens the write root before the formatter runs and
// computes the target relative to that descriptor-bound root. Atomic in-root
// replacement remains valid; a formatter that swaps the target to an escaping
// symlink is rejected when readFormattedFile opens it through the root.
func openFormattedFileRoot(workspaceRoot string, scope PathScope, absolutePath string) (*os.Root, string, error) {
	roots, err := scopedRoots(workspaceRoot, scope)
	if err != nil {
		return nil, "", err
	}
	var firstErr error
	for _, configuredRoot := range roots {
		resolvedRoot, err := filepath.Abs(configuredRoot)
		if err == nil {
			resolvedRoot, err = filepath.EvalSymlinks(resolvedRoot)
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		candidate := sandbox.NormalizePrefixForRoot(absolutePath, resolvedRoot)
		relativePath, err := filepath.Rel(resolvedRoot, candidate)
		if err != nil || relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) || filepath.IsAbs(relativePath) {
			continue
		}
		root, err := os.OpenRoot(resolvedRoot)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		return root, relativePath, nil
	}
	if firstErr != nil {
		return nil, "", firstErr
	}
	return nil, "", fmt.Errorf("%s must stay inside the configured write roots", absolutePath)
}
