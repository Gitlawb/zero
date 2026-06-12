package agenteval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadSuiteParsesJSONAndNormalizesDeterministicFields(t *testing.T) {
	path := writeSuite(t, `{
		"id": "quality-context",
		"name": "Quality context",
		"description": "Offline regression tasks",
		"tasks": [{
			"id": "edit-reader",
			"name": "Edit reader",
			"description": "Keep prompt context tight.",
			"tags": ["repo-map", "tool-use"],
			"difficulty": "medium",
			"prompt": "Update the reader.",
			"workspaceFixture": "fixtures/reader",
			"requiredTraceEvents": ["tool:read_file", "tool:apply_patch"],
			"contextChecks": {
				"requiredFiles": ["internal/reader/a/../b.go"],
				"forbiddenFiles": ["node_modules/cache.txt"]
			},
			"verificationCommands": [
				{"id": "test", "name": "Tests", "command": ["go", "test", "./..."]}
			],
			"expectedChangedFiles": ["internal/reader/a/../b.go", "internal/reader/a.go"],
			"forbiddenChangedFiles": ["docs/generated.log"]
		}]
	}`)

	suite, err := LoadSuite(path)
	if err != nil {
		t.Fatalf("LoadSuite returned error: %v", err)
	}

	if suite.ID != "quality-context" || suite.Tasks[0].WorkspaceFixture != "fixtures/reader" {
		t.Fatalf("unexpected suite: %#v", suite)
	}
	wantFiles := []string{"internal/reader/a.go", "internal/reader/b.go"}
	if !reflect.DeepEqual(suite.Tasks[0].ExpectedChangedFiles, wantFiles) {
		t.Fatalf("expected files = %#v, want %#v", suite.Tasks[0].ExpectedChangedFiles, wantFiles)
	}
	if got := strings.Join(suite.Tasks[0].VerificationCommands[0].Command, " "); got != "go test ./..." {
		t.Fatalf("command = %q, want go test ./...", got)
	}
	task := suite.Tasks[0]
	if !reflect.DeepEqual(task.Tags, []string{"repo-map", "tool-use"}) {
		t.Fatalf("tags = %#v", task.Tags)
	}
	if task.Difficulty != "medium" {
		t.Fatalf("difficulty = %q", task.Difficulty)
	}
	if !reflect.DeepEqual(task.RequiredTraceEvents, []string{"tool:apply_patch", "tool:read_file"}) {
		t.Fatalf("required trace events = %#v", task.RequiredTraceEvents)
	}
	if !reflect.DeepEqual(task.ContextChecks.RequiredFiles, []string{"internal/reader/b.go"}) {
		t.Fatalf("context required files = %#v", task.ContextChecks.RequiredFiles)
	}
	if !reflect.DeepEqual(task.ContextChecks.ForbiddenFiles, []string{"node_modules/cache.txt"}) {
		t.Fatalf("context forbidden files = %#v", task.ContextChecks.ForbiddenFiles)
	}
	if !reflect.DeepEqual(task.ForbiddenChangedFiles, []string{"docs/generated.log"}) {
		t.Fatalf("forbidden changed files = %#v", task.ForbiddenChangedFiles)
	}
}

func TestLoadSuiteRejectsTrailingJSON(t *testing.T) {
	path := writeSuite(t, `{
		"id": "quality-context",
		"name": "Quality context",
		"tasks": [{
			"id": "edit-reader",
			"name": "Edit reader",
			"prompt": "Update the reader.",
			"workspaceFixture": "fixtures/reader",
			"verificationCommands": [
				{"id": "test", "name": "Tests", "command": ["go", "test", "./..."]}
			],
			"expectedChangedFiles": ["internal/reader/a.go"]
		}]
	}
	{}`)

	_, err := LoadSuite(path)
	if err == nil {
		t.Fatal("LoadSuite returned nil, want trailing JSON error")
	}
	if !strings.Contains(err.Error(), "trailing JSON") {
		t.Fatalf("LoadSuite error = %q, want trailing JSON", err.Error())
	}
}

