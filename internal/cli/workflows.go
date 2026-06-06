package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/Gitlawb/zero/internal/redaction"
	"github.com/Gitlawb/zero/internal/selfverify"
	"github.com/Gitlawb/zero/internal/testrunner"
	"github.com/Gitlawb/zero/internal/verify"
	"github.com/Gitlawb/zero/internal/worktrees"
	"github.com/Gitlawb/zero/internal/zerogit"
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
	attempts  int
}

type changesCommandOptions struct {
	json         bool
	cwd          string
	message      string
	dryRun       bool
	maxDiffBytes int
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
	safeResult := redactWorktreeResult(result)
	if options.json {
		if err := writePrettyJSON(stdout, safeResult); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintln(stdout, formatWorktreeResult(safeResult)); err != nil {
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
	if options.attempts > 1 {
		loopReport := deps.runSelfVerify(context.Background(), plan, selfverify.Options{
			RunOptions: verify.RunOptions{
				Only:      options.only,
				TimeoutMS: options.timeoutMS,
				Now:       deps.now,
			},
			MaxAttempts: options.attempts,
		})
		safeLoopReport := redactVerifyLoopReport(loopReport)
		if options.json {
			if err := writePrettyJSON(stdout, selfverify.SnapshotFromReport(safeLoopReport)); err != nil {
				return exitCrash
			}
		} else if _, err := fmt.Fprintln(stdout, formatVerifyLoopReport(safeLoopReport)); err != nil {
			return exitCrash
		}
		if !loopReport.OK {
			return exitProvider
		}
		return exitSuccess
	}
	report := deps.runVerify(context.Background(), plan, verify.RunOptions{
		Only:      options.only,
		TimeoutMS: options.timeoutMS,
		Now:       deps.now,
	})
	safeReport := redactVerifyReport(report)
	if options.json {
		if err := writePrettyJSON(stdout, verify.SnapshotFromReport(safeReport)); err != nil {
			return exitCrash
		}
	} else if _, err := fmt.Fprintln(stdout, formatVerifyReport(safeReport)); err != nil {
		return exitCrash
	}
	if !report.OK {
		return exitProvider
	}
	return exitSuccess
}

func runChanges(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	command := "inspect"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command = strings.ToLower(strings.TrimSpace(args[0]))
		args = args[1:]
	}
	if command == "help" {
		if err := writeChangesHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	options, help, err := parseChangesArgs(args, command)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if err := writeChangesHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	workspaceRoot, err := resolveWorkspaceRoot(options.cwd, deps)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	switch command {
	case "inspect", "status":
		summary, err := deps.inspectChanges(context.Background(), zerogit.InspectOptions{
			Cwd:          workspaceRoot,
			MaxDiffBytes: options.maxDiffBytes,
		})
		if err != nil {
			return writeExecUsageError(stderr, err.Error())
		}
		safeSummary := redactChangeSummary(summary)
		if options.json {
			if err := writePrettyJSON(stdout, zerogit.SnapshotFromSummary(safeSummary)); err != nil {
				return exitCrash
			}
			return exitSuccess
		}
		if _, err := fmt.Fprintln(stdout, formatChangeSummary(safeSummary)); err != nil {
			return exitCrash
		}
		return exitSuccess
	case "commit":
		result, err := deps.commitChanges(context.Background(), zerogit.CommitOptions{
			Cwd:          workspaceRoot,
			Message:      options.message,
			DryRun:       options.dryRun,
			MaxDiffBytes: options.maxDiffBytes,
		})
		if err != nil {
			return writeExecUsageError(stderr, err.Error())
		}
		safeResult := redactCommitResult(result)
		if options.json {
			if err := writePrettyJSON(stdout, safeResult); err != nil {
				return exitCrash
			}
			return exitSuccess
		}
		if _, err := fmt.Fprintln(stdout, formatCommitResult(safeResult)); err != nil {
			return exitCrash
		}
		return exitSuccess
	default:
		return writeExecUsageError(stderr, fmt.Sprintf("unknown changes command %q", command))
	}
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
			if err := setWorktreeName(&options, value); err != nil {
				return options, false, err
			}
			index = next
		case strings.HasPrefix(arg, "--name="):
			if err := setWorktreeName(&options, strings.TrimPrefix(arg, "--name=")); err != nil {
				return options, false, err
			}
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
			if err := setWorktreeName(&options, arg); err != nil {
				return options, false, err
			}
		}
	}
	return options, false, nil
}

func setWorktreeName(options *worktreeCommandOptions, value string) error {
	name := strings.TrimSpace(value)
	if name == "" {
		return nil
	}
	if options.name != "" {
		return execUsageError{"worktree name was provided more than once"}
	}
	options.name = name
	return nil
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
		case arg == "--attempts":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			attempts, err := parsePositiveIntFlag("--attempts", value)
			if err != nil {
				return options, false, err
			}
			options.attempts = attempts
			index = next
		case strings.HasPrefix(arg, "--attempts="):
			attempts, err := parsePositiveIntFlag("--attempts", strings.TrimSpace(strings.TrimPrefix(arg, "--attempts=")))
			if err != nil {
				return options, false, err
			}
			options.attempts = attempts
		case strings.HasPrefix(arg, "-"):
			return options, false, execUsageError{fmt.Sprintf("unknown verify flag %q", arg)}
		default:
			return options, false, execUsageError{fmt.Sprintf("unexpected verify argument %q", arg)}
		}
	}
	return options, false, nil
}

