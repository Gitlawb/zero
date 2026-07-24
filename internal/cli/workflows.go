package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/redaction"
	"github.com/Gitlawb/zero/internal/selfverify"
	"github.com/Gitlawb/zero/internal/testrunner"
	"github.com/Gitlawb/zero/internal/verify"
	"github.com/Gitlawb/zero/internal/worktrees"
	"github.com/Gitlawb/zero/internal/zerogit"
	"github.com/Gitlawb/zero/internal/zeroruntime"
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
	baseRef      string
	message      string
	hasMessage   bool
	dryRun       bool
	maxDiffBytes int
	remote       string
	force        bool
	title        string
	body         string
	fill         bool
	draft        bool
	yes          bool
	auto         bool
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
			BaseRef:      options.baseRef,
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
		var message string
		if options.auto {
			summary, err := deps.inspectChanges(context.Background(), zerogit.InspectOptions{
				Cwd:          workspaceRoot,
				MaxDiffBytes: options.maxDiffBytes,
			})
			if err != nil {
				return writeExecUsageError(stderr, fmt.Sprintf("failed to inspect changes: %v", err))
			}
			if summary.Clean {
				return writeExecUsageError(stderr, "no changes to commit")
			}

			resolved, err := deps.resolveConfig(workspaceRoot, config.Overrides{})
			if err != nil {
				return writeExecUsageError(stderr, fmt.Sprintf("failed to resolve config: %v", err))
			}
			if !config.HasProviderProfile(resolved.Provider) {
				return writeExecUsageError(stderr, "no provider configured for auto-commit message")
			}
			provider, err := deps.newProvider(resolved.Provider)
			if err != nil {
				return writeExecUsageError(stderr, fmt.Sprintf("failed to create provider: %v", err))
			}

			if !options.json {
				if _, err := fmt.Fprintln(stdout, "Generating commit message using LLM..."); err != nil {
					return exitCrash
				}
			}

			safeSummary := redactChangeSummary(summary)
			genCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			msg, err := generateAutoCommitMessage(genCtx, provider, resolved.Provider.Model, safeSummary)
			if err != nil {
				return writeExecUsageError(stderr, fmt.Sprintf("failed to generate commit message: %v", err))
			}
			message = msg
		} else {
			message = options.message
		}

		result, err := deps.commitChanges(context.Background(), zerogit.CommitOptions{
			Cwd:          workspaceRoot,
			Message:      message,
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
	case "push":
		return runChangesPush(args, stdout, stderr, deps)
	case "pr", "pull-request":
		return runChangesPR(args, stdout, stderr, deps)
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
		case arg == "--base":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.baseRef = strings.TrimSpace(value)
			index = next
		case strings.HasPrefix(arg, "--base="):
			v := strings.TrimSpace(strings.TrimPrefix(arg, "--base="))
			if v == "" || flagValueLooksLikeOption(v) {
				return options, false, execUsageError{"--base requires a value"}
			}
			options.baseRef = v
		case arg == "-m" || arg == "--message":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.message = value
			options.hasMessage = true
			index = next
		case strings.HasPrefix(arg, "--message="):
			options.message = strings.TrimSpace(strings.TrimPrefix(arg, "--message="))
			options.hasMessage = true
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
		case arg == "--remote":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.remote = strings.TrimSpace(value)
			index = next
		case strings.HasPrefix(arg, "--remote="):
			options.remote = strings.TrimSpace(strings.TrimPrefix(arg, "--remote="))
		case arg == "--force":
			options.force = true
		case arg == "--title":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.title = value
			index = next
		case strings.HasPrefix(arg, "--title="):
			options.title = strings.TrimPrefix(arg, "--title=")
		case arg == "--body":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.body = value
			index = next
		case strings.HasPrefix(arg, "--body="):
			options.body = strings.TrimPrefix(arg, "--body=")
		case arg == "--fill":
			options.fill = true
		case arg == "--draft":
			options.draft = true
		case arg == "--yes":
			options.yes = true
		case arg == "-a" || arg == "--auto":
			options.auto = true
		case strings.HasPrefix(arg, "-"):
			return options, false, execUsageError{fmt.Sprintf("unknown changes flag %q", arg)}
		default:
			return options, false, execUsageError{fmt.Sprintf("unexpected changes argument %q", arg)}
		}
	}
	if command != "commit" && options.message != "" {
		return options, false, execUsageError{"--message is only valid with `zero changes commit`"}
	}
	if command != "commit" && options.hasMessage {
		return options, false, execUsageError{"--message is only valid with `zero changes commit`"}
	}
	if command == "commit" && options.hasMessage && options.auto {
		return options, false, execUsageError{"cannot specify both --message and --auto"}
	}
	if command != "commit" && command != "push" && options.dryRun {
		return options, false, execUsageError{"--dry-run is only valid with commit or push"}
	}
	// --auto on push/pr is the explicit opt-in for LLM branch naming (see
	// ensureFeatureBranch); on commit it opts into the LLM commit message.
	if command != "commit" && command != "push" && command != "pr" && options.auto {
		return options, false, execUsageError{"--auto is only valid with commit, push, or pr"}
	}
	if command != "inspect" && options.baseRef != "" {
		return options, false, execUsageError{"--base is only valid with `zero changes inspect`"}
	}
	if command != "push" && command != "pr" && (options.remote != "" || options.force) {
		return options, false, execUsageError{"--remote and --force are only valid with push or pr"}
	}
	if command != "pr" && (options.title != "" || options.body != "" || options.fill || options.draft) {
		return options, false, execUsageError{"--title, --body, --fill, and --draft are only valid with `zero changes pr`"}
	}
	if command != "push" && command != "pr" && options.yes {
		return options, false, execUsageError{"--yes is only valid with push or pr"}
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
	summary.Base = redactCLIString(summary.Base)
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
	if summary.Base != "" {
		lines = append(lines, "base: "+summary.Base)
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
  zero changes push [flags]
  zero changes pr [flags]

Inspects, commits, pushes, and creates pull requests for local git changes.

Flags:
  -C, --cwd <path>        Workspace directory
      --base <ref>        Diff against <ref>...HEAD instead of the working tree
      --diff-bytes <n>    Maximum diff bytes to include
  -m, --message <text>    Commit message for `+"`zero changes commit`"+`
      --dry-run           Preview commit metadata / push without mutating git state
      --remote <name>     Remote to push to (defaults to upstream tracked branch or origin)
      --force             Use force-with-lease when pushing
      --title <text>      PR title
      --body <text>       PR body
      --fill              Automatically populate PR title and body from commits
      --draft             Create PR as a draft
      --yes               Confirm pushing to a default/protected branch
  -a, --auto              Use the LLM: commit generates the message, push/pr name the auto-created branch (sends the diff to the provider)
      --json              Print JSON output
  -h, --help              Show this help
`)
	return err
}

func runChangesPush(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	options, help, err := parseChangesArgs(args, "push")
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

	branch, remote, created, err := ensureFeatureBranch(context.Background(), stdout, options.json, workspaceRoot, options.remote, options.yes, options.dryRun, options.auto, options.maxDiffBytes, deps)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}

	result, err := deps.pushChanges(context.Background(), zerogit.PushOptions{
		Cwd:                    workspaceRoot,
		Remote:                 firstNonEmptyString(options.remote, remote),
		Branch:                 branch,
		Force:                  options.force,
		DryRun:                 options.dryRun,
		RequireNewRemoteBranch: created,
		AllowPushDefaultBranch: options.yes,
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

	dryRunStr := ""
	if options.dryRun {
		dryRunStr = " (dry run)"
	}
	if _, err := fmt.Fprintf(stdout, "Pushed branch %s to remote %s%s\n", result.Branch, result.Remote, dryRunStr); err != nil {
		return exitCrash
	}
	if result.Output != "" {
		if _, err := fmt.Fprintln(stdout, result.Output); err != nil {
			return exitCrash
		}
	}
	return exitSuccess
}

func runChangesPR(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	options, help, err := parseChangesArgs(args, "pr")
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if err := writeChangesHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if !options.fill && options.title == "" {
		return writeExecUsageError(stderr, "must provide either --fill or --title to run non-interactively")
	}
	workspaceRoot, err := resolveWorkspaceRoot(options.cwd, deps)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}

	_, _, remoteForCheck, err := deps.isDefaultBranch(context.Background(), zerogit.DefaultBranchOptions{Cwd: workspaceRoot, Remote: options.remote})
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}

	targetRemote := firstNonEmptyString(options.remote, remoteForCheck)
	if deps.isUnbornRemote != nil {
		if unborn, unbornErr := deps.isUnbornRemote(context.Background(), workspaceRoot, targetRemote); unbornErr == nil && unborn {
			return writeExecUsageError(stderr, fmt.Sprintf("cannot create pull request on unborn remote %s: push the initial default branch first", targetRemote))
		}
	}

	branch, remote, created, err := ensureFeatureBranch(context.Background(), stdout, options.json, workspaceRoot, options.remote, options.yes, false, options.auto, options.maxDiffBytes, deps)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}

	if !options.json {
		if _, err := fmt.Fprintln(stdout, "Pushing current branch to set upstream..."); err != nil {
			return exitCrash
		}
	}
	pushResult, err := deps.pushChanges(context.Background(), zerogit.PushOptions{
		Cwd:                    workspaceRoot,
		Remote:                 firstNonEmptyString(options.remote, remote),
		Branch:                 branch,
		Force:                  options.force,
		RequireNewRemoteBranch: created,
		AllowPushDefaultBranch: options.yes,
	})
	if err != nil {
		return writeExecUsageError(stderr, fmt.Sprintf("auto-push failed: %v", err))
	}
	if !options.json {
		if _, err := fmt.Fprintf(stdout, "Pushed branch %s to remote %s\n", pushResult.Branch, pushResult.Remote); err != nil {
			return exitCrash
		}
	}

	prResult, err := deps.createPR(context.Background(), zerogit.PROptions{
		Cwd:   workspaceRoot,
		Fill:  options.fill,
		Draft: options.draft,
		Title: options.title,
		Body:  options.body,
	})
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}

	if options.json {
		type prJSONResult struct {
			Branch string `json:"branch"`
			Remote string `json:"remote"`
			Output string `json:"output"`
		}
		res := prJSONResult{
			Branch: pushResult.Branch,
			Remote: pushResult.Remote,
			Output: strings.TrimSpace(prResult.Output),
		}
		if err := writePrettyJSON(stdout, res); err != nil {
			return exitCrash
		}
		return exitSuccess
	}

	if _, err := fmt.Fprintln(stdout, strings.TrimSpace(prResult.Output)); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func generateAutoCommitMessage(ctx context.Context, provider zeroruntime.Provider, model string, summary zerogit.ChangeSummary) (string, error) {
	var promptBuilder strings.Builder
	promptBuilder.WriteString("Analyze the following git diff and generate a concise, conventional commit message.\n")
	promptBuilder.WriteString("The commit message subject line must be 72 characters or fewer, starting with a conventional commit type (e.g., feat, fix, docs, style, refactor, test, chore) followed by a colon and space, and a lowercase description.\n")
	promptBuilder.WriteString("You may optionally include a blank line and a bulleted list of changes for the body if there are multiple files or complex changes.\n")
	promptBuilder.WriteString("Output ONLY the raw commit message text. Do not wrap the message in markdown code block fence, do not include quotes or any introduction/explanation.\n\n")
	promptBuilder.WriteString("Git Diff:\n")
	promptBuilder.WriteString(summary.Diff)

	request := zeroruntime.CompletionRequest{
		Messages: []zeroruntime.Message{
			{Role: zeroruntime.MessageRoleUser, Content: promptBuilder.String()},
		},
	}
	stream, err := provider.StreamCompletion(ctx, request)
	if err != nil {
		return "", err
	}
	collected := zeroruntime.CollectStream(ctx, stream)
	if collected.Error != "" {
		return "", fmt.Errorf("%s", collected.Error)
	}

	msg := strings.TrimSpace(collected.Text)
	if strings.HasPrefix(msg, "```") {
		if idx := strings.Index(msg, "\n"); idx != -1 {
			msg = msg[idx+1:]
		}
	}
	msg = strings.TrimSuffix(msg, "```")
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return "", fmt.Errorf("provider returned empty commit message")
	}
	return msg, nil
}

