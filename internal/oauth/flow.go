package oauth

import (
	"context"
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

const tokenResponseLimit = 1 << 20 // 1 MiB cap on token-endpoint bodies

// tokenResponse is the JSON returned by a token endpoint.
type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	TokenType        string `json:"token_type"`
	ExpiresIn        int64  `json:"expires_in"`
	Scope            string `json:"scope"`
	IDToken          string `json:"id_token"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// BuildAuthorizationURL constructs the authorization request URL for an
// authorization-code + PKCE flow. PKCE S256 is always included; a non-S256
// method is refused (ErrPKCEDowngrade). extraParams (and cfg.ExtraAuthParams)
// are appended last.
func BuildAuthorizationURL(cfg Config, pkce PKCE, state, redirectURI string, extraParams map[string]string) (string, error) {
	if pkce.Method != MethodS256 {
		return "", ErrPKCEDowngrade
	}
	endpoint := trimmed(cfg.AuthorizationEndpoint)
	if endpoint == "" {
		return "", errors.New("oauth: no authorization endpoint configured")
	}
	// Backstop: validate the authorization endpoint at the shared choke point
	// both the provider and MCP flows build their browser URL through, so a
	// discovery-downgraded endpoint can never open in the browser even if a
	// merge site missed it.
	if err := ValidateEndpointURL(endpoint); err != nil {
		return "", err
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("oauth: parse authorization endpoint: %w", err)
	}
	query := parsed.Query()
	query.Set("response_type", "code")
	query.Set("client_id", cfg.ClientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("state", state)
	query.Set("code_challenge", pkce.Challenge)
	query.Set("code_challenge_method", pkce.Method)
	if len(cfg.Scopes) > 0 {
		query.Set("scope", strings.Join(cfg.Scopes, " "))
	}
	// Extra params must never override the reserved OAuth/PKCE fields above
	// (e.g. forcing code_challenge_method=plain or rewriting state/redirect_uri),
	// which would break the flow's security guarantees.
	for k, v := range cfg.ExtraAuthParams {
		if isReservedAuthParam(k) {
			continue
		}
		query.Set(k, v)
	}
	for k, v := range extraParams {
		if isReservedAuthParam(k) {
			continue
		}
		query.Set(k, v)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

// isReservedAuthParam reports whether a query key is one the engine controls and
// must not be overridable by caller-supplied extra params.
func isReservedAuthParam(key string) bool {
	switch key {
	case "response_type", "client_id", "redirect_uri", "state", "code_challenge", "code_challenge_method":
		return true
	default:
		return false
	}
}

// ValidateEndpointURL refuses an OAuth endpoint that is not https, unless it is
// a loopback host (mirrors oauth2-client.js validateTokenEndpointURL). Every
// credential-bearing endpoint — configured OR learned from discovery — must
// pass this single rule, so discovery metadata can never downgrade a login to
// cleartext or redirect it to an attacker-controlled http origin.
func ValidateEndpointURL(endpoint string) error {
	parsed, err := url.Parse(trimmed(endpoint))
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("oauth: invalid endpoint %q", endpoint)
	}
	if parsed.Scheme == "https" {
		return nil
	}
	if parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname()) {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrInsecureTokenEndpoint, parsed.Scheme+"://"+parsed.Host)
}

// validateTokenEndpoint checks a token endpoint against the shared endpoint rule
// (kept as a named helper for the token-exchange and device call sites). This
// prevents leaking codes/tokens over cleartext.
func validateTokenEndpoint(endpoint string) error {
	return ValidateEndpointURL(endpoint)
}

// withoutRedirects returns a shallow copy of client whose redirect policy
// refuses to follow ANY redirect. A credential-bearing OAuth POST (token
// exchange/refresh, device authorization, device-token poll) must never let a
// 307/308 replay its form body — codes, PKCE verifiers, refresh tokens, client
// secrets — to a target that was never endpoint-validated. The caller's client
// is left unmodified.
func withoutRedirects(client *http.Client) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}
	copied := *client
	copied.CheckRedirect = func(*http.Request, []*http.Request) error {
		return ErrUnsafeRedirect
	}
	return &copied
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// ExchangeCode swaps an authorization code + PKCE verifier for tokens.
func ExchangeCode(ctx context.Context, client *http.Client, cfg Config, code, verifier, redirectURI string, now func() time.Time) (Token, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", cfg.ClientID)
	form.Set("code_verifier", verifier)
	if secret := trimmed(cfg.ClientSecret); secret != "" {
		form.Set("client_secret", secret)
	}
	return PostToken(ctx, client, cfg.TokenEndpoint, form, Token{Scopes: cfg.Scopes}, now)
}

// Refresh exchanges a refresh token for a fresh access token. A response that
// omits a new refresh token preserves the current one.
func Refresh(ctx context.Context, client *http.Client, cfg Config, current Token, now func() time.Time) (Token, error) {
	refresh := trimmed(current.RefreshToken)
	if refresh == "" {
		return Token{}, ErrNoRefreshToken
	}
	if trimmed(cfg.TokenEndpoint) == "" {
		return Token{}, errors.New("oauth: no token endpoint configured for refresh")
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refresh)
	form.Set("client_id", cfg.ClientID)
	if secret := trimmed(cfg.ClientSecret); secret != "" {
		form.Set("client_secret", secret)
	}
	if len(cfg.Scopes) > 0 {
		form.Set("scope", strings.Join(cfg.Scopes, " "))
	}
	scopes := current.Scopes
	if len(scopes) == 0 {
		scopes = cfg.Scopes
	}
	base := Token{Scopes: scopes, RefreshToken: refresh, Account: current.Account, IDToken: current.IDToken, TokenType: current.TokenType}
	return PostToken(ctx, client, cfg.TokenEndpoint, form, base, now)
}

// PostToken performs a token-endpoint POST and maps the response onto a Token.
// The https guard is applied first. The base token supplies values to preserve
// (e.g. an existing refresh token or scopes) when the response omits them.
// Error messages carry only the server's error/error_description — never the raw
// body — so token material in an unexpected payload is not leaked.
func PostToken(ctx context.Context, client *http.Client, tokenEndpoint string, form url.Values, base Token, now func() time.Time) (Token, error) {
	if err := validateTokenEndpoint(tokenEndpoint); err != nil {
		return Token{}, err
	}
	// Refuse redirects so a 307/308 from the token endpoint can't replay this
	// credential-bearing POST body to another (unvalidated) origin.
	client = withoutRedirects(client)
	if now == nil {
		now = time.Now
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, trimmed(tokenEndpoint), strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")

	response, err := client.Do(request)
	if err != nil {
		return Token{}, fmt.Errorf("oauth: token request failed: %w", err)
	}
	defer response.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(response.Body, tokenResponseLimit))
	var parsed tokenResponse
	if len(body) > 0 {
		_ = json.Unmarshal(body, &parsed)
	}

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		if parsed.Error != "" {
			if parsed.ErrorDescription != "" {
				return Token{}, fmt.Errorf("oauth: token endpoint error %q: %s", parsed.Error, parsed.ErrorDescription)
			}
			return Token{}, fmt.Errorf("oauth: token endpoint error %q", parsed.Error)
		}
		return Token{}, fmt.Errorf("oauth: token endpoint returned HTTP %d", response.StatusCode)
	}
	if trimmed(parsed.AccessToken) == "" {
		return Token{}, errors.New("oauth: token endpoint returned no access token")
	}

	token := base
	token.AccessToken = parsed.AccessToken
	if trimmed(parsed.RefreshToken) != "" {
		token.RefreshToken = parsed.RefreshToken
	}
	if trimmed(parsed.TokenType) != "" {
		token.TokenType = parsed.TokenType
	}
	if parsed.ExpiresIn > 0 {
		token.ExpiresAt = now().Add(time.Duration(parsed.ExpiresIn) * time.Second).UTC()
	}
	if scope := trimmed(parsed.Scope); scope != "" {
		token.Scopes = strings.Fields(scope)
	}
	// A new ID token is honored on every exchange (login and refresh) — both
	// can rotate it. The previous ID token is preserved when the response omits
	// one, so a refresh that returns a new access token without a new id_token
	// (the common case) doesn't drop the chatgpt_account_id claim.
	if trimmed(parsed.IDToken) != "" {
		token.IDToken = parsed.IDToken
	}
	return token, nil
}
