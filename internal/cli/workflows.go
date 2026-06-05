package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/Gitlawb/zero/internal/verify"
	"github.com/Gitlawb/zero/internal/worktrees"
)

type worktreeCommandOptions struct {
	json    bool
	name    string
	baseDir string
	cwd     string
}

type verifyCommandOptions struct {
	json      bool
	cwd       string
	only      []string
	timeoutMS int
}

func runWorktrees(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	command := "prepare"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command = strings.ToLower(strings.TrimSpace(args[0]))
		args = args[1:]
	}
	if command == "help" {
		if err := writeWorktreesHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if command != "prepare" {
		return writeExecUsageError(stderr, fmt.Sprintf("unknown worktrees command %q", command))
	}
	options, help, err := parseWorktreeCommandArgs(args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if err := writeWorktreesHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	workspaceRoot, err := resolveWorkspaceRoot(options.cwd, deps)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	result, err := deps.prepareWorktree(context.Background(), worktrees.Options{
		Cwd:     workspaceRoot,
		Name:    options.name,
		BaseDir: options.baseDir,
		Now:     deps.now,
	})
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if options.json {
		if err := writePrettyJSON(stdout, result); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintln(stdout, formatWorktreeResult(result)); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func runVerifyCommand(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	options, help, err := parseVerifyCommandArgs(args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if err := writeVerifyHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	workspaceRoot, err := resolveWorkspaceRoot(options.cwd, deps)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	plan, err := deps.detectVerifyPlan(workspaceRoot)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	report := deps.runVerify(context.Background(), plan, verify.RunOptions{
		Only:      options.only,
		TimeoutMS: options.timeoutMS,
		Now:       deps.now,
	})
	if options.json {
		if err := writePrettyJSON(stdout, report); err != nil {
			return exitCrash
		}
	} else if _, err := fmt.Fprintln(stdout, formatVerifyReport(report)); err != nil {
		return exitCrash
	}
	if !report.OK {
		return exitProvider
	}
	return exitSuccess
}

func parseWorktreeCommandArgs(args []string) (worktreeCommandOptions, bool, error) {
	options := worktreeCommandOptions{}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "-h" || arg == "--help" || arg == "help":
			return options, true, nil
		case arg == "--json":
			options.json = true
		case arg == "--name":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.name = value
			index = next
		case strings.HasPrefix(arg, "--name="):
			options.name = strings.TrimSpace(strings.TrimPrefix(arg, "--name="))
		case arg == "--dir":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.baseDir = value
			index = next
		case strings.HasPrefix(arg, "--dir="):
			options.baseDir = strings.TrimSpace(strings.TrimPrefix(arg, "--dir="))
		case arg == "-C" || arg == "--cwd":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.cwd = value
			index = next
		case strings.HasPrefix(arg, "--cwd="):
			options.cwd = strings.TrimSpace(strings.TrimPrefix(arg, "--cwd="))
		case strings.HasPrefix(arg, "-"):
			return options, false, execUsageError{fmt.Sprintf("unknown worktrees flag %q", arg)}
		default:
			if options.name != "" {
				return options, false, execUsageError{fmt.Sprintf("unexpected worktrees argument %q", arg)}
			}
			options.name = strings.TrimSpace(arg)
		}
	}
	return options, false, nil
}

func parseVerifyCommandArgs(args []string) (verifyCommandOptions, bool, error) {
	options := verifyCommandOptions{}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "-h" || arg == "--help" || arg == "help":
			return options, true, nil
		case arg == "--json":
			options.json = true
		case arg == "-C" || arg == "--cwd":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.cwd = value
			index = next
		case strings.HasPrefix(arg, "--cwd="):
			options.cwd = strings.TrimSpace(strings.TrimPrefix(arg, "--cwd="))
		case arg == "--only":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.only = append(options.only, parseToolList(value)...)
			index = next
		case strings.HasPrefix(arg, "--only="):
			options.only = append(options.only, parseToolList(strings.TrimSpace(strings.TrimPrefix(arg, "--only=")))...)
		case arg == "--timeout-ms":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			timeoutMS, err := parsePositiveIntFlag("--timeout-ms", value)
			if err != nil {
				return options, false, err
			}
			options.timeoutMS = timeoutMS
			index = next
		case strings.HasPrefix(arg, "--timeout-ms="):
			timeoutMS, err := parsePositiveIntFlag("--timeout-ms", strings.TrimSpace(strings.TrimPrefix(arg, "--timeout-ms=")))
			if err != nil {
				return options, false, err
			}
			options.timeoutMS = timeoutMS
		case strings.HasPrefix(arg, "-"):
			return options, false, execUsageError{fmt.Sprintf("unknown verify flag %q", arg)}
		default:
			return options, false, execUsageError{fmt.Sprintf("unexpected verify argument %q", arg)}
		}
	}
	return options, false, nil
}

func parsePositiveIntFlag(flag string, value string) (int, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, execUsageError{flag + " requires a value"}
	}
	number, err := strconv.Atoi(trimmed)
	if err != nil || number <= 0 {
		return 0, execUsageError{fmt.Sprintf("invalid %s %q. Expected a positive integer.", flag, value)}
	}
	return number, nil
}

func formatWorktreeResult(result worktrees.Result) string {
	lines := []string{
		"Zero worktree ready",
		"name: " + result.Name,
		"path: " + result.Path,
		"repo: " + result.RepoRoot,
	}
	if result.SourceBranch != "" {
		lines = append(lines, "branch: "+result.SourceBranch)
	}
	if result.SourceCommit != "" {
		lines = append(lines, "commit: "+result.SourceCommit)
	}
	if result.Reused {
		lines = append(lines, "reused: true")
	}
	return strings.Join(lines, "\n")
}

func formatVerifyReport(report verify.Report) string {
	lines := []string{
		"Zero verification",
		"root: " + report.Root,
		fmt.Sprintf("summary: %d total, %d passed, %d failed, %d errors", report.Summary.Total, report.Summary.Passed, report.Summary.Failed, report.Summary.Errors),
	}
	if len(report.Results) == 0 {
		lines = append(lines, "  (no checks detected)")
		return strings.Join(lines, "\n")
	}
	for _, result := range report.Results {
		lines = append(lines, fmt.Sprintf("  [%s] %s - %s", result.Status, result.ID, strings.Join(result.Command, " ")))
		if result.Error != "" {
			lines = append(lines, "    error: "+result.Error)
		}
	}
	return strings.Join(lines, "\n")
}

func writeWorktreesHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero worktrees prepare [flags] [name]

Prepares an isolated git worktree for a Zero task.

Flags:
      --name <name>       Worktree name; defaults to a timestamped task name
      --dir <path>        Base directory for Zero worktrees
  -C, --cwd <path>        Source repository directory
      --json              Print JSON output
  -h, --help              Show this help
`)
	return err
}

func writeVerifyHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero verify [flags]

Detects and runs local verification checks for the workspace.

Flags:
  -C, --cwd <path>        Workspace directory
      --only <ids>        Run only matching check ids
      --timeout-ms <n>    Per-check timeout in milliseconds
      --json              Print JSON output
  -h, --help              Show this help
`)
	return err
}