// ensureFeatureBranch is the branch-naming step `zero changes push`/`pr` run
// before pushing: pushing straight to the default branch is refused deeper in
// zerogit.Push, so rather than surface that as a dead end, create and switch
// to a conventionally named "<user>/<slug>" branch first. It returns the
// branch push/pr should target, or "" to mean "current HEAD branch, unchanged"
// (zerogit.Push already treats an empty Branch that way), plus the remote the
// preflight resolved (requestedRemote, then the original branch's configured
// upstream, then "origin"), plus whether Push should require that branch not
// already exist on that remote. Callers must pass the remote to Push: a
// freshly created branch has no tracking configuration, so Push's own
// fallback would silently retarget "origin" even when the work came from a
// branch tracking a different remote. Callers must also pass that third value
// through as Push's RequireNewRemoteBranch: CreateBranch's own
// remote-collision probe runs before this returns, and closing that race
// requires Push's push itself to assert the destination is still new. That
// requirement also carries across a retry: if the current branch is already
// non-default (this call didn't create it) but has no configured upstream
// yet, it's either a fresh manual branch or this exact generated branch after
// a lost force-with-lease race, and either way the lease still needs to be
// asserted rather than silently dropped. allowDefaultBranch (the --yes flag)
// and dryRun both opt out via the "" branch / false return, leaving Push's own
// guard/preview behavior on the default branch unaffected.
//
// autoNaming gates the LLM naming path (--auto): these commands were
// git-only, and sending the change diff to a configured provider on every
// default-branch push would silently export source code nobody asked to
// share. Without the opt-in the name comes from deterministic local
// information only. maxDiffBytes caps the committed-range diff Inspect
// returns, so a user who passed --diff-bytes to bound the proprietary source
// sent for LLM naming has that cap honored here just as the commit path does.
// The working tree must be clean and HEAD must be ahead of the resolved remote
// default before a branch is created; otherwise the push would either leave
// uncommitted edits behind or publish an empty comparison. The one exception
// is a confirmed-unborn remote (freshly created, zero refs): it has no
// tracking ref to check ahead-ness or diff against at all, so that check is
// bypassed rather than failing the very first push a new remote will ever see.
func ensureFeatureBranch(ctx context.Context, stdout io.Writer, jsonMode bool, workspaceRoot string, requestedRemote string, allowDefaultBranch bool, dryRun bool, autoNaming bool, maxDiffBytes int, deps appDeps) (string, string, bool, error) {
	if allowDefaultBranch || dryRun {
		return "", strings.TrimSpace(requestedRemote), false, nil
	}

	isDefault, currentBranch, remote, err := deps.isDefaultBranch(ctx, zerogit.DefaultBranchOptions{Cwd: workspaceRoot, Remote: requestedRemote})
	if err != nil {
		return "", "", false, err
	}
	if !isDefault {
		requireNewRemoteBranch := false
		if deps.branchHasUpstream != nil && deps.isGeneratedBranch != nil {
			hasUpstream, upstreamErr := deps.branchHasUpstream(ctx, workspaceRoot, currentBranch)
			isGenerated := deps.isGeneratedBranch(ctx, workspaceRoot, currentBranch)
			requireNewRemoteBranch = isGenerated && (upstreamErr != nil || !hasUpstream)
		}
		return currentBranch, remote, requireNewRemoteBranch, nil
	}

	// CreateBranch and Push publish commits only. A dirty working tree would
	// leave uncommitted edits behind under a branch/PR that does not include
	// them, so refuse until the tree is clean (commit or stash first).
	workingTree, err := deps.inspectChanges(ctx, zerogit.InspectOptions{Cwd: workspaceRoot})
	if err != nil {
		return "", "", false, fmt.Errorf("failed to inspect working tree: %w", err)
	}
	if !workingTree.Clean {
		return "", "", false, fmt.Errorf("working tree has uncommitted changes; commit or stash them before pushing from the default branch")
	}

	// Refresh the local remote-tracking ref before trusting it: IsDefaultBranch
	// already contacted the remote for its symref check, but the tracking ref
	// commitsAhead reads from is only a local cache (written at clone or the
	// last fetch) and can sit behind the remote's live tip. Left stale, a real
	// publishable range could look like zero commits ahead, and the diff
	// derived below (which reuses this same ref as its base) would be stale
	// too. A failure here (offline, or a genuinely unborn remote with no such
	// ref to fetch yet) is folded into the same fail-closed path as an
	// unresolvable ahead count below.
	var fetchErr error
	if deps.refreshTrackingRef != nil {
		fetchErr = deps.refreshTrackingRef(ctx, workspaceRoot, remote, currentBranch)
	}

	// Branching off the default branch only makes sense when HEAD carries a
	// commit that is not already on the remote default branch. A clean,
	// up-to-date default branch would otherwise publish a feature branch at the
	// exact default tip. If the ahead count cannot be determined (for example
	// the remote-tracking ref was never fetched), fail rather than guess -
	// unless the remote is confirmed unborn (freshly created, zero refs): it
	// then has no <remote>/<currentBranch> tracking ref for commitsAhead to
	// exist against, which is proof there is nothing published yet rather than
	// an unknown state, and every commit on HEAD is new relative to it.
	ahead, aheadErr := deps.commitsAhead(ctx, workspaceRoot, remote, currentBranch)
	unbornRemote := false
	if fetchErr != nil || aheadErr != nil {
		var unbornErr error
		unbornRemote, unbornErr = deps.isUnbornRemote(ctx, workspaceRoot, remote)
		if unbornErr != nil || !unbornRemote {
			cause := aheadErr
			if cause == nil {
				cause = fetchErr
			}
			return "", "", false, fmt.Errorf("cannot determine whether HEAD is ahead of %s/%s: %w; fetch the remote tracking branch first", remote, currentBranch, cause)
		}
	} else if ahead == 0 {
		return "", "", false, fmt.Errorf("no changes to publish: HEAD is not ahead of %s/%s; commit your work before pushing", remote, currentBranch)
	}

	// Name the branch (and, with --auto, send the provider) from what HEAD is
	// actually ahead of the resolved remote branch by, using the same ref
	// commitsAhead just checked. A working-tree snapshot can describe edits a
	// commit-only push will never include. A confirmed-unborn remote has no
	// such ref to diff against either, so leave BaseRef empty: Inspect falls
	// back to the (already known clean) working-tree snapshot, summary.Files
	// comes back empty, and the headCommitSubject fallback below names the
	// branch instead.
	baseRef := remote + "/" + currentBranch
	if unbornRemote {
		baseRef = ""
	}
	summary, err := deps.inspectChanges(ctx, zerogit.InspectOptions{Cwd: workspaceRoot, BaseRef: baseRef, MaxDiffBytes: maxDiffBytes})
	if err != nil {
		return "", "", false, fmt.Errorf("failed to inspect changes: %w", err)
	}

	slug := fallbackBranchSlug(summary)
	if len(summary.Files) == 0 {
		// An empty commit (or one whose only content the diff omits) leaves
		// nothing to derive a slug from; name the branch from the commit
		// subject instead.
		if subject := deps.headCommitSubject(ctx, workspaceRoot); subject != "" {
			slug = zerogit.SlugifyBranchComponent(subject)
		}
	}
	if autoNaming && strings.TrimSpace(summary.Diff) != "" {
		if resolved, cfgErr := deps.resolveConfig(workspaceRoot, config.Overrides{}); cfgErr == nil && config.HasProviderProfile(resolved.Provider) {
			if provider, provErr := deps.newProvider(resolved.Provider); provErr == nil {
				if !jsonMode {
					fmt.Fprintln(stdout, "Generating branch name using LLM...")
				}
				genCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
				generated, genErr := generateAutoBranchSlug(genCtx, provider, resolved.Provider.Model, redactChangeSummary(summary))
				cancel()
				if genErr == nil && generated != "" {
					slug = generated
				}
			}
		}
	}

	name := zerogit.BuildBranchName(deps.currentGitUser(ctx, workspaceRoot), slug)
	result, err := deps.createBranch(ctx, zerogit.BranchOptions{Cwd: workspaceRoot, Name: name, Remote: remote})
	if err != nil {
		return "", "", false, fmt.Errorf("failed to create branch: %w", err)
	}
	if deps.markGeneratedBranch != nil {
		if err := deps.markGeneratedBranch(ctx, workspaceRoot, result.Branch); err != nil {
			return "", "", false, fmt.Errorf("failed to mark generated branch: %w", err)
		}
	}
	if !jsonMode {
		fmt.Fprintf(stdout, "Created branch %s (was on %s)\n", result.Branch, currentBranch)
	}
	return result.Branch, remote, true, nil
}

