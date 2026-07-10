package cli

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Gitlawb/zero/internal/marketplace"
	"github.com/Gitlawb/zero/internal/plugins"
	"github.com/Gitlawb/zero/internal/workspacetrust"
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

func TestRegisteredOfficialCatalogUsesEmbeddedPublicKey(t *testing.T) {
	catalogs, err := registeredCatalogs(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalogs) == 0 || catalogs[0].ID != marketplace.OfficialCatalogID {
		t.Fatalf("official catalog missing: %#v", catalogs)
	}
	official := catalogs[0]
	if len(official.PublicKey) != ed25519.PublicKeySize {
		t.Fatalf("official catalog public key length = %d, want %d", len(official.PublicKey), ed25519.PublicKeySize)
	}
	if official.VerificationStatus != marketplace.VerificationSigned {
		t.Fatalf("official catalog verification = %q, want signed", official.VerificationStatus)
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

func TestRunPluginsMarketplaceAddRejectsInvalidSignatureEvenWhenUnverifiedAllowed(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	catalogPath := writeMarketplaceTestCatalog(t)
	if err := os.WriteFile(filepath.Join(filepath.Dir(catalogPath), "catalog.sig"), []byte("not-a-valid-signature"), 0o644); err != nil {
		t.Fatal(err)
	}
	publicKey, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "catalog.pub")
	if err := os.WriteFile(keyPath, []byte(base64.StdEncoding.EncodeToString(publicKey)), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"plugins", "marketplace", "add", catalogPath, "--public-key", keyPath, "--allow-unverified"}, &stdout, &stderr, appDeps{})
	if exitCode != exitUsage {
		t.Fatalf("exitCode = %d stdout=%s stderr=%s", exitCode, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "invalid") {
		t.Fatalf("expected invalid signature error, got %s", stderr.String())
	}
}

func TestRunPluginsMarketplaceListGatesProjectCatalogsBehindTrust(t *testing.T) {
	setTrustConfigRoot(t)
	cwd := t.TempDir()
	catalogPath := writeMarketplaceTestCatalog(t)
	projectPath := filepath.Join(cwd, ".zero", "marketplaces.json")
	if err := marketplace.SaveRegistry(projectPath, marketplace.Registry{Catalogs: []marketplace.RegisteredCatalog{{
		ID:                 "team",
		Source:             catalogPath,
		VerificationStatus: marketplace.VerificationUnsigned,
	}}}); err != nil {
		t.Fatal(err)
	}
	deps := appDeps{getwd: func() (string, error) { return cwd, nil }}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"plugins", "marketplace", "list", "--json"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("list exitCode = %d stderr=%s", exitCode, stderr.String())
	}
	if strings.Contains(stdout.String(), `"id": "team"`) {
		t.Fatalf("untrusted project catalog leaked into list: %s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "zero trust") {
		t.Fatalf("missing trust notice: %s", stderr.String())
	}

	if err := workspacetrust.Trust(cwd); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	exitCode = runWithDeps([]string{"plugins", "marketplace", "list", "--json"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("trusted list exitCode = %d stderr=%s", exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"id": "team"`) {
		t.Fatalf("trusted project catalog missing: %s", stdout.String())
	}
}

func TestRunPluginsMarketplaceRemoteAddUpdateBrowse(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	catalogBytes, err := os.ReadFile(writeMarketplaceTestCatalog(t))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(catalogBytes)
	}))
	defer server.Close()
	cwd := t.TempDir()
	deps := appDeps{getwd: func() (string, error) { return cwd, nil }}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"plugins", "marketplace", "add", server.URL + "/catalog.json", "--allow-unverified", "--json"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("remote add exitCode = %d stderr=%s", exitCode, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runWithDeps([]string{"plugins", "marketplace", "update", "team", "--json"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("remote update exitCode = %d stderr=%s", exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"cached": true`) {
		t.Fatalf("update should report cache write: %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runWithDeps([]string{"plugins", "browse", "lookup", "--catalog", "team", "--json"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("browse cached remote exitCode = %d stderr=%s", exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"id": "zero.demo"`) {
		t.Fatalf("cached browse missing plugin: %s", stdout.String())
	}
}

func TestRunPluginsMarketplaceUpdateHonorsScopeWhenCatalogIDsCollide(t *testing.T) {
	setTrustConfigRoot(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	cwd := t.TempDir()
	if err := workspacetrust.Trust(cwd); err != nil {
		t.Fatal(err)
	}
	userCatalog := writeMarketplaceTestCatalog(t)
	projectCatalog := writeMarketplaceTestCatalog(t)
	userPath, err := marketplace.RegistryPathForScope(marketplace.ScopeUser, cwd, nil)
	if err != nil {
		t.Fatal(err)
	}
	projectPath, err := marketplace.RegistryPathForScope(marketplace.ScopeProject, cwd, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := marketplace.SaveRegistry(userPath, marketplace.Registry{Catalogs: []marketplace.RegisteredCatalog{{
		ID:                 "team",
		Source:             userCatalog,
		VerificationStatus: marketplace.VerificationStale,
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := marketplace.SaveRegistry(projectPath, marketplace.Registry{Catalogs: []marketplace.RegisteredCatalog{{
		ID:                 "team",
		Source:             projectCatalog,
		VerificationStatus: marketplace.VerificationStale,
	}}}); err != nil {
		t.Fatal(err)
	}
	deps := appDeps{getwd: func() (string, error) { return cwd, nil }}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"plugins", "marketplace", "update", "team", "--scope", "project", "--json"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("scoped update exitCode = %d stdout=%s stderr=%s", exitCode, stdout.String(), stderr.String())
	}
	userRegistry, err := marketplace.LoadRegistry(userPath)
	if err != nil {
		t.Fatal(err)
	}
	projectRegistry, err := marketplace.LoadRegistry(projectPath)
	if err != nil {
		t.Fatal(err)
	}
	if userRegistry.Catalogs[0].VerificationStatus != marketplace.VerificationStale {
		t.Fatalf("user catalog was updated despite --scope project: %#v", userRegistry.Catalogs)
	}
	if projectRegistry.Catalogs[0].VerificationStatus != marketplace.VerificationUnsigned {
		t.Fatalf("project catalog was not updated: %#v", projectRegistry.Catalogs)
	}
}

func TestRunPluginsBrowseShowsSelectedLatestRelease(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	catalogPath := writeMarketplaceMultiReleaseCatalog(t)
	cwd := t.TempDir()
	deps := appDeps{getwd: func() (string, error) { return cwd, nil }}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"plugins", "marketplace", "add", catalogPath, "--allow-unverified"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("add exitCode = %d stderr=%s", exitCode, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runWithDeps([]string{"plugins", "browse", "--catalog", "team"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("browse exitCode = %d stderr=%s", exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), "zero.demo@0.2.0") {
		t.Fatalf("browse should show selected latest release, got:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), "zero.demo@0.1.0") {
		t.Fatalf("browse displayed first catalog release instead of selected latest:\n%s", stdout.String())
	}
}

func TestRunPluginsMarketplaceRemoteCatalogRejectsLocalReleaseRepository(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	source, hash := marketplaceTestPluginSource(t, "zero.demo", "0.1.0")
	catalogPath := writeMarketplaceUpdateCatalog(t, map[string]marketplaceTestRelease{
		"zero.demo": {Source: source, Version: "0.1.0", Hash: hash},
	})
	catalogBytes, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(catalogBytes)
	}))
	defer server.Close()
	cwd := t.TempDir()
	deps := appDeps{getwd: func() (string, error) { return cwd, nil }}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"plugins", "marketplace", "add", server.URL + "/catalog.json", "--allow-unverified"}, &stdout, &stderr, deps)
	if exitCode != exitUsage {
		t.Fatalf("remote add exitCode = %d stdout=%s stderr=%s", exitCode, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "absolute git repository source") {
		t.Fatalf("expected remote local repository rejection, got %s", stderr.String())
	}
}

func TestTUIPluginSnapshotDoesNotRefreshMissingRemoteCatalogCache(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	cwd := t.TempDir()
	var serverCalled atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		serverCalled.Store(true)
		http.Error(w, "unexpected refresh", http.StatusInternalServerError)
	}))
	defer server.Close()
	registryPath, err := marketplace.RegistryPathForScope(marketplace.ScopeUser, cwd, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := marketplace.SaveRegistry(registryPath, marketplace.Registry{Catalogs: []marketplace.RegisteredCatalog{{
		ID:                 "team",
		Source:             server.URL + "/catalog.json",
		VerificationStatus: marketplace.VerificationUnsigned,
	}}}); err != nil {
		t.Fatal(err)
	}
	pluginRoot := filepath.Join(t.TempDir(), "plugins")

	snapshot := tuiPluginSnapshot(cwd, appDeps{
		pluginsDir: func() string { return pluginRoot },
		loadPlugins: func(plugins.LoadOptions) (plugins.LoadResult, error) {
			return plugins.LoadResult{}, nil
		},
	})

	if serverCalled.Load() {
		t.Fatal("TUI snapshot should not refresh missing remote catalog cache")
	}
	if len(snapshot.MarketplacePlugins) != 0 {
		t.Fatalf("missing cache should not populate marketplace plugins: %#v", snapshot.MarketplacePlugins)
	}
	foundTeam := false
	for _, catalog := range snapshot.Catalogs {
		if catalog.ID == "team" {
			foundTeam = true
			if !strings.Contains(catalog.LoadError, "no local cache") {
				t.Fatalf("expected no-cache load error, got %q", catalog.LoadError)
			}
		}
	}
	if !foundTeam {
		t.Fatalf("team catalog missing from snapshot: %#v", snapshot.Catalogs)
	}
}

func TestPluginCommandNeedsRestartSkipsPinMetadataOnly(t *testing.T) {
	for _, args := range [][]string{
		{"pin", "zero.demo"},
		{"unpin", "zero.demo"},
	} {
		if pluginCommandNeedsRestart(args) {
			t.Fatalf("pluginCommandNeedsRestart(%v) = true, want false", args)
		}
	}
	for _, args := range [][]string{
		{"install", "zero.demo@team", "--yes"},
		{"disable", "zero.demo"},
		{"update", "zero.demo", "--yes"},
	} {
		if !pluginCommandNeedsRestart(args) {
			t.Fatalf("pluginCommandNeedsRestart(%v) = false, want true", args)
		}
	}
}

func TestRunPluginsMarketplaceUpdateKeepsStaleCacheOnRefreshFailure(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	catalogBytes, err := os.ReadFile(writeMarketplaceTestCatalog(t))
	if err != nil {
		t.Fatal(err)
	}
	failRefresh := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failRefresh {
			http.Error(w, "offline", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(catalogBytes)
	}))
	defer server.Close()
	cwd := t.TempDir()
	deps := appDeps{getwd: func() (string, error) { return cwd, nil }}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"plugins", "marketplace", "add", server.URL + "/catalog.json", "--allow-unverified"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("remote add exitCode = %d stderr=%s", exitCode, stderr.String())
	}
	failRefresh = true

	stdout.Reset()
	stderr.Reset()
	exitCode = runWithDeps([]string{"plugins", "marketplace", "update", "team", "--json"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("stale update exitCode = %d stdout=%s stderr=%s", exitCode, stdout.String(), stderr.String())
	}
	var update struct {
		Verification marketplace.Verification `json:"verification"`
		Cached       bool                     `json:"cached"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &update); err != nil {
		t.Fatalf("update JSON: %v\n%s", err, stdout.String())
	}
	if update.Cached || update.Verification.Status != marketplace.VerificationStale {
		t.Fatalf("expected stale cached fallback, got %#v", update)
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runWithDeps([]string{"plugins", "browse", "lookup", "--catalog", "team", "--json"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("browse stale cache exitCode = %d stderr=%s", exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"status": "stale"`) || !strings.Contains(stdout.String(), `"id": "zero.demo"`) {
		t.Fatalf("browse should use stale cache with stale marker: %s", stdout.String())
	}
}

func TestRunPluginsBrowseRefreshPersistsStaleStatus(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	catalogBytes, err := os.ReadFile(writeMarketplaceTestCatalog(t))
	if err != nil {
		t.Fatal(err)
	}
	failRefresh := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failRefresh {
			http.Error(w, "offline", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(catalogBytes)
	}))
	defer server.Close()
	cwd := t.TempDir()
	deps := appDeps{getwd: func() (string, error) { return cwd, nil }}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"plugins", "marketplace", "add", server.URL + "/catalog.json", "--allow-unverified"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("remote add exitCode = %d stderr=%s", exitCode, stderr.String())
	}
	failRefresh = true

	stdout.Reset()
	stderr.Reset()
	exitCode = runWithDeps([]string{"plugins", "browse", "lookup", "--catalog", "team", "--refresh", "--json"}, &stdout, &stderr, deps)
	if exitCode != exitSuccess {
		t.Fatalf("browse refresh exitCode = %d stdout=%s stderr=%s", exitCode, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `"status": "stale"`) || !strings.Contains(stdout.String(), `"id": "zero.demo"`) {
		t.Fatalf("browse refresh should use stale cache with stale marker: %s", stdout.String())
	}

	registryPath, err := marketplace.RegistryPathForScope(marketplace.ScopeUser, cwd, nil)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := marketplace.LoadRegistry(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.Catalogs) != 1 || registry.Catalogs[0].VerificationStatus != marketplace.VerificationStale {
		t.Fatalf("browse refresh did not persist stale verification: %#v", registry.Catalogs)
	}
}

func TestRunPluginsMarketplaceSignWritesDetachedSignature(t *testing.T) {
	catalogPath := writeMarketplaceTestCatalog(t)
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	privateKeyPath := filepath.Join(t.TempDir(), "catalog.key")
	if err := os.WriteFile(privateKeyPath, []byte(base64.StdEncoding.EncodeToString(privateKey)), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"plugins", "marketplace", "sign", catalogPath, "--private-key", privateKeyPath, "--json"}, &stdout, &stderr, appDeps{})
	if exitCode != exitSuccess {
		t.Fatalf("sign exitCode = %d stderr=%s", exitCode, stderr.String())
	}
	data, signature, err := marketplace.ReadLocalCatalog(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	verification := marketplace.VerifyCatalogSignature(data, signature, publicKey)
	if verification.Status != marketplace.VerificationSigned {
		t.Fatalf("signature did not verify: %#v", verification)
	}
}

func TestRunPluginsMarketplaceUpdateRejectsTamperedSignedLocalCatalog(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	catalogPath := writeMarketplaceTestCatalog(t)
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	privateKeyPath := filepath.Join(t.TempDir(), "catalog.key")
	if err := os.WriteFile(privateKeyPath, []byte(base64.StdEncoding.EncodeToString(privateKey)), 0o600); err != nil {
		t.Fatal(err)
	}
	publicKeyPath := filepath.Join(t.TempDir(), "catalog.pub")
	if err := os.WriteFile(publicKeyPath, []byte(base64.StdEncoding.EncodeToString(publicKey)), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exitCode := runWithDeps([]string{"plugins", "marketplace", "sign", catalogPath, "--private-key", privateKeyPath}, &stdout, &stderr, appDeps{}); exitCode != exitSuccess {
		t.Fatalf("sign exitCode = %d stderr=%s", exitCode, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if exitCode := runWithDeps([]string{"plugins", "marketplace", "add", catalogPath, "--public-key", publicKeyPath}, &stdout, &stderr, appDeps{}); exitCode != exitSuccess {
		t.Fatalf("add exitCode = %d stderr=%s", exitCode, stderr.String())
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(catalogPath), "catalog.sig"), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	exitCode := runWithDeps([]string{"plugins", "marketplace", "update", "team", "--json"}, &stdout, &stderr, appDeps{})
	if exitCode != exitUsage {
		t.Fatalf("tampered update exitCode = %d stdout=%s stderr=%s", exitCode, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "invalid marketplace catalog signature") {
		t.Fatalf("expected invalid signature error, got %s", stderr.String())
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
      "review": {
        "status": "community",
        "date": "2026-07-10",
        "reviewer": "Zero Security",
        "url": "https://github.com/Gitlawb/zero-plugins/pull/1"
      },
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

func writeMarketplaceMultiReleaseCatalog(t *testing.T) string {
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
      "name": "Demo",
      "description": "Lookup helper",
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
          "repository": "https://github.com/Gitlawb/zero-demo-plugin.git",
          "commit": "` + strings.Repeat("a", 40) + `",
          "treeHash": "sha256:` + strings.Repeat("b", 64) + `",
          "components": {"tools": [{"name": "lookup", "permission": "prompt"}]}
        },
        {
          "version": "0.2.0",
          "repository": "https://github.com/Gitlawb/zero-demo-plugin.git",
          "commit": "` + strings.Repeat("c", 40) + `",
          "treeHash": "sha256:` + strings.Repeat("d", 64) + `",
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
