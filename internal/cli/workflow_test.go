package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/redaction"
	"github.com/Gitlawb/zero/internal/selfverify"
	"github.com/Gitlawb/zero/internal/testrunner"
	"github.com/Gitlawb/zero/internal/verify"
	"github.com/Gitlawb/zero/internal/worktrees"
	"github.com/Gitlawb/zero/internal/zerogit"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

func TestRunWorktreesPrepareTextAndJSON(t *testing.T) {
	cwd := t.TempDir()
	base := t.TempDir()
	prepared := worktrees.Result{
		Name:         "agent-task",
		Path:         filepath.Join(base, "agent-task"),
		RepoRoot:     cwd,
		SourceBranch: "main",
		SourceCommit: "abc1234",
	}

	for _, args := range [][]string{
		{"worktrees", "prepare", "--name", "agent-task", "--dir", base},
		{"worktrees", "prepare", "--name=agent-task", "--dir=" + base, "--json"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exitCode := runWithDeps(args, &stdout, &stderr, appDeps{
				getwd: func() (string, error) { return cwd, nil },
				prepareWorktree: func(ctx context.Context, options worktrees.Options) (worktrees.Result, error) {
					if options.Cwd != cwd || options.Name != "agent-task" || options.BaseDir != base {
						t.Fatalf("unexpected worktree options: %#v", options)
					}
					return prepared, nil
				},
			})

			if exitCode != exitSuccess {
				t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("expected empty stderr, got %q", stderr.String())
			}
			if strings.Contains(strings.Join(args, " "), "--json") {
				var decoded worktrees.Result
				if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
					t.Fatalf("decode worktree JSON: %v\n%s", err, stdout.String())
				}
				if decoded.Path != prepared.Path || decoded.Name != prepared.Name {
					t.Fatalf("unexpected JSON result: %#v", decoded)
				}
			} else if !strings.Contains(stdout.String(), "Zero worktree ready") || !strings.Contains(stdout.String(), prepared.Path) {
				t.Fatalf("unexpected worktree text output: %q", stdout.String())
			}
		})
	}
}

func TestRunWorktreesPrepareReportsErrors(t *testing.T) {
	cwd := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"worktrees", "prepare", "--name", "bad"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) { return cwd, nil },
		prepareWorktree: func(context.Context, worktrees.Options) (worktrees.Result, error) {
			return worktrees.Result{}, errors.New("not a git repository")
		},
	})

	if exitCode != exitUsage {
		t.Fatalf("expected usage exit %d, got %d", exitUsage, exitCode)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "not a git repository") {
		t.Fatalf("expected worktree error, got %q", stderr.String())
	}
}

func TestRunWorktreesPrepareRedactsPathsInOutput(t *testing.T) {
	secret := "sk-proj-abcdefghijklmnopqrstuvwxyz"
	cwd := filepath.Join(t.TempDir(), secret, "repo")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	prepared := worktrees.Result{
		Name:         "agent-task",
		Path:         filepath.Join(t.TempDir(), secret, "agent-task"),
		RepoRoot:     cwd,
		SourceBranch: "feature/" + secret,
		SourceCommit: "abc1234",
	}

	for _, args := range [][]string{
		{"worktrees", "prepare", "--name", "agent-task"},
		{"worktrees", "prepare", "--name", "agent-task", "--json"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exitCode := runWithDeps(args, &stdout, &stderr, appDeps{
				getwd: func() (string, error) { return cwd, nil },
				prepareWorktree: func(context.Context, worktrees.Options) (worktrees.Result, error) {
					return prepared, nil
				},
			})

			if exitCode != exitSuccess {
				t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
			}
			if strings.Contains(stdout.String(), secret) {
				t.Fatalf("worktree output leaked secret path segment: %q", stdout.String())
			}
			if !strings.Contains(stdout.String(), redaction.RedactedSecret) {
				t.Fatalf("expected redaction marker in worktree output, got %q", stdout.String())
			}
		})
	}
}

func TestRunWorktreesPrepareRejectsDuplicateNames(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"worktrees", "prepare", "--name", "first", "second"}, &stdout, &stderr, appDeps{
		prepareWorktree: func(context.Context, worktrees.Options) (worktrees.Result, error) {
			t.Fatal("prepareWorktree should not be called for invalid flags")
			return worktrees.Result{}, nil
		},
	})

	if exitCode != exitUsage {
		t.Fatalf("expected usage exit %d, got %d", exitUsage, exitCode)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "worktree name was provided more than once") {
		t.Fatalf("expected duplicate name error, got %q", stderr.String())
	}
}

