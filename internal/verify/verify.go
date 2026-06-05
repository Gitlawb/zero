package verify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Gitlawb/zero/internal/redaction"
)

type Status string

const (
	StatusPass  Status = "pass"
	StatusFail  Status = "fail"
	StatusError Status = "error"
)

type Check struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Command []string `json:"command"`
}

type Plan struct {
	Root   string  `json:"root"`
	Checks []Check `json:"checks"`
}

type Summary struct {
	Total  int `json:"total"`
	Passed int `json:"passed"`
	Failed int `json:"failed"`
	Errors int `json:"errors"`
}

type Result struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Command    []string `json:"command"`
	Status     Status   `json:"status"`
	ExitCode   int      `json:"exitCode"`
	Stdout     string   `json:"stdout,omitempty"`
	Stderr     string   `json:"stderr,omitempty"`
	StartedAt  string   `json:"startedAt"`
	EndedAt    string   `json:"endedAt"`
	DurationMs int      `json:"durationMs"`
	Error      string   `json:"error,omitempty"`
}

type Report struct {
	Root      string   `json:"root"`
	StartedAt string   `json:"startedAt"`
	EndedAt   string   `json:"endedAt"`
	OK        bool     `json:"ok"`
	Summary   Summary  `json:"summary"`
	Results   []Result `json:"results"`
}

type CommandResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

type Runner func(context.Context, string, []string, time.Duration) (CommandResult, error)

type RunOptions struct {
	Only      []string
	TimeoutMS int
	Runner    Runner
	Now       func() time.Time
}

const defaultTimeoutMS = 120000

func DetectPlan(root string) (Plan, error) {
	resolvedRoot, err := resolveRoot(root)
	if err != nil {
		return Plan{}, err
	}
	checks := []Check{}
	if fileExists(filepath.Join(resolvedRoot, "go.mod")) {
		checks = append(checks, Check{ID: "go.test", Name: "Go tests", Command: []string{"go", "test", "./..."}})
	}
	checks = append(checks, detectPackageChecks(resolvedRoot)...)
	return Plan{Root: resolvedRoot, Checks: checks}, nil
}

func Run(ctx context.Context, plan Plan, options RunOptions) Report {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	runner := options.Runner
	if runner == nil {
		runner = defaultRunner
	}
	timeout := time.Duration(firstPositive(options.TimeoutMS, defaultTimeoutMS)) * time.Millisecond
	start := now()
	report := Report{
		Root:      plan.Root,
		StartedAt: formatTime(start),
		OK:        true,
	}
	checks, unknownChecks := filterChecks(plan.Checks, options.Only)
	for _, check := range checks {
		checkStart := now()
		result := Result{
			ID:        check.ID,
			Name:      check.Name,
			Command:   append([]string{}, check.Command...),
			StartedAt: formatTime(checkStart),
		}
		commandResult, err := runner(ctx, plan.Root, check.Command, timeout)
		checkEnd := now()
		result.EndedAt = formatTime(checkEnd)
		result.DurationMs = int(checkEnd.Sub(checkStart).Milliseconds())
		result.Stdout = redaction.RedactString(commandResult.Stdout, redaction.Options{})
		result.Stderr = redaction.RedactString(commandResult.Stderr, redaction.Options{})
		result.ExitCode = commandResult.ExitCode
		if err != nil {
			result.Status = StatusError
			result.Error = redaction.RedactString(err.Error(), redaction.Options{})
			report.Summary.Errors++
		} else if commandResult.ExitCode == 0 {
			result.Status = StatusPass
			report.Summary.Passed++
		} else {
			result.Status = StatusFail
			report.Summary.Failed++
		}
		report.Results = append(report.Results, result)
	}
	for _, id := range unknownChecks {
		at := formatTime(now())
		report.Results = append(report.Results, Result{
			ID:        id,
			Name:      "Unknown verification check",
			Status:    StatusError,
			ExitCode:  -1,
			StartedAt: at,
			EndedAt:   at,
			Error:     fmt.Sprintf("unknown verification check %q", id),
		})
		report.Summary.Errors++
	}
	report.Summary.Total = len(report.Results)
	report.OK = report.Summary.Failed == 0 && report.Summary.Errors == 0
	report.EndedAt = formatTime(now())
	return report
}

func detectPackageChecks(root string) []Check {
	path := filepath.Join(root, "package.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil
	}
	checks := []Check{}
	for _, candidate := range []struct {
		script string
		id     string
		name   string
	}{
		{script: "typecheck", id: "bun.typecheck", name: "Bun typecheck"},
		{script: "test", id: "bun.test", name: "Bun tests"},
		{script: "build", id: "bun.build", name: "Bun build"},
		{script: "lint", id: "bun.lint", name: "Bun lint"},
	} {
		if strings.TrimSpace(pkg.Scripts[candidate.script]) == "" {
			continue
		}
		checks = append(checks, Check{
			ID:      candidate.id,
			Name:    candidate.name,
			Command: []string{"bun", "run", candidate.script},
		})
	}
	return checks
}

func defaultRunner(ctx context.Context, dir string, command []string, timeout time.Duration) (CommandResult, error) {
	if len(command) == 0 {
		return CommandResult{ExitCode: -1}, fmt.Errorf("verify command is empty")
	}
	commandCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		commandCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	cmd := exec.CommandContext(commandCtx, command[0], command[1:]...)
	cmd.Dir = dir
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		exitCode = -1
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
			err = nil
		}
	}
	if commandCtx.Err() == context.DeadlineExceeded {
		return CommandResult{
			ExitCode: -1,
			Stdout:   stdout.String(),
			Stderr:   stderr.String(),
		}, fmt.Errorf("command timed out after %dms", timeout.Milliseconds())
	}
	return CommandResult{
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	}, err
}

func resolveRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve verify root: %w", err)
		}
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve verify root: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("verify root must be an existing directory: %s", absolute)
	}
	return filepath.Clean(absolute), nil
}

func filterChecks(checks []Check, only []string) ([]Check, []string) {
	if len(only) == 0 {
		return append([]Check{}, checks...), nil
	}
	allowed := map[string]bool{}
	for _, id := range only {
		trimmed := strings.TrimSpace(id)
		if trimmed != "" {
			allowed[trimmed] = true
		}
	}
	filtered := []Check{}
	seen := map[string]bool{}
	for _, check := range checks {
		if allowed[check.ID] {
			filtered = append(filtered, check)
			seen[check.ID] = true
		}
	}
	unknown := []string{}
	for id := range allowed {
		if !seen[id] {
			unknown = append(unknown, id)
		}
	}
	sort.Strings(unknown)
	return filtered, unknown
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
