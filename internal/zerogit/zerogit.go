package zerogit

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Gitlawb/zero/internal/redaction"
)

type Runner func(context.Context, string, ...string) (CommandResult, error)

type CommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

type InspectOptions struct {
	Cwd          string
	MaxDiffBytes int
	RunGit       Runner
}

type CommitOptions struct {
	Cwd          string
	Message      string
	DryRun       bool
	MaxDiffBytes int
	RunGit       Runner
}

type FileChange struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	Staged    bool   `json:"staged,omitempty"`
	Unstaged  bool   `json:"unstaged,omitempty"`
	Untracked bool   `json:"untracked,omitempty"`
}

type ChangeSummary struct {
	Root      string       `json:"root"`
	Branch    string       `json:"branch,omitempty"`
	Commit    string       `json:"commit,omitempty"`
	Clean     bool         `json:"clean"`
	Files     []FileChange `json:"files"`
	DiffStat  string       `json:"diffStat,omitempty"`
	Diff      string       `json:"diff,omitempty"`
	Truncated bool         `json:"truncated,omitempty"`
}

type CommitResult struct {
	Root       string        `json:"root"`
	Message    string        `json:"message"`
	DryRun     bool          `json:"dryRun"`
	Committed  bool          `json:"committed"`
	CommitHash string        `json:"commitHash,omitempty"`
	Before     ChangeSummary `json:"before"`
}

const defaultMaxDiffBytes = 120000

func Inspect(ctx context.Context, options InspectOptions) (ChangeSummary, error) {
	cwd, err := resolveCwd(options.Cwd)
	if err != nil {
		return ChangeSummary{}, err
	}
	runGit := options.RunGit
	if runGit == nil {
		runGit = defaultRunGit
	}

	root, err := gitOutput(ctx, runGit, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return ChangeSummary{}, fmt.Errorf("not a git repository: %w", err)
	}
	root = filepath.Clean(root)
	branch, _ := gitOutput(ctx, runGit, root, "rev-parse", "--abbrev-ref", "HEAD")
	commit, _ := gitOutput(ctx, runGit, root, "rev-parse", "--short", "HEAD")
	status, err := gitRawOutput(ctx, runGit, root, "status", "--short", "--untracked-files=all")
	if err != nil {
		return ChangeSummary{}, fmt.Errorf("inspect git status: %w", err)
	}
	diffStat, err := gitRawOutput(ctx, runGit, root, "diff", "--stat", "HEAD", "--")
	if err != nil {
		return ChangeSummary{}, fmt.Errorf("inspect git diff stat: %w", err)
	}
	diff, err := gitRawOutput(ctx, runGit, root, "diff", "HEAD", "--")
	if err != nil {
		return ChangeSummary{}, fmt.Errorf("inspect git diff: %w", err)
	}

	maxDiffBytes := firstPositive(options.MaxDiffBytes, defaultMaxDiffBytes)
	redactedDiff, truncated := truncateString(redactText(diff), maxDiffBytes)
	files := parseStatus(status)
	return ChangeSummary{
		Root:      root,
		Branch:    redactText(branch),
		Commit:    redactText(commit),
		Clean:     len(files) == 0,
		Files:     files,
		DiffStat:  redactText(diffStat),
		Diff:      redactedDiff,
		Truncated: truncated,
	}, nil
}

func Commit(ctx context.Context, options CommitOptions) (CommitResult, error) {
	summary, err := Inspect(ctx, InspectOptions{
		Cwd:          options.Cwd,
		MaxDiffBytes: options.MaxDiffBytes,
		RunGit:       options.RunGit,
	})
	if err != nil {
		return CommitResult{}, err
	}
	if summary.Clean {
		return CommitResult{}, fmt.Errorf("no changes to commit")
	}
	message := strings.TrimSpace(options.Message)
	if message == "" {
		message = GenerateMessage(summary)
	}
	if err := ValidateMessage(message); err != nil {
		return CommitResult{}, err
	}
	result := CommitResult{
		Root:      summary.Root,
		Message:   message,
		DryRun:    options.DryRun,
		Committed: false,
		Before:    summary,
	}
	if options.DryRun {
		return result, nil
	}

	runGit := options.RunGit
	if runGit == nil {
		runGit = defaultRunGit
	}
	if _, err := gitOutput(ctx, runGit, summary.Root, "add", "-A"); err != nil {
		return CommitResult{}, fmt.Errorf("stage changes: %w", err)
	}
	if _, err := gitOutput(ctx, runGit, summary.Root, "commit", "-m", message); err != nil {
		return CommitResult{}, fmt.Errorf("commit changes: %w", err)
	}
	hash, err := gitOutput(ctx, runGit, summary.Root, "rev-parse", "--short", "HEAD")
	if err != nil {
		return CommitResult{}, fmt.Errorf("resolve created commit: %w", err)
	}
	result.Committed = true
	result.CommitHash = redactText(hash)
	return result, nil
}

