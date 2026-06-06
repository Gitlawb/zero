package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRunSpecialistListShowAndPath(t *testing.T) {
	cwd := t.TempDir()
	configRoot := setSpecialistConfigRoot(t)
	userDir := filepath.Join(configRoot, "zero", "specialists")
	writeSpecialistManifest(t, filepath.Join(userDir, "triage.md"), `---
name: triage
description: Triage failing tests
tools: [read-only]
---
Find the likely failure area.`)
	deps := appDeps{getwd: func() (string, error) { return cwd, nil }}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"specialist", "list"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("exitCode = %d stderr=%s", exitCode, stderr.String())
	}
	for _, want := range []string{"Zero Specialists", "worker [builtin]", "triage [user]", "code-review"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("list output missing %q: %s", want, stdout.String())
		}
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runWithDeps([]string{"specialist", "show", "triage"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("exitCode = %d stderr=%s", exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Zero Specialist: triage") || !strings.Contains(stdout.String(), "Find the likely failure area.") {
		t.Fatalf("unexpected show output: %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runWithDeps([]string{"specialist", "path"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("exitCode = %d stderr=%s", exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), userDir) || !strings.Contains(stdout.String(), filepath.Join(cwd, ".zero", "specialists")) {
		t.Fatalf("unexpected path output: %s", stdout.String())
	}
}

func TestRunSpecialistListJSON(t *testing.T) {
	cwd := t.TempDir()
	setSpecialistConfigRoot(t)
	deps := appDeps{getwd: func() (string, error) { return cwd, nil }}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"specialists", "list", "--json"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("exitCode = %d stderr=%s", exitCode, stderr.String())
	}
	var payload struct {
		Specialists []struct {
			Name     string `json:"name"`
			Location string `json:"location"`
		} `json:"specialists"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode JSON: %v\n%s", err, stdout.String())
	}
	if len(payload.Specialists) == 0 || payload.Specialists[0].Name == "" {
		t.Fatalf("unexpected JSON payload: %#v", payload)
	}
}

func TestRunSpecialistShowMissingReturnsUsage(t *testing.T) {
	setSpecialistConfigRoot(t)
	deps := appDeps{getwd: func() (string, error) { return t.TempDir(), nil }}
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"specialist", "show", "missing"}, &stdout, &stderr, deps)

	if exitCode != exitUsage {
		t.Fatalf("exitCode = %d, want usage", exitCode)
	}
	if !strings.Contains(stderr.String(), "not found") {
		t.Fatalf("expected not found error, got %q", stderr.String())
	}
}

func setSpecialistConfigRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("APPDATA", root)
	case "darwin":
		t.Setenv("HOME", root)
	default:
		t.Setenv("XDG_CONFIG_HOME", root)
	}
	configRoot, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("UserConfigDir() error = %v", err)
	}
	return configRoot
}

func writeSpecialistManifest(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create manifest dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}
