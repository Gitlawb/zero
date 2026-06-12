package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTaskSuite(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "suite.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write suite: %v", err)
	}
	return path
}

func TestParseTaskArgsRequiresSuiteAndModel(t *testing.T) {
	if _, err := parseTaskArgs([]string{"--model", "m"}, emptyEnv); err == nil || !strings.Contains(err.Error(), "--suite") {
		t.Fatalf("expected missing-suite error, got %v", err)
	}
	if _, err := parseTaskArgs([]string{"--suite", "/x.json"}, emptyEnv); err == nil || !strings.Contains(err.Error(), "--model") {
		t.Fatalf("expected missing-model error, got %v", err)
	}
}

func TestParseTaskArgsReadsFlagsAndEnv(t *testing.T) {
	env := func(key string) string {
		return map[string]string{
			"ZERO_BENCH_COMMIT":  "deadbeef",
			"ZERO_BENCH_VERSION": "9.9.9",
		}[key]
	}
	options, err := parseTaskArgs([]string{
		"--suite", "/tmp/suite.json",
		"--model", "test-model",
		"--mode", "build",
		"--self-correct",
		"--binary", "/usr/local/bin/zero",
		"--output", "dist/bench.json",
		"--json",
	}, env)
	if err != nil {
		t.Fatalf("parseTaskArgs error: %v", err)
	}
	if options.SuitePath != "/tmp/suite.json" || options.Model != "test-model" || options.Mode != "build" {
		t.Fatalf("options = %#v", options)
	}
	if !options.SelfCorrect || !options.JSON {
		t.Fatalf("flags not parsed: %#v", options)
	}
	if options.Binary != "/usr/local/bin/zero" || options.Output != "dist/bench.json" {
		t.Fatalf("binary/output = %q/%q", options.Binary, options.Output)
	}
	if options.Commit != "deadbeef" || options.Version != "9.9.9" {
		t.Fatalf("commit/version from env = %q/%q", options.Commit, options.Version)
	}
}

func TestParseTaskArgsFlagsOverrideEnv(t *testing.T) {
	env := func(key string) string {
		return map[string]string{"ZERO_BENCH_COMMIT": "fromenv"}[key]
	}
	options, err := parseTaskArgs([]string{"--suite", "/s.json", "--model", "m", "--commit", "fromflag"}, env)
	if err != nil {
		t.Fatalf("parseTaskArgs error: %v", err)
	}
	if options.Commit != "fromflag" {
		t.Fatalf("commit = %q, want fromflag (flag overrides env)", options.Commit)
	}
}

func TestRunTasksCommandWritesRecordWithoutBinary(t *testing.T) {
	// With --dry-run no agent is invoked: every task is recorded as skipped. This
	// exercises the full record path (model + commit + self-correct flag) without
	// needing a real zero binary or model.
	suite := writeTaskSuite(t, `{
		"id": "terminal-bench-mini",
		"name": "Terminal-Bench (mini)",
		"tasks": [
			{"id": "t1", "name": "fix", "prompt": "do the thing"},
			{"id": "t2", "name": "flag", "prompt": "add a flag"}
		]
	}`)
	outPath := filepath.Join(t.TempDir(), "bench.json")

	var stdout, stderr bytes.Buffer
	code := runTasksCommand([]string{
		"--suite", suite,
		"--model", "test-model",
		"--self-correct",
		"--commit", "abc1234",
		"--version", "1.2.3",
		"--dry-run",
		"--output", outPath,
	}, emptyEnv, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runTasksCommand code = %d, stderr = %q", code, stderr.String())
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var record struct {
		Model       string `json:"model"`
		Commit      string `json:"commit"`
		SelfCorrect bool   `json:"selfCorrect"`
		Suite       string `json:"suite"`
		Attempted   int    `json:"tasksAttempted"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("decode record: %v\n%s", err, data)
	}
	if record.Model != "test-model" || record.Commit != "abc1234" || !record.SelfCorrect {
		t.Fatalf("record = %#v", record)
	}
	if record.Suite != "terminal-bench-mini" || record.Attempted != 2 {
		t.Fatalf("record suite/attempted = %q/%d", record.Suite, record.Attempted)
	}
	if !strings.Contains(stdout.String(), "self-correct: on") {
		t.Fatalf("summary missing self-correct line:\n%s", stdout.String())
	}
}

func TestRunTasksCommandRejectsMissingSuiteFile(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runTasksCommand([]string{"--suite", "/no/such/suite.json", "--model", "m", "--dry-run"}, emptyEnv, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit for missing suite file")
	}
	if stderr.Len() == 0 {
		t.Fatalf("expected an error message on stderr")
	}
}

func TestSampleTaskSuiteIsValidAndRunnableDry(t *testing.T) {
	// The published example suite (referenced by docs/BENCHMARK.md) must load and
	// dry-run cleanly so the documented command never bit-rots.
	outPath := filepath.Join(t.TempDir(), "bench.json")
	var stdout, stderr bytes.Buffer
	code := runTasksCommand([]string{
		"--suite", filepath.Join("testdata", "terminal-bench-sample.json"),
		"--model", "example-model",
		"--dry-run",
		"--output", outPath,
	}, emptyEnv, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("sample suite dry run code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "terminal-bench-sample") {
		t.Fatalf("summary should name the suite:\n%s", stdout.String())
	}
}

func TestTasksSubcommandRoutedFromMain(t *testing.T) {
	var stdout, stderr bytes.Buffer
	// "tasks --help" must route to the task harness help, not the perf-bench flags.
	code := run([]string{"tasks", "--help"}, emptyEnv, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run tasks --help code = %d stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "zero-perf-bench tasks") {
		t.Fatalf("tasks help missing usage:\n%s", stdout.String())
	}
}