func TestRunWorktreesRelease(t *testing.T) {
	worktreeDir := t.TempDir()
	var releasedPath string

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"worktrees", "release", worktreeDir}, &stdout, &stderr, appDeps{
		releaseWorktree: func(ctx context.Context, options worktrees.Options, path string) error {
			releasedPath = path
			return nil
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	if releasedPath != worktreeDir {
		t.Fatalf("released path = %q, want %q", releasedPath, worktreeDir)
	}
	if !strings.Contains(stdout.String(), worktreeDir) {
		t.Fatalf("expected confirmation output to mention path, got %q", stdout.String())
	}
}

func TestRunWorktreesReleaseNormalizesRelativePath(t *testing.T) {
	// git worktree unlock matches against the path git recorded when the
	// worktree was created, so a relative argument (resolved against
	// whatever directory the caller happens to be running `zero` from) can
	// fail to match. Chdir into a known directory and pass a relative
	// argument to confirm it reaches releaseWorktree as an absolute path.
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	worktreeDir := filepath.Join(parent, "task-a")
	if err := os.Mkdir(worktreeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(parent); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(origWd); err != nil {
			t.Fatal(err)
		}
	}()

	// filepath.Abs resolves against os.Getwd, whose spelling of the temp dir
	// can differ from t.TempDir()'s (macOS reports /private/var for /var), so
	// derive the expected value through the same resolution instead of
	// joining onto the lexical parent path.
	expectedPath, err := filepath.Abs("task-a")
	if err != nil {
		t.Fatal(err)
	}

	var releasedPath string
	var stdout, stderr bytes.Buffer
	exitCode := runWithDeps([]string{"worktrees", "release", "task-a"}, &stdout, &stderr, appDeps{
		releaseWorktree: func(ctx context.Context, options worktrees.Options, path string) error {
			releasedPath = path
			return nil
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	if releasedPath != expectedPath {
		t.Fatalf("released path = %q, want absolute %q", releasedPath, expectedPath)
	}
}

func TestRunWorktreesReleaseRequiresPath(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"worktrees", "release"}, &stdout, &stderr, appDeps{
		releaseWorktree: func(context.Context, worktrees.Options, string) error {
			t.Fatal("releaseWorktree should not be called without a path")
			return nil
		},
	})

	if exitCode != exitUsage {
		t.Fatalf("expected usage exit %d, got %d", exitUsage, exitCode)
	}
	if !strings.Contains(stderr.String(), "requires a worktree path") {
		t.Fatalf("expected missing-path error, got %q", stderr.String())
	}
}

func TestRunWorktreesReleaseWiresWorkspaceCwd(t *testing.T) {
	// Release only consults Options.Cwd when the worktree directory itself is
	// already gone (deleted by hand instead of released); git then has to run
	// from the source repository to clear the orphaned lock. The CLI must
	// wire the resolved workspace root through, or that advertised recovery
	// path runs git from a possibly non-repository directory and the lock is
	// never cleared (Clean skips locked entries).
	root := t.TempDir()
	var gotCwd string

	var stdout, stderr bytes.Buffer
	exitCode := runWithDeps([]string{"worktrees", "release", filepath.Join(root, "already-deleted")}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) { return root, nil },
		releaseWorktree: func(ctx context.Context, options worktrees.Options, path string) error {
			gotCwd = options.Cwd
			return nil
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	if gotCwd != root {
		t.Fatalf("release Options.Cwd = %q, want workspace root %q", gotCwd, root)
	}
}

func TestRunWorktreesReleaseHonorsExplicitCwd(t *testing.T) {
	// The deleted-path recovery cannot derive the source repository from the
	// worktree path (the directory is gone and its name is a one-way hash),
	// so -C names it explicitly; the resolved root must reach Release as
	// Options.Cwd regardless of where the command was launched.
	launchDir := t.TempDir()
	repoDir := t.TempDir()
	var gotCwd string

	var stdout, stderr bytes.Buffer
	exitCode := runWithDeps([]string{"worktrees", "release", "-C", repoDir, filepath.Join(repoDir, "already-deleted")}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) { return launchDir, nil },
		releaseWorktree: func(ctx context.Context, options worktrees.Options, path string) error {
			gotCwd = options.Cwd
			return nil
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	if gotCwd != repoDir {
		t.Fatalf("release Options.Cwd = %q, want explicit -C root %q", gotCwd, repoDir)
	}
}

func TestRunWorktreesReleaseReportsErrors(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"worktrees", "release", "/no/such/worktree"}, &stdout, &stderr, appDeps{
		releaseWorktree: func(context.Context, worktrees.Options, string) error {
			return errors.New("unlock git worktree: not a valid worktree")
		},
	})

	if exitCode != exitUsage {
		t.Fatalf("expected usage exit %d, got %d", exitUsage, exitCode)
	}
	if !strings.Contains(stderr.String(), "not a valid worktree") {
		t.Fatalf("expected underlying error, got %q", stderr.String())
	}
}

func TestRunWorktreesReleaseRedactsErrorText(t *testing.T) {
	// Release errors interpolate the caller-supplied path (see
	// verifyZeroOwnedWorktree's "refusing to release %s" messages); unlike the
	// success path, which redacts before printing, the error was previously
	// forwarded to stderr verbatim, so a rejected path containing a key-shaped
	// segment would reach terminal/model-visible output unredacted.
	secret := "sk-proj-abcDEF123_ghiJKL456-mnoPQR789stu"
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"worktrees", "release", "/tmp/" + secret + "/task"}, &stdout, &stderr, appDeps{
		releaseWorktree: func(context.Context, worktrees.Options, string) error {
			return fmt.Errorf("refusing to release /tmp/%s/task: not a registered worktree of this repository", secret)
		},
	})

	if exitCode != exitUsage {
		t.Fatalf("expected usage exit %d, got %d", exitUsage, exitCode)
	}
	if strings.Contains(stderr.String(), secret) {
		t.Fatalf("release error leaked unredacted key-shaped path: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "[REDACTED]") {
		t.Fatalf("expected redaction placeholder in error output, got %q", stderr.String())
	}
}

func TestRunVerifyTextAndJSON(t *testing.T) {
	cwd := t.TempDir()
	plan := verify.Plan{Root: cwd, Checks: []verify.Check{{ID: "go.test", Name: "Go tests", Command: []string{"go", "test", "./..."}}}}
	report := verify.Report{
		Root:      cwd,
		StartedAt: "2026-06-05T11:00:00Z",
		EndedAt:   "2026-06-05T11:00:01Z",
		OK:        true,
		Summary:   verify.Summary{Total: 1, Passed: 1},
		Results: []verify.Result{{
			ID:       "go.test",
			Name:     "Go tests",
			Command:  []string{"go", "test", "./..."},
			Status:   verify.StatusPass,
			ExitCode: 0,
			Stdout:   "ok",
			TestSummary: &testrunner.Summary{
				Framework: testrunner.FrameworkGo,
				Total:     2,
				Passed:    1,
				Failed:    1,
				Failures:  []testrunner.Failure{{Name: "TestBroken"}},
			},
		}},
	}

	for _, args := range [][]string{
		{"verify"},
		{"verify", "--json", "--only", "go.test"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exitCode := runWithDeps(args, &stdout, &stderr, appDeps{
				getwd: func() (string, error) { return cwd, nil },
				detectVerifyPlan: func(root string) (verify.Plan, error) {
					if root != cwd {
						t.Fatalf("verify root = %q, want %q", root, cwd)
					}
					return plan, nil
				},
				runVerify: func(ctx context.Context, gotPlan verify.Plan, options verify.RunOptions) verify.Report {
					if gotPlan.Root != cwd {
						t.Fatalf("plan root = %q, want %q", gotPlan.Root, cwd)
					}
					if strings.Contains(strings.Join(args, " "), "--only") && (len(options.Only) != 1 || options.Only[0] != "go.test") {
						t.Fatalf("Only = %#v, want go.test", options.Only)
					}
					return report
				},
				now: fixedCLITime("2026-06-05T11:00:00Z"),
			})

			if exitCode != exitSuccess {
				t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("expected empty stderr, got %q", stderr.String())
			}
			if strings.Contains(strings.Join(args, " "), "--json") {
				var decoded verify.Report
				if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
					t.Fatalf("decode verify JSON: %v\n%s", err, stdout.String())
				}
				if !decoded.OK || decoded.Summary.Passed != 1 {
					t.Fatalf("unexpected verify JSON: %#v", decoded)
				}
				if decoded.Root != cwd {
					t.Fatalf("decoded verify root = %q, want %q", decoded.Root, cwd)
				}
				var snapshot verify.ReportSnapshot
				if err := json.Unmarshal(stdout.Bytes(), &snapshot); err != nil {
					t.Fatalf("decode verify snapshot JSON: %v\n%s", err, stdout.String())
				}
				if snapshot.Contract != verify.ReportContractVersion || len(snapshot.Events) == 0 {
					t.Fatalf("verify JSON did not expose runtime contract: %#v", snapshot)
				}
			} else if !strings.Contains(stdout.String(), "Zero verification") || !strings.Contains(stdout.String(), "go.test") || !strings.Contains(stdout.String(), cwd) || !strings.Contains(stdout.String(), "tests: 2 total, 1 passed, 1 failed") {
				t.Fatalf("unexpected verify text output: %q", stdout.String())
			}
		})
	}
}