func parseChangesArgs(args []string, command string) (changesCommandOptions, bool, error) {
	options := changesCommandOptions{}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "-h" || arg == "--help" || arg == "help":
			return options, true, nil
		case arg == "--json":
			options.json = true
		case arg == "--dry-run":
			options.dryRun = true
		case arg == "-C" || arg == "--cwd":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.cwd = value
			index = next
		case strings.HasPrefix(arg, "--cwd="):
			options.cwd = strings.TrimSpace(strings.TrimPrefix(arg, "--cwd="))
		case arg == "-m" || arg == "--message":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.message = value
			index = next
		case strings.HasPrefix(arg, "--message="):
			options.message = strings.TrimSpace(strings.TrimPrefix(arg, "--message="))
		case arg == "--diff-bytes":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			maxDiffBytes, err := parsePositiveIntFlag("--diff-bytes", value)
			if err != nil {
				return options, false, err
			}
			options.maxDiffBytes = maxDiffBytes
			index = next
		case strings.HasPrefix(arg, "--diff-bytes="):
			maxDiffBytes, err := parsePositiveIntFlag("--diff-bytes", strings.TrimSpace(strings.TrimPrefix(arg, "--diff-bytes=")))
			if err != nil {
				return options, false, err
			}
			options.maxDiffBytes = maxDiffBytes
		case strings.HasPrefix(arg, "-"):
			return options, false, execUsageError{fmt.Sprintf("unknown changes flag %q", arg)}
		default:
			return options, false, execUsageError{fmt.Sprintf("unexpected changes argument %q", arg)}
		}
	}
	if command != "commit" && (options.message != "" || options.dryRun) {
		return options, false, execUsageError{"--message and --dry-run are only valid with `zero changes commit`"}
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

func redactWorktreeResult(result worktrees.Result) worktrees.Result {
	result.Name = redactCLIString(result.Name)
	result.Path = redactCLIString(result.Path)
	result.RepoRoot = redactCLIString(result.RepoRoot)
	result.SourceBranch = redactCLIString(result.SourceBranch)
	result.SourceCommit = redactCLIString(result.SourceCommit)
	return result
}

func redactVerifyReport(report verify.Report) verify.Report {
	report.Root = redactCLIString(report.Root)
	report.Results = append([]verify.Result{}, report.Results...)
	for index := range report.Results {
		report.Results[index].Stdout = redactCLIString(report.Results[index].Stdout)
		report.Results[index].Stderr = redactCLIString(report.Results[index].Stderr)
		report.Results[index].Error = redactCLIString(report.Results[index].Error)
		if report.Results[index].OutputSummary != nil {
			summary := *report.Results[index].OutputSummary
			summary.Lines = append([]string{}, summary.Lines...)
			for lineIndex := range summary.Lines {
				summary.Lines[lineIndex] = redactCLIString(summary.Lines[lineIndex])
			}
			report.Results[index].OutputSummary = &summary
		}
		if report.Results[index].TestSummary != nil {
			summary := *report.Results[index].TestSummary
			summary.Failures = append([]testrunner.Failure{}, summary.Failures...)
			for failureIndex := range summary.Failures {
				summary.Failures[failureIndex].Name = redactCLIString(summary.Failures[failureIndex].Name)
				summary.Failures[failureIndex].File = redactCLIString(summary.Failures[failureIndex].File)
				summary.Failures[failureIndex].Message = redactCLIString(summary.Failures[failureIndex].Message)
			}
			report.Results[index].TestSummary = &summary
		}
	}
	return report
}

func redactVerifyLoopReport(report selfverify.Report) selfverify.Report {
	report.Root = redactCLIString(report.Root)
	report.Error = redactCLIString(report.Error)
	for index := range report.Attempts {
		report.Attempts[index].Report = redactVerifyReport(report.Attempts[index].Report)
		if report.Attempts[index].Remediation != nil {
			remediation := *report.Attempts[index].Remediation
			remediation.StartedAt = redactCLIString(remediation.StartedAt)
			remediation.EndedAt = redactCLIString(remediation.EndedAt)
			remediation.Message = redactCLIString(remediation.Message)
			remediation.Error = redactCLIString(remediation.Error)
			report.Attempts[index].Remediation = &remediation
		}
	}
	return report
}

func redactChangeSummary(summary zerogit.ChangeSummary) zerogit.ChangeSummary {
	summary.Root = redactCLIString(summary.Root)
	summary.Branch = redactCLIString(summary.Branch)
	summary.Commit = redactCLIString(summary.Commit)
	summary.DiffStat = redactCLIString(summary.DiffStat)
	summary.Diff = redactCLIString(summary.Diff)
	for index := range summary.Files {
		summary.Files[index].Path = redactCLIString(summary.Files[index].Path)
		summary.Files[index].Status = redactCLIString(summary.Files[index].Status)
	}
	return summary
}

func redactCommitResult(result zerogit.CommitResult) zerogit.CommitResult {
	result.Root = redactCLIString(result.Root)
	result.Message = redactCLIString(result.Message)
	result.CommitHash = redactCLIString(result.CommitHash)
	result.Before = redactChangeSummary(result.Before)
	return result
}

func redactCLIString(value string) string {
	// Keep ordinary paths visible; these commands report useful locations.
	// Central redaction still removes secret-looking tokens embedded in paths.
	return redaction.RedactString(value, redaction.Options{})
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
		if result.TestSummary != nil {
			lines = append(lines, formatVerifyTestSummary(result.TestSummary))
			for _, failure := range result.TestSummary.Failures {
				if failure.Name == "" {
					continue
				}
				detail := failure.Name
				if failure.File != "" {
					detail += " at " + failure.File
				}
				lines = append(lines, "    failure: "+detail)
			}
		}
		if result.Error != "" {
			lines = append(lines, "    error: "+result.Error)
		}
	}
	return strings.Join(lines, "\n")
}

