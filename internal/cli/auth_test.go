package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/oauth"
	"github.com/Gitlawb/zero/internal/provideroauth"
)

// withAuthStore points the provider OAuth store at a temp file for the test,
// pinning the file backend so an inherited ZERO_OAUTH_STORAGE=keyring can't
// ignore the temp path and hit the OS keychain.
func withAuthStore(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "oauth-tokens.json")
	t.Setenv("ZERO_OAUTH_TOKENS_PATH", path)
	t.Setenv("ZERO_OAUTH_STORAGE", "file")
	return path
}

func TestRunAuthRejectsInvalidStorageMode(t *testing.T) {
	withAuthStore(t)
	// A mistyped value must fail fast, not silently fall back to plaintext while
	// the user believes encryption is active.
	t.Setenv("ZERO_OAUTH_STORAGE", "encryptd")
	var stdout, stderr bytes.Buffer
	if code := runWithDeps([]string{"auth", "status"}, &stdout, &stderr, appDeps{}); code == exitSuccess {
		t.Fatalf("invalid ZERO_OAUTH_STORAGE should fail, got success; stdout=%q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "ZERO_OAUTH_STORAGE") {
		t.Fatalf("error should name the offending env var, stderr=%q", stderr.String())
	}
}

func TestRunAuthStatusEmpty(t *testing.T) {
	withAuthStore(t)
	var stdout, stderr bytes.Buffer
	if code := runWithDeps([]string{"auth", "status"}, &stdout, &stderr, appDeps{}); code != exitSuccess {
		t.Fatalf("exit = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "No OAuth provider logins are stored.") {
		t.Fatalf("status output = %q", stdout.String())
	}
}

func TestRunAuthStatusReportsLoginWithoutSecret(t *testing.T) {
	path := withAuthStore(t)
	store, err := oauth.NewStore(oauth.StoreOptions{FilePath: path})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.Save(oauth.ProviderKey("demo"), oauth.Token{
		AccessToken: "super-secret", RefreshToken: "super-secret-rt", Account: "me@example.com",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := runWithDeps([]string{"auth", "status"}, &stdout, &stderr, appDeps{}); code != exitSuccess {
		t.Fatalf("exit = %d stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "demo") || !strings.Contains(out, "me@example.com") {
		t.Fatalf("status should show provider + account: %q", out)
	}
	if strings.Contains(out, "super-secret") {
		t.Fatalf("status leaked token material: %q", out)
	}
}

func TestRunAuthStatusResolvesCatalogLoginCandidates(t *testing.T) {
	storePath := withAuthStore(t)
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"providers":[{"name":"my-xai","catalogId":"xai"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := oauth.NewStore(oauth.StoreOptions{FilePath: storePath})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(oauth.ProviderKey("xai"), oauth.Token{AccessToken: "secret", Account: "catalog-account"}); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := runWithDeps([]string{"auth", "status", "my-xai"}, &stdout, &stderr, appDeps{
		userConfigPath: func() (string, error) { return configPath, nil },
	}); code != exitSuccess {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "catalog-account") || !strings.Contains(stdout.String(), "xai") {
		t.Fatalf("status did not find the catalog-addressed login: %q", stdout.String())
	}
}

func TestRunAuthLogoutNothing(t *testing.T) {
	withAuthStore(t)
	var stdout, stderr bytes.Buffer
	if code := runWithDeps([]string{"auth", "logout", "demo"}, &stdout, &stderr, appDeps{}); code != exitSuccess {
		t.Fatalf("exit = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "No stored credential for demo") {
		t.Fatalf("logout output = %q", stdout.String())
	}
}

func TestRunAuthLoginValidation(t *testing.T) {
	withAuthStore(t)
	var stdout, stderr bytes.Buffer
	// Missing provider.
	if code := runWithDeps([]string{"auth", "login"}, &stdout, &stderr, appDeps{}); code == exitSuccess {
		t.Fatal("login with no provider should fail")
	}
	// --json is rejected for the interactive login.
	stdout.Reset()
	stderr.Reset()
	if code := runWithDeps([]string{"auth", "login", "demo", "--json"}, &stdout, &stderr, appDeps{}); code == exitSuccess {
		t.Fatal("login --json should be rejected")
	}
}

func TestRunAuthLoginUnknownProvider(t *testing.T) {
	withAuthStore(t)
	var stdout, stderr bytes.Buffer
	if code := runWithDeps([]string{"auth", "login", "does-not-exist"}, &stdout, &stderr, appDeps{}); code == exitSuccess {
		t.Fatal("unknown provider login should fail")
	}
	if !strings.Contains(stderr.String(), "not configured") {
		t.Fatalf("stderr = %q, want not-configured error", stderr.String())
	}
}

func TestRunAuthLoginRevalidatesConfigImmediatelyBeforeSave(t *testing.T) {
	storePath := withAuthStore(t)
	configPath := filepath.Join(t.TempDir(), "config.json")
	initial := `{"providers":[{"name":"demo"}]}`
	ambiguous := `{"providers":[{"name":"demo"},{"name":"DEMO"}]}`
	if err := os.WriteFile(configPath, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := oauth.NewStore(oauth.StoreOptions{FilePath: storePath})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(oauth.ProviderKey("demo"), oauth.Token{AccessToken: "unchanged"}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	mux.HandleFunc("/device", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"device_code":"dc","user_code":"code","verification_uri":"https://example.test","expires_in":60,"interval":1}`)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		if err := os.WriteFile(configPath, []byte(ambiguous), 0o600); err != nil {
			t.Errorf("mutate config: %v", err)
		}
		_, _ = io.WriteString(w, `{"access_token":"replacement","token_type":"Bearer"}`)
	})
	t.Setenv("ZERO_OAUTH_DEMO_CLIENT_ID", "client")
	t.Setenv("ZERO_OAUTH_DEMO_TOKEN_URL", server.URL+"/token")
	t.Setenv("ZERO_OAUTH_DEMO_DEVICE_URL", server.URL+"/device")
	var stdout, stderr bytes.Buffer
	code := runWithDeps([]string{"auth", "login", "demo", "--device"}, &stdout, &stderr, appDeps{
		userConfigPath: func() (string, error) { return configPath, nil },
	})
	if code == exitSuccess || !strings.Contains(stderr.String(), "ambiguous persisted provider names") {
		t.Fatalf("exit = %d, stderr = %q; want ambiguous-config failure", code, stderr.String())
	}
	token, ok, err := store.Load(oauth.ProviderKey("demo"))
	if err != nil || !ok || token.AccessToken != "unchanged" {
		t.Fatalf("stored token = %+v, %v, %v; want unchanged", token, ok, err)
	}
}

func TestRunAuthChatGPTRevalidatesConfigImmediatelyBeforeSave(t *testing.T) {
	storePath := withAuthStore(t)
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"providers":[{"name":"chatgpt","catalogId":"chatgpt"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := oauth.NewStore(oauth.StoreOptions{FilePath: storePath})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(oauth.ProviderKey("chatgpt"), oauth.Token{AccessToken: "unchanged"}); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := runWithDeps([]string{"auth", "chatgpt"}, &stdout, &stderr, appDeps{
		userConfigPath: func() (string, error) { return configPath, nil },
		chatGPTLogin: func(context.Context, provideroauth.ChatGPTOptions) (oauth.Token, error) {
			ambiguous := `{"providers":[{"name":"chatgpt"},{"name":"ChatGPT"}]}`
			if err := os.WriteFile(configPath, []byte(ambiguous), 0o600); err != nil {
				return oauth.Token{}, err
			}
			return oauth.Token{AccessToken: "replacement"}, nil
		},
	})
	if code == exitSuccess || !strings.Contains(stderr.String(), "ambiguous persisted provider names") {
		t.Fatalf("exit = %d, stderr = %q; want ambiguous-config failure", code, stderr.String())
	}
	token, ok, err := store.Load(oauth.ProviderKey("chatgpt"))
	if err != nil || !ok || token.AccessToken != "unchanged" {
		t.Fatalf("stored token = %+v, %v, %v; want unchanged", token, ok, err)
	}
}

// TestRunAuthChatGPTAllowsCaseVariantPersistedProfile is the regression test for
// jatmn's #725 finding: preflighting a login as if it were a new provider write
// rejected the very row it was logging into. A config whose sole ChatGPT profile
// is spelled "ChatGPT" made `zero auth chatgpt` fail before the browser flow with
// `provider "chatgpt" already exists as "ChatGPT"` — while the TUI, which only
// validates the file, completed the same login. A login mints no new spelling,
// but reuse still requires the row's catalogId to prove catalog ownership.
func TestRunAuthChatGPTAllowsCaseVariantPersistedProfile(t *testing.T) {
	storePath := withAuthStore(t)
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"providers":[{"name":"ChatGPT","catalogId":"chatgpt"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := oauth.NewStore(oauth.StoreOptions{FilePath: storePath})
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runWithDeps([]string{"auth", "chatgpt"}, &stdout, &stderr, appDeps{
		userConfigPath: func() (string, error) { return configPath, nil },
		chatGPTLogin: func(context.Context, provideroauth.ChatGPTOptions) (oauth.Token, error) {
			return oauth.Token{AccessToken: "fresh"}, nil
		},
	})
	if code != exitSuccess {
		t.Fatalf("exit = %d, want success; stderr = %q", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "already exists as") {
		t.Fatalf("a case-variant re-login must not be treated as a colliding new provider: %q", stderr.String())
	}
	token, ok, err := store.Load(oauth.ProviderKey("chatgpt"))
	if err != nil || !ok || token.AccessToken != "fresh" {
		t.Fatalf("stored token = %+v, %v, %v; want the fresh login saved", token, ok, err)
	}
	// The ambiguous-config guard is unchanged: a login still refuses to run
	// against a file with two case-duplicate rows.
	if err := os.WriteFile(configPath, []byte(`{"providers":[{"name":"chatgpt"},{"name":"ChatGPT"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	code = runWithDeps([]string{"auth", "chatgpt"}, &stdout, &stderr, appDeps{
		userConfigPath: func() (string, error) { return configPath, nil },
		chatGPTLogin: func(context.Context, provideroauth.ChatGPTOptions) (oauth.Token, error) {
			return oauth.Token{AccessToken: "should-not-save"}, nil
		},
	})
	if code == exitSuccess || !strings.Contains(stderr.String(), "ambiguous persisted provider names") {
		t.Fatalf("exit = %d, stderr = %q; want the ambiguous-config failure preserved", code, stderr.String())
	}
}

func TestRunAuthRefreshNoToken(t *testing.T) {
	withAuthStore(t)
	t.Setenv("ZERO_OAUTH_DEMO_CLIENT_ID", "client") // so config resolves; refresh still fails (no token)
	var stdout, stderr bytes.Buffer
	if code := runWithDeps([]string{"auth", "refresh", "demo"}, &stdout, &stderr, appDeps{}); code == exitSuccess {
		t.Fatal("refresh with no stored token should fail")
	}
}

func TestRunAuthRefreshResolvesCatalogLoginCandidates(t *testing.T) {
	storePath := withAuthStore(t)
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"providers":[{"name":"my-xai","catalogId":"xai"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := oauth.NewStore(oauth.StoreOptions{FilePath: storePath})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(oauth.ProviderKey("xai"), oauth.Token{
		AccessToken: "old", RefreshToken: "refresh", ExpiresAt: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"access_token":"fresh","token_type":"Bearer","expires_in":3600}`)
	}))
	t.Cleanup(server.Close)
	t.Setenv("ZERO_OAUTH_XAI_TOKEN_URL", server.URL)

	var stdout, stderr bytes.Buffer
	if code := runWithDeps([]string{"auth", "refresh", "my-xai"}, &stdout, &stderr, appDeps{
		userConfigPath: func() (string, error) { return configPath, nil },
	}); code != exitSuccess {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	token, ok, err := store.Load(oauth.ProviderKey("xai"))
	if err != nil || !ok || token.AccessToken != "fresh" {
		t.Fatalf("refreshed token = %+v, ok = %v, err = %v", token, ok, err)
	}
}

func TestAuthReadCommandsRejectAmbiguousCatalogAddress(t *testing.T) {
	withAuthStore(t)
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"providers":[{"name":"work-xai","catalogId":"xai"},{"name":"personal-xai","catalogId":"xai"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"auth", "status", "xai"}, {"auth", "refresh", "xai"}} {
		var stdout, stderr bytes.Buffer
		if code := runWithDeps(args, &stdout, &stderr, appDeps{
			userConfigPath: func() (string, error) { return configPath, nil },
		}); code == exitSuccess || !strings.Contains(stderr.String(), "ambiguous") {
			t.Fatalf("args %v: exit = %d, stdout = %q, stderr = %q; want ambiguity", args, code, stdout.String(), stderr.String())
		}
	}
}

func TestRunAuthLoginRejectsAmbiguousCatalogAddress(t *testing.T) {
	withAuthStore(t)
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"providers":[{"name":"work-xai","catalogId":"xai"},{"name":"personal-xai","catalogId":"xai"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runWithDeps([]string{"auth", "login", "xai"}, &stdout, &stderr, appDeps{
		userConfigPath: func() (string, error) { return configPath, nil },
	}); code == exitSuccess || !strings.Contains(stderr.String(), "ambiguous") {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q; want pre-login ambiguity", code, stdout.String(), stderr.String())
	}
}

func TestRunAuthRejectsWrongFlags(t *testing.T) {
	withAuthStore(t)
	cases := [][]string{
		{"auth", "login", "demo", "--watch"},       // watch is refresh-only
		{"auth", "login", "demo", "--json"},        // json not for interactive login
		{"auth", "status", "demo", "--device"},     // device is login-only
		{"auth", "logout", "demo", "--scope", "x"}, // scope is login-only
		{"auth", "refresh", "demo", "--json"},      // json not for refresh
		{"auth", "login", "demo", "--scope", ""},   // empty scope rejected
	}
	for _, args := range cases {
		var stdout, stderr bytes.Buffer
		if code := runWithDeps(args, &stdout, &stderr, appDeps{}); code == exitSuccess {
			t.Errorf("args %v should be rejected, got success", args)
		}
	}
}

// TestRunAuthLogoutResolvesCatalogIdentity covers jatmn's #725 finding: login
// accepts a catalog id and stores its token under that key, and the TUI tells
// users to run `zero auth logout chatgpt` — but logout hard-stopped whenever a
// persisted row matched case-insensitively without matching exactly, so the
// documented command left the token and any stored key in place.
func TestRunAuthLogoutResolvesCatalogIdentity(t *testing.T) {
	storePath := withAuthStore(t)
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"providers":[{"name":"ChatGPT","catalogId":"chatgpt"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := oauth.NewStore(oauth.StoreOptions{FilePath: storePath})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(oauth.ProviderKey("chatgpt"), oauth.Token{AccessToken: "stored"}); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runWithDeps([]string{"auth", "logout", "chatgpt"}, &stdout, &stderr, appDeps{
		userConfigPath: func() (string, error) { return configPath, nil },
	})
	if code != exitSuccess {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "capitalization") {
		t.Fatalf("logout refused the spelling the UI documents: %q", stderr.String())
	}
	if _, ok, err := store.Load(oauth.ProviderKey("chatgpt")); err != nil || ok {
		t.Fatalf("stored token survived logout: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(stdout.String(), "Logged out") {
		t.Fatalf("stdout = %q, want a logout confirmation", stdout.String())
	}
}

// TestRunAuthLogoutDeletesCatalogIDToken covers jatmn's #725 follow-up
// finding: a profile addressed by its own name but logged in under its
// catalog id (e.g. {name:"my-xai", catalogId:"xai"} via `zero auth login
// xai`) left the "xai" OAuth token behind when logged out as "my-xai",
// because logout only ever deleted the exact spelling the user typed.
func TestRunAuthLogoutDeletesCatalogIDToken(t *testing.T) {
	storePath := withAuthStore(t)
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"providers":[{"name":"my-xai","catalogId":"xai"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := oauth.NewStore(oauth.StoreOptions{FilePath: storePath})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(oauth.ProviderKey("xai"), oauth.Token{AccessToken: "stored"}); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runWithDeps([]string{"auth", "logout", "my-xai"}, &stdout, &stderr, appDeps{
		userConfigPath: func() (string, error) { return configPath, nil },
	})
	if code != exitSuccess {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if _, ok, err := store.Load(oauth.ProviderKey("xai")); err != nil || ok {
		t.Fatalf("catalog-id OAuth token survived logout: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(stdout.String(), "Logged out") {
		t.Fatalf("stdout = %q, want a logout confirmation", stdout.String())
	}
}

// TestRunAuthLogoutDeletesCatalogIDAPIKey covers jatmn's second #725 follow-up
// finding: logout's OAuth-token deletion covers the profile name, canonical
// persisted name, and catalog id, but API-key deletion only covered the first
// two — a key stored under the catalog id (e.g. captured via `zero auth
// openrouter`-style catalog flows) survived `zero auth logout my-xai`.
func TestRunAuthLogoutDeletesCatalogIDAPIKey(t *testing.T) {
	t.Setenv("ZERO_CRED_STORAGE", "encrypted-file")
	withAuthStore(t)
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"providers":[{"name":"my-xai","catalogId":"xai","apiKeyStored":true}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	keyStore, err := config.ProviderKeyStoreAt(filepath.Dir(configPath))
	if err != nil {
		t.Fatal(err)
	}
	if err := keyStore.Set("xai", "catalog-id-key"); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runWithDeps([]string{"auth", "logout", "my-xai"}, &stdout, &stderr, appDeps{
		userConfigPath: func() (string, error) { return configPath, nil },
	})
	if code != exitSuccess {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if _, ok, err := keyStore.Get("xai"); err != nil || ok {
		t.Fatalf("catalog-id API key survived logout: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(stdout.String(), "Logged out") {
		t.Fatalf("stdout = %q, want a logout confirmation", stdout.String())
	}
}

// TestRunAuthLogoutKeepsDistinctUnicodeCredentials pins the end-to-end
// guarantee behind jatmn's #725 finding that destructive candidate expansion
// used strings.EqualFold as authority for credential ownership: a saved "s"
// profile with its own token and key must survive `zero auth logout ſ`, which
// names a provider the config never saved (the credential store defines
// identity with credstore.NormalizeProvider, under which "s" and Unicode
// long-s "ſ" are separate entries).
//
// Two independent layers now enforce that, and this test deliberately asserts
// the outcome rather than either mechanism. The one that fires first is
// oauth.ValidateKey, which rejects the non-ASCII spelling before any deletion
// runs — so the folded-name adoption itself is pinned where it is reachable, in
// TestPersistedProviderIdentityRulesMatchTheCredentialStore (internal/config).
func TestRunAuthLogoutKeepsDistinctUnicodeCredentials(t *testing.T) {
	const longS = "ſ"
	t.Setenv("ZERO_CRED_STORAGE", "encrypted-file")
	storePath := withAuthStore(t)
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"providers":[{"name":"s","apiKeyStored":true}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	tokens, err := oauth.NewStore(oauth.StoreOptions{FilePath: storePath})
	if err != nil {
		t.Fatal(err)
	}
	if err := tokens.Save(oauth.ProviderKey("s"), oauth.Token{AccessToken: "stored"}); err != nil {
		t.Fatal(err)
	}
	keyStore, err := config.ProviderKeyStoreAt(filepath.Dir(configPath))
	if err != nil {
		t.Fatal(err)
	}
	if err := keyStore.Set("s", "long-s-is-not-s"); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	runWithDeps([]string{"auth", "logout", longS}, &stdout, &stderr, appDeps{
		userConfigPath: func() (string, error) { return configPath, nil },
	})

	if _, ok, err := tokens.Load(oauth.ProviderKey("s")); err != nil || !ok {
		t.Fatalf("logging out of a distinct identity deleted the saved token: ok=%v err=%v", ok, err)
	}
	if key, ok, err := keyStore.Get("s"); err != nil || !ok || key != "long-s-is-not-s" {
		t.Fatalf("stored API key = %q, %v, %v; want the unrelated profile untouched", key, ok, err)
	}
	saved := readCLIConfigFixture(t, configPath).Providers
	if len(saved) != 1 || saved[0].Name != "s" || !saved[0].APIKeyStored {
		t.Fatalf("providers = %+v, want the saved profile keeping its stored-key marker", saved)
	}
}

// TestRunAuthLogoutResolvesCandidatesDespiteUnrelatedAmbiguousConfig covers
// jatmn's third #725 follow-up finding: identity resolution and OAuth/API-key
// candidate expansion were gated on PreflightUserConfig succeeding, even
// though PersistedProviderIdentity/ProviderRow only read+parse raw JSON and
// never validate case-duplicate names. An unrelated ambiguous pair elsewhere
// in the file (demo/DEMO) must not suppress deleting every credential for the
// unambiguous profile actually being logged out — only the final marker-write
// should fail on that unrelated validation error.
func TestRunAuthLogoutResolvesCandidatesDespiteUnrelatedAmbiguousConfig(t *testing.T) {
	storePath := withAuthStore(t)
	t.Setenv("ZERO_CRED_STORAGE", "encrypted-file")
	configPath := filepath.Join(t.TempDir(), "config.json")
	configData := []byte(`{"providers":[{"name":"demo"},{"name":"DEMO"},{"name":"my-xai","catalogId":"xai","apiKeyStored":true}]}`)
	if err := os.WriteFile(configPath, configData, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := oauth.NewStore(oauth.StoreOptions{FilePath: storePath})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(oauth.ProviderKey("xai"), oauth.Token{AccessToken: "stored"}); err != nil {
		t.Fatal(err)
	}
	keyStore, err := config.ProviderKeyStoreAt(filepath.Dir(configPath))
	if err != nil {
		t.Fatal(err)
	}
	if err := keyStore.Set("xai", "catalog-id-key"); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runWithDeps([]string{"auth", "logout", "my-xai"}, &stdout, &stderr, appDeps{
		userConfigPath: func() (string, error) { return configPath, nil },
	})
	if code == exitSuccess || !strings.Contains(stderr.String(), "ambiguous persisted provider names") {
		t.Fatalf("exit = %d stderr = %q, want the unrelated ambiguity surfaced as a truthful marker-update failure", code, stderr.String())
	}
	if _, ok, err := store.Load(oauth.ProviderKey("xai")); err != nil || ok {
		t.Fatalf("catalog-id OAuth token survived logout despite the unrelated ambiguous config: ok=%v err=%v", ok, err)
	}
	if _, ok, err := keyStore.Get("xai"); err != nil || ok {
		t.Fatalf("catalog-id API key survived logout despite the unrelated ambiguous config: ok=%v err=%v", ok, err)
	}
}

func TestRunAuthLogoutCleansCredentialsWhenConfigIsAmbiguous(t *testing.T) {
	storePath := withAuthStore(t)
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("ZERO_CRED_STORAGE", "encrypted-file")
	configPath := filepath.Join(configHome, "zero", "config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	configData := []byte(`{"providers":[{"name":"demo"},{"name":"DEMO","apiKeyStored":true}]}`)
	if err := os.WriteFile(configPath, configData, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := oauth.NewStore(oauth.StoreOptions{FilePath: storePath})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(oauth.ProviderKey("demo"), oauth.Token{AccessToken: "stored"}); err != nil {
		t.Fatal(err)
	}
	keyStore, err := config.ProviderKeyStoreAt(filepath.Dir(configPath))
	if err != nil {
		t.Fatal(err)
	}
	if err := keyStore.Set("demo", "stored-key"); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runWithDeps([]string{"auth", "logout", "demo"}, &stdout, &stderr, appDeps{
		userConfigPath: func() (string, error) { return configPath, nil },
	})
	if code == exitSuccess || !strings.Contains(stderr.String(), "ambiguous persisted provider names") {
		t.Fatalf("exit = %d stderr = %q, want truthful marker-update failure", code, stderr.String())
	}
	if _, ok, err := store.Load(oauth.ProviderKey("demo")); err != nil || ok {
		t.Fatalf("OAuth credential survived recovery logout: ok=%v err=%v", ok, err)
	}
	if _, ok, err := keyStore.Get("demo"); err != nil || ok {
		t.Fatalf("API key survived recovery logout: ok=%v err=%v", ok, err)
	}
	after, err := os.ReadFile(configPath)
	if err != nil || !bytes.Equal(after, configData) {
		t.Fatalf("invalid config changed during recovery logout: err=%v content=%s", err, after)
	}
}

// TestRunAuthOpenRouterPreflightsBeforeTheBrowserFlow covers the second half of
// the same finding: every other auth entry point validates the config before
// opening a browser, and this one minted a key first and only discovered the
// config was unusable when trying to save it.
func TestRunAuthOpenRouterPreflightsBeforeTheBrowserFlow(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"providers":[{"name":"work"},{"name":"WORK"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	loginCalled := false
	var stdout, stderr bytes.Buffer
	code := runWithDeps([]string{"auth", "openrouter"}, &stdout, &stderr, appDeps{
		userConfigPath: func() (string, error) { return configPath, nil },
		openRouterLogin: func(context.Context, provideroauth.OpenRouterOptions) (string, error) {
			loginCalled = true
			return "sk-should-not-be-minted", nil
		},
	})
	if code == exitSuccess {
		t.Fatalf("an unusable config must fail before login; stdout = %q", stdout.String())
	}
	if loginCalled {
		t.Fatal("the browser flow ran before the config was validated")
	}
	if !strings.Contains(stderr.String(), "ambiguous persisted provider names") {
		t.Fatalf("stderr = %q, want the config error", stderr.String())
	}
}

// TestRunAuthOpenRouterFailsWhenTheKeyCannotBeSaved pins the exit code: the
// minted key is still printed so the user does not lose it, but nothing was
// persisted, and reporting success left a script believing the provider was
// configured.
func TestRunAuthOpenRouterFailsWhenTheKeyCannotBeSaved(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"providers":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	var stdout, stderr bytes.Buffer
	code := runWithDeps([]string{"auth", "openrouter"}, &stdout, &stderr, appDeps{
		userConfigPath: func() (string, error) {
			calls++
			if calls > 1 {
				// The preflight passed; break the path only for the save that follows.
				return "", errors.New("config path unavailable")
			}
			return configPath, nil
		},
		openRouterLogin: func(context.Context, provideroauth.OpenRouterOptions) (string, error) {
			return "sk-openrouter-test", nil
		},
	})
	if code == exitSuccess {
		t.Fatal("a failed save must not report success")
	}
	if !strings.Contains(stdout.String(), "sk-openrouter-test") {
		t.Fatalf("stdout = %q, want the minted key printed so it is not lost", stdout.String())
	}
	if !strings.Contains(stderr.String(), "could not save") {
		t.Fatalf("stderr = %q, want the save failure reported", stderr.String())
	}
}

// TestProviderSetupAdoptsPersistedCatalogRowCasing covers the third: catalog
// OAuth reused an existing row through EnsureCatalogProvider while setup and the
// wizard still collided with it on write, so re-running setup for a provider
// saved as "OpenRouter" failed after a successful capture.
func TestProviderSetupAdoptsPersistedCatalogRowCasing(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"providers":[{"name":"OpenRouter","catalogId":"openrouter","model":"x"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	profile := config.ProviderProfile{Name: "openrouter", CatalogID: "openrouter", Model: "y"}

	adopted, err := config.AdoptPersistedCatalogProviderName(configPath, profile)
	if err != nil {
		t.Fatalf("AdoptPersistedCatalogProviderName: %v", err)
	}
	if adopted.Name != "OpenRouter" {
		t.Fatalf("adopted name = %q, want the persisted row's spelling", adopted.Name)
	}
	if err := config.PreflightProviderWrite(configPath, adopted.Name); err != nil {
		t.Fatalf("write preflight rejected the adopted name: %v", err)
	}

	// A user-chosen name is left alone: colliding with an existing row there is a
	// real collision, and silently overwriting it would be worse than the error.
	custom := config.ProviderProfile{Name: "openrouter", CatalogID: "anthropic"}
	kept, err := config.AdoptPersistedCatalogProviderName(configPath, custom)
	if err != nil {
		t.Fatalf("AdoptPersistedCatalogProviderName: %v", err)
	}
	if kept.Name != "openrouter" {
		t.Fatalf("name = %q, want a non-catalog-default name left untouched", kept.Name)
	}
}

func TestRunAuthOpenRouterRejectsArgs(t *testing.T) {
	withAuthStore(t)
	var stdout, stderr bytes.Buffer
	// An unexpected arg/flag must fail fast, not silently run the login.
	if code := runWithDeps([]string{"auth", "openrouter", "--json"}, &stdout, &stderr, appDeps{}); code == exitSuccess {
		t.Fatalf("openrouter with an unexpected flag should fail; stdout=%q", stdout.String())
	}
	// --help still works.
	stdout.Reset()
	stderr.Reset()
	if code := runWithDeps([]string{"auth", "openrouter", "--help"}, &stdout, &stderr, appDeps{}); code != exitSuccess {
		t.Fatalf("openrouter --help should succeed, stderr=%q", stderr.String())
	}
}

func TestRunAuthOpenRouterSavesMintedKey(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	var stdout, stderr bytes.Buffer

	code := runWithDeps([]string{"auth", "openrouter"}, &stdout, &stderr, appDeps{
		userConfigPath: func() (string, error) { return configPath, nil },
		openRouterLogin: func(context.Context, provideroauth.OpenRouterOptions) (string, error) {
			return "sk-openrouter-test", nil
		},
	})

	if code != exitSuccess {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "new API key saved") {
		t.Fatalf("expected saved-key confirmation, got %q", stdout.String())
	}
	cfg := readCLIConfigFixture(t, configPath)
	if cfg.ActiveProvider != "openrouter" || len(cfg.Providers) != 1 {
		t.Fatalf("config = %#v", cfg)
	}
	profile := cfg.Providers[0]
	if profile.Name != "openrouter" || profile.CatalogID != "openrouter" || !profile.APIKeyStored || profile.APIKey != "" || profile.APIKeyEnv != "" {
		t.Fatalf("provider not stored-key sanitized: %#v", profile)
	}
	store, err := config.ProviderKeyStoreAt(filepath.Dir(configPath))
	if err != nil {
		t.Fatal(err)
	}
	key, ok, err := store.Get("openrouter")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || key != "sk-openrouter-test" {
		t.Fatalf("stored key = %q, %v", key, ok)
	}
}

func TestRunAuthHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runWithDeps([]string{"auth", "--help"}, &stdout, &stderr, appDeps{}); code != exitSuccess {
		t.Fatalf("exit = %d", code)
	}
	for _, want := range []string{"zero auth", "login", "logout", "status", "refresh", "--device"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("help missing %q:\n%s", want, stdout.String())
		}
	}
}

// TestRunAuthLoginChatGPTRoutesToDedicatedFlow verifies `zero auth login
// chatgpt` reaches the dedicated ChatGPT login (fixed-port loopback + mandatory
// authorize params), not the generic manager path. The generic login accepts
// --device, so a ChatGPT-specific rejection proves the routing took effect.
// See issue #430: the generic path built a random-port 127.0.0.1 redirect_uri
// without the required extra params, so OpenAI's authorize endpoint rejected it.
func TestRunAuthLoginChatGPTRoutesToDedicatedFlow(t *testing.T) {
	withAuthStore(t)
	var stdout, stderr bytes.Buffer
	if code := runWithDeps([]string{"auth", "login", "chatgpt", "--device"}, &stdout, &stderr, appDeps{}); code == exitSuccess {
		t.Fatal("auth login chatgpt --device should be rejected (ChatGPT is loopback-only)")
	}
	if !strings.Contains(stderr.String(), "ChatGPT login does not support --device") {
		t.Fatalf("stderr = %q, want the ChatGPT-specific --device rejection", stderr.String())
	}
	// Case-insensitive provider name should still route.
	stdout.Reset()
	stderr.Reset()
	if code := runWithDeps([]string{"auth", "login", "ChatGPT", "--device"}, &stdout, &stderr, appDeps{}); code == exitSuccess {
		t.Fatal("auth login ChatGPT --device should be rejected")
	}
	if !strings.Contains(stderr.String(), "ChatGPT login does not support --device") {
		t.Fatalf("stderr = %q, want the ChatGPT-specific rejection (case-insensitive)", stderr.String())
	}
}

// TestRunAuthLoginChatGPTRejectsScope mirrors the --device rejection: --scope
// must not be silently dropped on the ChatGPT path. The Codex client
// registration pins a fixed scope set (incl. api.connectors.*), so custom
// scopes are rejected up front rather than plumbed through.
func TestRunAuthLoginChatGPTRejectsScope(t *testing.T) {
	withAuthStore(t)
	var stdout, stderr bytes.Buffer
	if code := runWithDeps([]string{"auth", "login", "chatgpt", "--scope", "custom-scope"}, &stdout, &stderr, appDeps{}); code == exitSuccess {
		t.Fatal("auth login chatgpt --scope should be rejected")
	}
	if !strings.Contains(stderr.String(), "ChatGPT login does not support --scope") {
		t.Fatalf("stderr = %q, want the ChatGPT-specific --scope rejection", stderr.String())
	}
}

func TestEnsureLoginProviderProfileAddsProviderWithoutStealingActive(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	seed := `{"activeProvider":"opengateway","providers":[{"name":"opengateway","provider_kind":"openai-compatible","baseURL":"https://gateway.example.com/v1","apiKeyStored":true,"model":"some-model"}]}`
	if err := os.WriteFile(configPath, []byte(seed), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	deps := appDeps{userConfigPath: func() (string, error) { return configPath, nil }}

	line := ensureLoginProviderProfile(deps, "chatgpt")
	if !strings.Contains(line, `Added provider "chatgpt"`) {
		t.Fatalf("expected added-provider guidance, got %q", line)
	}
	if !strings.Contains(line, "zero providers use chatgpt") {
		t.Fatalf("expected switch hint, got %q", line)
	}

	cfg := readCLIConfigFixture(t, configPath)
	if cfg.ActiveProvider != "opengateway" {
		t.Fatalf("active provider changed to %q", cfg.ActiveProvider)
	}
	names := make([]string, 0, len(cfg.Providers))
	for _, provider := range cfg.Providers {
		names = append(names, provider.Name)
	}
	if len(cfg.Providers) != 2 || cfg.Providers[1].CatalogID != "chatgpt" {
		t.Fatalf("expected chatgpt profile appended, got %v", names)
	}

	// A second login must be a no-op with switch guidance, not a duplicate.
	line = ensureLoginProviderProfile(deps, "chatgpt")
	if !strings.Contains(line, "already configured") {
		t.Fatalf("expected already-configured guidance, got %q", line)
	}
	cfg = readCLIConfigFixture(t, configPath)
	if len(cfg.Providers) != 2 {
		t.Fatalf("repeat login duplicated the profile: %d providers", len(cfg.Providers))
	}
}

func TestEnsureLoginProviderProfileActivatesOnFreshConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	deps := appDeps{userConfigPath: func() (string, error) { return configPath, nil }}

	line := ensureLoginProviderProfile(deps, "chatgpt")
	if !strings.Contains(line, "set it active") {
		t.Fatalf("fresh config should adopt the login as active, got %q", line)
	}
	cfg := readCLIConfigFixture(t, configPath)
	if cfg.ActiveProvider != "chatgpt" {
		t.Fatalf("active provider = %q, want chatgpt", cfg.ActiveProvider)
	}
}

func TestEnsureLoginProviderProfileSkipsNonCatalogProviders(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	deps := appDeps{userConfigPath: func() (string, error) { return configPath, nil }}

	if line := ensureLoginProviderProfile(deps, "my-custom-oauth-server"); line != "" {
		t.Fatalf("custom OAuth server must not scaffold a profile, got %q", line)
	}
	if _, err := os.Stat(configPath); err == nil {
		t.Fatalf("config must not be created for a non-catalog login")
	}
}

func readCLIConfigFixture(t *testing.T, path string) config.FileConfig {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg config.FileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	return cfg
}

// TestRunAuthLogoutLeavesSharedCatalogCredentialsAlone covers jatmn's #725
// finding that logout cleanup was scoped by catalog id rather than by proven
// profile ownership. Catalog ids are shared by design: stored-key "work-xai",
// stored-key "xai", and keyless "personal-xai" can all carry catalogId "xai".
// Logging out "work-xai" deleted the shared "xai" OAuth token and the "xai"
// profile's API key — another profile's credentials — while clearing only
// work-xai's own marker.
func TestRunAuthLogoutLeavesSharedCatalogCredentialsAlone(t *testing.T) {
	storePath := withAuthStore(t)
	t.Setenv("ZERO_CRED_STORAGE", "encrypted-file")
	configPath := filepath.Join(t.TempDir(), "config.json")
	configData := []byte(`{"providers":[` +
		`{"name":"work-xai","catalogId":"xai","apiKeyStored":true},` +
		`{"name":"xai","catalogId":"xai","apiKeyStored":true},` +
		`{"name":"personal-xai","catalogId":"xai"}]}`)
	if err := os.WriteFile(configPath, configData, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := oauth.NewStore(oauth.StoreOptions{FilePath: storePath})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(oauth.ProviderKey("xai"), oauth.Token{AccessToken: "shared"}); err != nil {
		t.Fatal(err)
	}
	keyStore, err := config.ProviderKeyStoreAt(filepath.Dir(configPath))
	if err != nil {
		t.Fatal(err)
	}
	if err := keyStore.Set("xai", "sibling-key"); err != nil {
		t.Fatal(err)
	}
	if err := keyStore.Set("work-xai", "own-key"); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runWithDeps([]string{"auth", "logout", "work-xai"}, &stdout, &stderr, appDeps{
		userConfigPath: func() (string, error) { return configPath, nil },
	})
	if code != exitSuccess {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if _, ok, err := keyStore.Get("work-xai"); err != nil || ok {
		t.Fatalf("the profile's own API key must be deleted: ok=%v err=%v", ok, err)
	}
	if _, ok, err := store.Load(oauth.ProviderKey("xai")); err != nil || !ok {
		t.Fatalf("a catalog token three profiles can use must survive one profile's logout: ok=%v err=%v", ok, err)
	}
	if _, ok, err := keyStore.Get("xai"); err != nil || !ok {
		t.Fatalf("the sibling xai profile's API key must survive: ok=%v err=%v", ok, err)
	}
}

func TestRunAuthLogoutRejectsAmbiguousCatalogAddress(t *testing.T) {
	storePath := withAuthStore(t)
	t.Setenv("ZERO_CRED_STORAGE", "encrypted-file")
	configPath := filepath.Join(t.TempDir(), "config.json")
	configData := []byte(`{"providers":[` +
		`{"name":"work-xai","catalogId":"xai"},` +
		`{"name":"personal-xai","catalogId":"xai"}]}`)
	if err := os.WriteFile(configPath, configData, 0o600); err != nil {
		t.Fatal(err)
	}
	tokenStore, err := oauth.NewStore(oauth.StoreOptions{FilePath: storePath})
	if err != nil {
		t.Fatal(err)
	}
	if err := tokenStore.Save(oauth.ProviderKey("xai"), oauth.Token{AccessToken: "shared"}); err != nil {
		t.Fatal(err)
	}
	keyStore, err := config.ProviderKeyStoreAt(filepath.Dir(configPath))
	if err != nil {
		t.Fatal(err)
	}
	if err := keyStore.Set("xai", "shared-key"); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := runWithDeps([]string{"auth", "logout", "xai"}, &stdout, &stderr, appDeps{
		userConfigPath: func() (string, error) { return configPath, nil },
	}); code == exitSuccess || !strings.Contains(stderr.String(), "ambiguous") {
		t.Fatalf("exit succeeded or ambiguity was not reported: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if _, ok, err := tokenStore.Load(oauth.ProviderKey("xai")); err != nil || !ok {
		t.Fatalf("ambiguous catalog token was deleted: ok=%v err=%v", ok, err)
	}
	if _, ok, err := keyStore.Get("xai"); err != nil || !ok {
		t.Fatalf("ambiguous catalog key was deleted: ok=%v err=%v", ok, err)
	}
}

// TestRunAuthLogoutPrefersTheExactlyNamedProfile is the other half of the same
// finding: identity resolution took the first row matching name OR catalog id,
// so `zero auth logout xai` retargeted an earlier {name:"work-xai",
// catalogId:"xai"} row and cleared that profile's marker instead.
func TestRunAuthLogoutPrefersTheExactlyNamedProfile(t *testing.T) {
	withAuthStore(t)
	t.Setenv("ZERO_CRED_STORAGE", "encrypted-file")
	configPath := filepath.Join(t.TempDir(), "config.json")
	configData := []byte(`{"providers":[` +
		`{"name":"work-xai","catalogId":"xai","apiKeyStored":true},` +
		`{"name":"xai","catalogId":"xai","apiKeyStored":true}]}`)
	if err := os.WriteFile(configPath, configData, 0o600); err != nil {
		t.Fatal(err)
	}
	keyStore, err := config.ProviderKeyStoreAt(filepath.Dir(configPath))
	if err != nil {
		t.Fatal(err)
	}
	if err := keyStore.Set("work-xai", "work-key"); err != nil {
		t.Fatal(err)
	}
	if err := keyStore.Set("xai", "own-key"); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := runWithDeps([]string{"auth", "logout", "xai"}, &stdout, &stderr, appDeps{
		userConfigPath: func() (string, error) { return configPath, nil },
	}); code != exitSuccess {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if _, ok, err := keyStore.Get("xai"); err != nil || ok {
		t.Fatalf("the exactly named profile's key must be deleted: ok=%v err=%v", ok, err)
	}
	if _, ok, err := keyStore.Get("work-xai"); err != nil || !ok {
		t.Fatalf("an earlier catalog sibling must not be logged out instead: ok=%v err=%v", ok, err)
	}
	cfg := readFileConfig(t, configPath)
	for _, provider := range cfg.Providers {
		if provider.Name == "xai" && provider.APIKeyStored {
			t.Fatal("the named profile's apiKeyStored marker must be cleared")
		}
		if provider.Name == "work-xai" && !provider.APIKeyStored {
			t.Fatal("the sibling profile's apiKeyStored marker must be left alone")
		}
	}
}
