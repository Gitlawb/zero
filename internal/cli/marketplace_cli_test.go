package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunPluginsMarketplaceValidateJSON(t *testing.T) {
	catalogPath := writeMarketplaceTestCatalog(t)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"plugins", "marketplace", "validate", catalogPath, "--json"}, &stdout, &stderr, appDeps{})
	if exitCode != exitSuccess {
		t.Fatalf("exitCode = %d stderr=%s", exitCode, stderr.String())
	}

	var payload struct {
		Catalog struct {
			ID string `json:"id"`
		} `json:"catalog"`
		Verification struct {
			Status string `json:"status"`
		} `json:"verification"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("validate JSON failed to decode: %v\n%s", err, stdout.String())
	}
	if payload.Catalog.ID != "team" || payload.Verification.Status != "unsigned" {
		t.Fatalf("unexpected validate payload: %#v", payload)
	}
}

func TestRunPluginsMarketplaceAddListAndBrowse(t *testing.T) {
	configHome := filepath.Join(t.TempDir(), "config")
	t.Setenv("XDG_CONFIG_HOME", configHome)
	catalogPath := writeMarketplaceTestCatalog(t)
	cwd := t.TempDir()
	deps := appDeps{getwd: func() (string, error) { return cwd, nil }}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"plugins", "marketplace", "add", catalogPath, "--allow-unverified", "--json"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("add exitCode = %d stderr=%s", exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"id": "team"`) || !strings.Contains(stdout.String(), `"verificationStatus": "unsigned"`) {
		t.Fatalf("unexpected add output: %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runWithDeps([]string{"plugins", "marketplace", "list", "--json"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("list exitCode = %d stderr=%s", exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"id": "official"`) || !strings.Contains(stdout.String(), `"id": "team"`) {
		t.Fatalf("marketplace list missing catalogs: %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runWithDeps([]string{"plugins", "browse", "lookup", "--catalog", "team", "--json"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("browse exitCode = %d stderr=%s", exitCode, stderr.String())
	}
	var browse struct {
		Catalog string `json:"catalog"`
		Plugins []struct {
			ID string `json:"id"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &browse); err != nil {
		t.Fatalf("browse JSON failed to decode: %v\n%s", err, stdout.String())
	}
	if browse.Catalog != "team" || len(browse.Plugins) != 1 || browse.Plugins[0].ID != "zero.demo" {
		t.Fatalf("unexpected browse output: %#v", browse)
	}
}

func TestRunPluginsMarketplaceAddRequiresAllowUnverified(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	catalogPath := writeMarketplaceTestCatalog(t)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"plugins", "marketplace", "add", catalogPath}, &stdout, &stderr, appDeps{})
	if exitCode != exitUsage {
		t.Fatalf("exitCode = %d stderr=%s", exitCode, stderr.String())
	}
	if !strings.Contains(stderr.String(), "--allow-unverified") {
		t.Fatalf("expected allow-unverified guidance, got %s", stderr.String())
	}
}

func writeMarketplaceTestCatalog(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")
	body := `{
  "schemaVersion": 1,
  "id": "team",
  "owner": "Platform",
  "description": "Team plugins",
  "plugins": [
    {
      "id": "zero.demo",
      "name": "Demo",
      "description": "Lookup helper",
      "author": {"name": "Platform"},
      "license": "MIT",
      "tags": ["lookup"],
      "category": "productivity",
      "review": {"status": "community"},
      "releases": [
        {
          "version": "0.1.0",
          "repository": "https://github.com/Gitlawb/zero-demo-plugin.git",
          "commit": "` + strings.Repeat("a", 40) + `",
          "treeHash": "sha256:` + strings.Repeat("b", 64) + `",
          "components": {
            "tools": [{"name": "lookup", "permission": "prompt"}],
            "hooks": [{"name": "preflight", "event": "beforeTool"}]
          }
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