func TestRunVerifyRedactsWorkspacePathsInOutput(t *testing.T) {
	secret := "sk-proj-abcdefghijklmnopqrstuvwxyz"
	cwd := filepath.Join(t.TempDir(), secret, "workspace")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	plan := verify.Plan{Root: cwd}
	report := verify.Report{
		Root:      cwd,
		StartedAt: "2026-06-05T11:05:00Z",
		EndedAt:   "2026-06-05T11:05:01Z",
		OK:        true,
		Summary:   verify.Summary{},
		Results: []verify.Result{{
			ID:            "go.test",
			Name:          "Go tests",
			Command:       []string{"go", "test", "./..."},
			Status:        verify.StatusFail,
			OutputSummary: &verify.OutputSummary{Lines: []string{"failure at " + secret}},
			TestSummary: &testrunner.Summary{
				Framework: testrunner.FrameworkGo,
				Total:     1,
				Failed:    1,
				Failures: []testrunner.Failure{{
					Name:    secret,
					File:    filepath.Join(secret, "secret_test.go:12"),
					Message: "token " + secret,
				}},
			},
		}},
	}

	for _, args := range [][]string{
		{"verify"},
		{"verify", "--json"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exitCode := runWithDeps(args, &stdout, &stderr, appDeps{
				getwd: func() (string, error) { return cwd, nil },
				detectVerifyPlan: func(root string) (verify.Plan, error) {
					if root != cwd {
						t.Fatalf("verify root = %q, want %q", root, cwd)
					}
					return plan, nil
				},
				runVerify: func(context.Context, verify.Plan, verify.RunOptions) verify.Report {
					return report
				},
			})

			if exitCode != exitSuccess {
				t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
			}
			if strings.Contains(stdout.String(), secret) {
				t.Fatalf("verify output leaked secret path segment: %q", stdout.String())
			}
			if !strings.Contains(stdout.String(), redaction.RedactedSecret) {
				t.Fatalf("expected redaction marker in verify output, got %q", stdout.String())
			}
		})
	}
}

