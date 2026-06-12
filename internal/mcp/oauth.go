package mcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ServerAuthOAuth is the value of an MCP server's auth field that selects the
// OAuth 2.0 + PKCE authorization-code flow.
const ServerAuthOAuth = "oauth"

const (
	wellKnownAuthServerPath = "/.well-known/oauth-authorization-server"
	defaultLoginTimeout     = 3 * time.Minute
	// pkceVerifierBytes yields a 43-character base64url verifier, the minimum
	// length permitted by the PKCE spec while remaining high-entropy.
	pkceVerifierBytes = 32
	stateBytes        = 32
)

// OAuthConfig describes how to authenticate to a remote MCP server using OAuth.
// Endpoints may be discovered from the server's metadata document; explicit
// values here override or fill in anything discovery cannot provide.
type OAuthConfig struct {
	ClientID              string
	ClientSecret          string
	Scopes                []string
	AuthorizationEndpoint string
	TokenEndpoint         string
	RegistrationEndpoint  string
	// IssuerURL overrides the base URL used for metadata discovery. When empty
	// the MCP server URL is used.
	IssuerURL string
}

// authServerMetadata is the subset of the OAuth 2.0 authorization server
// metadata document that the flow consumes.
type authServerMetadata struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	RegistrationEndpoint  string   `json:"registration_endpoint"`
	ScopesSupported       []string `json:"scopes_supported"`
}

// pkceParams holds a PKCE verifier/challenge pair.
type pkceParams struct {
	Verifier  string
	Challenge string
	Method    string
}

// LoginOptions configures a single interactive authorization-code login.
type LoginOptions struct {
	ServerName string
	ServerURL  string
	Config     OAuthConfig
	HTTPClient *http.Client
	// OpenBrowser is invoked with the authorization URL. The default prints the
	// URL; tests inject a function that drives the loopback redirect.
	OpenBrowser func(authURL string) error
	Timeout     time.Duration
	Now         func() time.Time
}

// authorizationFlow carries the per-login state shared across helpers.
type authorizationFlow struct {
	httpClient *http.Client
	metadata   authServerMetadata
	config     OAuthConfig
	pkce       pkceParams
	state      string
	now        func() time.Time
}

