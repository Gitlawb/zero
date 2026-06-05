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

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/redaction"
	"github.com/Gitlawb/zero/internal/verify"
	"github.com/Gitlawb/zero/internal/worktrees"
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
			} else if !strings.Contains(stdout.String(), "Zero verification") || !strings.Contains(stdout.String(), "go.test") || !strings.Contains(stdout.String(), cwd) {
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
