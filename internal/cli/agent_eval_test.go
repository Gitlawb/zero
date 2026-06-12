package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
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
			if options.SuitePath != "evals/context.yaml" || !options.JSON {
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
	for _, key := range []string{"tasks", "checks", "failed", "errors"} {
		if string(raw[key]) != "0" {
			t.Fatalf("expected JSON key %q to be zero, got %s", key, string(raw[key]))
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