func TestRunVerifyReturnsProviderExitWhenChecksFail(t *testing.T) {
	cwd := t.TempDir()
	report := verify.Report{
		Root:    cwd,
		OK:      false,
		Summary: verify.Summary{Total: 1, Failed: 1},
		Results: []verify.Result{{
			ID:       "bun.test",
			Name:     "Bun tests",
			Command:  []string{"bun", "test"},
			Status:   verify.StatusFail,
			ExitCode: 1,
		}},
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"verify"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) { return cwd, nil },
		detectVerifyPlan: func(string) (verify.Plan, error) {
			return verify.Plan{Root: cwd, Checks: []verify.Check{{ID: "bun.test", Name: "Bun tests", Command: []string{"bun", "test"}}}}, nil
		},
		runVerify: func(context.Context, verify.Plan, verify.RunOptions) verify.Report { return report },
	})

	if exitCode != exitProvider {
		t.Fatalf("expected provider-style failure exit %d, got %d", exitProvider, exitCode)
	}
	if !strings.Contains(stdout.String(), "failed") {
		t.Fatalf("expected failure summary in stdout, got %q", stdout.String())
	}
}

func TestRunVerifyAttemptsUsesSelfVerifyLoop(t *testing.T) {
	cwd := t.TempDir()
	plan := verify.Plan{Root: cwd, Checks: []verify.Check{{ID: "go.test", Name: "Go tests", Command: []string{"go", "test", "./..."}}}}
	loopReport := selfverify.Report{
		Root:       cwd,
		OK:         true,
		StopReason: selfverify.StopReasonPassed,
		Attempts: []selfverify.Attempt{
			{Number: 1, Report: verify.Report{Root: cwd, OK: false, Summary: verify.Summary{Total: 1, Failed: 1}}},
			{Number: 2, Report: verify.Report{Root: cwd, OK: true, Summary: verify.Summary{Total: 1, Passed: 1}}},
		},
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"verify", "--attempts", "2", "--json"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) { return cwd, nil },
		detectVerifyPlan: func(root string) (verify.Plan, error) {
			if root != cwd {
				t.Fatalf("verify root = %q, want %q", root, cwd)
			}
			return plan, nil
		},
		runSelfVerify: func(ctx context.Context, gotPlan verify.Plan, options selfverify.Options) selfverify.Report {
			if options.MaxAttempts != 2 {
				t.Fatalf("MaxAttempts = %d, want 2", options.MaxAttempts)
			}
			return loopReport
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	var decoded selfverify.Report
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("decode verify loop JSON: %v\n%s", err, stdout.String())
	}
	if len(decoded.Attempts) != 2 || !decoded.OK || decoded.StopReason != selfverify.StopReasonPassed {
		t.Fatalf("unexpected loop JSON: %#v", decoded)
	}
	var snapshot selfverify.LoopSnapshot
	if err := json.Unmarshal(stdout.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode verify loop snapshot JSON: %v\n%s", err, stdout.String())
	}
	if snapshot.Contract != selfverify.LoopContractVersion || len(snapshot.Events) == 0 {
		t.Fatalf("verify loop JSON did not expose runtime contract: %#v", snapshot)
	}
}

