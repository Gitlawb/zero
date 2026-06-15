package mcp

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestTokenStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp-oauth-tokens.json")
	store, err := NewTokenStore(TokenStoreOptions{FilePath: path})
	if err != nil {
		t.Fatalf("NewTokenStore() error = %v", err)
	}

	expiry := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	saved := StoredToken{
		AccessToken:  "access-abc",
		RefreshToken: "refresh-abc",
		TokenType:    "Bearer",
		Scopes:       []string{"read", "write"},
		ExpiresAt:    expiry,
	}
	if err := store.Save("demo", saved); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, ok, err := store.Load("demo")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !ok {
		t.Fatal("Load() ok = false, want stored token")
	}
	if loaded.AccessToken != saved.AccessToken || loaded.RefreshToken != saved.RefreshToken {
		t.Fatalf("loaded = %#v", loaded)
	}
	if !loaded.ExpiresAt.Equal(expiry) {
		t.Fatalf("expiry = %v, want %v", loaded.ExpiresAt, expiry)
	}
}

func TestTokenStoreFileIs0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file permissions are not enforced on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp-oauth-tokens.json")
	store, err := NewTokenStore(TokenStoreOptions{FilePath: path})
	if err != nil {
		t.Fatalf("NewTokenStore() error = %v", err)
	}
	if err := store.Save("demo", StoredToken{AccessToken: "a", RefreshToken: "r"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat token file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("file mode = %o, want 600", perm)
	}
}

func TestTokenStoreDelete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp-oauth-tokens.json")
	store, err := NewTokenStore(TokenStoreOptions{FilePath: path})
	if err != nil {
		t.Fatalf("NewTokenStore() error = %v", err)
	}
	if err := store.Save("demo", StoredToken{AccessToken: "a", RefreshToken: "r"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	removed, err := store.Delete("demo")
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if !removed {
		t.Fatal("Delete() removed = false, want true")
	}
	_, ok, err := store.Load("demo")
	if err != nil {
		t.Fatalf("Load() after delete error = %v", err)
	}
	if ok {
		t.Fatal("Load() ok = true after delete")
	}

	// Deleting a missing entry reports false without error.
	removed, err = store.Delete("missing")
	if err != nil {
		t.Fatalf("Delete(missing) error = %v", err)
	}
	if removed {
		t.Fatal("Delete(missing) removed = true, want false")
	}
}

func TestTokenStoreLoadMissingIsNotError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp-oauth-tokens.json")
	store, err := NewTokenStore(TokenStoreOptions{FilePath: path})
	if err != nil {
		t.Fatalf("NewTokenStore() error = %v", err)
	}
	_, ok, err := store.Load("demo")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if ok {
		t.Fatal("Load() ok = true for empty store")
	}
}

func TestTokenStoreStatusReportsPresenceWithoutToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp-oauth-tokens.json")
	store, err := NewTokenStore(TokenStoreOptions{FilePath: path})
	if err != nil {
		t.Fatalf("NewTokenStore() error = %v", err)
	}
	expiry := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	if err := store.Save("demo", StoredToken{
		AccessToken:  "secret-access",
		RefreshToken: "secret-refresh",
		ExpiresAt:    expiry,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	statuses, err := store.Status()
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("statuses = %#v, want one entry", statuses)
	}
	status := statuses[0]
	if status.ServerName != "demo" {
		t.Fatalf("server name = %q", status.ServerName)
	}
	if !status.HasToken {
		t.Fatal("HasToken = false, want true")
	}
	if !status.HasRefreshToken {
		t.Fatal("HasRefreshToken = false, want true")
	}
	if !status.ExpiresAt.Equal(expiry) {
		t.Fatalf("expiry = %v, want %v", status.ExpiresAt, expiry)
	}
	// The status struct must not carry the secret material at all.
	if got := FormatTokenStatuses(statuses); contains(got, "secret-access") || contains(got, "secret-refresh") {
		t.Fatalf("status output leaked token: %s", got)
	}
}