func TestSampleSuiteLoads(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join("testdata", "*.json"))
	if err != nil {
		t.Fatalf("glob sample suites: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("expected at least one sample suite")
	}
	for _, path := range matches {
		t.Run(filepath.Base(path), func(t *testing.T) {
			if _, err := LoadSuite(path); err != nil {
				t.Fatalf("sample suite should load: %v", err)
			}
		})
	}

	suite, err := LoadSuite(filepath.Join("testdata", "sample_suite.json"))
	if err != nil {
		t.Fatalf("sample suite should load: %v", err)
	}
	if len(suite.Tasks) < 8 || len(suite.Tasks) > 12 {
		t.Fatalf("sample suite tasks = %d, want 8-12", len(suite.Tasks))
	}
	traceTasks := 0
	for _, task := range suite.Tasks {
		if len(task.Tags) == 0 {
			t.Fatalf("sample task %q has no tags", task.ID)
		}
		if task.Difficulty == "" {
			t.Fatalf("sample task %q has no difficulty", task.ID)
		}
		if len(task.ExpectedChangedFiles) == 0 {
			t.Fatalf("sample task %q has no expected changed files", task.ID)
		}
		if len(task.ForbiddenChangedFiles) == 0 {
			t.Fatalf("sample task %q has no forbidden changed files", task.ID)
		}
		if len(task.ContextChecks.RequiredFiles) == 0 {
			t.Fatalf("sample task %q has no required context files", task.ID)
		}
		if len(task.VerificationCommands) == 0 {
			t.Fatalf("sample task %q has no verification commands", task.ID)
		}
		if len(task.RequiredTraceEvents) > 0 {
			traceTasks++
		}
	}
	if traceTasks < 4 {
		t.Fatalf("sample suite trace tasks = %d, want at least 4", traceTasks)
	}
}

func TestValidateRejectsMalformedExpectedChangedFiles(t *testing.T) {
	err := Suite{
		ID:   "suite",
		Name: "Suite",
		Tasks: []Task{{
			ID:               "task",
			Name:             "Task",
			Prompt:           "Do it",
			WorkspaceFixture: "fixtures/task",
			ExpectedChangedFiles: []string{
				"",
				"/tmp/outside.go",
				"../outside.go",
				`C:\tmp\outside.go`,
			},
			VerificationCommands: []Command{{
				ID:      "test",
				Name:    "Tests",
				Command: []string{"go", "test", "./..."},
			}},
		}},
	}.Validate()

	if err == nil {
		t.Fatal("Validate returned nil, want malformed expectedChangedFiles errors")
	}
	message := err.Error()
	for _, want := range []string{
		"expectedChangedFiles[0] must not be empty",
		"expectedChangedFiles[1] must be a relative workspace path",
		"expectedChangedFiles[2] must be a relative workspace path",
		"expectedChangedFiles[3] must be a relative workspace path",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("expected validation error %q in:\n%s", want, message)
		}
	}
}

func TestValidateRejectsDuplicateNormalizedExpectedChangedFiles(t *testing.T) {
	err := Suite{
		ID:   "suite",
		Name: "Suite",
		Tasks: []Task{{
			ID:                   "task",
			Name:                 "Task",
			Prompt:               "Do it",
			WorkspaceFixture:     "fixtures/task",
			ExpectedChangedFiles: []string{"internal/reader/b.go", "internal/reader/a/../b.go"},
			VerificationCommands: []Command{{
				ID:      "test",
				Name:    "Tests",
				Command: []string{"go", "test", "./..."},
			}},
		}},
	}.Validate()

	if err == nil {
		t.Fatal("Validate returned nil, want duplicate expectedChangedFiles error")
	}
	if !strings.Contains(err.Error(), "expectedChangedFiles[1] duplicates expectedChangedFiles[0]") {
		t.Fatalf("unexpected validation error:\n%s", err.Error())
	}
}