func TestRunVerifyAttemptsFormatsSelfVerifyText(t *testing.T) {
	cwd := t.TempDir()
	plan := verify.Plan{Root: cwd, Checks: []verify.Check{{ID: "go.test", Name: "Go tests", Command: []string{"go", "test", "./..."}}}}
	loopReport := selfverify.Report{
		Root:       cwd,
		OK:         true,
		StopReason: selfverify.StopReasonPassed,
		Summary:    verify.Summary{Total: 1, Passed: 1},
		Attempts: []selfverify.Attempt{
			{
				Number:      1,
				Report:      verify.Report{Root: cwd, OK: false, Summary: verify.Summary{Total: 1, Failed: 1}},
				Remediation: &selfverify.Remediation{Applied: true, Message: "prepared retry"},
			},
			{Number: 2, Report: verify.Report{Root: cwd, OK: true, Summary: verify.Summary{Total: 1, Passed: 1}}},
		},
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"verify", "--attempts=2"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) { return cwd, nil },
		detectVerifyPlan: func(string) (verify.Plan, error) {
			return plan, nil
		},
		runSelfVerify: func(context.Context, verify.Plan, selfverify.Options) selfverify.Report {
			return loopReport
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{"Zero self-verification", "root: " + cwd, "stop: passed", "attempt 1: failed", "remediation: applied - prepared retry", "attempt 2: passed"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected %q in output: %q", want, output)
		}
	}
}

func TestRunChangesInspectAndCommit(t *testing.T) {
	cwd := t.TempDir()
	summary := zerogit.ChangeSummary{
		Root:   cwd,
		Branch: "main",
		Commit: "abc1234",
		Files:  []zerogit.FileChange{{Path: "README.md", Status: "modified", Unstaged: true}},
	}
	commit := zerogit.CommitResult{
		Root:       cwd,
		Message:    "Update README",
		DryRun:     true,
		Committed:  false,
		Before:     summary,
		CommitHash: "",
	}

	t.Run("inspect json", func(t *testing.T) {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		exitCode := runWithDeps([]string{"changes", "inspect", "--json"}, &stdout, &stderr, appDeps{
			getwd: func() (string, error) { return cwd, nil },
			inspectChanges: func(ctx context.Context, options zerogit.InspectOptions) (zerogit.ChangeSummary, error) {
				if options.Cwd != cwd {
					t.Fatalf("inspect cwd = %q, want %q", options.Cwd, cwd)
				}
				return summary, nil
			},
		})

		if exitCode != exitSuccess {
			t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
		}
		var decoded zerogit.ChangeSummary
		if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
			t.Fatalf("decode changes JSON: %v\n%s", err, stdout.String())
		}
		if len(decoded.Files) != 1 || decoded.Files[0].Path != "README.md" {
			t.Fatalf("unexpected changes JSON: %#v", decoded)
		}
		var snapshot zerogit.ChangeSnapshot
		if err := json.Unmarshal(stdout.Bytes(), &snapshot); err != nil {
			t.Fatalf("decode changes snapshot JSON: %v\n%s", err, stdout.String())
		}
		if snapshot.Contract != zerogit.ChangeContractVersion || len(snapshot.Events) == 0 {
			t.Fatalf("changes JSON did not expose runtime contract: %#v", snapshot)
		}
	})

	t.Run("commit dry-run", func(t *testing.T) {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		exitCode := runWithDeps([]string{"changes", "commit", "--message", "Update README", "--dry-run"}, &stdout, &stderr, appDeps{
			getwd: func() (string, error) { return cwd, nil },
			commitChanges: func(ctx context.Context, options zerogit.CommitOptions) (zerogit.CommitResult, error) {
				if options.Cwd != cwd || options.Message != "Update README" || !options.DryRun {
					t.Fatalf("unexpected commit options: %#v", options)
				}
				return commit, nil
			},
		})

		if exitCode != exitSuccess {
			t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
		}
		if !strings.Contains(stdout.String(), "Zero changes commit") || !strings.Contains(stdout.String(), "dry-run: true") {
			t.Fatalf("unexpected changes commit output: %q", stdout.String())
		}
	})
}

func TestRunChangesInspectThreadsBaseRef(t *testing.T) {
	cwd := t.TempDir()
	summary := zerogit.ChangeSummary{
		Root:   cwd,
		Branch: "feature",
		Base:   "main",
		Files:  []zerogit.FileChange{{Path: "feature.md", Status: "added"}},
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"changes", "inspect", "--base", "main"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) { return cwd, nil },
		inspectChanges: func(ctx context.Context, options zerogit.InspectOptions) (zerogit.ChangeSummary, error) {
			if options.BaseRef != "main" {
				t.Fatalf("InspectOptions.BaseRef = %q, want main", options.BaseRef)
			}
			return summary, nil
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), "base: main") {
		t.Fatalf("expected base line in output, got %q", stdout.String())
	}
}

func TestRunChangesCommitRejectsBaseRef(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"changes", "commit", "--base", "main"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) { return t.TempDir(), nil },
		commitChanges: func(context.Context, zerogit.CommitOptions) (zerogit.CommitResult, error) {
			t.Fatal("commitChanges should not be called when --base is rejected")
			return zerogit.CommitResult{}, nil
		},
	})

	if exitCode != exitUsage {
		t.Fatalf("expected usage exit %d, got %d", exitUsage, exitCode)
	}
	if !strings.Contains(stderr.String(), "--base") {
		t.Fatalf("expected --base rejection error, got %q", stderr.String())
	}
}

