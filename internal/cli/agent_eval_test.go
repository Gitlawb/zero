package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunEvalHelpIsListed(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"--help"}, &stdout, &stderr, appDeps{})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "eval") || !strings.Contains(stdout.String(), "offline agent eval") {
		t.Fatalf("expected eval command in help, got %q", stdout.String())
	}
}

func TestRunEvalRequiresSuitePath(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"eval"}, &stdout, &stderr, appDeps{
		runAgentEval: func(context.Context, agentEvalOptions) (agentEvalReport, error) {
			t.Fatal("runAgentEval should not be called without --suite")
			return agentEvalReport{}, nil
		},
	})

	if exitCode != exitUsage {
		t.Fatalf("expected usage exit %d, got %d", exitUsage, exitCode)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "--suite requires a path") {
		t.Fatalf("expected missing suite error, got %q", stderr.String())
	}
}

func TestRunEvalJSONMode(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	report := agentEvalReport{
		Suite:  "evals/context.yaml",
		OK:     true,
		Total:  2,
		Passed: 2,
	}

	exitCode := runWithDeps([]string{"eval", "--suite", "evals/context.yaml", "--json"}, &stdout, &stderr, appDeps{
		runAgentEval: func(ctx context.Context, options agentEvalOptions) (agentEvalReport, error) {
			if options.Mode != "validate" || options.SuitePath != "evals/context.yaml" || !options.JSON {
				t.Fatalf("unexpected eval options: %#v", options)
			}
			return report, nil
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
	var decoded agentEvalReport
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("decode eval JSON: %v\n%s", err, stdout.String())
	}
	if decoded.Suite != report.Suite || !decoded.OK || decoded.Passed != 2 {
		t.Fatalf("unexpected eval JSON: %#v", decoded)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw eval JSON: %v", err)
	}
	for _, key := range []string{"tasks", "checks", "total", "passed", "failed", "errors"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("expected JSON key %q in %s", key, stdout.String())
		}
	}
	for _, key := range []string{"tasks", "checks", "failed", "blocked", "errors"} {
		if string(raw[key]) != "0" {
			t.Fatalf("expected JSON key %q to be zero, got %s", key, string(raw[key]))
		}
	}
}

func TestRunEvalRunJSONModePassesRunnerOptions(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{
		"eval", "run",
		"--suite", "evals/context.json",
		"--task", "edit-reader",
		"--workspace", "D:\\work\\zero-fixture",
		"--json",
	}, &stdout, &stderr, appDeps{
		runAgentEval: func(ctx context.Context, options agentEvalOptions) (agentEvalReport, error) {
			if options.Mode != "run" || options.SuitePath != "evals/context.json" || options.TaskID != "edit-reader" || options.WorkspacePath != "D:\\work\\zero-fixture" || !options.JSON {
				t.Fatalf("unexpected eval run options: %#v", options)
			}
			return agentEvalReport{
				Suite:  "quality-context",
				TaskID: "edit-reader",
				Status: "pass",
				OK:     true,
				Total:  2,
				Passed: 2,
			}, nil
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
	var decoded agentEvalReport
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("decode eval run JSON: %v\n%s", err, stdout.String())
	}
	if decoded.TaskID != "edit-reader" || decoded.Status != "pass" || decoded.Blocked != 0 {
		t.Fatalf("unexpected eval run JSON: %#v", decoded)
	}
}

func TestRunEvalBenchJSONModePassesHarnessOptions(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{
		"eval", "bench",
		"--suite", "evals/context.json",
		"--task", "edit-reader",
		"--work-root", "D:\\tmp\\zero-evals",
		"--json",
		"--agent-command", "zero", "exec", "{prompt}",
	}, &stdout, &stderr, appDeps{
		runAgentEval: func(ctx context.Context, options agentEvalOptions) (agentEvalReport, error) {
			if options.Mode != "bench" || options.SuitePath != "evals/context.json" || options.TaskID != "edit-reader" || options.WorkRoot != "D:\\tmp\\zero-evals" || !options.JSON {
				t.Fatalf("unexpected eval bench options: %#v", options)
			}
			if got, want := strings.Join(options.AgentCommand, "\x00"), strings.Join([]string{"zero", "exec", "{prompt}"}, "\x00"); got != want {
				t.Fatalf("agent command = %#v, want zero exec {prompt}", options.AgentCommand)
			}
			return agentEvalReport{
				Suite:  "quality-context",
				TaskID: "edit-reader",
				Status: "pass",
				OK:     true,
				Total:  1,
				Passed: 1,
			}, nil
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
	var decoded agentEvalReport
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("decode eval bench JSON: %v\n%s", err, stdout.String())
	}
	if decoded.TaskID != "edit-reader" || decoded.Status != "pass" || decoded.Passed != 1 {
		t.Fatalf("unexpected eval bench JSON: %#v", decoded)
	}
}

func TestRunEvalBenchReportDirAndKeepWorkspacesPassHarnessOptions(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	reportDir := t.TempDir()

	exitCode := runWithDeps([]string{
		"eval", "bench",
		"--suite=evals/context.json",
		"--task=edit-reader",
		"--keep-workspaces",
		"--report-dir", reportDir,
	}, &stdout, &stderr, appDeps{
		runAgentEval: func(ctx context.Context, options agentEvalOptions) (agentEvalReport, error) {
			if options.Mode != "bench" || !options.KeepWorkspaces || options.ReportDir != reportDir {
				t.Fatalf("unexpected eval bench options: %#v", options)
			}
			if options.WorkRoot != "" {
				t.Fatalf("default work root should remain empty at parse layer: %#v", options)
			}
			return agentEvalReport{
				Suite:   "quality-context",
				TaskID:  "edit-reader",
				Status:  "blocked",
				OK:      false,
				Total:   1,
				Blocked: 1,
			}, nil
		},
	})

	if exitCode != exitProvider {
		t.Fatalf("expected provider-style failure exit %d, got %d", exitProvider, exitCode)
	}
	reportPath := filepath.Join(reportDir, "agent-eval-report.json")
	if _, err := os.Stat(reportPath); err != nil {
		t.Fatalf("expected report file: %v", err)
	}
	if !strings.Contains(stdout.String(), "report: "+reportPath) {
		t.Fatalf("expected text output to mention report path, got:\n%s", stdout.String())
	}
}

func TestRunEvalBenchDefaultHarnessBlocksWithoutAgentCommand(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	suitePath := filepath.Join("..", "agenteval", "testdata", "sample_suite.json")

	exitCode := runWithDeps([]string{
		"eval", "bench",
		"--suite", suitePath,
		"--task", "document-stream-json-verify-events",
		"--json",
	}, &stdout, &stderr, appDeps{})

	if exitCode != exitProvider {
		t.Fatalf("expected provider-style failure exit %d, got %d: %s", exitProvider, exitCode, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
	var decoded agentEvalReport
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("decode eval bench JSON: %v\n%s", err, stdout.String())
	}
	if decoded.Status != "blocked" || decoded.Blocked != 1 {
		t.Fatalf("expected blocked benchmark report, got %#v", decoded)
	}
	if len(decoded.Failures) == 0 || !strings.Contains(decoded.Failures[0].Message, "agent command is required") {
		t.Fatalf("expected agent command failure, got %#v", decoded.Failures)
	}
}

func TestRunEvalBenchRejectsRunOnlyWorkspaceFlag(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"eval", "bench", "--suite", "evals/context.json", "--workspace", "."}, &stdout, &stderr, appDeps{
		runAgentEval: func(context.Context, agentEvalOptions) (agentEvalReport, error) {
			t.Fatal("runAgentEval should not be called for invalid bench flags")
			return agentEvalReport{}, nil
		},
	})

	if exitCode != exitUsage {
		t.Fatalf("expected usage exit %d, got %d", exitUsage, exitCode)
	}
	if !strings.Contains(stderr.String(), "--workspace is only valid for eval run") {
		t.Fatalf("expected workspace mode error, got %q", stderr.String())
	}
}

func TestRunEvalRunRejectsBenchOnlyFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "work root",
			args: []string{"eval", "run", "--suite", "evals/context.json", "--work-root", "D:\\tmp\\zero-evals"},
			want: "--work-root is only valid for eval bench",
		},
		{
			name: "keep workspaces",
			args: []string{"eval", "run", "--suite", "evals/context.json", "--keep-workspaces"},
			want: "--keep-workspaces is only valid for eval bench",
		},
		{
			name: "agent command",
			args: []string{"eval", "run", "--suite", "evals/context.json", "--agent-command", "zero", "exec", "{prompt}"},
			want: "--agent-command is only valid for eval bench",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			exitCode := runWithDeps(tt.args, &stdout, &stderr, appDeps{
				runAgentEval: func(context.Context, agentEvalOptions) (agentEvalReport, error) {
					t.Fatal("runAgentEval should not be called for invalid run flags")
					return agentEvalReport{}, nil
				},
			})

			if exitCode != exitUsage {
				t.Fatalf("expected usage exit %d, got %d", exitUsage, exitCode)
			}
			if !strings.Contains(stderr.String(), tt.want) {
				t.Fatalf("expected %q, got %q", tt.want, stderr.String())
			}
		})
	}
}

