package repoinfo

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeGit returns canned output per subcommand and records every subcommand used.
func fakeGit(t *testing.T, out map[string]string, used *[]string) RunGit {
	t.Helper()
	return func(_ context.Context, _ string, args ...string) (string, error) {
		if used != nil && len(args) > 0 {
			*used = append(*used, args[0])
		}
		key := args[0]
		v, ok := out[key]
		if !ok {
			return "", errors.New("no canned output for " + key)
		}
		return v, nil
	}
}

// ls-tree -z output: NUL-terminated records, paths unquoted.
const lsTree = "" +
	"100644 blob aaa 5000\tmain.go\x00" +
	"100644 blob bbb 2500\tinternal/util.go\x00" +
	"100644 blob ccc 100\tgo.mod\x00" +
	"100644 blob ddd 9000\tweb/app.ts\x00" +
	"100644 blob eee 50\t.github/workflows/ci.yml\x00" +
	"160000 commit fff -\tvendored\x00"

func TestCollectCoreMetrics(t *testing.T) {
	now := time.Unix(100*86400, 0) // 100 days after first commit ts=0
	info, err := Collect(context.Background(), Options{
		Now: now,
		RunGit: fakeGit(t, map[string]string{
			"ls-tree":   lsTree,
			"rev-parse": "main\n",
			"remote":    "https://github.com/x/y.git\n",
			"log":       "0\n",
			"rev-list":  "12\n",
		}, nil),
	})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if !info.HasGit {
		t.Fatal("HasGit should be true")
	}
	if info.FileCount != 5 { // gitlink ("-" size) skipped
		t.Fatalf("FileCount=%d want 5", info.FileCount)
	}
	// Dirs: internal, web, .github, .github/workflows (passthrough .github counted).
	if info.DirectoryCount != 4 {
		t.Fatalf("DirectoryCount=%d want 4", info.DirectoryCount)
	}
	if info.MaxDepth != 2 {
		t.Fatalf("MaxDepth=%d want 2", info.MaxDepth)
	}
	if info.PrimaryLanguage != "TypeScript" { // 9000 bytes > Go's 7500
		t.Fatalf("PrimaryLanguage=%q want TypeScript", info.PrimaryLanguage)
	}
	if info.LanguageCount != 2 {
		t.Fatalf("LanguageCount=%d want 2 (Go, TypeScript)", info.LanguageCount)
	}
	if len(info.BuildTools) != 1 || info.BuildTools[0] != "go.mod" {
		t.Fatalf("BuildTools=%v", info.BuildTools)
	}
	if len(info.CICD) != 1 || info.CICD[0] != "GitHub Actions" {
		t.Fatalf("CICD=%v", info.CICD)
	}
	if info.Branch != "main" {
		t.Fatalf("Branch=%q", info.Branch)
	}
	if info.RemoteURL != "https://github.com/x/y.git" {
		t.Fatalf("RemoteURL=%q", info.RemoteURL)
	}
	if info.AgeDays == nil || *info.AgeDays != 100 {
		t.Fatalf("AgeDays=%v want 100", info.AgeDays)
	}
	if info.CommitVelocity30d == nil || *info.CommitVelocity30d != 12 {
		t.Fatalf("CommitVelocity30d=%v want 12", info.CommitVelocity30d)
	}
}

func TestCollectContributorsUnique(t *testing.T) {
	run := func(_ context.Context, _ string, args ...string) (string, error) {
		switch args[0] {
		case "ls-tree":
			return lsTree, nil
		case "log":
			for _, a := range args {
				if a == "--format=%aN" {
					return "Ann\nBob\nAnn\n\n", nil
				}
			}
			return "0\n", nil // first-commit ts
		case "rev-parse":
			return "main\n", nil
		case "rev-list":
			return "3\n", nil
		case "remote":
			return "", errors.New("no remote")
		}
		return "", errors.New("unexpected " + args[0])
	}
	info, err := Collect(context.Background(), Options{Now: time.Unix(0, 0), RunGit: run})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if info.Contributors90d == nil || *info.Contributors90d != 2 {
		t.Fatalf("Contributors90d=%v want 2", info.Contributors90d)
	}
	if info.RemoteURL != "" {
		t.Fatalf("RemoteURL should be empty when origin missing, got %q", info.RemoteURL)
	}
}