func TestWriteChangesHelpMentionsBase(t *testing.T) {
	var out bytes.Buffer
	if err := writeChangesHelp(&out); err != nil {
		t.Fatalf("writeChangesHelp error: %v", err)
	}
	if !strings.Contains(out.String(), "--base") {
		t.Fatalf("expected --base in changes help, got %q", out.String())
	}
}

func TestParseChangesArgsBaseRef(t *testing.T) {
	for _, args := range [][]string{
		{"--base", "main"},
		{"--base=main"},
	} {
		options, help, err := parseChangesArgs(args, "inspect")
		if err != nil {
			t.Fatalf("parseChangesArgs(%v) error: %v", args, err)
		}
		if help {
			t.Fatalf("parseChangesArgs(%v) returned help", args)
		}
		if options.baseRef != "main" {
			t.Fatalf("baseRef = %q, want main (args %v)", options.baseRef, args)
		}
	}
}

func TestParseChangesArgsRejectsBaseOnCommit(t *testing.T) {
	_, _, err := parseChangesArgs([]string{"--base", "main"}, "commit")
	if err == nil || !strings.Contains(err.Error(), "--base") {
		t.Fatalf("expected --base rejection on commit, got %v", err)
	}
}

func TestParseChangesArgsRequiresBaseValue(t *testing.T) {
	if _, _, err := parseChangesArgs([]string{"--base"}, "inspect"); err == nil {
		t.Fatalf("expected error when --base has no value")
	}
}

func TestRunExecWorktreeUsesPreparedWorkspace(t *testing.T) {
	root := t.TempDir()
	worktreeDir := t.TempDir()
	var resolvedWorkspace string

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"exec", "--worktree", "task-a", "hello"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) { return root, nil },
		prepareWorktree: func(ctx context.Context, options worktrees.Options) (worktrees.Result, error) {
			if options.Cwd != root || options.Name != "task-a" {
				t.Fatalf("unexpected worktree options: %#v", options)
			}
			return worktrees.Result{Name: "task-a", Path: worktreeDir, RepoRoot: root, SourceBranch: "main", SourceCommit: "abc1234"}, nil
		},
		resolveConfig: func(workspaceRoot string, overrides config.Overrides) (config.ResolvedConfig, error) {
			resolvedWorkspace = workspaceRoot
			return execResolvedConfig(), nil
		},
		newProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
			return echoExecProvider{}, nil
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	if resolvedWorkspace != worktreeDir {
		t.Fatalf("resolved workspace = %q, want worktree %q", resolvedWorkspace, worktreeDir)
	}
	if !strings.Contains(stdout.String(), "hello") {
		t.Fatalf("expected provider output, got %q", stdout.String())
	}
}

func TestRunExecWorktreeReleasesLockAfterRun(t *testing.T) {
	root := t.TempDir()
	worktreeDir := t.TempDir()
	var releasedPath string
	releaseCalls := 0

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"exec", "--worktree", "task-a", "hello"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) { return root, nil },
		prepareWorktree: func(ctx context.Context, options worktrees.Options) (worktrees.Result, error) {
			return worktrees.Result{Name: "task-a", Path: worktreeDir, RepoRoot: root, SourceBranch: "main", SourceCommit: "abc1234", LockAcquired: true}, nil
		},
		releaseWorktree: func(ctx context.Context, options worktrees.Options, path string) error {
			releaseCalls++
			releasedPath = path
			return nil
		},
		resolveConfig: func(workspaceRoot string, overrides config.Overrides) (config.ResolvedConfig, error) {
			return execResolvedConfig(), nil
		},
		newProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
			return echoExecProvider{}, nil
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	// A `zero exec --worktree` run is the only user of the worktree it prepares:
	// its own process created it and is now exiting, so it must release the
	// Prepare lock itself rather than leaving it locked forever (unlike `zero
	// worktrees prepare`, which hands the path to a longer-lived external
	// caller and has no such end-of-life signal to act on).
	if releaseCalls != 1 {
		t.Fatalf("releaseWorktree call count = %d, want 1", releaseCalls)
	}
	if releasedPath != worktreeDir {
		t.Fatalf("released path = %q, want %q", releasedPath, worktreeDir)
	}
}

func TestRunExecWorktreeKeepsLockItDidNotAcquire(t *testing.T) {
	// Defense in depth: Prepare now rejects an in-use lease outright, so a
	// successful result always reports LockAcquired=true. Should that ever
	// change, exec must still only release the ownership its own invocation
	// established; releasing a lock it did not acquire would clear another
	// caller's lease and let a later Clean force-delete a live workspace.
	root := t.TempDir()
	worktreeDir := t.TempDir()
	releaseCalls := 0

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"exec", "--worktree", "task-a", "hello"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) { return root, nil },
		prepareWorktree: func(ctx context.Context, options worktrees.Options) (worktrees.Result, error) {
			return worktrees.Result{Name: "task-a", Path: worktreeDir, RepoRoot: root, SourceBranch: "main", SourceCommit: "abc1234", Reused: true, LockAcquired: false}, nil
		},
		releaseWorktree: func(ctx context.Context, options worktrees.Options, path string) error {
			releaseCalls++
			return nil
		},
		resolveConfig: func(workspaceRoot string, overrides config.Overrides) (config.ResolvedConfig, error) {
			return execResolvedConfig(), nil
		},
		newProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
			return echoExecProvider{}, nil
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	if releaseCalls != 0 {
		t.Fatalf("releaseWorktree call count = %d, want 0 for a lock this run did not acquire", releaseCalls)
	}
}

