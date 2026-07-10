package marketplace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRegistryAddRejectsReservedAndDuplicateCatalogIDs(t *testing.T) {
	registry := Registry{}

	if err := registry.Add(RegisteredCatalog{ID: OfficialCatalogID, Source: "https://example.com/catalog.json"}); err == nil {
		t.Fatalf("expected official catalog id collision to be rejected")
	}

	if err := registry.Add(RegisteredCatalog{ID: "team", Source: "./catalog.json"}); err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
	if err := registry.Add(RegisteredCatalog{ID: "team", Source: "./other.json"}); err == nil {
		t.Fatalf("expected duplicate catalog id rejection")
	}
}

func TestRegistryLoadSaveRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marketplaces.json")
	registry := Registry{}
	if err := registry.Add(RegisteredCatalog{
		ID:                 "team",
		Source:             "./catalog.json",
		PublicKeyPath:      "./catalog.pub",
		VerificationStatus: VerificationUnsigned,
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := SaveRegistry(path, registry); err != nil {
		t.Fatalf("SaveRegistry: %v", err)
	}
	loaded, err := LoadRegistry(path)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	if len(loaded.Catalogs) != 1 || loaded.Catalogs[0].ID != "team" || loaded.Catalogs[0].Source != "./catalog.json" {
		t.Fatalf("unexpected registry: %#v", loaded)
	}
	if loaded.Catalogs[0].PublicKeyPath != "./catalog.pub" {
		t.Fatalf("publicKeyPath = %q, want ./catalog.pub", loaded.Catalogs[0].PublicKeyPath)
	}
	if loaded.Catalogs[0].VerificationStatus != VerificationUnsigned {
		t.Fatalf("verificationStatus = %q, want %q", loaded.Catalogs[0].VerificationStatus, VerificationUnsigned)
	}
}

func TestRegistryPathForScope(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	cwd := filepath.Join(t.TempDir(), "repo")
	env := map[string]string{"XDG_CONFIG_HOME": filepath.Join(home, "config")}

	userPath, err := RegistryPathForScope(ScopeUser, cwd, env)
	if err != nil {
		t.Fatalf("user path: %v", err)
	}
	if !strings.HasSuffix(userPath, filepath.Join("config", "zero", "marketplaces.json")) {
		t.Fatalf("unexpected user path: %s", userPath)
	}

	projectPath, err := RegistryPathForScope(ScopeProject, cwd, env)
	if err != nil {
		t.Fatalf("project path: %v", err)
	}
	if projectPath != filepath.Join(cwd, ".zero", "marketplaces.json") {
		t.Fatalf("project path = %s", projectPath)
	}
}

func TestReadLocalCatalogReadsCatalogAndOptionalSignature(t *testing.T) {
	dir := t.TempDir()
	catalogPath := filepath.Join(dir, "catalog.json")
	sigPath := filepath.Join(dir, "catalog.sig")
	if err := os.WriteFile(catalogPath, []byte(testCatalogJSON()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sigPath, []byte("signature"), 0o644); err != nil {
		t.Fatal(err)
	}

	data, sig, err := ReadLocalCatalog(catalogPath)
	if err != nil {
		t.Fatalf("ReadLocalCatalog: %v", err)
	}
	if !strings.Contains(string(data), `"id": "official"`) || string(sig) != "signature" {
		t.Fatalf("unexpected data/signature: %q %q", data, sig)
	}
}

func TestCatalogGitFetchContextHasDeadline(t *testing.T) {
	ctx, cancel := catalogGitFetchContext(context.Background())
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected catalog git fetch context to have a deadline")
	}
	if time.Until(deadline) <= 0 {
		t.Fatalf("expected future catalog git fetch deadline, got %s", deadline)
	}
}
