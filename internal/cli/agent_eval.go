package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Gitlawb/zero/internal/agenteval"
)

type agentEvalOptions struct {
	Mode          string `json:"mode"`
	SuitePath     string `json:"suite_path"`
	TaskID        string `json:"task_id,omitempty"`
	WorkspacePath string `json:"workspace_path,omitempty"`
	ReportDir     string `json:"report_dir,omitempty"`
	JSON          bool   `json:"json"`
}

type agentEvalReport struct {
	Suite      string             `json:"suite"`
	Name       string             `json:"name,omitempty"`
	TaskID     string             `json:"task_id,omitempty"`
	Status     string             `json:"status,omitempty"`
	OK         bool               `json:"ok"`
	Tasks      int                `json:"tasks"`
	Checks     int                `json:"checks"`
	Total      int                `json:"total"`
	Passed     int                `json:"passed"`
	Failed     int                `json:"failed"`
	Blocked    int                `json:"blocked"`
	Errors     int                `json:"errors"`
	ReportPath string             `json:"report_path,omitempty"`
	Failures   []agentEvalFailure `json:"failures,omitempty"`
}

type agentEvalFailure struct {
	ID      string `json:"id,omitempty"`
	Message string `json:"message,omitempty"`
}

func runAgentEvalCommand(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	options, help, err := parseAgentEvalArgs(args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if err := writeAgentEvalHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	// Run under a signal-aware context so Ctrl+C / SIGTERM cancels in-flight
	// verification commands (which honor context cancellation) instead of leaking them.
	ctx, stop := signalContext()
	defer stop()
	report, err := deps.runAgentEval(ctx, options)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if options.ReportDir != "" {
		report.ReportPath = filepath.Join(options.ReportDir, "agent-eval-report.json")
		if err := writeAgentEvalReportFile(options.ReportDir, report); err != nil {
			return writeAppError(stderr, "failed to write eval report: "+err.Error(), exitCrash)
		}
	}
	if options.JSON {
		if err := writePrettyJSON(stdout, report); err != nil {
			return exitCrash
		}
	} else if _, err := fmt.Fprintln(stdout, formatAgentEvalReport(report)); err != nil {
		return exitCrash
	}
	if !report.OK {
		return exitProvider
	}
	return exitSuccess
}

func parseAgentEvalArgs(args []string) (agentEvalOptions, bool, error) {
	options := agentEvalOptions{Mode: "validate"}
	if len(args) > 0 {
		switch args[0] {
		case "run", "validate":
			options.Mode = args[0]
			args = args[1:]
		}
	}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "-h" || arg == "--help" || arg == "help":
			return options, true, nil
		case arg == "--json":
			options.JSON = true
		case arg == "--suite":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.SuitePath = strings.TrimSpace(value)
			index = next
		case strings.HasPrefix(arg, "--suite="):
			options.SuitePath = strings.TrimSpace(strings.TrimPrefix(arg, "--suite="))
		case arg == "--task":
			if options.Mode != "run" {
				return options, false, execUsageError{"--task is only valid for eval run"}
			}
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.TaskID = strings.TrimSpace(value)
			index = next
		case strings.HasPrefix(arg, "--task="):
			if options.Mode != "run" {
				return options, false, execUsageError{"--task is only valid for eval run"}
			}
			value, err := requiredInlineFlagValue(arg, "--task")
			if err != nil {
				return options, false, err
			}
			options.TaskID = strings.TrimSpace(value)
		case arg == "--workspace":
			if options.Mode != "run" {
				return options, false, execUsageError{"--workspace is only valid for eval run"}
			}
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.WorkspacePath = strings.TrimSpace(value)
			index = next
		case strings.HasPrefix(arg, "--workspace="):
			if options.Mode != "run" {
				return options, false, execUsageError{"--workspace is only valid for eval run"}
			}
			value, err := requiredInlineFlagValue(arg, "--workspace")
			if err != nil {
				return options, false, err
			}
			options.WorkspacePath = strings.TrimSpace(value)
		case arg == "--report-dir":
			if options.Mode != "run" {
				return options, false, execUsageError{"--report-dir is only valid for eval run"}
			}
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.ReportDir = strings.TrimSpace(value)
			index = next
		case strings.HasPrefix(arg, "--report-dir="):
			if options.Mode != "run" {
				return options, false, execUsageError{"--report-dir is only valid for eval run"}
			}
			value, err := requiredInlineFlagValue(arg, "--report-dir")
			if err != nil {
				return options, false, err
			}
			options.ReportDir = strings.TrimSpace(value)
		case strings.HasPrefix(arg, "-"):
			return options, false, execUsageError{fmt.Sprintf("unknown eval flag %q", arg)}
		default:
			return options, false, execUsageError{fmt.Sprintf("unexpected eval argument %q", arg)}
		}
	}
	if options.SuitePath == "" {
		return options, false, execUsageError{"--suite requires a path"}
	}
	if options.Mode == "run" && options.WorkspacePath == "" {
		// Require an explicit workspace: defaulting to "." would run the suite's
		// verification commands (go test/git) against the real working tree instead
		// of a staged fixture.
		return options, false, execUsageError{"--workspace requires a path for eval run"}
	}
	return options, false, nil
}