func GenerateMessage(summary ChangeSummary) string {
	count := len(summary.Files)
	switch count {
	case 0:
		return "Update workspace"
	case 1:
		return truncateSubject("Update " + summary.Files[0].Path)
	default:
		return fmt.Sprintf("Update %d files", count)
	}
}

func ValidateMessage(message string) error {
	trimmed := strings.TrimSpace(message)
	if trimmed == "" {
		return fmt.Errorf("commit message is required")
	}
	firstLine := strings.Split(trimmed, "\n")[0]
	if len(firstLine) > 72 {
		return fmt.Errorf("commit message subject must be 72 characters or fewer")
	}
	return nil
}

func parseStatus(status string) []FileChange {
	files := []FileChange{}
	for _, line := range strings.Split(status, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(line) < 3 {
			continue
		}
		code := line[:2]
		path := strings.TrimSpace(line[3:])
		if path == "" {
			continue
		}
		files = append(files, FileChange{
			Path:      redactText(path),
			Status:    statusName(code),
			Staged:    code[0] != ' ' && code[0] != '?',
			Unstaged:  code[1] != ' ' && code[1] != '?',
			Untracked: code == "??",
		})
	}
	return files
}

func statusName(code string) string {
	if code == "??" {
		return "untracked"
	}
	if strings.Contains(code, "U") {
		return "conflicted"
	}
	if code[0] == 'A' || code[1] == 'A' {
		return "added"
	}
	if code[0] == 'D' || code[1] == 'D' {
		return "deleted"
	}
	if code[0] == 'R' || code[1] == 'R' {
		return "renamed"
	}
	if code[0] == 'C' || code[1] == 'C' {
		return "copied"
	}
	return "modified"
}

func resolveCwd(cwd string) (string, error) {
	if strings.TrimSpace(cwd) == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve git cwd: %w", err)
		}
	}
	absolute, err := filepath.Abs(cwd)
	if err != nil {
		return "", fmt.Errorf("resolve git cwd: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("git cwd must be an existing directory: %s", absolute)
	}
	return filepath.Clean(absolute), nil
}

func gitOutput(ctx context.Context, runGit Runner, dir string, args ...string) (string, error) {
	output, err := gitRawOutput(ctx, runGit, dir, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(output), nil
}

func gitRawOutput(ctx context.Context, runGit Runner, dir string, args ...string) (string, error) {
	result, err := runGit(ctx, dir, args...)
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		message := strings.TrimSpace(firstNonEmpty(result.Stderr, result.Stdout))
		if message == "" {
			message = fmt.Sprintf("git exited with code %d", result.ExitCode)
		}
		return "", fmt.Errorf("%s", redactText(message))
	}
	return result.Stdout, nil
}

func defaultRunGit(ctx context.Context, dir string, args ...string) (CommandResult, error) {
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = dir
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	exitCode := 0
	if err != nil {
		exitCode = -1
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
			err = nil
		}
	}
	return CommandResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: exitCode}, err
}

func truncateString(value string, maxBytes int) (string, bool) {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value, false
	}
	suffix := "\n[truncated]"
	if maxBytes <= len(suffix) {
		return suffix, true
	}
	head := value[:maxBytes-len(suffix)]
	if strings.Contains(value, redaction.RedactedSecret) && !strings.Contains(head, redaction.RedactedSecret) {
		marker := "\n" + redaction.RedactedSecret
		budget := maxBytes - len(suffix) - len(marker)
		if budget <= 0 {
			return redaction.RedactedSecret + suffix, true
		}
		return value[:budget] + marker + suffix, true
	}
	return head + suffix, true
}

func truncateSubject(value string) string {
	if len(value) <= 72 {
		return value
	}
	return strings.TrimSpace(value[:69]) + "..."
}

func redactText(value string) string {
	return redaction.RedactString(value, redaction.Options{})
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
