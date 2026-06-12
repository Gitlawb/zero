package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Gitlawb/zero/internal/agenteval"
)

type agentEvalOptions struct {
	SuitePath string `json:"suite_path"`
	JSON      bool   `json:"json"`
}

type agentEvalReport struct {
	Suite    string             `json:"suite"`
	Name     string             `json:"name,omitempty"`
	Status   string             `json:"status,omitempty"`
	OK       bool               `json:"ok"`
	Tasks    int                `json:"tasks"`
	Checks   int                `json:"checks"`
	Total    int                `json:"total"`
	Passed   int                `json:"passed"`
	Failed   int                `json:"failed"`
	Errors   int                `json:"errors"`
	Failures []agentEvalFailure `json:"failures,omitempty"`
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
	report, err := deps.runAgentEval(context.Background(), options)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
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
	options := agentEvalOptions{}
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
		case strings.HasPrefix(arg, "-"):
			return options, false, execUsageError{fmt.Sprintf("unknown eval flag %q", arg)}
		default:
			return options, false, execUsageError{fmt.Sprintf("unexpected eval argument %q", arg)}
		}
	}
	if options.SuitePath == "" {
		return options, false, execUsageError{"--suite requires a path"}
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
	if report.Tasks > 0 || report.Checks > 0 {
		lines = append(lines, fmt.Sprintf("summary: %d tasks, %d checks", report.Tasks, report.Checks))
	} else {
		lines = append(lines, fmt.Sprintf("summary: %d total, %d passed, %d failed, %d errors", report.Total, report.Passed, report.Failed, report.Errors))
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

Validates offline agent eval suites for maintainers.

Flags:
      --suite <path>      Eval suite JSON file
      --json              Print JSON output
  -h, --help              Show this help
`)
	return err
}

func defaultRunAgentEval(_ context.Context, options agentEvalOptions) (agentEvalReport, error) {
	suite, err := agenteval.LoadSuite(options.SuitePath)
	if err != nil {
		return agentEvalReport{}, err
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
