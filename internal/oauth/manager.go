package oauth

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// defaultRefreshBuffer refreshes a token this long before its hard expiry.
const defaultRefreshBuffer = 60 * time.Second

const oidcWellKnownPath = "/.well-known/openid-configuration"

// Manager ties the token store, provider registry, and HTTP client together to
// run logins and serve fresh access tokens. It is the high-level entrypoint the
// CLI and request paths use.
type Manager struct {
	store    *Store
	registry *Registry
	client   *http.Client
	env      map[string]string
	now      func() time.Time
	buffer   time.Duration
	out      io.Writer
	// openBrowser is invoked with the authorization URL for loopback logins.
	// Tests inject a function that drives the loopback redirect.
	openBrowser func(authURL string) error
}

// ManagerOptions configures a Manager.
type ManagerOptions struct {
	Store         *Store
	Registry      *Registry
	HTTPClient    *http.Client
	Env           map[string]string
	Now           func() time.Time
	RefreshBuffer time.Duration
	Out           io.Writer
	OpenBrowser   func(authURL string) error
}

// NewManager builds a Manager, filling defaults.
func NewManager(opts ManagerOptions) (*Manager, error) {
	if opts.Store == nil {
		return nil, fmt.Errorf("oauth: manager requires a store")
	}
	registry := opts.Registry
	if registry == nil {
		registry = NewRegistry()
	}
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	buffer := opts.RefreshBuffer
	if buffer <= 0 {
		buffer = defaultRefreshBuffer
	}
	out := opts.Out
	if out == nil {
		out = io.Discard
	}
	open := opts.OpenBrowser
	if open == nil {
		open = func(string) error { return nil }
	}
	return &Manager{
		store: opts.Store, registry: registry, client: client,
		env: opts.Env, now: now, buffer: buffer, out: out, openBrowser: open,
	}, nil
}

// LoginOptions configures a single provider login.
type LoginOptions struct {
	Provider    string
	Device      bool          // force device-code flow
	ExtraScopes []string      // appended to the provider's scopes
	Timeout     time.Duration // bounds the whole interactive login
}