// fallbackBranchSlug derives a deterministic branch-name slug from a change
// summary without calling an LLM, so ensureFeatureBranch still works when no
// provider is configured.
func fallbackBranchSlug(summary zerogit.ChangeSummary) string {
	switch len(summary.Files) {
	case 0:
		return "changes"
	case 1:
		return zerogit.SlugifyBranchComponent(filepath.Base(summary.Files[0].Path))
	default:
		return fmt.Sprintf("update-%d-files", len(summary.Files))
	}
}

// generateAutoBranchSlug asks the model for a short kebab-case slug
// describing the diff, mirroring generateAutoCommitMessage's prompt shape.
func generateAutoBranchSlug(ctx context.Context, provider zeroruntime.Provider, model string, summary zerogit.ChangeSummary) (string, error) {
	var promptBuilder strings.Builder
	promptBuilder.WriteString("Analyze the following git diff and generate a short git branch name slug for it.\n")
	promptBuilder.WriteString("The slug must be 2 to 5 lowercase words separated by hyphens (kebab-case), using only letters, digits, and hyphens, with no prefix like \"feature/\" or \"fix/\" and no surrounding quotes.\n")
	promptBuilder.WriteString("Output ONLY the raw slug text, nothing else.\n\n")
	promptBuilder.WriteString("Git Diff:\n")
	promptBuilder.WriteString(summary.Diff)

	request := zeroruntime.CompletionRequest{
		Messages: []zeroruntime.Message{
			{Role: zeroruntime.MessageRoleUser, Content: promptBuilder.String()},
		},
	}
	stream, err := provider.StreamCompletion(ctx, request)
	if err != nil {
		return "", err
	}
	collected := zeroruntime.CollectStream(ctx, stream)
	if collected.Error != "" {
		return "", fmt.Errorf("%s", collected.Error)
	}

	slug := zerogit.SlugifyBranchComponent(extractBranchSlug(collected.Text))
	if slug == "" {
		return "", fmt.Errorf("provider returned empty branch slug")
	}
	return slug, nil
}

// slugLineRe matches a line that already reads as a kebab-case slug (letters,
// digits, and internal single hyphens only). extractBranchSlug prefers such a
// line so a preamble sentence is never mistaken for the slug.
var slugLineRe = regexp.MustCompile(`^[a-zA-Z0-9]+(-[a-zA-Z0-9]+)*$`)

// extractBranchSlug pulls the intended slug out of a model response that didn't
// follow the "output only the raw slug" instruction exactly. It drops Markdown
// code-fence lines, then prefers a line that already looks like a kebab-case
// slug: that skips a leading preamble such as "Here is a suggested branch
// name:" in favor of the "add-login-page" line that follows it, and it unwraps
// a fenced reply whose only real content is the slug. When no line is already
// slug-shaped it falls back to the first non-fence, non-empty line (trimmed of
// surrounding quotes) so a plain multi-word "add login page" reply still
// slugifies correctly rather than being returned verbatim.
func extractBranchSlug(text string) string {
	fallback := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "```") {
			continue
		}
		line = strings.TrimSpace(strings.Trim(line, `"'`))
		if line == "" {
			continue
		}
		if slugLineRe.MatchString(line) {
			return line
		}
		if fallback == "" {
			fallback = line
		}
	}
	return fallback
}