func TestValidateRejectsMalformedQualityFileLists(t *testing.T) {
	err := Suite{
		ID:   "suite",
		Name: "Suite",
		Tasks: []Task{{
			ID:                    "task",
			Name:                  "Task",
			Prompt:                "Do it",
			WorkspaceFixture:      "fixtures/task",
			ExpectedChangedFiles:  []string{"internal/reader/a.go"},
			ForbiddenChangedFiles: []string{"../outside.go", "logs/../logs/run.txt", "logs/run.txt"},
			ContextChecks: ContextChecks{
				RequiredFiles:  []string{"/tmp/outside.go"},
				ForbiddenFiles: []string{"docs/ok.md", "docs/./ok.md"},
			},
			VerificationCommands: []Command{{
				ID:      "test",
				Name:    "Tests",
				Command: []string{"go", "test", "./..."},
			}},
		}},
	}.Validate()

	if err == nil {
		t.Fatal("Validate returned nil, want malformed quality file list errors")
	}
	message := err.Error()
	for _, want := range []string{
		"forbiddenChangedFiles[0] must be a relative workspace path",
		"forbiddenChangedFiles[2] duplicates forbiddenChangedFiles[1]",
		"contextChecks.requiredFiles[0] must be a relative workspace path",
		"contextChecks.forbiddenFiles[1] duplicates contextChecks.forbiddenFiles[0]",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("expected validation error %q in:\n%s", want, message)
		}
	}
}

func TestValidateReportsUsefulErrors(t *testing.T) {
	err := Suite{
		ID: "suite",
		Tasks: []Task{
			{ID: "dup", Prompt: "do it", WorkspaceFixture: "fixtures/a"},
			{ID: "dup", VerificationCommands: []Command{{ID: "test"}}},
		},
	}.Validate()

	if err == nil {
		t.Fatal("Validate returned nil, want errors")
	}
	message := err.Error()
	for _, want := range []string{
		"suite name is required",
		"tasks[0] name is required",
		"tasks[0] verificationCommands must not be empty",
		"tasks[1] id duplicates tasks[0]",
		"tasks[1] prompt is required",
		"tasks[1] workspaceFixture is required",
		"tasks[1] verificationCommands[0] command must not be empty",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("expected validation error %q in:\n%s", want, message)
		}
	}
}

func TestScoreNormalizesSingleTaskExpectedChangedFiles(t *testing.T) {
	report := Score(Suite{
		ID:   "quality-context",
		Name: "Quality context",
		Tasks: []Task{{
			ID:                   "edit-reader",
			Name:                 "Edit reader",
			ExpectedChangedFiles: []string{"internal/reader/a/../b.go"},
			VerificationCommands: []Command{{
				ID:      "test",
				Name:    "Tests",
				Command: []string{"go", "test", "./..."},
			}},
		}},
	}, ScoreInput{
		CommandResults: []CommandResult{{ID: "test", ExitCode: 0}},
		ChangedFiles:   []string{"internal/reader/b.go"},
	})

	if !report.OK || report.Status != StatusPass {
		t.Fatalf("expected normalized single-task report to pass, got %#v", report)
	}
	if got, want := report.Results[1].ExpectedFiles, []string{"internal/reader/b.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected files = %#v, want %#v", got, want)
	}
}

func TestScorePassesWhenCommandsAndChangedFilesMatch(t *testing.T) {
	suite := sampleSuite()

	report := Score(suite, ScoreInput{
		TaskID: "edit-reader",
		CommandResults: []CommandResult{
			{ID: "test", ExitCode: 0, Stdout: "ok\n"},
		},
		ChangedFiles: []string{"internal/reader/b.go", "internal/reader/a.go"},
	})

	if !report.OK || report.Status != StatusPass {
		t.Fatalf("expected passing report, got %#v", report)
	}
	if report.Summary.Total != 2 || report.Summary.Passed != 2 {
		t.Fatalf("unexpected summary: %#v", report.Summary)
	}
	if report.Results[0].Kind != ResultCommand || report.Results[0].Status != StatusPass {
		t.Fatalf("unexpected command result: %#v", report.Results)
	}
	if report.Results[1].Kind != ResultChangedFiles || report.Results[1].Status != StatusPass {
		t.Fatalf("unexpected changed-file result: %#v", report.Results)
	}
}