func formatAgentEvalReport(report agentEvalReport) string {
	lines := []string{
		"Zero agent eval",
		"suite: " + report.Suite,
	}
	if report.Name != "" {
		lines = append(lines, "name: "+report.Name)
	}
	if report.TaskID != "" {
		lines = append(lines, "task: "+report.TaskID)
	}
	if report.Tasks > 0 || report.Checks > 0 {
		lines = append(lines, fmt.Sprintf("summary: %d tasks, %d checks", report.Tasks, report.Checks))
	} else {
		lines = append(lines, fmt.Sprintf("summary: %d total, %d passed, %d failed, %d blocked, %d errors", report.Total, report.Passed, report.Failed, report.Blocked, report.Errors))
	}
	status := strings.TrimSpace(report.Status)
	if status == "" {
		if report.OK {
			status = "passed"
		} else {
			status = "failed"
		}
	}
	lines = append(lines, "status: "+status)
	if report.ReportPath != "" {
		lines = append(lines, "report: "+report.ReportPath)
	}
	if len(report.Failures) > 0 {
		lines = append(lines, "failures:")
	}
	for _, failure := range report.Failures {
		detail := strings.TrimSpace(failure.ID)
		message := strings.TrimSpace(failure.Message)
		if detail == "" {
			detail = "failure"
		}
		if message != "" {
			detail += " - " + message
		}
		lines = append(lines, "  "+detail)
	}
	return strings.Join(lines, "\n")
}

func writeAgentEvalHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero eval --suite <path> [flags]
  zero eval run --suite <path> [flags]

Validates offline agent eval suites and runs local scoring when a runner is wired in.

Flags:
      --suite <path>      Eval suite JSON file
      --task <id>         Run one task (eval run only)
      --workspace <path>  Workspace path for local scoring (eval run only, required)
      --report-dir <path> Write agent-eval-report.json (eval run only)
      --json              Print JSON output
  -h, --help              Show this help
`)
	return err
}

func defaultRunAgentEval(ctx context.Context, options agentEvalOptions) (agentEvalReport, error) {
	suite, err := agenteval.LoadSuite(options.SuitePath)
	if err != nil {
		return agentEvalReport{}, err
	}
	if options.Mode == "run" {
		report := (agenteval.Runner{}).Run(ctx, suite, agenteval.RunInput{
			TaskID:        options.TaskID,
			WorkspacePath: options.WorkspacePath,
		})
		return agentEvalReportFromRunner(suite, report), nil
	}
	checks := 0
	for _, task := range suite.Tasks {
		// Every task has N verification commands plus one changed-file expectation
		// check in the scoring contract.
		checks += len(task.VerificationCommands) + 1
	}
	return agentEvalReport{
		Suite:  suite.ID,
		Name:   suite.Name,
		Status: "valid",
		OK:     true,
		Tasks:  len(suite.Tasks),
		Checks: checks,
	}, nil
}

func agentEvalReportFromRunner(suite agenteval.Suite, report agenteval.Report) agentEvalReport {
	converted := agentEvalReport{
		Suite:   report.SuiteID,
		Name:    suite.Name,
		TaskID:  report.TaskID,
		Status:  string(report.Status),
		OK:      report.OK,
		Total:   report.Summary.Total,
		Passed:  report.Summary.Passed,
		Failed:  report.Summary.Failed,
		Blocked: report.Summary.Blocked,
		Errors:  report.Summary.Errors,
	}
	if converted.Suite == "" {
		converted.Suite = suite.ID
	}
	if report.Error != "" {
		converted.Failures = append(converted.Failures, agentEvalFailure{
			ID:      "task",
			Message: report.Error,
		})
	}
	for _, result := range report.Results {
		if result.Status == agenteval.StatusPass {
			continue
		}
		converted.Failures = append(converted.Failures, agentEvalFailure{
			ID:      result.ID,
			Message: agentEvalResultMessage(result),
		})
	}
	return converted
}

func agentEvalResultMessage(result agenteval.Result) string {
	for _, value := range []string{result.Message, result.Stderr, string(result.Status)} {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func writeAgentEvalReportFile(reportDir string, report agentEvalReport) error {
	if err := os.MkdirAll(reportDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(report.ReportPath, append(data, '\n'), 0o644)
}
