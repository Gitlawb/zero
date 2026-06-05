package verify

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDetectPlanFindsBunAndGoChecks(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/zero\n")
	writeFile(t, filepath.Join(root, "package.json"), `{
		"scripts": {
			"test": "bun test ./tests",
			"typecheck": "tsc --noEmit",
			"build": "bun run scripts/build.ts",
			"lint": "eslint ."
		}
	}`)

	plan, err := DetectPlan(root)
	if err != nil {
		t.Fatalf("DetectPlan returned error: %v", err)
	}

	ids := checkIDs(plan.Checks)
	for _, want := range []string{"go.test", "bun.typecheck", "bun.test", "bun.build", "bun.lint"} {
		if !contains(ids, want) {
			t.Fatalf("expected check %q in %#v", want, ids)
		}
	}
	if plan.Checks[0].Command[0] != "go" || strings.Join(plan.Checks[0].Command, " ") != "go test ./..." {
		t.Fatalf("first check = %#v, want go test ./...", plan.Checks[0])
	}
}

func TestRunExecutesPlanAndRedactsOutput(t *testing.T) {
	root := t.TempDir()
	plan := Plan{Root: root, Checks: []Check{
		{ID: "go.test", Name: "Go tests", Command: []string{"go", "test", "./..."}},
		{ID: "bun.test", Name: "Bun tests", Command: []string{"bun", "test"}},
	}}
	runner := &fakeCommandRunner{results: []CommandResult{
		{ExitCode: 0, Stdout: "ok\n"},
		{ExitCode: 1, Stdout: "token sk-proj-secret1234567890", Stderr: "fail\n"},
	}}

	report := Run(context.Background(), plan, RunOptions{
		Runner:    runner.Run,
		Now:       fixedVerifyTime("2026-06-05T10:45:00Z"),
		TimeoutMS: 5000,
	})

	if report.OK {
		t.Fatalf("report.OK = true, want false")
	}
	if report.Summary.Total != 2 || report.Summary.Passed != 1 || report.Summary.Failed != 1 {
		t.Fatalf("unexpected summary: %#v", report.Summary)
	}
	if report.Results[1].Status != StatusFail || report.Results[1].ExitCode != 1 {
		t.Fatalf("unexpected failing result: %#v", report.Results[1])
	}
	if strings.Contains(report.Results[1].Stdout, "sk-proj-secret") || !strings.Contains(report.Results[1].Stdout, "[REDACTED]") {
		t.Fatalf("expected redacted stdout, got %q", report.Results[1].Stdout)
	}
	if got := runner.calls[0].dir; got != root {
		t.Fatalf("runner dir = %q, want %q", got, root)
	}
}

func TestRunFiltersChecksByID(t *testing.T) {
	root := t.TempDir()
	plan := Plan{Root: root, Checks: []Check{
		{ID: "go.test", Name: "Go tests", Command: []string{"go", "test", "./..."}},
		{ID: "bun.test", Name: "Bun tests", Command: []string{"bun", "test"}},
	}}
	runner := &fakeCommandRunner{results: []CommandResult{{ExitCode: 0, Stdout: "ok\n"}}}

	report := Run(context.Background(), plan, RunOptions{
		Only:   []string{"bun.test"},
		Runner: runner.Run,
		Now:    fixedVerifyTime("2026-06-05T10:50:00Z"),
	})

	if report.Summary.Total != 1 || report.Results[0].ID != "bun.test" {
		t.Fatalf("unexpected filtered report: %#v", report)
	}
	if got := strings.Join(runner.calls[0].args, " "); got != "bun test" {
		t.Fatalf("runner command = %q, want bun test", got)
	}
}

func TestRunReportsUnknownOnlyChecks(t *testing.T) {
	root := t.TempDir()
	plan := Plan{Root: root, Checks: []Check{
		{ID: "go.test", Name: "Go tests", Command: []string{"go", "test", "./..."}},
	}}

	report := Run(context.Background(), plan, RunOptions{
		Only: []string{"missing.check"},
		Now:  fixedVerifyTime("2026-06-05T10:55:00Z"),
	})

	if report.OK {
		t.Fatalf("report.OK = true, want false")
	}
	if report.Summary.Total != 1 || report.Summary.Errors != 1 {
		t.Fatalf("unexpected summary for unknown check: %#v", report.Summary)
	}
	if report.Results[0].Status != StatusError || !strings.Contains(report.Results[0].Error, "unknown verification check") {
		t.Fatalf("unexpected unknown check result: %#v", report.Results[0])
	}
}

func TestRunReportsUnknownOnlyChecksInStableOrder(t *testing.T) {
	root := t.TempDir()
	plan := Plan{Root: root}

	report := Run(context.Background(), plan, RunOptions{
		Only: []string{"missing.z", "missing.a"},
		Now:  fixedVerifyTime("2026-06-05T10:56:00Z"),
	})

	if len(report.Results) != 2 {
		t.Fatalf("expected two unknown check results, got %#v", report.Results)
	}
	if report.Results[0].ID != "missing.a" || report.Results[1].ID != "missing.z" {
		t.Fatalf("unknown checks are not stable sorted: %#v", report.Results)
	}
}

func TestDetectPlanRejectsMissingRoot(t *testing.T) {
	_, err := DetectPlan(filepath.Join(t.TempDir(), "missing"))
	if err == nil || !strings.Contains(err.Error(), "verify root must be an existing directory") {
		t.Fatalf("expected missing root error, got %v", err)
	}
}

type fakeCommandRunner struct {
	calls   []commandCall
	results []CommandResult
}

func (runner *fakeCommandRunner) Run(ctx context.Context, dir string, command []string, timeout time.Duration) (CommandResult, error) {
	runner.calls = append(runner.calls, commandCall{dir: dir, args: append([]string{}, command...)})
	if len(runner.results) == 0 {
		return CommandResult{}, nil
	}
	result := runner.results[0]
	runner.results = runner.results[1:]
	return result, nil
}

type commandCall struct {
	dir  string
	args []string
}

func checkIDs(checks []Check) []string {
	ids := make([]string, 0, len(checks))
	for _, check := range checks {
		ids = append(ids, check.ID)
	}
	return ids
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func fixedVerifyTime(value string) func() time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return func() time.Time { return parsed }
}
