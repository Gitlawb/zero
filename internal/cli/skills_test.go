package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/tui"
)

func writeSkillFixture(t *testing.T, dir string, name string, content string) {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", skillDir, err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
}

func TestRunSkillsListText(t *testing.T) {
	dir := t.TempDir()
	writeSkillFixture(t, dir, "confirmation-policy", "---\nname: confirmation-policy\ndescription: Ask before risky actions.\n---\nbody")

	var stdout, stderr bytes.Buffer
	exit := runWithDeps([]string{"skills", "list"}, &stdout, &stderr, appDeps{
		skillsDir: func() string { return dir },
	})
	if exit != 0 {
		t.Fatalf("exit = %d, stderr = %s", exit, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"confirmation-policy", "Ask before risky actions."} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestRunSkillsDefaultsToList(t *testing.T) {
	dir := t.TempDir()
	writeSkillFixture(t, dir, "demo", "body")

	var stdout, stderr bytes.Buffer
	exit := runWithDeps([]string{"skills"}, &stdout, &stderr, appDeps{
		skillsDir: func() string { return dir },
	})
	if exit != 0 {
		t.Fatalf("exit = %d, stderr = %s", exit, stderr.String())
	}
	if !strings.Contains(stdout.String(), "demo") {
		t.Fatalf("output missing demo:\n%s", stdout.String())
	}
}

func TestRunSkillsListJSON(t *testing.T) {
	dir := t.TempDir()
	writeSkillFixture(t, dir, "demo", "---\nname: demo\ndescription: a demo\n---\nbody")

	var stdout, stderr bytes.Buffer
	exit := runWithDeps([]string{"skills", "list", "--json"}, &stdout, &stderr, appDeps{
		skillsDir: func() string { return dir },
	})
	if exit != 0 {
		t.Fatalf("exit = %d, stderr = %s", exit, stderr.String())
	}
	var payload struct {
		Skills []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Path        string `json:"path"`
			Content     string `json:"content"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout.String())
	}
	if len(payload.Skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(payload.Skills))
	}
	if payload.Skills[0].Name != "demo" || payload.Skills[0].Description != "a demo" {
		t.Fatalf("unexpected skill: %#v", payload.Skills[0])
	}
	if payload.Skills[0].Path == "" {
		t.Fatalf("path should be present")
	}
}

func TestRunSkillsEmptyDir(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exit := runWithDeps([]string{"skills", "list"}, &stdout, &stderr, appDeps{
		skillsDir: func() string { return filepath.Join(t.TempDir(), "missing") },
	})
	if exit != 0 {
		t.Fatalf("exit = %d, stderr = %s", exit, stderr.String())
	}
	if !strings.Contains(strings.ToLower(stdout.String()), "no") {
		t.Fatalf("expected a no-skills message, got:\n%s", stdout.String())
	}
}

func TestRunSkillsDoesNotLaunchTUI(t *testing.T) {
	var stdout, stderr bytes.Buffer
	launchCalled := false
	_ = runWithDeps([]string{"skills"}, &stdout, &stderr, appDeps{
		skillsDir: func() string { return t.TempDir() },
		runTUI: func(ctx context.Context, options tui.Options) int {
			launchCalled = true
			return 0
		},
	})
	if launchCalled {
		t.Fatalf("TUI launcher should not be called for skills command")
	}
}