// Login runs the provider login (loopback by default, device-code when
// requested or when the provider only supports device), stores the token under
// "provider:<name>", and returns a redaction-safe status.
func (m *Manager) Login(ctx context.Context, opts LoginOptions) (Status, error) {
	cfg, flow, err := m.registry.ResolveConfig(opts.Provider, m.env)
	if err != nil {
		return Status{}, err
	}
	if len(opts.ExtraScopes) > 0 {
		cfg.Scopes = append(append([]string{}, cfg.Scopes...), opts.ExtraScopes...)
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	loginCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cfg, err = m.resolveEndpoints(loginCtx, cfg)
	if err != nil {
		return Status{}, err
	}

	useDevice := opts.Device || flow == FlowDevice
	var token Token
	if useDevice {
		token, err = m.loginDevice(loginCtx, cfg)
	} else {
		token, err = m.loginLoopback(loginCtx, cfg)
	}
	if err != nil {
		return Status{}, err
	}

	key := ProviderKey(opts.Provider)
	if err := m.store.Save(key, token); err != nil {
		return Status{}, err
	}
	return m.statusFor(key)
}

// resolveEndpoints fills missing authorize/token/device endpoints from issuer
// discovery (RFC 8414 then OIDC), leaving any explicitly-pinned endpoint intact.
func (m *Manager) resolveEndpoints(ctx context.Context, cfg Config) (Config, error) {
	if trimmed(cfg.IssuerURL) == "" {
		return cfg, nil
	}
	if cfg.AuthorizationEndpoint != "" && cfg.TokenEndpoint != "" && cfg.DeviceAuthorizationEndpoint != "" {
		return cfg, nil
	}
	// Discovery is best-effort: a failure is non-fatal because pinned endpoints
	// may already be sufficient for the chosen flow. Only merge on success (and
	// never return from the error branch, which would trip the nilerr linter).
	if meta, err := m.discover(ctx, cfg.IssuerURL); err == nil {
		if cfg.AuthorizationEndpoint == "" {
			cfg.AuthorizationEndpoint = meta.AuthorizationEndpoint
		}
		if cfg.TokenEndpoint == "" {
			cfg.TokenEndpoint = meta.TokenEndpoint
		}
		if cfg.DeviceAuthorizationEndpoint == "" {
			cfg.DeviceAuthorizationEndpoint = meta.DeviceAuthorizationEndpoint
		}
	}
	return cfg, nil
}

// discover tries the OAuth (RFC 8414) well-known path, then the OIDC
// openid-configuration path (some issuers publish only the latter).
func (m *Manager) discover(ctx context.Context, issuer string) (ServerMetadata, error) {
	meta, err := DiscoverAuthorizationServer(ctx, m.client, issuer)
	if err == nil && (meta.AuthorizationEndpoint != "" || meta.TokenEndpoint != "") {
		return meta, nil
	}
	oidcURL := strings.TrimRight(strings.TrimSpace(issuer), "/") + oidcWellKnownPath
	return fetchMetadata(ctx, m.client, oidcURL)
}

func (m *Manager) loginLoopback(ctx context.Context, cfg Config) (Token, error) {
	if trimmed(cfg.AuthorizationEndpoint) == "" {
		return Token{}, fmt.Errorf("oauth: no authorization endpoint (set the authorize URL or a discoverable issuer)")
	}
	state, err := NewState()
	if err != nil {
		return Token{}, err
	}
	pkce, err := NewPKCE()
	if err != nil {
		return Token{}, err
	}
	listener, err := NewLoopbackListener(state)
	if err != nil {
		return Token{}, err
	}
	defer listener.Close()
	redirectURI := listener.RedirectURI()
	authURL, err := BuildAuthorizationURL(cfg, pkce, state, redirectURI, nil)
	if err != nil {
		return Token{}, err
	}
	fmt.Fprintf(m.out, "Open this URL to authorize:\n  %s\n", authURL)
	if err := m.openBrowser(authURL); err != nil {
		return Token{}, fmt.Errorf("oauth: open authorization URL: %w", err)
	}
	code, err := listener.Wait(ctx)
	if err != nil {
		return Token{}, err
	}
	return ExchangeCode(ctx, m.client, cfg, code, pkce.Verifier, redirectURI, m.now)
}

func (m *Manager) loginDevice(ctx context.Context, cfg Config) (Token, error) {
	auth, err := RequestDeviceCode(ctx, m.client, cfg, m.now)
	if err != nil {
		return Token{}, err
	}
	target := auth.VerificationURIComplete
	if target == "" {
		target = auth.VerificationURI
	}
	// user_code is meant to be displayed to the user; it is not a secret.
	fmt.Fprintf(m.out, "To authorize, visit:\n  %s\nand enter code: %s\n", target, auth.UserCode)
	return PollDeviceToken(ctx, m.client, cfg, auth, m.now)
}

// GetFresh returns a valid access token for key, refreshing on-demand if the
// stored token is expired or within the refresh buffer. Mirrors
// checkAndRefreshOAuthTokenIfNeeded.
func (m *Manager) GetFresh(ctx context.Context, key string) (string, error) {
	token, cfg, err := m.loadForKey(key)
	if err != nil {
		return "", err
	}
	if !token.NeedsRefresh(m.now(), m.buffer) {
		return token.AccessToken, nil
	}
	return m.refreshAndSave(ctx, key, cfg, token)
}

// Handle401 forces a refresh after an upstream 401, returning the new access
// token. Mirrors handleOAuth401Error.
func (m *Manager) Handle401(ctx context.Context, key string) (string, error) {
	token, cfg, err := m.loadForKey(key)
	if err != nil {
		return "", err
	}
	return m.refreshAndSave(ctx, key, cfg, token)
}

func (m *Manager) refreshAndSave(ctx context.Context, key string, cfg Config, current Token) (string, error) {
	refreshed, err := Refresh(ctx, m.client, cfg, current, m.now)
	if err != nil {
		return "", err
	}
	if err := m.store.Save(key, refreshed); err != nil {
		return "", err
	}
	return refreshed.AccessToken, nil
}

// loadForKey loads a stored token and resolves the provider config for its key.
func (m *Manager) loadForKey(key string) (Token, Config, error) {
	if err := ValidateKey(key); err != nil {
		return Token{}, Config{}, err
	}
	token, ok, err := m.store.Load(key)
	if err != nil {
		return Token{}, Config{}, err
	}
	if !ok {
		return Token{}, Config{}, fmt.Errorf("oauth: no stored token for %q", key)
	}
	name := strings.TrimPrefix(key, KeyPrefixProvider)
	if name == key {
		return Token{}, Config{}, fmt.Errorf("oauth: refresh is only supported for provider tokens (got %q)", key)
	}
	cfg, _, err := m.registry.ResolveConfig(name, m.env)
	if err != nil {
		return Token{}, Config{}, err
	}
	return token, cfg, nil
}

// Logout removes a provider's stored token, reporting whether one was present.
func (m *Manager) Logout(name string) (bool, error) {
	return m.store.Delete(ProviderKey(name))
}

// StatusAll returns the status of every provider login.
func (m *Manager) StatusAll() ([]Status, error) {
	return m.store.Status(KeyPrefixProvider)
}

func (m *Manager) statusFor(key string) (Status, error) {
	statuses, err := m.store.Status(KeyPrefixProvider)
	if err != nil {
		return Status{}, err
	}
	for _, st := range statuses {
		if st.Key == key {
			return st, nil
		}
	}
	return Status{Key: key}, nil
}
