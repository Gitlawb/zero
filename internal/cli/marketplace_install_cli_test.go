package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/plugins"
)

func TestRunPluginsInstallFromMarketplaceCatalog(t *testing.T) {
	configHome := filepath.Join(t.TempDir(), "config")
	t.Setenv("XDG_CONFIG_HOME", configHome)
	pluginsDir := t.TempDir()
	source := writeSourcePluginDir(t, filepath.Join(t.TempDir(), "src"), map[string]any{
		"schemaVersion": 1,
		"id":            "zero.demo",
		"name":          "Zero Demo",
		"version":       "0.1.0",
		"tools": []any{map[string]any{
			"name":       "lookup",
			"command":    "node",
			"permission": "prompt",
		}},
	})
	hashRoot := t.TempDir()
	hashResult, err := plugins.Install(context.Background(), plugins.InstallOptions{Source: source, Dir: hashRoot})
	if err != nil {
		t.Fatalf("seed hash install: %v", err)
	}
	catalogPath := writeMarketplaceInstallCatalog(t, source, hashResult.Hash)
	deps := appDeps{
		getwd:      func() (string, error) { return t.TempDir(), nil },
		pluginsDir: func() string { return pluginsDir },
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exit := runWithDeps([]string{"plugins", "marketplace", "add", catalogPath, "--allow-unverified"}, &stdout, &stderr, deps); exit != exitSuccess {
		t.Fatalf("marketplace add exit = %d stderr=%s", exit, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if exit := runWithDeps([]string{"plugins", "install", "zero.demo@team", "--yes", "--json"}, &stdout, &stderr, deps); exit != exitSuccess {
		t.Fatalf("plugins install exit = %d stderr=%s", exit, stderr.String())
	}
	var payload struct {
		ID      string `json:"id"`
		Version string `json:"version"`
		Catalog string `json:"catalog"`
		Hash    string `json:"hash"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("install JSON: %v\n%s", err, stdout.String())
	}
	if payload.ID != "zero.demo" || payload.Version != "0.1.0" || payload.Catalog != "team" || payload.Hash != hashResult.Hash {
		t.Fatalf("unexpected install payload: %#v", payload)
	}
	if _, err := os.Stat(filepath.Join(pluginsDir, "zero.demo", "plugin.json")); err != nil {
		t.Fatalf("plugin not installed: %v", err)
	}
	lock, err := plugins.ReadLock(pluginsDir)
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	entry := lock["zero.demo"]
	if entry.Catalog != "team" || entry.Version != "0.1.0" || entry.Commit != strings.Repeat("a", 40) {
		t.Fatalf("marketplace lock metadata missing: %#v", entry)
	}
}

func TestRunPluginsInstallFromMarketplaceRequiresYes(t *testing.T) {
	configHome := filepath.Join(t.TempDir(), "config")
	t.Setenv("XDG_CONFIG_HOME", configHome)
	source := writeSourcePluginDir(t, filepath.Join(t.TempDir(), "src"), map[string]any{
		"schemaVersion": 1,
		"id":            "zero.demo",
		"name":          "Zero Demo",
		"version":       "0.1.0",
	})
	hashRoot := t.TempDir()
	hashResult, err := plugins.Install(context.Background(), plugins.InstallOptions{Source: source, Dir: hashRoot})
	if err != nil {
		t.Fatalf("seed hash install: %v", err)
	}
	catalogPath := writeMarketplaceInstallCatalog(t, source, hashResult.Hash)
	deps := appDeps{
		getwd:      func() (string, error) { return t.TempDir(), nil },
		pluginsDir: func() string { return t.TempDir() },
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exit := runWithDeps([]string{"plugins", "marketplace", "add", catalogPath, "--allow-unverified"}, &stdout, &stderr, deps); exit != exitSuccess {
		t.Fatalf("marketplace add exit = %d stderr=%s", exit, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	exit := runWithDeps([]string{"plugins", "install", "zero.demo@team"}, &stdout, &stderr, deps)
	if exit != exitUsage {
		t.Fatalf("install without yes exit = %d stderr=%s", exit, stderr.String())
	}
	if !strings.Contains(stderr.String(), "--yes") {
		t.Fatalf("expected --yes guidance, got %s", stderr.String())
	}
}

func writeMarketplaceInstallCatalog(t *testing.T, source string, hash string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")
	body := `{
  "schemaVersion": 1,
  "id": "team",
  "owner": "Platform",
  "plugins": [
    {
      "id": "zero.demo",
      "name": "Zero Demo",
      "author": {"name": "Platform"},
      "license": "MIT",
      "review": {"status": "community"},
      "releases": [
        {
          "version": "0.1.0",
          "repository": "` + filepath.ToSlash(source) + `",
          "commit": "` + strings.Repeat("a", 40) + `",
          "treeHash": "` + hash + `",
          "components": {"tools": [{"name": "lookup", "permission": "prompt"}]}
        }
      ]
    }
  ]
}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
