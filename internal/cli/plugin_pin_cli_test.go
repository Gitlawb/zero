package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/plugins"
)

func TestRunPluginPinUnpinJSON(t *testing.T) {
	pluginsDir := t.TempDir()
	src := writeSourcePluginDir(t, filepath.Join(t.TempDir(), "src"), map[string]any{
		"schemaVersion": 1,
		"id":            "zero.demo",
		"name":          "Zero Demo",
		"version":       "0.1.0",
	})
	deps := appDeps{pluginsDir: func() string { return pluginsDir }}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exit := runWithDeps([]string{"plugin", "add", src}, &stdout, &stderr, deps); exit != exitSuccess {
		t.Fatalf("plugin add exit = %d stderr=%s", exit, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if exit := runWithDeps([]string{"plugins", "pin", "zero.demo", "--version", "0.1.0", "--json"}, &stdout, &stderr, deps); exit != exitSuccess {
		t.Fatalf("pin exit = %d stderr=%s", exit, stderr.String())
	}
	var pinned struct {
		ID      string `json:"id"`
		Pinned  bool   `json:"pinned"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &pinned); err != nil {
		t.Fatalf("pin JSON: %v\n%s", err, stdout.String())
	}
	if pinned.ID != "zero.demo" || !pinned.Pinned || pinned.Version != "0.1.0" {
		t.Fatalf("unexpected pin payload: %#v", pinned)
	}
	lock, err := plugins.ReadLock(pluginsDir)
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if !lock["zero.demo"].Pinned || lock["zero.demo"].Version != "0.1.0" {
		t.Fatalf("lock not pinned: %#v", lock["zero.demo"])
	}

	stdout.Reset()
	stderr.Reset()
	if exit := runWithDeps([]string{"plugins", "unpin", "zero.demo", "--json"}, &stdout, &stderr, deps); exit != exitSuccess {
		t.Fatalf("unpin exit = %d stderr=%s", exit, stderr.String())
	}
	var unpinned struct {
		ID     string `json:"id"`
		Pinned bool   `json:"pinned"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &unpinned); err != nil {
		t.Fatalf("unpin JSON: %v\n%s", err, stdout.String())
	}
	if unpinned.ID != "zero.demo" || unpinned.Pinned {
		t.Fatalf("unexpected unpin payload: %#v", unpinned)
	}
}

func TestRunPluginPinRejectsVersionDifferentFromInstalledPlugin(t *testing.T) {
	pluginsDir := t.TempDir()
	src := writeSourcePluginDir(t, filepath.Join(t.TempDir(), "src"), map[string]any{
		"schemaVersion": 1,
		"id":            "zero.demo",
		"name":          "Zero Demo",
		"version":       "0.1.0",
	})
	deps := appDeps{pluginsDir: func() string { return pluginsDir }}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exit := runWithDeps([]string{"plugin", "add", src}, &stdout, &stderr, deps); exit != exitSuccess {
		t.Fatalf("plugin add exit = %d stderr=%s", exit, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if exit := runWithDeps([]string{"plugins", "pin", "zero.demo", "--version", "9.9.9"}, &stdout, &stderr, deps); exit != exitUsage {
		t.Fatalf("pin mismatch exit = %d stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "installed version") {
		t.Fatalf("expected installed version error, got %s", stderr.String())
	}
	lock, err := plugins.ReadLock(pluginsDir)
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if lock["zero.demo"].Pinned {
		t.Fatalf("mismatched pin should not mutate lock: %#v", lock["zero.demo"])
	}
}
