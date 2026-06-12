package agenteval

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRunnerMapsPassingAndFailingCommandResults(t *testing.T) {
	workspace := t.TempDir()
	runner := Runner{
		RunCommand: func(_ context.Context, gotWorkspace string, command Command) CommandResult {
			if gotWorkspace != workspace {
				t.Fatalf("workspace = %q, want %q", gotWorkspace, workspace)
			}
			switch command.ID {
			case "pass":
				return CommandResult{ID: command.ID, ExitCode: 0, Stdout: "ok"}
			case "fail":
				return CommandResult{ID: command.ID, ExitCode: 2, Stderr: "nope"}
			default:
				t.Fatalf("unexpected command %q", command.ID)
			}
			return CommandResult{}
		},
		ChangedFiles: func(context.Context, string) ([]string, error) {
			return []string{"internal/reader/a.go"}, nil
		},
	}

	report := runner.Run(context.Background(), runnerSuite(), RunInput{
		TaskID:        "two-commands",
		WorkspacePath: workspace,
	})

	if report.Status != StatusFail || report.OK {
		t.Fatalf("status = %s ok = %v, want fail false", report.Status, report.OK)
	}
	if got := report.Results[0]; got.ID != "pass" || got.Status != StatusPass || got.Stdout != "ok" {
		t.Fatalf("passing command result = %#v", got)
	}
	if got := report.Results[1]; got.ID != "fail" || got.Status != StatusFail || got.Stderr != "nope" {
		t.Fatalf("failing command result = %#v", got)
	}
	if report.Summary.Failed != 1 || report.Summary.Passed != 2 {
		t.Fatalf("summary = %#v, want one failed command and two passing checks", report.Summary)
	}
}

func TestRunnerBoundsCommandsWithDefaultTimeout(t *testing.T) {
	workspace := t.TempDir()
	var hadDeadline bool
	runner := Runner{
		RunCommand: func(ctx context.Context, _ string, command Command) CommandResult {
			_, hadDeadline = ctx.Deadline()
			return CommandResult{ID: command.ID, ExitCode: 0}
		},
		ChangedFiles: func(context.Context, string) ([]string, error) {
			return []string{"internal/reader/a.go"}, nil
		},
	}

	// No CommandTimeout set: a per-command deadline must still apply by default so a
	// hung verification command cannot run unbounded.
	report := runner.Run(context.Background(), runnerSuite(), RunInput{
		TaskID:        "two-commands",
		WorkspacePath: workspace,
	})

	if report.Status == StatusError {
		t.Fatalf("unexpected error report: %#v", report)
	}
	if !hadDeadline {
		t.Fatal("verification commands must run under a per-command deadline by default")
	}
}

func TestRunnerBlocksWhenWorkspaceSetupFails(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	calledCommand := false
	calledChangedFiles := false
	runner := Runner{
		RunCommand: func(context.Context, string, Command) CommandResult {
			calledCommand = true
			return CommandResult{}
		},
		ChangedFiles: func(context.Context, string) ([]string, error) {
			calledChangedFiles = true
			return nil, nil
		},
	}

	report := runner.Run(context.Background(), runnerSuite(), RunInput{
		TaskID:        "single",
		WorkspacePath: missing,
	})

	if report.Status != StatusBlocked || report.OK {
		t.Fatalf("status = %s ok = %v, want blocked false", report.Status, report.OK)
	}
	if calledCommand || calledChangedFiles {
		t.Fatalf("blocked workspace should not execute commands or changed-file collection")
	}
	if report.Summary.Blocked != 2 {
		t.Fatalf("summary = %#v, want both checks blocked", report.Summary)
	}
	if got := report.Results[0].Message; got == "" {
		t.Fatal("blocked result should include a message")
	}
}

func TestRunnerCollectsChangedFilesFromInjectedFunc(t *testing.T) {
	workspace := t.TempDir()
	runner := Runner{
		RunCommand: func(_ context.Context, _ string, command Command) CommandResult {
			return CommandResult{ID: command.ID, ExitCode: 0}
		},
		ChangedFiles: func(_ context.Context, gotWorkspace string) ([]string, error) {
			if gotWorkspace != workspace {
				t.Fatalf("workspace = %q, want %q", gotWorkspace, workspace)
			}
			return []string{"z.go", "internal/reader/a.go", "internal/reader/a/../b.go"}, nil
		},
	}

	report := runner.Run(context.Background(), runnerSuite(), RunInput{
		TaskID:        "single",
		WorkspacePath: workspace,
	})

	want := []string{"internal/reader/a.go", "internal/reader/b.go", "z.go"}
	if !reflect.DeepEqual(report.ChangedFiles, want) {
		t.Fatalf("changed files = %#v, want %#v", report.ChangedFiles, want)
	}
	changed := report.Results[1]
	if !reflect.DeepEqual(changed.UnexpectedFiles, []string{"z.go"}) {
		t.Fatalf("unexpected files = %#v, want z.go", changed.UnexpectedFiles)
	}
}