func TestRunEvalRunFailureTextShowsSummaryAndFailures(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"eval", "run", "--suite=evals/context.json", "--task=edit-reader"}, &stdout, &stderr, appDeps{
		runAgentEval: func(ctx context.Context, options agentEvalOptions) (agentEvalReport, error) {
			if options.Mode != "run" || options.TaskID != "edit-reader" || options.WorkspacePath != "." {
				t.Fatalf("unexpected eval run options: %#v", options)
			}
			return agentEvalReport{
				Suite:   "quality-context",
				TaskID:  "edit-reader",
				Status:  "fail",
				OK:      false,
				Total:   3,
				Passed:  1,
				Failed:  1,
				Blocked: 1,
				Failures: []agentEvalFailure{{
					ID:      "test",
					Message: "go test ./... exited 1",
				}},
			}, nil
		},
	})

	if exitCode != exitProvider {
		t.Fatalf("expected provider-style failure exit %d, got %d", exitProvider, exitCode)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
	for _, want := range []string{
		"suite: quality-context",
		"task: edit-reader",
		"status: fail",
		"summary: 3 total, 1 passed, 1 failed, 1 blocked, 0 errors",
		"failures:",
		"test - go test ./... exited 1",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, stdout.String())
		}
	}
}

func TestRunEvalRunReportDirWritesJSONReport(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	reportDir := t.TempDir()

	exitCode := runWithDeps([]string{"eval", "run", "--suite", "evals/context.json", "--report-dir", reportDir}, &stdout, &stderr, appDeps{
		runAgentEval: func(ctx context.Context, options agentEvalOptions) (agentEvalReport, error) {
			if options.ReportDir != reportDir {
				t.Fatalf("unexpected report dir: %#v", options)
			}
			return agentEvalReport{
				Suite:  "quality-context",
				TaskID: "edit-reader",
				Status: "pass",
				OK:     true,
				Total:  1,
				Passed: 1,
			}, nil
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	reportPath := filepath.Join(reportDir, "agent-eval-report.json")
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var decoded agentEvalReport
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("decode report: %v\n%s", err, string(data))
	}
	if decoded.ReportPath != reportPath || decoded.Suite != "quality-context" {
		t.Fatalf("unexpected written report: %#v", decoded)
	}
	if !strings.Contains(stdout.String(), "report: "+reportPath) {
		t.Fatalf("expected text output to mention report path, got:\n%s", stdout.String())
	}
}

func TestRunEvalRunDefaultRunnerUsesAgentEvalRunner(t *testing.T) {
	workspace := t.TempDir()
	if output, err := exec.Command("git", "-C", workspace, "init").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, string(output))
	}
	if err := os.WriteFile(filepath.Join(workspace, "expected.txt"), []byte("changed"), 0o600); err != nil {
		t.Fatalf("write expected file: %v", err)
	}
	suitePath := filepath.Join(t.TempDir(), "suite.json")
	if err := os.WriteFile(suitePath, []byte(`{
		"id": "runner-cli",
		"name": "Runner CLI",
		"tasks": [{
			"id": "local-score",
			"name": "Local score",
			"prompt": "Touch the expected file.",
			"workspaceFixture": "fixtures/runner",
			"verificationCommands": [
				{"id": "go-version", "name": "Go version", "command": ["go", "version"]}
			],
			"expectedChangedFiles": ["expected.txt"]
		}]
	}`), 0o600); err != nil {
		t.Fatalf("write suite: %v", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"eval", "run", "--suite", suitePath, "--workspace", workspace}, &stdout, &stderr, appDeps{})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit %d, got %d: %s\n%s", exitSuccess, exitCode, stderr.String(), stdout.String())
	}
	for _, want := range []string{
		"suite: runner-cli",
		"name: Runner CLI",
		"task: local-score",
		"summary: 2 total, 2 passed, 0 failed, 0 blocked, 0 errors",
		"status: pass",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, stdout.String())
		}
	}
}