func TestScoreFailsForCommandFailureAndChangedFileMismatch(t *testing.T) {
	report := Score(sampleSuite(), ScoreInput{
		TaskID: "edit-reader",
		CommandResults: []CommandResult{
			{ID: "test", ExitCode: 1, Stderr: "FAIL\n"},
		},
		ChangedFiles: []string{"internal/reader/a.go", "internal/reader/extra.go"},
	})

	if report.OK || report.Status != StatusFail {
		t.Fatalf("expected failing report, got %#v", report)
	}
	if report.Summary.Failed != 2 {
		t.Fatalf("expected two failed checks, got %#v", report.Summary)
	}
	changed := report.Results[1]
	if !reflect.DeepEqual(changed.MissingFiles, []string{"internal/reader/b.go"}) {
		t.Fatalf("missing files = %#v", changed.MissingFiles)
	}
	if !reflect.DeepEqual(changed.UnexpectedFiles, []string{"internal/reader/extra.go"}) {
		t.Fatalf("unexpected files = %#v", changed.UnexpectedFiles)
	}
}

func TestScoreFailsWhenForbiddenFilesChange(t *testing.T) {
	suite := sampleSuite()
	suite.Tasks[0].ForbiddenChangedFiles = []string{"internal/reader/private.go", "docs/generated.log"}

	report := Score(suite, ScoreInput{
		TaskID: "edit-reader",
		CommandResults: []CommandResult{
			{ID: "test", ExitCode: 0},
		},
		ChangedFiles: []string{"internal/reader/a.go", "internal/reader/b.go", "internal/reader/private.go"},
	})

	if report.OK || report.Status != StatusFail {
		t.Fatalf("expected forbidden-file failure, got %#v", report)
	}
	result := findResultByID(t, report.Results, "forbidden_changed_files")
	if result.Status != StatusFail {
		t.Fatalf("forbidden result = %#v", result)
	}
	if !reflect.DeepEqual(result.UnexpectedFiles, []string{"internal/reader/private.go"}) {
		t.Fatalf("forbidden touched files = %#v", result.UnexpectedFiles)
	}
}

func TestScoreErrorsWhenExpectedCommandResultIsMissing(t *testing.T) {
	report := Score(sampleSuite(), ScoreInput{
		TaskID:       "edit-reader",
		ChangedFiles: []string{"internal/reader/a.go", "internal/reader/b.go"},
	})

	if report.OK || report.Status != StatusError {
		t.Fatalf("expected missing command result to error, got %#v", report)
	}
	if report.Summary.Errors != 1 {
		t.Fatalf("expected one error, got %#v", report.Summary)
	}
	if got := report.Results[0].Message; got != "missing command result" {
		t.Fatalf("missing-command message = %q", got)
	}
	if report.Results[0].ExitCode != nil {
		t.Fatalf("missing command result should not expose an exit code: %#v", report.Results[0])
	}
}

func TestScoreErrorsWhenTaskSelectionFails(t *testing.T) {
	suite := Suite{
		ID:   "quality-context",
		Name: "Quality context",
		Tasks: []Task{
			sampleSuite().Tasks[0],
			{ID: "other-task", Name: "Other task"},
		},
	}

	report := Score(suite, ScoreInput{})

	if report.OK || report.Status != StatusError {
		t.Fatalf("expected task selection error report, got %#v", report)
	}
	if report.Summary.Errors != 1 || report.Error == "" {
		t.Fatalf("unexpected task selection summary/error: %#v", report)
	}
	if !strings.Contains(report.Error, "taskId is required") {
		t.Fatalf("task selection error = %q", report.Error)
	}
}

func TestScoreReportsBlockedRun(t *testing.T) {
	report := Score(sampleSuite(), ScoreInput{
		TaskID:       "edit-reader",
		Blocked:      true,
		BlockReason:  "fixture setup failed",
		ChangedFiles: []string{"internal/reader/a.go"},
	})

	if report.OK || report.Status != StatusBlocked {
		t.Fatalf("expected blocked report, got %#v", report)
	}
	if report.Summary.Blocked != 2 || report.Results[0].Status != StatusBlocked {
		t.Fatalf("unexpected blocked summary/results: %#v", report)
	}
	if !strings.Contains(report.Results[0].Message, "fixture setup failed") {
		t.Fatalf("expected block reason in result message: %#v", report.Results[0])
	}
}