func TestRunExecWorktreeSurfacesReleaseFailure(t *testing.T) {
	// A failed unlock leaves a lock Clean permanently skips, recreating the
	// disk leak silently; the failure must reach the user with the affected
	// path so the leaked lock can be cleared by hand.
	root := t.TempDir()
	worktreeDir := t.TempDir()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"exec", "--worktree", "task-a", "hello"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) { return root, nil },
		prepareWorktree: func(ctx context.Context, options worktrees.Options) (worktrees.Result, error) {
			return worktrees.Result{Name: "task-a", Path: worktreeDir, RepoRoot: root, SourceBranch: "main", SourceCommit: "abc1234", LockAcquired: true}, nil
		},
		releaseWorktree: func(ctx context.Context, options worktrees.Options, path string) error {
			return errors.New("unlock git worktree: boom")
		},
		resolveConfig: func(workspaceRoot string, overrides config.Overrides) (config.ResolvedConfig, error) {
			return execResolvedConfig(), nil
		},
		newProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
			return echoExecProvider{}, nil
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	if !strings.Contains(stderr.String(), worktreeDir) || !strings.Contains(stderr.String(), "boom") {
		t.Fatalf("expected release failure with path on stderr, got %q", stderr.String())
	}
}

func TestRunExecWorktreeRedactsReleaseFailureText(t *testing.T) {
	// The release path argument was already redacted before this diagnostic
	// was added, but the error's own text was forwarded verbatim; Release's
	// ownership errors interpolate the caller-supplied path (see
	// verifyZeroOwnedWorktree), so a key-shaped path reaching this message
	// leaked unredacted onto stderr.
	root := t.TempDir()
	worktreeDir := t.TempDir()
	secret := "sk-proj-abcDEF123_ghiJKL456-mnoPQR789stu"

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"exec", "--worktree", "task-a", "hello"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) { return root, nil },
		prepareWorktree: func(ctx context.Context, options worktrees.Options) (worktrees.Result, error) {
			return worktrees.Result{Name: "task-a", Path: worktreeDir, RepoRoot: root, SourceBranch: "main", SourceCommit: "abc1234", LockAcquired: true}, nil
		},
		releaseWorktree: func(ctx context.Context, options worktrees.Options, path string) error {
			return fmt.Errorf("refusing to release /tmp/%s/task: not a registered worktree of this repository", secret)
		},
		resolveConfig: func(workspaceRoot string, overrides config.Overrides) (config.ResolvedConfig, error) {
			return execResolvedConfig(), nil
		},
		newProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
			return echoExecProvider{}, nil
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	if strings.Contains(stderr.String(), secret) {
		t.Fatalf("deferred release diagnostic leaked unredacted key-shaped path: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "[REDACTED]") {
		t.Fatalf("expected redaction placeholder in deferred release diagnostic, got %q", stderr.String())
	}
}

func TestRunExecRejectsForkWithWorktree(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"exec", "--worktree", "--fork", "zero_parent", "hello"}, &stdout, &stderr, appDeps{
		prepareWorktree: func(context.Context, worktrees.Options) (worktrees.Result, error) {
			t.Fatal("prepareWorktree should not be called for invalid flags")
			return worktrees.Result{}, nil
		},
	})

	if exitCode != exitUsage {
		t.Fatalf("expected usage exit %d, got %d", exitUsage, exitCode)
	}
	if !strings.Contains(stderr.String(), "--fork cannot be used with --worktree") {
		t.Fatalf("expected flag conflict error, got %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
}

func TestRunExecRejectsWorktreeDirWithoutWorktree(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"exec", "--worktree-dir", "/tmp/zero", "hello"}, &stdout, &stderr, appDeps{
		prepareWorktree: func(context.Context, worktrees.Options) (worktrees.Result, error) {
			t.Fatal("prepareWorktree should not be called for invalid flags")
			return worktrees.Result{}, nil
		},
	})

	if exitCode != exitUsage {
		t.Fatalf("expected usage exit %d, got %d", exitUsage, exitCode)
	}
	if !strings.Contains(stderr.String(), "--worktree-dir requires --worktree") {
		t.Fatalf("expected worktree-dir dependency error, got %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
}

func TestParseChangesArgsAuto(t *testing.T) {
	// 1. Correct parsing of auto
	for _, args := range [][]string{
		{"--auto"},
		{"-a"},
	} {
		options, help, err := parseChangesArgs(args, "commit")
		if err != nil {
			t.Fatalf("parseChangesArgs(%v) error: %v", args, err)
		}
		if help {
			t.Fatalf("parseChangesArgs(%v) returned help", args)
		}
		if !options.auto {
			t.Fatalf("auto = false, want true (args %v)", args)
		}
	}

	// 2. Reject --auto on inspect
	_, _, err := parseChangesArgs([]string{"--auto"}, "inspect")
	if err == nil || !strings.Contains(err.Error(), "--auto") {
		t.Fatalf("expected --auto rejection on inspect, got %v", err)
	}

	// 3. Reject both --message and --auto on commit
	_, _, err = parseChangesArgs([]string{"--message", "foo", "--auto"}, "commit")
	if err == nil || !strings.Contains(err.Error(), "cannot specify both --message and --auto") {
		t.Fatalf("expected --message and --auto conflict error, got %v", err)
	}

	// 4. Reject both empty message and --auto on commit
	_, _, err = parseChangesArgs([]string{"--message=", "--auto"}, "commit")
	if err == nil || !strings.Contains(err.Error(), "cannot specify both --message and --auto") {
		t.Fatalf("expected empty --message and --auto conflict error, got %v", err)
	}
}

type mockCommitMsgProvider struct {
	response string
	req      zeroruntime.CompletionRequest
}

func (p *mockCommitMsgProvider) StreamCompletion(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	p.req = request
	events := make(chan zeroruntime.StreamEvent, 2)
	events <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventText, Content: p.response}
	events <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventDone}
	close(events)
	return events, nil
}