func TestTokenStoreMigratesLegacyFile(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "mcp-oauth-tokens.json")
	unified := filepath.Join(dir, "oauth-tokens.json")
	legacyData := `{"schemaVersion":1,"tokens":{"demo":{"access_token":"a","refresh_token":"r","token_type":"Bearer"}}}`
	if err := os.WriteFile(legacy, []byte(legacyData), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := NewTokenStore(TokenStoreOptions{FilePath: unified, LegacyPath: legacy})
	if err != nil {
		t.Fatalf("NewTokenStore: %v", err)
	}

	tok, ok, err := store.Load("demo")
	if err != nil || !ok {
		t.Fatalf("migrated token not loadable: ok=%v err=%v", ok, err)
	}
	if tok.AccessToken != "a" || tok.RefreshToken != "r" {
		t.Fatalf("migrated token = %#v", tok)
	}

	// The unified file keys the token under the mcp: namespace.
	raw, err := os.ReadFile(unified)
	if err != nil {
		t.Fatalf("read unified: %v", err)
	}
	if !contains(string(raw), "mcp:demo") {
		t.Fatalf("unified file should key under mcp: namespace:\n%s", raw)
	}

	// The legacy file is renamed to a .migrated backup (non-destructive, one-time).
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy file should be renamed away; stat err = %v", err)
	}
	if _, err := os.Stat(legacy + ".migrated"); err != nil {
		t.Fatalf("legacy backup missing: %v", err)
	}

	// Idempotent: a second construction (legacy now absent) keeps the token.
	store2, err := NewTokenStore(TokenStoreOptions{FilePath: unified, LegacyPath: legacy})
	if err != nil {
		t.Fatalf("NewTokenStore#2: %v", err)
	}
	if _, ok, _ := store2.Load("demo"); !ok {
		t.Fatal("token lost after second construction")
	}
}

func TestTokenStoreMigrationPreservesNewerUnified(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "mcp-oauth-tokens.json")
	unified := filepath.Join(dir, "oauth-tokens.json")
	if err := os.WriteFile(legacy, []byte(`{"schemaVersion":1,"tokens":{"demo":{"access_token":"OLD"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// Pre-seed the unified store with a newer token (no migration: FilePath set, no LegacyPath).
	pre, err := NewTokenStore(TokenStoreOptions{FilePath: unified})
	if err != nil {
		t.Fatal(err)
	}
	if err := pre.Save("demo", StoredToken{AccessToken: "NEW"}); err != nil {
		t.Fatal(err)
	}
	// Migrating must not overwrite the newer unified entry.
	store, err := NewTokenStore(TokenStoreOptions{FilePath: unified, LegacyPath: legacy})
	if err != nil {
		t.Fatal(err)
	}
	tok, _, _ := store.Load("demo")
	if tok.AccessToken != "NEW" {
		t.Fatalf("migration overwrote a newer token: %q", tok.AccessToken)
	}
}

func TestTokenStoreNamespacedFromProvider(t *testing.T) {
	// An MCP token and a provider login of the same name coexist in one file.
	dir := t.TempDir()
	unified := filepath.Join(dir, "oauth-tokens.json")
	mcpStore, err := NewTokenStore(TokenStoreOptions{FilePath: unified})
	if err != nil {
		t.Fatal(err)
	}
	if err := mcpStore.Save("shared", StoredToken{AccessToken: "mcp-token"}); err != nil {
		t.Fatal(err)
	}
	statuses, err := mcpStore.Status()
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 || statuses[0].ServerName != "shared" {
		t.Fatalf("status = %#v, want one entry for 'shared'", statuses)
	}
}

func TestResolveTokenStorePathUsesXDG(t *testing.T) {
	// Use a real temp dir so the base is absolute on every OS (a literal
	// "/tmp/..." isn't absolute on Windows, where ResolveTokenStorePath would
	// then prepend the drive letter and diverge from a hard-coded want).
	configHome := t.TempDir()
	path, err := ResolveTokenStorePath(map[string]string{"XDG_CONFIG_HOME": configHome})
	if err != nil {
		t.Fatalf("ResolveTokenStorePath() error = %v", err)
	}
	want := filepath.Join(configHome, "zero", "mcp-oauth-tokens.json")
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
