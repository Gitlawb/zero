package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/repoinfo"
)

func TestParseRepoInfoArgs(t *testing.T) {
	opts, help, err := parseRepoInfoArgs([]string{"--json", "--cwd", "/tmp/x"})
	if err != nil || help {
		t.Fatalf("parse: help=%v err=%v", help, err)
	}
	if !opts.json || opts.cwd != "/tmp/x" {
		t.Fatalf("got %+v", opts)
	}
	if _, h, _ := parseRepoInfoArgs([]string{"--help"}); !h {
		t.Fatal("expected help")
	}
	if _, _, err := parseRepoInfoArgs([]string{"--bogus"}); err == nil {
		t.Fatal("expected error for unknown flag")
	}
}

func TestFormatRepoInfoText(t *testing.T) {
	age := 42
	contrib := 3
	info := repoInfoTestData(age, contrib)
	out := formatRepoInfo(info)
	for _, want := range []string{"Files", "TypeScript", "main", "GitHub Actions", "42", "3"} {
		if !strings.Contains(out, want) {
			t.Fatalf("text output missing %q:\n%s", want, out)
		}
	}
}

func TestRunRepoInfoJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runRepoInfo([]string{"--json"}, &stdout, &stderr, testRepoInfoDeps())
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	var info map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &info); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout.String())
	}
	if info["hasGit"] != true {
		t.Fatalf("expected hasGit=true, got %v", info["hasGit"])
	}
}

func repoInfoTestData(age, contrib int) repoinfo.Info {
	return repoinfo.Info{
		FileCount: 10, DirectoryCount: 3, MaxDepth: 2, LOCEstimate: 500,
		Languages:       []repoinfo.LangStat{{Name: "TypeScript", LOCEstimate: 400, FileCount: 4}},
		PrimaryLanguage: "TypeScript", LanguageCount: 1,
		WorkspaceType: "none", CICD: []string{"GitHub Actions"},
		HasGit: true, Branch: "main", AgeDays: &age, Contributors90d: &contrib,
	}
}

func testRepoInfoDeps() appDeps {
	return defaultAppDeps()
}
