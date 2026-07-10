package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
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
	if exit := runWithDeps([]string{"plugins", "install", "zero.demo@team", "--version", "0.1.0", "--yes", "--allow-unverified", "--json"}, &stdout, &stderr, deps); exit != exitSuccess {
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
	if !entry.Pinned || entry.Enabled == nil || !*entry.Enabled {
		t.Fatalf("marketplace version install should pin and record enabled:true: %#v", entry)
	}
}

func TestRunPluginsUpdateFromMarketplaceHonorsProjectScope(t *testing.T) {
	configHome := filepath.Join(t.TempDir(), "config")
	t.Setenv("XDG_CONFIG_HOME", configHome)
	userPluginsDir := t.TempDir()
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
	cwd := t.TempDir()
	deps := appDeps{
		getwd:      func() (string, error) { return cwd, nil },
		pluginsDir: func() string { return userPluginsDir },
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exit := runWithDeps([]string{"plugins", "marketplace", "add", catalogPath, "--allow-unverified"}, &stdout, &stderr, deps); exit != exitSuccess {
		t.Fatalf("marketplace add exit = %d stderr=%s", exit, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if exit := runWithDeps([]string{"plugins", "install", "zero.demo@team", "--version", "0.1.0", "--scope", "project", "--yes", "--allow-unverified"}, &stdout, &stderr, deps); exit != exitSuccess {
		t.Fatalf("project install exit = %d stderr=%s", exit, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if exit := runWithDeps([]string{"plugins", "update", "zero.demo", "--scope", "project", "--yes", "--allow-unverified", "--json"}, &stdout, &stderr, deps); exit != exitSuccess {
		t.Fatalf("project update exit = %d stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	projectPluginsDir := filepath.Join(cwd, ".zero", "plugins")
	if _, err := os.Stat(filepath.Join(projectPluginsDir, "zero.demo", "plugin.json")); err != nil {
		t.Fatalf("project plugin missing after update: %v", err)
	}
	if _, err := os.Stat(filepath.Join(userPluginsDir, "zero.demo")); err == nil {
		t.Fatalf("project-scoped update wrote to user plugin root")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat user plugin root: %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	if exit := runWithDeps([]string{"plugins", "verify", "zero.demo", "--scope", "project", "--json"}, &stdout, &stderr, deps); exit != exitSuccess {
		t.Fatalf("project verify exit = %d stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
}

func TestRunPluginsUpdateCheckAllSkipsPinnedAndDoesNotMutate(t *testing.T) {
	configHome := filepath.Join(t.TempDir(), "config")
	t.Setenv("XDG_CONFIG_HOME", configHome)
	pluginsDir := t.TempDir()
	cwd := t.TempDir()
	deps := appDeps{
		getwd:      func() (string, error) { return cwd, nil },
		pluginsDir: func() string { return pluginsDir },
	}

	demoV1, demoHashV1 := marketplaceTestPluginSource(t, "zero.demo", "0.1.0")
	pinnedV1, pinnedHashV1 := marketplaceTestPluginSource(t, "zero.pinned", "0.1.0")
	catalogPath := writeMarketplaceUpdateCatalog(t, map[string]marketplaceTestRelease{
		"zero.demo":   {Source: demoV1, Version: "0.1.0", Hash: demoHashV1},
		"zero.pinned": {Source: pinnedV1, Version: "0.1.0", Hash: pinnedHashV1},
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exit := runWithDeps([]string{"plugins", "marketplace", "add", catalogPath, "--allow-unverified"}, &stdout, &stderr, deps); exit != exitSuccess {
		t.Fatalf("marketplace add exit = %d stderr=%s", exit, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if exit := runWithDeps([]string{"plugins", "install", "zero.demo@team", "--yes", "--allow-unverified"}, &stdout, &stderr, deps); exit != exitSuccess {
		t.Fatalf("demo install exit = %d stderr=%s", exit, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if exit := runWithDeps([]string{"plugins", "install", "zero.pinned@team", "--version", "0.1.0", "--yes", "--allow-unverified"}, &stdout, &stderr, deps); exit != exitSuccess {
		t.Fatalf("pinned install exit = %d stderr=%s", exit, stderr.String())
	}

	demoV2, demoHashV2 := marketplaceTestPluginSource(t, "zero.demo", "0.2.0")
	pinnedV2, pinnedHashV2 := marketplaceTestPluginSource(t, "zero.pinned", "0.2.0")
	writeMarketplaceUpdateCatalogAt(t, catalogPath, map[string]marketplaceTestRelease{
		"zero.demo":   {Source: demoV2, Version: "0.2.0", Hash: demoHashV2},
		"zero.pinned": {Source: pinnedV2, Version: "0.2.0", Hash: pinnedHashV2},
	})

	stdout.Reset()
	stderr.Reset()
	if exit := runWithDeps([]string{"plugins", "update", "--scope", "user", "--check", "--allow-unverified", "--json"}, &stdout, &stderr, deps); exit != exitSuccess {
		t.Fatalf("update check exit = %d stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	check := decodePluginUpdateResults(t, stdout.Bytes())
	if got := check["zero.demo"]; !got.UpdateAvailable || got.Status != "available" || got.Version != "0.2.0" {
		t.Fatalf("zero.demo check result = %#v", got)
	}
	if got := check["zero.pinned"]; got.Status != "pinned" || got.UpdateAvailable {
		t.Fatalf("zero.pinned check result = %#v", got)
	}
	lock, err := plugins.ReadLock(pluginsDir)
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if lock["zero.demo"].Version != "0.1.0" || lock["zero.demo"].Hash != demoHashV1 {
		t.Fatalf("check mutated zero.demo lock: %#v", lock["zero.demo"])
	}

	stdout.Reset()
	stderr.Reset()
	if exit := runWithDeps([]string{"plugins", "update", "--scope", "user", "--yes", "--allow-unverified", "--json"}, &stdout, &stderr, deps); exit != exitSuccess {
		t.Fatalf("update all exit = %d stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	updated := decodePluginUpdateResults(t, stdout.Bytes())
	if got := updated["zero.demo"]; got.Status != "updated" || got.Version != "0.2.0" {
		t.Fatalf("zero.demo update result = %#v", got)
	}
	if got := updated["zero.pinned"]; got.Status != "pinned" {
		t.Fatalf("zero.pinned update result = %#v", got)
	}
	lock, err = plugins.ReadLock(pluginsDir)
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if lock["zero.demo"].Version != "0.2.0" || lock["zero.demo"].Hash != demoHashV2 {
		t.Fatalf("update did not mutate zero.demo lock: %#v", lock["zero.demo"])
	}
	if lock["zero.pinned"].Version != "0.1.0" || lock["zero.pinned"].Hash != pinnedHashV1 {
		t.Fatalf("pinned plugin mutated: %#v", lock["zero.pinned"])
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

func TestRunPluginsInstallFromUnsignedMarketplaceRequiresAllowUnverified(t *testing.T) {
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
	cwd := t.TempDir()
	deps := appDeps{
		getwd:      func() (string, error) { return cwd, nil },
		pluginsDir: func() string { return t.TempDir() },
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exit := runWithDeps([]string{"plugins", "marketplace", "add", catalogPath, "--allow-unverified"}, &stdout, &stderr, deps); exit != exitSuccess {
		t.Fatalf("marketplace add exit = %d stderr=%s", exit, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	exit := runWithDeps([]string{"plugins", "install", "zero.demo@team", "--yes"}, &stdout, &stderr, deps)
	if exit != exitUsage {
		t.Fatalf("install unsigned without allow-unverified exit = %d stderr=%s", exit, stderr.String())
	}
	if !strings.Contains(stderr.String(), "--allow-unverified") {
		t.Fatalf("expected allow-unverified guidance, got %s", stderr.String())
	}
}

func TestRunPluginsInfoShowsMarketplaceRelease(t *testing.T) {
	configHome := filepath.Join(t.TempDir(), "config")
	t.Setenv("XDG_CONFIG_HOME", configHome)
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
	cwd := t.TempDir()
	deps := appDeps{getwd: func() (string, error) { return cwd, nil }}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exit := runWithDeps([]string{"plugins", "marketplace", "add", catalogPath, "--allow-unverified"}, &stdout, &stderr, deps); exit != exitSuccess {
		t.Fatalf("marketplace add exit = %d stderr=%s", exit, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if exit := runWithDeps([]string{"plugins", "info", "zero.demo@team", "--allow-unverified", "--json"}, &stdout, &stderr, deps); exit != exitSuccess {
		t.Fatalf("plugins info exit = %d stderr=%s", exit, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"id": "zero.demo"`) || !strings.Contains(stdout.String(), `"version": "0.1.0"`) {
		t.Fatalf("unexpected info output: %s", stdout.String())
	}
}

func TestRunPluginsVerifyDetectsTamperedManagedPlugin(t *testing.T) {
	pluginsDir := t.TempDir()
	source := writeSourcePluginDir(t, filepath.Join(t.TempDir(), "src"), map[string]any{
		"schemaVersion": 1,
		"id":            "zero.demo",
		"name":          "Zero Demo",
		"version":       "0.1.0",
	})
	deps := appDeps{pluginsDir: func() string { return pluginsDir }}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exit := runWithDeps([]string{"plugin", "add", source}, &stdout, &stderr, deps); exit != exitSuccess {
		t.Fatalf("plugin add exit = %d stderr=%s", exit, stderr.String())
	}
	if err := os.WriteFile(filepath.Join(pluginsDir, "zero.demo", "plugin.json"), []byte(`{"schemaVersion":1,"id":"zero.demo","name":"Tampered","version":"0.1.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	exit := runWithDeps([]string{"plugins", "verify", "zero.demo", "--json"}, &stdout, &stderr, deps)
	if exit != exitUsage {
		t.Fatalf("verify tampered exit = %d stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "integrity") && !strings.Contains(stderr.String(), "mismatch") {
		t.Fatalf("expected integrity mismatch, got %s", stderr.String())
	}
}

type marketplaceTestRelease struct {
	Source  string
	Version string
	Hash    string
}

func marketplaceTestPluginSource(t *testing.T, id string, version string) (string, string) {
	t.Helper()
	source := writeSourcePluginDir(t, filepath.Join(t.TempDir(), "src"), map[string]any{
		"schemaVersion": 1,
		"id":            id,
		"name":          "Zero " + id,
		"version":       version,
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
	return source, hashResult.Hash
}

func writeMarketplaceUpdateCatalog(t *testing.T, releases map[string]marketplaceTestRelease) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "catalog.json")
	writeMarketplaceUpdateCatalogAt(t, path, releases)
	return path
}

func writeMarketplaceUpdateCatalogAt(t *testing.T, path string, releases map[string]marketplaceTestRelease) {
	t.Helper()
	ids := make([]string, 0, len(releases))
	for id := range releases {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	pluginsJSON := make([]string, 0, len(ids))
	for _, id := range ids {
		release := releases[id]
		pluginsJSON = append(pluginsJSON, `{
      "id": "`+id+`",
      "name": "`+id+`",
      "author": {"name": "Platform"},
      "license": "MIT",
      "review": {
        "status": "community",
        "date": "2026-07-10",
        "reviewer": "Zero Security",
        "url": "https://github.com/Gitlawb/zero-plugins/pull/1"
      },
      "releases": [
        {
          "version": "`+release.Version+`",
          "repository": "`+filepath.ToSlash(release.Source)+`",
          "commit": "`+strings.Repeat("a", 40)+`",
          "treeHash": "`+release.Hash+`",
          "components": {"tools": [{"name": "lookup", "permission": "prompt"}]}
        }
      ]
    }`)
	}
	body := `{
  "schemaVersion": 1,
  "id": "team",
  "owner": "Platform",
  "plugins": [
    ` + strings.Join(pluginsJSON, ",\n    ") + `
  ]
}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

type pluginUpdateResultForTest struct {
	ID              string `json:"id"`
	Status          string `json:"status"`
	Version         string `json:"version"`
	UpdateAvailable bool   `json:"updateAvailable"`
}

func decodePluginUpdateResults(t *testing.T, data []byte) map[string]pluginUpdateResultForTest {
	t.Helper()
	var payload struct {
		Results []pluginUpdateResultForTest `json:"results"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("update JSON: %v\n%s", err, string(data))
	}
	out := map[string]pluginUpdateResultForTest{}
	for _, result := range payload.Results {
		out[result.ID] = result
	}
	return out
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
      "review": {
        "status": "community",
        "date": "2026-07-10",
        "reviewer": "Zero Security",
        "url": "https://github.com/Gitlawb/zero-plugins/pull/1"
      },
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
