package testrunner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectFindsWorkspaceChecksInStableOrder(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/app\n")
	writeFile(t, filepath.Join(root, "bun.lock"), "")
	writeFile(t, filepath.Join(root, "package.json"), `{
		"scripts": {
			"test": "bun test ./tests",
			"typecheck": "tsc --noEmit",
			"build": "go run ./cmd/zero-release build",
			"lint": "eslint ."
		}
	}`)
	writeFile(t, filepath.Join(root, "pyproject.toml"), "[tool.pytest.ini_options]\n")
	writeFile(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = \"sample\"\n")

	checks, err := Detect(root)
	if err != nil {
		t.Fatalf("Detect returned error: %v", err)
	}

	if got, want := checkIDs(checks), []string{
		"go.test",
		"bun.typecheck",
		"bun.test",
		"bun.build",
		"bun.lint",
		"python.pytest",
		"cargo.test",
	}; !equalStrings(got, want) {
		t.Fatalf("check ids = %#v, want %#v", got, want)
	}
	if got := checks[1].Command; !equalStrings(got, []string{"bun", "run", "typecheck"}) {
		t.Fatalf("typecheck command = %#v, want bun run typecheck", got)
	}
	if checks[5].Framework != FrameworkPytest || checks[6].Framework != FrameworkCargo {
		t.Fatalf("unexpected framework metadata: %#v", checks)
	}
}

func TestDetectUsesPackageManagerLockfiles(t *testing.T) {
	for _, test := range []struct {
		name        string
		lockfile    string
		wantID      string
		wantCommand []string
	}{
		{name: "npm", lockfile: "package-lock.json", wantID: "npm.test", wantCommand: []string{"npm", "run", "test"}},
		{name: "pnpm", lockfile: "pnpm-lock.yaml", wantID: "pnpm.test", wantCommand: []string{"pnpm", "run", "test"}},
		{name: "yarn", lockfile: "yarn.lock", wantID: "yarn.test", wantCommand: []string{"yarn", "run", "test"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, test.lockfile), "")
			writeFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"test command"}}`)

			checks, err := Detect(root)
			if err != nil {
				t.Fatalf("Detect returned error: %v", err)
			}
			if len(checks) != 1 {
				t.Fatalf("checks = %#v, want one package test check", checks)
			}
			if checks[0].ID != test.wantID || !equalStrings(checks[0].Command, test.wantCommand) {
				t.Fatalf("check = %#v, want id %q command %#v", checks[0], test.wantID, test.wantCommand)
			}
		})
	}
}

func TestDetectDefaultsPlainNodeWorkspacesToNPM(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"node --test"}}`)

	checks, err := Detect(root)
	if err != nil {
		t.Fatalf("Detect returned error: %v", err)
	}

	if len(checks) != 1 {
		t.Fatalf("checks = %#v, want one package test check", checks)
	}
	if checks[0].ID != "npm.test" || checks[0].Framework != FrameworkNode || !equalStrings(checks[0].Command, []string{"npm", "run", "test"}) {
		t.Fatalf("plain node check = %#v, want npm.test with npm run test", checks[0])
	}
}

func TestParseSummaryExtractsGoFailures(t *testing.T) {
	check := Check{ID: "go.test", Framework: FrameworkGo}
	output := `=== RUN   TestPass
--- PASS: TestPass (0.00s)
=== RUN   TestSecret
--- FAIL: TestSecret (0.00s)
    secret_test.go:12: expected [REDACTED] to stay hidden
FAIL`

	summary := ParseSummary(check, output, "")

	if summary == nil {
		t.Fatal("expected parsed summary")
	}
	if summary.Total != 2 || summary.Passed != 1 || summary.Failed != 1 {
		t.Fatalf("unexpected counts: %#v", summary)
	}
	if len(summary.Failures) != 1 || summary.Failures[0].Name != "TestSecret" {
		t.Fatalf("unexpected failures: %#v", summary.Failures)
	}
	if summary.Failures[0].File != "secret_test.go:12" || summary.Failures[0].Message == "" {
		t.Fatalf("unexpected failure detail: %#v", summary.Failures[0])
	}
}