func TestRunChangesCommitAuto(t *testing.T) {
	cwd := t.TempDir()
	summary := zerogit.ChangeSummary{
		Root:   cwd,
		Branch: "main",
		Files:  []zerogit.FileChange{{Path: "README.md", Status: "modified"}},
		Diff:   "some diff content with ghp_SECRETKEYHERE",
	}

	mockProv := &mockCommitMsgProvider{
		response: "```\nfeat: auto commit message\n```",
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	commitCalled := false

	exitCode := runWithDeps([]string{"changes", "commit", "--auto"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) { return cwd, nil },
		inspectChanges: func(ctx context.Context, options zerogit.InspectOptions) (zerogit.ChangeSummary, error) {
			return summary, nil
		},
		resolveConfig: func(workspaceRoot string, overrides config.Overrides) (config.ResolvedConfig, error) {
			cfg := execResolvedConfig()
			cfg.Provider.Name = "openai"
			cfg.Provider.Model = "gpt-4o"
			return cfg, nil
		},
		newProvider: func(profile config.ProviderProfile) (zeroruntime.Provider, error) {
			return mockProv, nil
		},
		commitChanges: func(ctx context.Context, options zerogit.CommitOptions) (zerogit.CommitResult, error) {
			commitCalled = true
			if options.Message != "feat: auto commit message" {
				t.Fatalf("expected commit message 'feat: auto commit message', got %q", options.Message)
			}
			return zerogit.CommitResult{
				Root:       cwd,
				Message:    options.Message,
				Committed:  true,
				CommitHash: "abc1234",
				Before:     summary,
			}, nil
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	if !commitCalled {
		t.Fatal("expected commitChanges to be called")
	}
	// Verify that secret in the diff was redacted
	promptContent := mockProv.req.Messages[0].Content
	if strings.Contains(promptContent, "ghp_SECRETKEYHERE") {
		t.Fatal("expected secret in diff to be redacted, but it was found in the prompt")
	}
	if !strings.Contains(promptContent, "[REDACTED]") && !strings.Contains(promptContent, "REDACTED") {
		t.Fatalf("expected redacted diff content in prompt, got: %q", promptContent)
	}
	if !strings.Contains(stdout.String(), "Generating commit message using LLM...") {
		t.Fatalf("expected status message in stdout, got %q", stdout.String())
	}

	t.Run("EmptyLLMResponse", func(t *testing.T) {
		mockProvEmpty := &mockCommitMsgProvider{
			response: "   \n\n   ",
		}
		var stdout, stderr bytes.Buffer
		code := runWithDeps([]string{"changes", "commit", "--auto"}, &stdout, &stderr, appDeps{
			getwd: func() (string, error) { return cwd, nil },
			inspectChanges: func(ctx context.Context, options zerogit.InspectOptions) (zerogit.ChangeSummary, error) {
				return summary, nil
			},
			resolveConfig: func(workspaceRoot string, overrides config.Overrides) (config.ResolvedConfig, error) {
				return execResolvedConfig(), nil
			},
			newProvider: func(profile config.ProviderProfile) (zeroruntime.Provider, error) {
				return mockProvEmpty, nil
			},
		})
		if code == exitSuccess {
			t.Fatal("expected command to fail when LLM returns empty response, got success")
		}
		if !strings.Contains(stderr.String(), "empty commit message") {
			t.Fatalf("expected empty commit message error in stderr, got %q", stderr.String())
		}
	})
}