func TestRunnerBlocksWhenChangedFileCollectionFails(t *testing.T) {
	workspace := t.TempDir()
	runner := Runner{
		RunCommand: func(_ context.Context, _ string, command Command) CommandResult {
			return CommandResult{ID: command.ID, ExitCode: 0}
		},
		ChangedFiles: func(context.Context, string) ([]string, error) {
			return nil, errors.New("git unavailable")
		},
	}

	report := runner.Run(context.Background(), runnerSuite(), RunInput{
		TaskID:        "single",
		WorkspacePath: workspace,
	})

	if report.Status != StatusBlocked || report.Summary.Blocked != 2 {
		t.Fatalf("report = %#v, want blocked command and changed-file checks", report)
	}
	if got := report.Results[1].Message; got == "" {
		t.Fatal("changed-file block should include a message")
	}
}

func TestRunnerSelectsRequestedTask(t *testing.T) {
	workspace := t.TempDir()
	var executed []string
	runner := Runner{
		RunCommand: func(_ context.Context, _ string, command Command) CommandResult {
			executed = append(executed, command.ID)
			return CommandResult{ID: command.ID, ExitCode: 0}
		},
		ChangedFiles: func(context.Context, string) ([]string, error) {
			return []string{"cmd/zero/main.go"}, nil
		},
	}

	report := runner.Run(context.Background(), runnerSuite(), RunInput{
		TaskID:        "other",
		WorkspacePath: workspace,
	})

	if report.TaskID != "other" || report.Status != StatusPass {
		t.Fatalf("report task/status = %q/%s, want other/pass", report.TaskID, report.Status)
	}
	if !reflect.DeepEqual(executed, []string{"other-test"}) {
		t.Fatalf("executed commands = %#v, want only selected task command", executed)
	}
}

func TestRunnerDoesNotRequireWorkspaceForTaskSelectionErrors(t *testing.T) {
	runner := Runner{}

	report := runner.Run(context.Background(), runnerSuite(), RunInput{})

	if report.Status != StatusError {
		t.Fatalf("status = %s, want task selection error", report.Status)
	}
	if report.Error == "" {
		t.Fatal("task selection error should be reported")
	}
}

func TestDefaultChangedFilesParsesGitStatusPorcelain(t *testing.T) {
	workspace := t.TempDir()
	runner := Runner{
		RunCommand: func(context.Context, string, Command) CommandResult {
			t.Fatal("command should not be needed")
			return CommandResult{}
		},
		runGit: func(_ context.Context, gotWorkspace string, args ...string) ([]byte, error) {
			if gotWorkspace != workspace {
				t.Fatalf("workspace = %q, want %q", gotWorkspace, workspace)
			}
			if !reflect.DeepEqual(args, []string{"status", "--porcelain", "--untracked-files=all"}) {
				t.Fatalf("git args = %#v", args)
			}
			return []byte(" M internal/reader/a.go\nR  old.go -> internal/reader/b.go\n?? z.go\n"), nil
		},
	}

	files, err := runner.changedFiles(context.Background(), workspace)
	if err != nil {
		t.Fatalf("changedFiles returned error: %v", err)
	}

	want := []string{"internal/reader/a.go", "internal/reader/b.go", "z.go"}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("files = %#v, want %#v", files, want)
	}
}

func TestRunnerBlocksWhenWorkspacePathIsAFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write file workspace: %v", err)
	}

	report := (Runner{}).Run(context.Background(), runnerSuite(), RunInput{
		TaskID:        "single",
		WorkspacePath: path,
	})

	if report.Status != StatusBlocked {
		t.Fatalf("status = %s, want blocked", report.Status)
	}
}

func runnerSuite() Suite {
	return Suite{
		ID:   "runner-suite",
		Name: "Runner suite",
		Tasks: []Task{
			{
				ID:               "single",
				Name:             "Single command",
				Prompt:           "Change reader.",
				WorkspaceFixture: "fixtures/reader",
				VerificationCommands: []Command{{
					ID:      "test",
					Name:    "Tests",
					Command: []string{"go", "test", "./..."},
				}},
				ExpectedChangedFiles: []string{"internal/reader/a.go", "internal/reader/b.go"},
			},
			{
				ID:               "two-commands",
				Name:             "Two commands",
				Prompt:           "Run two commands.",
				WorkspaceFixture: "fixtures/reader",
				VerificationCommands: []Command{
					{ID: "pass", Name: "Passing", Command: []string{"go", "test", "./..."}},
					{ID: "fail", Name: "Failing", Command: []string{"go", "test", "./internal/nope"}},
				},
				ExpectedChangedFiles: []string{"internal/reader/a.go"},
			},
			{
				ID:               "other",
				Name:             "Other task",
				Prompt:           "Change CLI.",
				WorkspaceFixture: "fixtures/cli",
				VerificationCommands: []Command{{
					ID:      "other-test",
					Name:    "Other tests",
					Command: []string{"go", "test", "./internal/cli"},
				}},
				ExpectedChangedFiles: []string{"cmd/zero/main.go"},
			},
		},
	}
}
