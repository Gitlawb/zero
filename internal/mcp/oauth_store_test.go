package mcp

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestTokenStoreIdentityBoundRoundTrip(t *testing.T) {
	store, err := NewTokenStore(TokenStoreOptions{FilePath: filepath.Join(t.TempDir(), "oauth-tokens.json")})
	if err != nil {
		t.Fatalf("NewTokenStore() error = %v", err)
	}
	server := testOAuthServer("demo", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false)
	otherTarget := testOAuthServer("demo", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", false)
	if err := store.SaveForServer(server, StoredToken{AccessToken: "identity-token"}); err != nil {
		t.Fatalf("SaveForServer() error = %v", err)
	}

	loaded, ok, err := store.LoadForServer(server)
	if err != nil || !ok {
		t.Fatalf("LoadForServer() ok=%v err=%v", ok, err)
	}
	if loaded.AccessToken != "identity-token" {
		t.Fatalf("access token = %q", loaded.AccessToken)
	}
	if _, ok, err := store.LoadForServer(otherTarget); err != nil || ok {
		t.Fatalf("LoadForServer(other target) ok=%v err=%v, want no token", ok, err)
	}
}

func TestStoreTokenSourceRequiresMatchingIdentity(t *testing.T) {
	store, err := NewTokenStore(TokenStoreOptions{FilePath: filepath.Join(t.TempDir(), "oauth-tokens.json")})
	if err != nil {
		t.Fatalf("NewTokenStore() error = %v", err)
	}
	server := testOAuthServer("demo", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false)
	otherTarget := testOAuthServer("demo", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", false)
	if err := store.SaveForServer(server, StoredToken{AccessToken: "access-a"}); err != nil {
		t.Fatalf("SaveForServer() error = %v", err)
	}

	source := &storeTokenSource{server: server, store: store}
	token, err := source.AccessToken(context.Background())
	if err != nil {
		t.Fatalf("AccessToken() error = %v", err)
	}
	if token != "access-a" {
		t.Fatalf("token = %q, want access-a", token)
	}

	source.server = otherTarget
	if _, err := source.AccessToken(context.Background()); err == nil {
		t.Fatal("AccessToken() error = nil for mismatched identity")
	}
}

func TestTokenStoreProjectConfiguredDoesNotLoadLegacyToken(t *testing.T) {
	store, err := NewTokenStore(TokenStoreOptions{FilePath: filepath.Join(t.TempDir(), "oauth-tokens.json")})
	if err != nil {
		t.Fatalf("NewTokenStore() error = %v", err)
	}
	if err := store.Save("demo", StoredToken{AccessToken: "legacy-token"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	server := testOAuthServer("demo", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", true)

	_, ok, err := store.LoadForServer(server)
	if err != nil {
		t.Fatalf("LoadForServer() error = %v", err)
	}
	if ok {
		t.Fatal("LoadForServer() ok = true for project-configured legacy token")
	}
}

func TestTokenStoreMigratesLegacyForUserOnlyServer(t *testing.T) {
	store, err := NewTokenStore(TokenStoreOptions{FilePath: filepath.Join(t.TempDir(), "oauth-tokens.json")})
	if err != nil {
		t.Fatalf("NewTokenStore() error = %v", err)
	}
	if err := store.Save("demo", StoredToken{AccessToken: "legacy-token"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	server := testOAuthServer("demo", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false)

	loaded, ok, err := store.LoadForServer(server)
	if err != nil || !ok {
		t.Fatalf("LoadForServer() ok=%v err=%v", ok, err)
	}
	if loaded.AccessToken != "legacy-token" {
		t.Fatalf("access token = %q", loaded.AccessToken)
	}
	if _, err := store.Delete("demo"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	loaded, ok, err = store.LoadForServer(server)
	if err != nil || !ok || loaded.AccessToken != "legacy-token" {
		t.Fatalf("identity-bound migrated token missing: loaded=%#v ok=%v err=%v", loaded, ok, err)
	}
}

func TestTokenStoreRejectsLongIdentityBoundServerName(t *testing.T) {
	store, err := NewTokenStore(TokenStoreOptions{FilePath: filepath.Join(t.TempDir(), "oauth-tokens.json")})
	if err != nil {
		t.Fatalf("NewTokenStore() error = %v", err)
	}
	name := strings.Repeat("a", maxMCPServerNameForIdentityToken+1)
	err = store.SaveForServer(testOAuthServer(name, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false), StoredToken{AccessToken: "x"})
	if err == nil {
		t.Fatal("SaveForServer() error = nil for long server name")
	}
	if !strings.Contains(err.Error(), `MCP OAuth server name "`+name+`" is too long for identity-bound token storage`) {
		t.Fatalf("error = %q, want long-name message", err.Error())
	}
}

func TestTokenStoreRejectsAmbiguousIdentitySuffixServerName(t *testing.T) {
	store, err := NewTokenStore(TokenStoreOptions{FilePath: filepath.Join(t.TempDir(), "oauth-tokens.json")})
	if err != nil {
		t.Fatalf("NewTokenStore() error = %v", err)
	}
	name := "demo.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	if err := store.SaveForServer(testOAuthServer(name, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", false), StoredToken{AccessToken: "x"}); err == nil {
		t.Fatal("SaveForServer() error = nil for ambiguous server name")
	} else if !strings.Contains(err.Error(), `MCP OAuth server name "`+name+`" cannot end with .<32 hex chars>`) {
		t.Fatalf("SaveForServer() error = %q, want ambiguous-name rejection", err.Error())
	}
	if err := store.Save(name, StoredToken{AccessToken: "legacy"}); err == nil {
		t.Fatal("Save() error = nil for ambiguous legacy server name")
	}
}

func TestTokenStoreDeleteForServerNameRemovesLegacyAndIdentityTokens(t *testing.T) {
	store, err := NewTokenStore(TokenStoreOptions{FilePath: filepath.Join(t.TempDir(), "oauth-tokens.json")})
	if err != nil {
		t.Fatalf("NewTokenStore() error = %v", err)
	}
	if err := store.Save("demo", StoredToken{AccessToken: "legacy"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveForServer(testOAuthServer("demo", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false), StoredToken{AccessToken: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveForServer(testOAuthServer("demo", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", false), StoredToken{AccessToken: "b"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveForServer(testOAuthServer("other", "cccccccccccccccccccccccccccccccc", false), StoredToken{AccessToken: "c"}); err != nil {
		t.Fatal(err)
	}

	removed, err := store.DeleteForServerName("demo")
	if err != nil {
		t.Fatalf("DeleteForServerName() error = %v", err)
	}
	if !removed {
		t.Fatal("DeleteForServerName() removed = false")
	}
	statuses, err := store.Status()
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if len(statuses) != 1 || statuses[0].ServerName != "other" {
		t.Fatalf("statuses after delete = %#v, want only other", statuses)
	}
}

func TestTokenStoreStatusParsesIdentityAndRedacts(t *testing.T) {
	store, err := NewTokenStore(TokenStoreOptions{FilePath: filepath.Join(t.TempDir(), "oauth-tokens.json")})
	if err != nil {
		t.Fatalf("NewTokenStore() error = %v", err)
	}
	identity := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := store.SaveForServer(testOAuthServer("demo", identity, false), StoredToken{AccessToken: "secret-access", RefreshToken: "secret-refresh"}); err != nil {
		t.Fatalf("SaveForServer() error = %v", err)
	}
	statuses, err := store.Status()
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if len(statuses) != 1 || statuses[0].ServerName != "demo" || statuses[0].ServerIdentity != identity {
		t.Fatalf("statuses = %#v, want parsed identity", statuses)
	}
	output := FormatTokenStatuses(statuses)
	if !strings.Contains(output, "demo ["+identity+"]") {
		t.Fatalf("status output = %q, want identity label", output)
	}
	if contains(output, "secret-access") || contains(output, "secret-refresh") {
		t.Fatalf("status output leaked token: %s", output)
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

func testOAuthServer(name string, identity string, projectConfigured bool) Server {
	return Server{
		Name:              name,
		Type:              ServerTypeHTTP,
		URL:               "https://example.com/mcp",
		Auth:              ServerAuthOAuth,
		Identity:          identity,
		ProjectConfigured: projectConfigured,
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