func TestRunEvalDefaultRunnerLoadsSuite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "suite.json")
	if err := os.WriteFile(path, []byte(`{
		"id": "quality-foundation",
		"name": "Quality foundation",
		"tasks": [{
			"id": "prompt-discipline",
			"name": "Prompt discipline",
			"prompt": "Improve the system prompt.",
			"workspaceFixture": "fixtures/zero",
			"verificationCommands": [
				{"id": "test", "name": "Tests", "command": ["go", "test", "./internal/agent"]}
			],
			"expectedChangedFiles": ["internal/agent/system_prompt.md"]
		}]
	}`), 0o600); err != nil {
		t.Fatalf("write suite: %v", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"eval", "--suite", path}, &stdout, &stderr, appDeps{})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
	for _, want := range []string{
		"suite: quality-foundation",
		"name: Quality foundation",
		"summary: 1 tasks, 2 checks",
		"status: valid",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, stdout.String())
		}
	}
}

func TestRunEvalFailingSuiteReturnsProviderExit(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"eval", "--suite=evals/failing.yaml"}, &stdout, &stderr, appDeps{
		runAgentEval: func(context.Context, agentEvalOptions) (agentEvalReport, error) {
			return agentEvalReport{
				Suite:  "evals/failing.yaml",
				OK:     false,
				Total:  2,
				Passed: 1,
				Failed: 1,
				Failures: []agentEvalFailure{{
					ID:      "context.recall",
					Message: "expected answer to cite loaded context",
				}},
			}, nil
		},
	})

	if exitCode != exitProvider {
		t.Fatalf("expected provider-style failure exit %d, got %d", exitProvider, exitCode)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "Zero agent eval") || !strings.Contains(stdout.String(), "context.recall") {
		t.Fatalf("unexpected eval text output: %q", stdout.String())
	}
}

func TestRunEvalRunnerErrorReturnsUsage(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"eval", "--suite", "missing.yaml"}, &stdout, &stderr, appDeps{
		runAgentEval: func(context.Context, agentEvalOptions) (agentEvalReport, error) {
			return agentEvalReport{}, errors.New("suite file not found")
		},
	})

	if exitCode != exitUsage {
		t.Fatalf("expected usage exit %d, got %d", exitUsage, exitCode)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "suite file not found") {
		t.Fatalf("expected runner error, got %q", stderr.String())
	}
}