func formatVerifyTestSummary(summary *testrunner.Summary) string {
	line := fmt.Sprintf("    tests: %d total, %d passed, %d failed", summary.Total, summary.Passed, summary.Failed)
	if summary.Skipped > 0 {
		line += fmt.Sprintf(", %d skipped", summary.Skipped)
	}
	return line
}

func formatVerifyLoopReport(report selfverify.Report) string {
	lines := []string{
		"Zero self-verification",
	}
	if report.Root != "" {
		lines = append(lines, "root: "+report.Root)
	}
	lines = append(lines,
		fmt.Sprintf("attempts: %d", len(report.Attempts)),
		"stop: "+string(report.StopReason),
		fmt.Sprintf("summary: %d total, %d passed, %d failed, %d errors", report.Summary.Total, report.Summary.Passed, report.Summary.Failed, report.Summary.Errors),
	)
	for _, attempt := range report.Attempts {
		status := "failed"
		if attempt.Report.OK {
			status = "passed"
		}
		lines = append(lines, fmt.Sprintf("  attempt %d: %s", attempt.Number, status))
		if attempt.Remediation != nil {
			lines = append(lines, "    remediation: "+formatRemediation(*attempt.Remediation))
		}
	}
	if report.Error != "" {
		lines = append(lines, "error: "+report.Error)
	}
	return strings.Join(lines, "\n")
}

func formatRemediation(remediation selfverify.Remediation) string {
	status := "not applied"
	if remediation.Applied {
		status = "applied"
	}
	details := []string{status}
	if remediation.Message != "" {
		details = append(details, remediation.Message)
	}
	if remediation.Error != "" {
		details = append(details, "error: "+remediation.Error)
	}
	return strings.Join(details, " - ")
}

func formatChangeSummary(summary zerogit.ChangeSummary) string {
	lines := []string{
		"Zero changes",
		"root: " + summary.Root,
		fmt.Sprintf("files: %d changed", len(summary.Files)),
	}
	if summary.Branch != "" {
		lines = append(lines, "branch: "+summary.Branch)
	}
	if summary.Commit != "" {
		lines = append(lines, "commit: "+summary.Commit)
	}
	if summary.Clean {
		lines = append(lines, "clean: true")
		return strings.Join(lines, "\n")
	}
	for _, file := range summary.Files {
		lines = append(lines, fmt.Sprintf("  [%s] %s", file.Status, file.Path))
	}
	return strings.Join(lines, "\n")
}

func formatCommitResult(result zerogit.CommitResult) string {
	lines := []string{
		"Zero changes commit",
		"root: " + result.Root,
		"message: " + result.Message,
		fmt.Sprintf("dry-run: %t", result.DryRun),
		fmt.Sprintf("committed: %t", result.Committed),
		fmt.Sprintf("files: %d changed", len(result.Before.Files)),
	}
	if result.CommitHash != "" {
		lines = append(lines, "commit: "+result.CommitHash)
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
      --attempts <n>      Run a bounded self-verification loop
      --json              Print JSON output
  -h, --help              Show this help
`)
	return err
}

func writeChangesHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero changes inspect [flags]
  zero changes commit [flags]

Inspects local git changes and optionally creates a commit.

Flags:
  -C, --cwd <path>        Workspace directory
      --diff-bytes <n>    Maximum diff bytes to include
  -m, --message <text>    Commit message for `+"`zero changes commit`"+`
      --dry-run           Preview commit metadata without mutating git state
      --json              Print JSON output
  -h, --help              Show this help
`)
	return err
}