// discoverAuthorizationServer fetches the OAuth 2.0 authorization server
// metadata document published at the well-known path under the given base URL.
func discoverAuthorizationServer(ctx context.Context, client *http.Client, baseURL string) (authServerMetadata, error) {
	if client == nil {
		client = http.DefaultClient
	}
	metadataURL, err := joinWellKnown(baseURL)
	if err != nil {
		return authServerMetadata{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return authServerMetadata{}, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return authServerMetadata{}, fmt.Errorf("fetch authorization server metadata: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return authServerMetadata{}, fmt.Errorf("authorization server metadata returned HTTP %d", response.StatusCode)
	}
	var metadata authServerMetadata
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&metadata); err != nil {
		return authServerMetadata{}, fmt.Errorf("decode authorization server metadata: %w", err)
	}
	return metadata, nil
}

// resolveAuthorizationServer discovers metadata and applies explicit config
// overrides. Configured endpoints take precedence over discovered ones, and act
// as a fallback when discovery fails or omits a value.
func resolveAuthorizationServer(ctx context.Context, client *http.Client, baseURL string, cfg OAuthConfig) (authServerMetadata, error) {
	discoveryBase := strings.TrimSpace(cfg.IssuerURL)
	if discoveryBase == "" {
		discoveryBase = baseURL
	}

	metadata, err := discoverAuthorizationServer(ctx, client, discoveryBase)
	if err != nil {
		// Discovery failures are non-fatal when the config supplies the endpoints
		// directly; otherwise surface the discovery error.
		metadata = authServerMetadata{}
	}

	if endpoint := strings.TrimSpace(cfg.AuthorizationEndpoint); endpoint != "" {
		metadata.AuthorizationEndpoint = endpoint
	}
	if endpoint := strings.TrimSpace(cfg.TokenEndpoint); endpoint != "" {
		metadata.TokenEndpoint = endpoint
	}
	if endpoint := strings.TrimSpace(cfg.RegistrationEndpoint); endpoint != "" {
		metadata.RegistrationEndpoint = endpoint
	}

	if strings.TrimSpace(metadata.AuthorizationEndpoint) == "" {
		return authServerMetadata{}, errors.New("no authorization endpoint discovered or configured")
	}
	if strings.TrimSpace(metadata.TokenEndpoint) == "" {
		return authServerMetadata{}, errors.New("no token endpoint discovered or configured")
	}
	return metadata, nil
}

// newPKCE generates a high-entropy code verifier and its S256 challenge.
func newPKCE() (pkceParams, error) {
	verifierRaw := make([]byte, pkceVerifierBytes)
	if _, err := rand.Read(verifierRaw); err != nil {
		return pkceParams{}, fmt.Errorf("generate PKCE verifier: %w", err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(verifierRaw)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	return pkceParams{Verifier: verifier, Challenge: challenge, Method: "S256"}, nil
}

func newState() (string, error) {
	raw := make([]byte, stateBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// authorizationURL builds the authorization request URL.
func (flow *authorizationFlow) authorizationURL(redirectURI string) (string, error) {
	parsed, err := url.Parse(flow.metadata.AuthorizationEndpoint)
	if err != nil {
		return "", fmt.Errorf("parse authorization endpoint: %w", err)
	}
	query := parsed.Query()
	query.Set("response_type", "code")
	query.Set("client_id", flow.config.ClientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("state", flow.state)
	query.Set("code_challenge", flow.pkce.Challenge)
	query.Set("code_challenge_method", flow.pkce.Method)
	if len(flow.config.Scopes) > 0 {
		query.Set("scope", strings.Join(flow.config.Scopes, " "))
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

// parseCallback validates the redirect query and returns the authorization
// code. It rejects a mismatched state (CSRF) and surfaces provider errors.
func (flow *authorizationFlow) parseCallback(values url.Values) (string, error) {
	if got := values.Get("state"); got != flow.state {
		return "", errors.New("OAuth callback state mismatch: possible CSRF, login aborted")
	}
	if providerErr := strings.TrimSpace(values.Get("error")); providerErr != "" {
		description := strings.TrimSpace(values.Get("error_description"))
		if description != "" {
			return "", fmt.Errorf("authorization server returned error %q: %s", providerErr, description)
		}
		return "", fmt.Errorf("authorization server returned error %q", providerErr)
	}
	code := strings.TrimSpace(values.Get("code"))
	if code == "" {
		return "", errors.New("OAuth callback missing authorization code")
	}
	return code, nil
}

// exchangeCode swaps an authorization code + PKCE verifier for tokens.
func (flow *authorizationFlow) exchangeCode(ctx context.Context, code string, redirectURI string) (StoredToken, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", flow.config.ClientID)
	form.Set("code_verifier", flow.pkce.Verifier)
	if secret := strings.TrimSpace(flow.config.ClientSecret); secret != "" {
		form.Set("client_secret", secret)
	}
	return postTokenRequest(ctx, flow.httpClient, flow.metadata.TokenEndpoint, form, StoredToken{Scopes: flow.config.Scopes}, flow.now)
}

// refreshAccessToken exchanges a refresh token for a fresh access token. A
// response that omits a new refresh token preserves the previous one.
func refreshAccessToken(ctx context.Context, client *http.Client, cfg OAuthConfig, current StoredToken, now func() time.Time) (StoredToken, error) {
	refresh := strings.TrimSpace(current.RefreshToken)
	if refresh == "" {
		return StoredToken{}, errors.New("no refresh token available")
	}
	tokenEndpoint := strings.TrimSpace(cfg.TokenEndpoint)
	if tokenEndpoint == "" {
		return StoredToken{}, errors.New("no token endpoint configured for refresh")
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refresh)
	form.Set("client_id", cfg.ClientID)
	if secret := strings.TrimSpace(cfg.ClientSecret); secret != "" {
		form.Set("client_secret", secret)
	}
	if len(cfg.Scopes) > 0 {
		form.Set("scope", strings.Join(cfg.Scopes, " "))
	}
	refreshed, err := postTokenRequest(ctx, client, tokenEndpoint, form, StoredToken{Scopes: current.Scopes, RefreshToken: refresh}, now)
	if err != nil {
		return StoredToken{}, err
	}
	return refreshed, nil
}

// tokenResponse is the JSON returned by the token endpoint.
type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	TokenType        string `json:"token_type"`
	ExpiresIn        int64  `json:"expires_in"`
	Scope            string `json:"scope"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// postTokenRequest performs a token endpoint POST and maps the response onto a
// StoredToken. The base token supplies values to preserve (e.g. an existing
// refresh token or scope set) when the response omits them.
func postTokenRequest(ctx context.Context, client *http.Client, tokenEndpoint string, form url.Values, base StoredToken, now func() time.Time) (StoredToken, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if now == nil {
		now = time.Now
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return StoredToken{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")

	response, err := client.Do(request)
	if err != nil {
		return StoredToken{}, fmt.Errorf("token request failed: %w", err)
	}
	defer response.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	var parsed tokenResponse
	if len(body) > 0 {
		// A malformed body on an error status is reported as a status error below.
		_ = json.Unmarshal(body, &parsed)
	}

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		if parsed.Error != "" {
			if parsed.ErrorDescription != "" {
				return StoredToken{}, fmt.Errorf("token endpoint error %q: %s", parsed.Error, parsed.ErrorDescription)
			}
			return StoredToken{}, fmt.Errorf("token endpoint error %q", parsed.Error)
		}
		return StoredToken{}, fmt.Errorf("token endpoint returned HTTP %d", response.StatusCode)
	}
	if strings.TrimSpace(parsed.AccessToken) == "" {
		return StoredToken{}, errors.New("token endpoint returned no access token")
	}

	token := base
	token.AccessToken = parsed.AccessToken
	if strings.TrimSpace(parsed.RefreshToken) != "" {
		token.RefreshToken = parsed.RefreshToken
	}
	if strings.TrimSpace(parsed.TokenType) != "" {
		token.TokenType = parsed.TokenType
	}
	if parsed.ExpiresIn > 0 {
		token.ExpiresAt = now().Add(time.Duration(parsed.ExpiresIn) * time.Second).UTC()
	}
	if scope := strings.TrimSpace(parsed.Scope); scope != "" {
		token.Scopes = strings.Fields(scope)
	}
	return token, nil
}

// registerClient performs dynamic client registration against the registration
// endpoint and returns the issued client_id and optional client_secret.
func registerClient(ctx context.Context, client *http.Client, registrationEndpoint string, redirectURI string, scopes []string) (string, string, error) {
	if client == nil {
		client = http.DefaultClient
	}
	payload := map[string]any{
		"client_name":                "zero",
		"redirect_uris":              []string{redirectURI},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	}
	if len(scopes) > 0 {
		payload["scope"] = strings.Join(scopes, " ")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, registrationEndpoint, strings.NewReader(string(body)))
	if err != nil {
		return "", "", err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")

	response, err := client.Do(request)
	if err != nil {
		return "", "", fmt.Errorf("client registration failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", "", fmt.Errorf("client registration returned HTTP %d", response.StatusCode)
	}
	var registered struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&registered); err != nil {
		return "", "", fmt.Errorf("decode client registration response: %w", err)
	}
	if strings.TrimSpace(registered.ClientID) == "" {
		return "", "", errors.New("client registration returned no client_id")
	}
	return registered.ClientID, registered.ClientSecret, nil
}

// Login runs the full OAuth 2.0 + PKCE authorization-code flow: it discovers (or
// falls back to configured) endpoints, optionally registers a client, starts a
// loopback redirect listener, opens the authorization URL, validates the
// callback state, and exchanges the code for tokens. Tokens are returned and are
// never logged.
func Login(ctx context.Context, options LoginOptions) (StoredToken, error) {
	if err := ValidateServerName(options.ServerName); err != nil {
		return StoredToken{}, err
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = defaultLoginTimeout
	}
	// One deadline bounds the WHOLE interactive login — discovery, optional client
	// registration, the callback wait, and the code exchange — so a hung
	// metadata/registration/token endpoint can't block the command forever (the
	// CLI passes a non-cancelable context).
	loginCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cfg := options.Config
	metadata, err := resolveAuthorizationServer(loginCtx, httpClient, options.ServerURL, cfg)
	if err != nil {
		return StoredToken{}, err
	}

	// Bind the loopback redirect listener first so the redirect URI is known
	// before client registration and authorization URL construction.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return StoredToken{}, fmt.Errorf("start loopback redirect listener: %w", err)
	}
	defer listener.Close()
	redirectURI := fmt.Sprintf("http://%s/callback", listener.Addr().String())

	if strings.TrimSpace(cfg.ClientID) == "" {
		if registration := strings.TrimSpace(metadata.RegistrationEndpoint); registration != "" {
			clientID, clientSecret, regErr := registerClient(loginCtx, httpClient, registration, redirectURI, cfg.Scopes)
			if regErr != nil {
				return StoredToken{}, regErr
			}
			cfg.ClientID = clientID
			if clientSecret != "" {
				cfg.ClientSecret = clientSecret
			}
		}
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		return StoredToken{}, errors.New("no client_id configured and dynamic registration unavailable")
	}

	pkce, err := newPKCE()
	if err != nil {
		return StoredToken{}, err
	}
	state, err := newState()
	if err != nil {
		return StoredToken{}, err
	}

	flow := &authorizationFlow{
		httpClient: httpClient,
		metadata:   metadata,
		config:     cfg,
		pkce:       pkce,
		state:      state,
		now:        now,
	}

	authURL, err := flow.authorizationURL(redirectURI)
	if err != nil {
		return StoredToken{}, err
	}

	type callbackResult struct {
		code string
		err  error
	}
	resultChan := make(chan callbackResult, 1)
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/callback" {
				http.NotFound(w, r)
				return
			}
			code, parseErr := flow.parseCallback(r.URL.Query())
			if parseErr != nil {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, "Authorization failed. You may close this window.")
			} else {
				_, _ = io.WriteString(w, "Authorization complete. You may close this window.")
			}
			select {
			case resultChan <- callbackResult{code: code, err: parseErr}:
			default:
			}
		}),
	}
	go func() { _ = server.Serve(listener) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	open := options.OpenBrowser
	if open == nil {
		open = func(string) error { return nil }
	}
	if err := open(authURL); err != nil {
		return StoredToken{}, fmt.Errorf("open authorization URL: %w", err)
	}

	select {
	case result := <-resultChan:
		if result.err != nil {
			return StoredToken{}, result.err
		}
		token, err := flow.exchangeCode(loginCtx, result.code, redirectURI)
		if err != nil {
			return StoredToken{}, err
		}
		return token, nil
	case <-loginCtx.Done():
		return StoredToken{}, fmt.Errorf("timed out waiting for OAuth authorization callback: %w", loginCtx.Err())
	}
}

func joinWellKnown(baseURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("invalid base URL for metadata discovery: %q", baseURL)
	}
	// RFC 8414: the well-known segment is inserted between the host and any issuer
	// path component, so a path-based issuer (https://host/tenant-a) is probed at
	// https://host/.well-known/oauth-authorization-server/tenant-a — not at the
	// host root (which would break tenant-scoped discovery).
	issuerPath := strings.Trim(parsed.Path, "/")
	parsed.Path = wellKnownAuthServerPath
	if issuerPath != "" {
		parsed.Path = strings.TrimRight(wellKnownAuthServerPath, "/") + "/" + issuerPath
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}
