package oauth

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "oauth-tokens.json")
	s, err := NewStore(StoreOptions{FilePath: path})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s, path
}

func TestStoreSaveLoadDelete(t *testing.T) {
	s, _ := newTestStore(t)
	tok := Token{AccessToken: "at", RefreshToken: "rt", Account: "me@x"}
	if err := s.Save(ProviderKey("demo"), tok); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Save(KeyPrefixMCP+"server1", Token{AccessToken: "mcp-at"}); err != nil {
		t.Fatalf("Save mcp: %v", err)
	}
	got, ok, err := s.Load(ProviderKey("demo"))
	if err != nil || !ok || got.AccessToken != "at" || got.Account != "me@x" {
		t.Fatalf("Load = %+v ok=%v err=%v", got, ok, err)
	}
	removed, err := s.Delete(ProviderKey("demo"))
	if err != nil || !removed {
		t.Fatalf("Delete = %v %v", removed, err)
	}
	if _, ok, _ := s.Load(ProviderKey("demo")); ok {
		t.Fatal("token should be gone after delete")
	}
	// The mcp-namespaced token is untouched.
	if _, ok, _ := s.Load(KeyPrefixMCP + "server1"); !ok {
		t.Fatal("mcp token should survive provider delete")
	}
}

func TestStoreRejectsInvalidKeys(t *testing.T) {
	s, _ := newTestStore(t)
	for _, bad := range []string{"demo", "provider:", "provider:../escape", "mcp:bad/key", "other:x", ""} {
		if err := s.Save(bad, Token{AccessToken: "x"}); err == nil {
			t.Errorf("Save(%q) should be rejected", bad)
		}
	}
	for _, ok := range []string{"provider:demo", "mcp:server-1", "provider:two-svc"} {
		if err := ValidateKey(ok); err != nil {
			t.Errorf("ValidateKey(%q) = %v, want nil", ok, err)
		}
	}
}

func TestStoreStatusFiltersByPrefix(t *testing.T) {
	s, _ := newTestStore(t)
	_ = s.Save(ProviderKey("demo"), Token{AccessToken: "a", RefreshToken: "r", ExpiresAt: time.Now().Add(time.Hour)})
	_ = s.Save(KeyPrefixMCP+"srv", Token{AccessToken: "m"})
	statuses, err := s.Status(KeyPrefixProvider)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(statuses) != 1 || statuses[0].Key != ProviderKey("demo") {
		t.Fatalf("provider status = %+v", statuses)
	}
	if !statuses[0].HasRefreshToken {
		t.Fatal("status should report a refresh token")
	}
}

func TestStoreFileMode0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix file modes")
	}
	s, path := newTestStore(t)
	if err := s.Save(ProviderKey("x"), Token{AccessToken: "a"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("token file mode = %o, want 600", perm)
	}
}

func TestStoreUsesFixedProtectedPublicationSibling(t *testing.T) {
	s, path := newTestStore(t)
	tempPath := path + ".tmp"
	if err := os.WriteFile(tempPath, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ProviderKey("x"), Token{AccessToken: "secret"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(tempPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("publication sibling remains after save: %v", err)
	}
}

func TestEncryptedStoreUsesFixedProtectedSecretSibling(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	secretTempPath := path + ".secret.tmp"
	if err := os.WriteFile(secretTempPath, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(StoreOptions{FilePath: path, Storage: "encrypted-file"})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s.Save(ProviderKey("x"), Token{AccessToken: "secret"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(secretTempPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("secret publication sibling remains after save: %v", err)
	}
}

func TestStoreMalformedFailsClosed(t *testing.T) {
	s, path := newTestStore(t)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("corrupt: %v", err)
	}
	if _, _, err := s.Load(ProviderKey("x")); err == nil {
		t.Fatal("malformed store must fail closed")
	}
}

func TestEnvValueHermetic(t *testing.T) {
	t.Setenv("ZERO_OAUTH_DEMO_CLIENT_ID", "ambient")
	// A non-nil map omitting the key must NOT leak the ambient process value.
	if got := envValue(map[string]string{}, "ZERO_OAUTH_DEMO_CLIENT_ID"); got != "" {
		t.Fatalf("non-nil env map must be hermetic, got %q", got)
	}
	// A nil map reads the process environment.
	if got := envValue(nil, "ZERO_OAUTH_DEMO_CLIENT_ID"); got != "ambient" {
		t.Fatalf("nil env map should read os env, got %q", got)
	}
}

func TestResolveStorePathHonorsOverride(t *testing.T) {
	// Use an OS-appropriate absolute path: a unix-style "/tmp/..." literal is not
	// absolute on Windows (no drive), so it would be resolved against the current
	// drive and a verbatim comparison would fail there.
	override := filepath.Join(t.TempDir(), "custom", "tok.json")
	path, err := ResolveStorePath(map[string]string{"ZERO_OAUTH_TOKENS_PATH": override})
	if err != nil {
		t.Fatalf("ResolveStorePath: %v", err)
	}
	if path != override {
		t.Fatalf("path = %q, want %q", path, override)
	}
}

// TestProviderKeyNormalizesCase: every provider-login write and lookup funnels
// through ProviderKey, so `zero auth login xAI` and the scaffolded profile's
// candidate "xai" must resolve to the SAME store entry — the raw-typed casing
// previously produced an invisible login.
func TestProviderKeyNormalizesCase(t *testing.T) {
	if got := ProviderKey(" xAI "); got != "provider:xai" {
		t.Fatalf("ProviderKey must trim and lower-case, got %q", got)
	}
	store, _ := newTestStore(t)
	if err := store.Save(ProviderKey("xAI"), Token{AccessToken: "tok"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, key, ok := FirstStored(store, []string{"xai"}); !ok || key != "provider:xai" {
		t.Fatalf("candidate \"xai\" must find the mixed-case login, ok=%v key=%q", ok, key)
	}
}
