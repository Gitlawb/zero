package oauth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeProvider is an httptest server that plays a token + device endpoint.
type fakeProvider struct {
	server    *httptest.Server
	tokenHits atomic.Int32
}

func newFakeProvider(t *testing.T, tokenJSON string) *fakeProvider {
	t.Helper()
	fp := &fakeProvider{}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		fp.tokenHits.Add(1)
		_, _ = io.WriteString(w, tokenJSON)
	})
	mux.HandleFunc("/device", func(w http.ResponseWriter, _ *http.Request) {
		// Approve immediately on poll; short interval so the test is fast.
		_, _ = io.WriteString(w, `{"device_code":"dc","user_code":"U-1","verification_uri":"https://example/dev","expires_in":600,"interval":1}`)
	})
	fp.server = httptest.NewServer(mux)
	t.Cleanup(fp.server.Close)
	return fp
}

func managerFor(t *testing.T, env map[string]string, openBrowser func(string) error) *Manager {
	t.Helper()
	store, err := NewStore(StoreOptions{FilePath: filepath.Join(t.TempDir(), "tok.json")})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	m, err := NewManager(ManagerOptions{
		Store:       store,
		Env:         env,
		OpenBrowser: openBrowser,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m
}

// AllowPresets layers the opt-in onto the manager's env so a login/refresh for a
// preset provider resolves; without it (and no Env) the env stays nil so callers
// and tests remain hermetic.
func TestNewManagerAllowPresetsForcesOptIn(t *testing.T) {
	store, err := NewStore(StoreOptions{FilePath: filepath.Join(t.TempDir(), "tok.json")})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	m, err := NewManager(ManagerOptions{Store: store, AllowPresets: true})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if m.env["ZERO_OAUTH_ALLOW_PRESETS"] != "1" {
		t.Fatalf("AllowPresets should force the opt-in; got env[%q]=%q", "ZERO_OAUTH_ALLOW_PRESETS", m.env["ZERO_OAUTH_ALLOW_PRESETS"])
	}
	m2, err := NewManager(ManagerOptions{Store: store})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if m2.env != nil {
		t.Fatalf("env should stay nil without AllowPresets, got %v", m2.env)
	}
}

func TestManagerLoginLoopback(t *testing.T) {
	fp := newFakeProvider(t, `{"access_token":"at","refresh_token":"rt","token_type":"Bearer","expires_in":3600}`)
	env := map[string]string{
		"ZERO_OAUTH_DEMO_CLIENT_ID":     "client",
		"ZERO_OAUTH_DEMO_AUTHORIZE_URL": "https://auth.example.com/authorize",
		"ZERO_OAUTH_DEMO_TOKEN_URL":     fp.server.URL + "/token",
	}
	// The fake browser drives the loopback redirect with the captured state.
	openBrowser := func(authURL string) error {
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		redirect := u.Query().Get("redirect_uri")
		state := u.Query().Get("state")
		_, err = http.Get(redirect + "?code=the-code&state=" + url.QueryEscape(state))
		return err
	}
	m := managerFor(t, env, openBrowser)

	status, err := m.Login(context.Background(), LoginOptions{Provider: "demo"})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if !status.HasToken || status.Key != ProviderKey("demo") {
		t.Fatalf("status = %+v", status)
	}
	// The token is persisted and GetFresh returns it without a needless refresh.
	access, err := m.GetFresh(context.Background(), ProviderKey("demo"))
	if err != nil {
		t.Fatalf("GetFresh: %v", err)
	}
	if access != "at" {
		t.Fatalf("access = %q, want at", access)
	}
}

func TestManagerLoginDevice(t *testing.T) {
	fp := newFakeProvider(t, `{"access_token":"dev-at","token_type":"Bearer","expires_in":3600}`)
	env := map[string]string{
		"ZERO_OAUTH_DEMODEV_CLIENT_ID":  "client",
		"ZERO_OAUTH_DEMODEV_TOKEN_URL":  fp.server.URL + "/token",
		"ZERO_OAUTH_DEMODEV_DEVICE_URL": fp.server.URL + "/device",
	}
	m := managerFor(t, env, nil)
	status, err := m.Login(context.Background(), LoginOptions{Provider: "demodev", Device: true})
	if err != nil {
		t.Fatalf("device Login: %v", err)
	}
	if !status.HasToken {
		t.Fatalf("device status = %+v", status)
	}
}

func TestManagerPrepareAndCompleteDeviceLogin(t *testing.T) {
	fp := newFakeProvider(t, `{"access_token":"two-phase-at","token_type":"Bearer","expires_in":3600}`)
	env := map[string]string{
		"ZERO_OAUTH_DEMO2_CLIENT_ID":  "client",
		"ZERO_OAUTH_DEMO2_TOKEN_URL":  fp.server.URL + "/token",
		"ZERO_OAUTH_DEMO2_DEVICE_URL": fp.server.URL + "/device",
	}
	m := managerFor(t, env, nil)

	// Phase 1: request the device code (what the UI displays to the user).
	auth, cfg, err := m.PrepareDeviceLogin(context.Background(), LoginOptions{Provider: "demo2"})
	if err != nil {
		t.Fatalf("PrepareDeviceLogin: %v", err)
	}
	if auth.UserCode != "U-1" || auth.VerificationURI == "" {
		t.Fatalf("device auth missing user code/uri: %+v", auth)
	}

	// Phase 2: poll for the token and persist it.
	status, err := m.CompleteDeviceLogin(context.Background(), "demo2", cfg, auth)
	if err != nil {
		t.Fatalf("CompleteDeviceLogin: %v", err)
	}
	if !status.HasToken || status.Key != ProviderKey("demo2") {
		t.Fatalf("status = %+v", status)
	}
	access, err := m.GetFresh(context.Background(), ProviderKey("demo2"))
	if err != nil {
		t.Fatalf("GetFresh: %v", err)
	}
	if access != "two-phase-at" {
		t.Fatalf("access = %q, want two-phase-at", access)
	}
}

// A provider configured with only an issuer URL (no TOKEN_URL) must still be
// refreshable: GetFresh resolves the token endpoint via discovery before
// refreshing. Guards resolveConfigForKey calling resolveEndpoints.
func TestManagerGetFreshDiscoversIssuerEndpoints(t *testing.T) {
	var tokenHits int32
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"issuer":"`+server.URL+`","token_endpoint":"`+server.URL+`/token","authorization_endpoint":"`+server.URL+`/authorize"}`)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&tokenHits, 1)
		_, _ = io.WriteString(w, `{"access_token":"refreshed-at","token_type":"Bearer","expires_in":3600}`)
	})
	env := map[string]string{
		"ZERO_OAUTH_ISSUERONLY_CLIENT_ID":  "client",
		"ZERO_OAUTH_ISSUERONLY_ISSUER_URL": server.URL, // no TOKEN_URL → discovered
	}
	m := managerFor(t, env, nil)
	if err := m.store.Save(ProviderKey("issueronly"), Token{AccessToken: "stale", RefreshToken: "rt", ExpiresAt: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, err := m.GetFresh(context.Background(), ProviderKey("issueronly"))
	if err != nil {
		t.Fatalf("GetFresh (issuer-only) must discover the token endpoint and refresh: %v", err)
	}
	if got != "refreshed-at" {
		t.Fatalf("access = %q, want refreshed-at", got)
	}
	if atomic.LoadInt32(&tokenHits) == 0 {
		t.Fatal("token endpoint never hit — discovery/refresh did not run")
	}
}

// A discovery document that serves an insecure (non-https, non-loopback)
// endpoint must be rejected before it is merged, so discovery metadata cannot
// silently downgrade the login to an attacker-controlled origin (issue #511).
func TestResolveEndpointsRejectsInsecureDiscoveredEndpoint(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		// Loopback issuer, but an attacker-controlled http authorization endpoint.
		_, _ = io.WriteString(w, `{"issuer":"`+server.URL+`","token_endpoint":"`+server.URL+`/token","authorization_endpoint":"http://evil.example/authorize"}`)
	})
	m := managerFor(t, map[string]string{}, nil)
	_, err := m.resolveEndpoints(context.Background(), Config{IssuerURL: server.URL})
	if !errors.Is(err, ErrInsecureTokenEndpoint) {
		t.Fatalf("resolveEndpoints err = %v, want ErrInsecureTokenEndpoint", err)
	}
}

func TestManagerGetFreshRefreshesExpired(t *testing.T) {
	fp := newFakeProvider(t, `{"access_token":"fresh-at","expires_in":3600}`)
	env := map[string]string{
		"ZERO_OAUTH_DEMO_CLIENT_ID": "client",
		"ZERO_OAUTH_DEMO_TOKEN_URL": fp.server.URL + "/token",
	}
	m := managerFor(t, env, nil)
	// Seed an expired token with a refresh token.
	if err := m.store.Save(ProviderKey("demo"), Token{AccessToken: "stale", RefreshToken: "rt", ExpiresAt: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	access, err := m.GetFresh(context.Background(), ProviderKey("demo"))
	if err != nil {
		t.Fatalf("GetFresh: %v", err)
	}
	if access != "fresh-at" {
		t.Fatalf("access = %q, want fresh-at (refreshed)", access)
	}
	if fp.tokenHits.Load() != 1 {
		t.Fatalf("token endpoint hit %d times, want 1 refresh", fp.tokenHits.Load())
	}
	// The refreshed token is persisted.
	stored, _, _ := m.store.Load(ProviderKey("demo"))
	if stored.AccessToken != "fresh-at" {
		t.Fatalf("stored token not updated: %+v", stored)
	}
}

func TestManagerGetFreshSkipsValidToken(t *testing.T) {
	fp := newFakeProvider(t, `{"access_token":"should-not-be-used"}`)
	env := map[string]string{
		"ZERO_OAUTH_DEMO_CLIENT_ID": "client",
		"ZERO_OAUTH_DEMO_TOKEN_URL": fp.server.URL + "/token",
	}
	m := managerFor(t, env, nil)
	_ = m.store.Save(ProviderKey("demo"), Token{AccessToken: "valid", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	access, err := m.GetFresh(context.Background(), ProviderKey("demo"))
	if err != nil {
		t.Fatalf("GetFresh: %v", err)
	}
	if access != "valid" || fp.tokenHits.Load() != 0 {
		t.Fatalf("a valid token must not be refreshed (access=%q hits=%d)", access, fp.tokenHits.Load())
	}
}

func TestManagerHandle401ForcesRefresh(t *testing.T) {
	fp := newFakeProvider(t, `{"access_token":"after-401"}`)
	env := map[string]string{
		"ZERO_OAUTH_DEMO_CLIENT_ID": "client",
		"ZERO_OAUTH_DEMO_TOKEN_URL": fp.server.URL + "/token",
	}
	m := managerFor(t, env, nil)
	// Token is still valid by clock, but Handle401 forces a refresh anyway.
	_ = m.store.Save(ProviderKey("demo"), Token{AccessToken: "valid", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	access, err := m.Handle401(context.Background(), ProviderKey("demo"))
	if err != nil {
		t.Fatalf("Handle401: %v", err)
	}
	if access != "after-401" || fp.tokenHits.Load() != 1 {
		t.Fatalf("Handle401 must force a refresh (access=%q hits=%d)", access, fp.tokenHits.Load())
	}
}

func TestManagerLogout(t *testing.T) {
	m := managerFor(t, map[string]string{"ZERO_OAUTH_DEMO_CLIENT_ID": "c"}, nil)
	_ = m.store.Save(ProviderKey("demo"), Token{AccessToken: "a"})
	removed, err := m.Logout("demo")
	if err != nil || !removed {
		t.Fatalf("Logout = %v %v", removed, err)
	}
	if removed2, _ := m.Logout("demo"); removed2 {
		t.Fatal("second logout should report nothing removed")
	}
}

func TestCompleteDeviceLoginCancelAtCommitRollsBackNewToken(t *testing.T) {
	fp := newFakeProvider(t, `{"access_token":"authorized-token"}`)
	env := map[string]string{
		"ZERO_OAUTH_DEMO_CLIENT_ID": "client",
		"ZERO_OAUTH_DEMO_TOKEN_URL": fp.server.URL + "/token",
	}
	m := managerFor(t, env, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m.beforeDeviceCommit = func() {
		// Cancel the attempt during commit.
		cancel()
	}
	defer func() { m.beforeDeviceCommit = nil }()

	cfg := Config{ClientID: "client", TokenEndpoint: fp.server.URL + "/token"}
	auth := DeviceAuth{DeviceCode: "dc", Interval: 5 * time.Millisecond, ExpiresAt: time.Now().Add(5 * time.Second)}

	_, err := m.CompleteDeviceLogin(ctx, "demo", cfg, auth)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CompleteDeviceLogin err = %v, want context.Canceled", err)
	}

	_, ok, err := m.store.Load(ProviderKey("demo"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if ok {
		t.Fatal("expected store to have no token after rollback")
	}
}

func TestCompleteDeviceLoginCancelAtCommitRestoresPreviousToken(t *testing.T) {
	fp := newFakeProvider(t, `{"access_token":"new-token"}`)
	env := map[string]string{
		"ZERO_OAUTH_DEMO_CLIENT_ID": "client",
		"ZERO_OAUTH_DEMO_TOKEN_URL": fp.server.URL + "/token",
	}
	m := managerFor(t, env, nil)

	prevToken := Token{AccessToken: "previous-valid-token", RefreshToken: "prev-rt"}
	_ = m.store.Save(ProviderKey("demo"), prevToken)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m.beforeDeviceCommit = func() {
		cancel()
	}
	defer func() { m.beforeDeviceCommit = nil }()

	cfg := Config{ClientID: "client", TokenEndpoint: fp.server.URL + "/token"}
	auth := DeviceAuth{DeviceCode: "dc", Interval: 5 * time.Millisecond, ExpiresAt: time.Now().Add(5 * time.Second)}

	_, err := m.CompleteDeviceLogin(ctx, "demo", cfg, auth)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CompleteDeviceLogin err = %v, want context.Canceled", err)
	}

	stored, ok, err := m.store.Load(ProviderKey("demo"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !ok {
		t.Fatal("expected previous token to be present")
	}
	if stored.AccessToken != "previous-valid-token" {
		t.Fatalf("expected previous token %q restored, got %q", "previous-valid-token", stored.AccessToken)
	}
}

func TestCompleteDeviceLoginTwoManagersSharingStore(t *testing.T) {
	fp := newFakeProvider(t, `{"access_token":"concurrent-token"}`)
	storePath := filepath.Join(t.TempDir(), "tokens.json")
	store, err := NewStore(StoreOptions{FilePath: storePath})
	if err != nil {
		t.Fatal(err)
	}

	env := map[string]string{
		"ZERO_OAUTH_DEMO_CLIENT_ID": "client",
		"ZERO_OAUTH_DEMO_TOKEN_URL": fp.server.URL + "/token",
	}
	m1, err := NewManager(ManagerOptions{Store: store, Env: env})
	if err != nil {
		t.Fatal(err)
	}
	m2, err := NewManager(ManagerOptions{Store: store, Env: env})
	if err != nil {
		t.Fatal(err)
	}

	cfg := Config{ClientID: "client", TokenEndpoint: fp.server.URL + "/token"}
	auth := DeviceAuth{DeviceCode: "dc", Interval: 5 * time.Millisecond, ExpiresAt: time.Now().Add(5 * time.Second)}

	status, err := m1.CompleteDeviceLogin(context.Background(), "demo", cfg, auth)
	if err != nil {
		t.Fatalf("m1 CompleteDeviceLogin: %v", err)
	}
	if !status.HasToken {
		t.Fatal("expected status to report HasToken")
	}

	tok, ok, err := m2.store.Load(ProviderKey("demo"))
	if err != nil || !ok || tok.AccessToken != "concurrent-token" {
		t.Fatalf("m2 saw token = %+v, ok = %v, err = %v", tok, ok, err)
	}
}

func TestCompleteDeviceLoginTwoManagersSharingStoreConcurrent(t *testing.T) {
	fp1 := newFakeProvider(t, `{"access_token":"token-1"}`)
	fp2 := newFakeProvider(t, `{"access_token":"token-2"}`)
	storePath := filepath.Join(t.TempDir(), "tokens.json")
	// Two independent Store values over the same file exercise the cross-process
	// file lock (fileBlob.withLock), not just the in-process Store.mu, which is
	// what two concurrent Zero processes actually rely on.
	store1, err := NewStore(StoreOptions{FilePath: storePath})
	if err != nil {
		t.Fatal(err)
	}
	store2, err := NewStore(StoreOptions{FilePath: storePath})
	if err != nil {
		t.Fatal(err)
	}

	m1, err := NewManager(ManagerOptions{Store: store1})
	if err != nil {
		t.Fatal(err)
	}
	m2, err := NewManager(ManagerOptions{Store: store2})
	if err != nil {
		t.Fatal(err)
	}

	cfg1 := Config{ClientID: "client1", TokenEndpoint: fp1.server.URL + "/token"}
	auth1 := DeviceAuth{DeviceCode: "dc1", Interval: 5 * time.Millisecond, ExpiresAt: time.Now().Add(5 * time.Second)}

	cfg2 := Config{ClientID: "client2", TokenEndpoint: fp2.server.URL + "/token"}
	auth2 := DeviceAuth{DeviceCode: "dc2", Interval: 5 * time.Millisecond, ExpiresAt: time.Now().Add(5 * time.Second)}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = m1.CompleteDeviceLogin(context.Background(), "prov1", cfg1, auth1)
	}()
	go func() {
		defer wg.Done()
		_, _ = m2.CompleteDeviceLogin(context.Background(), "prov2", cfg2, auth2)
	}()
	wg.Wait()

	tok1, ok1, err1 := store2.Load(ProviderKey("prov1"))
	tok2, ok2, err2 := store2.Load(ProviderKey("prov2"))
	if err1 != nil || !ok1 || tok1.AccessToken != "token-1" {
		t.Fatalf("prov1: tok=%+v ok=%v err=%v", tok1, ok1, err1)
	}
	if err2 != nil || !ok2 || tok2.AccessToken != "token-2" {
		t.Fatalf("prov2: tok=%+v ok=%v err=%v", tok2, ok2, err2)
	}
}