func TestScoreMarksUnknownCommandsBlockedWhenRunBlocked(t *testing.T) {
	report := Score(sampleSuite(), ScoreInput{
		TaskID:      "edit-reader",
		Blocked:     true,
		BlockReason: "fixture setup failed",
		CommandResults: []CommandResult{
			{ID: "unexpected", ExitCode: 1},
		},
	})

	if report.Status != StatusBlocked || report.Summary.Blocked != 3 {
		t.Fatalf("unexpected blocked report: %#v", report)
	}
	if report.Summary.Failed != 0 || report.Summary.Errors != 0 {
		t.Fatalf("blocked run should not report failed/error unknown commands: %#v", report.Summary)
	}
	if got := report.Results[2]; got.ID != "unknown_command.unexpected" || got.Status != StatusBlocked {
		t.Fatalf("unexpected unknown command result: %#v", got)
	}
}

func TestScoreUsesDeterministicOrderingForInjectedInputs(t *testing.T) {
	report := Score(sampleSuite(), ScoreInput{
		TaskID: "edit-reader",
		CommandResults: []CommandResult{
			{ID: "zzz", ExitCode: 1},
			{ID: "test", ExitCode: 0},
			{ID: "aaa", ExitCode: 1},
			{ID: "zzz", ExitCode: 1},
		},
		ChangedFiles: []string{"z.go", "internal/reader/a.go", "m.go"},
	})

	gotIDs := []string{}
	for _, result := range report.Results {
		gotIDs = append(gotIDs, result.ID)
	}
	wantIDs := []string{"test", "changed_files", "unknown_command.aaa", "unknown_command.zzz"}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("result IDs = %#v, want %#v", gotIDs, wantIDs)
	}
	if !reflect.DeepEqual(report.ChangedFiles, []string{"internal/reader/a.go", "m.go", "z.go"}) {
		t.Fatalf("changed files not sorted: %#v", report.ChangedFiles)
	}
	if !reflect.DeepEqual(report.Results[1].UnexpectedFiles, []string{"m.go", "z.go"}) {
		t.Fatalf("unexpected files not sorted: %#v", report.Results[1].UnexpectedFiles)
	}
}

func TestReportJSONIsStable(t *testing.T) {
	report := Score(sampleSuite(), ScoreInput{
		TaskID: "edit-reader",
		CommandResults: []CommandResult{
			{ID: "test", ExitCode: 0},
		},
		ChangedFiles: []string{"internal/reader/a.go", "internal/reader/b.go"},
	})

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	want := `{
  "contract": "zero.agenteval.report.v1",
  "suiteId": "quality-context",
  "taskId": "edit-reader",
  "status": "pass",
  "ok": true,
  "summary": {
    "total": 2,
    "passed": 2,
    "failed": 0,
    "blocked": 0,
    "errors": 0
  },
  "changedFiles": [
    "internal/reader/a.go",
    "internal/reader/b.go"
  ],
  "results": [
    {
      "id": "test",
      "name": "Tests",
      "kind": "command",
      "status": "pass",
      "command": [
        "go",
        "test",
        "./..."
      ],
      "exitCode": 0
    },
    {
      "id": "changed_files",
      "name": "Expected changed files",
      "kind": "changed_files",
      "status": "pass",
      "expectedFiles": [
        "internal/reader/a.go",
        "internal/reader/b.go"
      ],
      "actualFiles": [
        "internal/reader/a.go",
        "internal/reader/b.go"
      ]
    }
  ]
}`
	if string(data) != want {
		t.Fatalf("stable JSON mismatch:\n%s", string(data))
	}
}

func findResultByID(t *testing.T, results []Result, id string) Result {
	t.Helper()
	for _, result := range results {
		if result.ID == id {
			return result
		}
	}
	t.Fatalf("missing result %q in %#v", id, results)
	return Result{}
}

func writeSuite(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "suite.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write suite: %v", err)
	}
	return path
}

func sampleSuite() Suite {
	return Suite{
		ID:   "quality-context",
		Name: "Quality context",
		Tasks: []Task{{
			ID:               "edit-reader",
			Name:             "Edit reader",
			Prompt:           "Update the reader.",
			WorkspaceFixture: "fixtures/reader",
			VerificationCommands: []Command{{
				ID:      "test",
				Name:    "Tests",
				Command: []string{"go", "test", "./..."},
			}},
			ExpectedChangedFiles: []string{"internal/reader/a.go", "internal/reader/b.go"},
		}},
	}
}
