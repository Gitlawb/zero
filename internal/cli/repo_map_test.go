package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRepoMapHelpDocumentsFlags(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"repo-map", "--help"}, &stdout, &stderr, appDeps{})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	for _, want := range []string{"Usage:", "zero repo-map [flags]", "--json", "--query", "--max-files", "--scan-max-files", "--max-depth"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected repo-map help to contain %q, got %q", want, stdout.String())
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func TestRunRepoMapTextSummaryUsesCurrentWorkingDirectory(t *testing.T) {
	cwd := newRepoMapFixture(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"repo-map", "--max-files", "3", "--max-depth", "2"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) {
			return cwd, nil
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	for _, want := range []string{"Repo map", "Root: " + cwd, "Files:", "Languages:", "cmd/zero/main.go"} {
		if !repoMapOutputContains(stdout.String(), want) {
			t.Fatalf("expected repo-map output to contain %q, got %q", want, stdout.String())
		}
	}
	if repoMapOutputContains(stdout.String(), "docs/deep/nested/notes.md") {
		t.Fatalf("expected --max-depth 2 to omit deep file, got %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func TestRunRepoMapJSONSummary(t *testing.T) {
	cwd := newRepoMapFixture(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"repo-map", "--json", "--max-files", "2", "--max-depth", "2"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) {
			return cwd, nil
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	var report struct {
		Root    string `json:"root"`
		Summary struct {
			FileCount      int `json:"fileCount"`
			DirectoryCount int `json:"directoryCount"`
			MaxDepth       int `json:"maxDepth"`
		} `json:"summary"`
		Languages []struct {
			Name      string `json:"name"`
			FileCount int    `json:"fileCount"`
		} `json:"languages"`
		Files []struct {
			Path     string `json:"path"`
			Language string `json:"language"`
		} `json:"files"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("repo-map JSON did not decode: %v\n%s", err, stdout.String())
	}
	if report.Root != cwd {
		t.Fatalf("Root = %q, want %q", report.Root, cwd)
	}
	if report.Summary.FileCount == 0 {
		t.Fatalf("Summary.FileCount = 0, want scanned files")
	}
	if report.Summary.DirectoryCount == 0 {
		t.Fatalf("Summary.DirectoryCount = 0, want scanned directories")
	}
	if report.Summary.MaxDepth > 2 {
		t.Fatalf("Summary.MaxDepth = %d, want <= 2", report.Summary.MaxDepth)
	}
	if len(report.Files) == 0 || len(report.Files) > 2 {
		t.Fatalf("Files length = %d, want 1..2", len(report.Files))
	}
	if len(report.Languages) == 0 {
		t.Fatalf("Languages length = 0, want detected languages")
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func TestRunRepoMapQueryPrintsRankedMatches(t *testing.T) {
	cwd := newRepoMapFixture(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"repo-map", "--query", "agent runtime", "--max-files", "1"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) {
			return cwd, nil
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(strings.ToLower(out), "rank") {
		t.Fatalf("expected ranked query output, got %q", out)
	}
	if !repoMapOutputContains(out, "internal/agent/runtime.go") {
		t.Fatalf("expected query output to include best match, got %q", out)
	}
	if repoMapOutputContains(out, "cmd/zero/main.go") {
		t.Fatalf("expected --max-files 1 to limit query output, got %q", out)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func TestRunRepoMapScanMaxFilesBoundsSummary(t *testing.T) {
	cwd := newRepoMapFixture(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"repo-map", "--json", "--scan-max-files", "1", "--max-files", "5"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) {
			return cwd, nil
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	var report struct {
		Summary struct {
			FileCount int  `json:"fileCount"`
			Truncated bool `json:"truncated"`
		} `json:"summary"`
		Files []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("repo-map JSON did not decode: %v\n%s", err, stdout.String())
	}
	if report.Summary.FileCount != 1 {
		t.Fatalf("Summary.FileCount = %d, want 1", report.Summary.FileCount)
	}
	if !report.Summary.Truncated {
		t.Fatal("Summary.Truncated=false want true")
	}
	if len(report.Files) != 1 {
		t.Fatalf("Files length = %d, want 1", len(report.Files))
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func TestRunRepoMapRejectsInvalidLimits(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{name: "max files", args: []string{"repo-map", "--max-files", "0"}, want: "invalid --max-files"},
		{name: "scan max files", args: []string{"repo-map", "--scan-max-files", "0"}, want: "invalid --scan-max-files"},
		{name: "max depth", args: []string{"repo-map", "--max-depth", "-1"}, want: "invalid --max-depth"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			exitCode := runWithDeps(tt.args, &stdout, &stderr, appDeps{})

			if exitCode != exitUsage {
				t.Fatalf("expected exit code %d, got %d", exitUsage, exitCode)
			}
			if stdout.Len() != 0 {
				t.Fatalf("expected empty stdout, got %q", stdout.String())
			}
			if !strings.Contains(stderr.String(), tt.want) {
				t.Fatalf("expected stderr to contain %q, got %q", tt.want, stderr.String())
			}
		})
	}
}

func TestRunHelpIncludesRepoMap(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"--help"}, &stdout, &stderr, appDeps{})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), "repo-map") {
		t.Fatalf("expected global help to include repo-map, got %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func newRepoMapFixture(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	writeRepoMapFixtureFile(t, dir, "go.mod", "module example.test/repo-map\n\ngo 1.24\n")
	writeRepoMapFixtureFile(t, dir, "cmd/zero/main.go", "package main\n\nfunc main() {}\n")
	writeRepoMapFixtureFile(t, dir, "internal/agent/runtime.go", "package agent\n\n// Agent runtime coordinates tools and model calls.\nfunc RunAgentRuntime() {}\n")
	writeRepoMapFixtureFile(t, dir, "web/app.js", "export function renderApp() { return 'ui'; }\n")
	writeRepoMapFixtureFile(t, dir, "docs/deep/nested/notes.md", "# Deep notes\n\nThis file is beyond max depth two.\n")
	return dir
}

func writeRepoMapFixtureFile(t *testing.T, root string, name string, contents string) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func repoMapOutputContains(output string, want string) bool {
	normalizedOutput := filepath.ToSlash(output)
	normalizedWant := filepath.ToSlash(want)
	return strings.Contains(normalizedOutput, normalizedWant)
}