func TestParseSummaryCountsNonVerboseGoPackagePassesWithFailures(t *testing.T) {
	check := Check{ID: "go.test", Framework: FrameworkGo}
	output := `--- FAIL: TestBroken (0.00s)
    broken_test.go:12: expected value
FAIL
FAIL	example.com/app/internal/broken	0.02s
ok  	example.com/app/internal/healthy	0.12s
`

	summary := ParseSummary(check, output, "")

	if summary == nil {
		t.Fatal("expected parsed summary")
	}
	if summary.Total != 2 || summary.Passed != 1 || summary.Failed != 1 {
		t.Fatalf("unexpected mixed non-verbose summary: %#v", summary)
	}
	if len(summary.Failures) != 1 || summary.Failures[0].Name != "TestBroken" {
		t.Fatalf("unexpected failures: %#v", summary.Failures)
	}
}

func TestParseSummaryExtractsGoPackageStatus(t *testing.T) {
	check := Check{ID: "go.test", Framework: FrameworkGo}
	output := "?   \texample.com/app/cmd\t[no test files]\nok  \texample.com/app/internal/one\t(cached)\nok  \texample.com/app/internal/two\t0.12s\n"

	summary := ParseSummary(check, output, "")

	if summary == nil {
		t.Fatal("expected parsed summary")
	}
	if summary.Total != 2 || summary.Passed != 2 || summary.Failed != 0 {
		t.Fatalf("unexpected package summary: %#v", summary)
	}
}

func TestParseSummaryDoesNotDoubleCountVerboseGoPackageStatus(t *testing.T) {
	check := Check{ID: "go.test", Framework: FrameworkGo}
	output := `=== RUN   TestOne
--- PASS: TestOne (0.00s)
=== RUN   TestTwo
--- PASS: TestTwo (0.00s)
ok  	example.com/app/internal/two	0.12s
`

	summary := ParseSummary(check, output, "")

	if summary == nil {
		t.Fatal("expected parsed summary")
	}
	if summary.Total != 2 || summary.Passed != 2 || summary.Failed != 0 {
		t.Fatalf("unexpected verbose package summary: %#v", summary)
	}
}

func TestParseSummaryExtractsCommonRunnerCounts(t *testing.T) {
	for _, test := range []struct {
		name      string
		check     Check
		output    string
		wantTotal int
		wantPass  int
		wantFail  int
		wantSkip  int
		wantName  string
	}{
		{
			name:      "bun",
			check:     Check{ID: "bun.test", Framework: FrameworkBun},
			output:    "(pass) package works\n(fail) cli rejects bad flags\n\n 1 pass\n 1 fail\n",
			wantTotal: 2,
			wantPass:  1,
			wantFail:  1,
			wantName:  "cli rejects bad flags",
		},
		{
			name:      "node tap",
			check:     Check{ID: "npm.test", Framework: FrameworkNode},
			output:    "ok 1 - config loads\nnot ok 2 - session forks\n# pass 1\n# fail 1\n",
			wantTotal: 2,
			wantPass:  1,
			wantFail:  1,
			wantName:  "session forks",
		},
		{
			name:      "pytest",
			check:     Check{ID: "python.pytest", Framework: FrameworkPytest},
			output:    "FAILED tests/test_cli.py::test_stream - AssertionError: boom\n1 failed, 2 passed, 1 skipped in 0.12s\n",
			wantTotal: 4,
			wantPass:  2,
			wantFail:  1,
			wantSkip:  1,
			wantName:  "tests/test_cli.py::test_stream",
		},
		{
			name:      "cargo",
			check:     Check{ID: "cargo.test", Framework: FrameworkCargo},
			output:    "test tests::runs_zero ... ok\ntest tests::rejects_bad_config ... FAILED\n\ntest result: FAILED. 1 passed; 1 failed; 0 ignored\n",
			wantTotal: 2,
			wantPass:  1,
			wantFail:  1,
			wantName:  "tests::rejects_bad_config",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			summary := ParseSummary(test.check, test.output, "")
			if summary == nil {
				t.Fatal("expected parsed summary")
			}
			if summary.Total != test.wantTotal || summary.Passed != test.wantPass || summary.Failed != test.wantFail || summary.Skipped != test.wantSkip {
				t.Fatalf("summary = %#v, want total/pass/fail/skip %d/%d/%d/%d", summary, test.wantTotal, test.wantPass, test.wantFail, test.wantSkip)
			}
			if len(summary.Failures) != 1 || summary.Failures[0].Name != test.wantName {
				t.Fatalf("failures = %#v, want %q", summary.Failures, test.wantName)
			}
		})
	}
}

func checkIDs(checks []Check) []string {
	ids := make([]string, 0, len(checks))
	for _, check := range checks {
		ids = append(ids, check.ID)
	}
	return ids
}

func equalStrings(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