func TestCollectHistoryMetricsFailSoft(t *testing.T) {
	// The author-list log fails, but first-commit log + rev-list succeed: only
	// Contributors90d is omitted; the rest of the report still renders.
	run := func(_ context.Context, _ string, args ...string) (string, error) {
		switch args[0] {
		case "ls-tree":
			return lsTree, nil
		case "rev-parse":
			return "main\n", nil
		case "remote":
			return "u\n", nil
		case "rev-list":
			return "4\n", nil
		case "log":
			for _, a := range args {
				if a == "--format=%aN" {
					return "", errors.New("boom")
				}
			}
			return "0\n", nil
		}
		return "", errors.New("unexpected " + args[0])
	}
	info, err := Collect(context.Background(), Options{Now: time.Unix(0, 0), RunGit: run})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if info.Contributors90d != nil {
		t.Fatalf("Contributors90d should be nil on log failure, got %d", *info.Contributors90d)
	}
	if info.AgeDays == nil {
		t.Fatal("AgeDays should still be set (first-commit log succeeded)")
	}
	if info.CommitVelocity30d == nil {
		t.Fatal("CommitVelocity30d should still be set")
	}
}

func TestCollectAgeFromOldestRootCommit(t *testing.T) {
	// Age must come from the FIRST commit (root, via --max-parents=0), and the
	// OLDEST root when there are several — not the latest commit. This fails
	// against the old `log --reverse -1` form (which returned the latest commit).
	run := func(_ context.Context, _ string, args ...string) (string, error) {
		switch args[0] {
		case "ls-tree":
			return lsTree, nil
		case "rev-parse":
			return "main\n", nil
		case "remote":
			return "", errors.New("no remote")
		case "rev-list":
			return "1\n", nil
		case "log":
			for _, a := range args {
				if a == "--max-parents=0" {
					return "500\n100\n", nil // two roots; oldest is 100
				}
				if a == "--format=%aN" {
					return "Ann\n", nil
				}
			}
			return "", errors.New("unexpected log args")
		}
		return "", errors.New("unexpected " + args[0])
	}
	info, err := Collect(context.Background(), Options{Now: time.Unix(100+10*86400, 0), RunGit: run})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if info.AgeDays == nil || *info.AgeDays != 10 {
		t.Fatalf("AgeDays=%v want 10 (now - oldest root 100)", info.AgeDays)
	}
}

func TestSanitizeRemoteURL(t *testing.T) {
	cases := map[string]string{
		"https://x-access-token:ghp_secret@github.com/o/r.git": "https://github.com/o/r.git",
		"https://user:pass@gitlab.com/o/r.git":                 "https://gitlab.com/o/r.git",
		"https://github.com/o/r.git":                           "https://github.com/o/r.git",
		"ssh://git@github.com/o/r.git":                         "ssh://github.com/o/r.git",
		"git@github.com:o/r.git":                               "github.com:o/r.git",
		"":                                                     "",
	}
	for in, want := range cases {
		got := sanitizeRemoteURL(in)
		if got != want {
			t.Fatalf("sanitizeRemoteURL(%q)=%q want %q", in, got, want)
		}
		if strings.Contains(got, "secret") || strings.Contains(got, "pass") || strings.Contains(got, "ghp_") {
			t.Fatalf("credential leaked for %q: %q", in, got)
		}
	}
}

func TestCollectStripsRemoteCredentials(t *testing.T) {
	run := func(_ context.Context, _ string, args ...string) (string, error) {
		switch args[0] {
		case "ls-tree":
			return lsTree, nil
		case "remote":
			return "https://x-access-token:ghp_SECRET@github.com/o/r.git\n", nil
		case "rev-parse":
			return "main\n", nil
		case "log":
			return "0\n", nil
		case "rev-list":
			return "1\n", nil
		}
		return "", errors.New("unexpected " + args[0])
	}
	info, err := Collect(context.Background(), Options{Now: time.Unix(0, 0), RunGit: run})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if info.RemoteURL != "https://github.com/o/r.git" {
		t.Fatalf("RemoteURL=%q want sanitized", info.RemoteURL)
	}
	if strings.Contains(info.RemoteURL, "ghp_SECRET") {
		t.Fatal("credential leaked into RemoteURL")
	}
}

func TestCollectNotGitRepo(t *testing.T) {
	run := func(_ context.Context, _ string, args ...string) (string, error) {
		return "", errors.New("fatal: not a git repository")
	}
	if _, err := Collect(context.Background(), Options{RunGit: run}); !errors.Is(err, ErrNotGitRepo) {
		t.Fatalf("expected ErrNotGitRepo, got %v", err)
	}
}

func TestCollectOnlyReadOnlyLocalSubcommands(t *testing.T) {
	allowed := map[string]bool{"ls-tree": true, "rev-parse": true, "remote": true, "log": true, "rev-list": true}
	var used []string
	_, err := Collect(context.Background(), Options{
		Now: time.Unix(0, 0),
		RunGit: fakeGit(t, map[string]string{
			"ls-tree": lsTree, "rev-parse": "main\n", "remote": "u\n", "log": "0\n", "rev-list": "1\n",
		}, &used),
	})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, sub := range used {
		if !allowed[sub] {
			t.Fatalf("disallowed git subcommand used: %q (network-free invariant)", sub)
		}
	}
}
