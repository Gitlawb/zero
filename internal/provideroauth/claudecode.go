// Claude Code token refresh. See openrouter.go for the package doc.
package provideroauth

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Gitlawb/zero/internal/oauth"
)

// claudeCodeClientID is Claude Code's public OAuth client id. Tokens minted for
// this client (the ones the claude CLI stores locally) can only be refreshed
// against it; it is a public identifier by design (the CLI ships it), not a
// secret.
const claudeCodeClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"

// claudeCodeTokenEndpoint is Anthropic's OAuth token endpoint for the Claude
// Code client. (The former console.anthropic.com path now 404s — the console
// moved to platform.claude.com; api.anthropic.com serves the same endpoint.)
const claudeCodeTokenEndpoint = "https://platform.claude.com/v1/oauth/token"

// claudeCodeUserAgent identifies the refresh request. The endpoint sits behind
// Cloudflare rules that reject bare default library user agents (error 1010),
// so an explicit product string is load-bearing, not cosmetic.
const claudeCodeUserAgent = "zero-cli (claude-code-token-refresh)"

// claudeCodeAuthorizeURL is the browser page where the user approves the
// Claude Code client. With code=true the page displays a "code#state" string
// to paste back (no loopback server needed).
const claudeCodeAuthorizeURL = "https://claude.ai/oauth/authorize"

// claudeCodeRedirectURI is the registered redirect for the paste-code flow;
// the callback page renders the code for copying rather than delivering it to
// a local listener.
const claudeCodeRedirectURI = "https://platform.claude.com/oauth/code/callback"

// claudeCodeScopes are the scopes Claude Code itself requests; user:inference
// is the one the completion API needs.
const claudeCodeScopes = "org:create_api_key user:profile user:inference"

// ClaudeCodeLoginOptions configures ClaudeCodeLogin.
type ClaudeCodeLoginOptions struct {
	// HTTPClient performs the token exchange; nil => a client with a sane timeout.
	HTTPClient *http.Client
	// Endpoint overrides the token endpoint; "" => the real endpoint. For tests.
	Endpoint string
	// Out receives the "open this URL" and prompt lines; nil discards them.
	Out io.Writer
	// In supplies the pasted "code#state" line; nil => os.Stdin.
	In io.Reader
	// OpenBrowser is invoked with the authorize URL; nil => URL is only printed.
	OpenBrowser func(authURL string) error
	// Now is the time source; nil => time.Now. For tests.
	Now func() time.Time
	// randReader overrides crypto/rand for deterministic tests.
	randReader io.Reader
}

// ClaudeCodeLogin runs the "Sign in with Claude" paste-code PKCE flow against
// the Claude Code public client and returns the minted token chain. Zero runs
// this flow ITSELF (rather than borrowing the claude CLI's stored login)
// because newer CLI versions keep their live chain in per-install keychain
// entries that cannot be discovered, leaving only dead chains readable — a
// login zero owns, stored in zero's own token store, is refreshable forever
// via RefreshClaudeCode. Returning the token keeps the function composable,
// mirroring ChatGPTLogin; the caller persists it.
func ClaudeCodeLogin(ctx context.Context, opts ClaudeCodeLoginOptions) (oauth.Token, error) {
	out := opts.Out
	if out == nil {
		out = io.Discard
	}
	in := opts.In
	if in == nil {
		in = os.Stdin
	}
	randSource := opts.randReader
	if randSource == nil {
		randSource = rand.Reader
	}

	verifier, challenge, err := pkcePair(randSource)
	if err != nil {
		return oauth.Token{}, fmt.Errorf("claude code login: %w", err)
	}
	state, err := randomURLSafe(randSource, 32)
	if err != nil {
		return oauth.Token{}, fmt.Errorf("claude code login: %w", err)
	}

	query := url.Values{
		"code":                  {"true"},
		"client_id":             {claudeCodeClientID},
		"response_type":         {"code"},
		"redirect_uri":          {claudeCodeRedirectURI},
		"scope":                 {claudeCodeScopes},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
	}
	authURL := claudeCodeAuthorizeURL + "?" + query.Encode()
	fmt.Fprintf(out, "Open this URL to sign in with Claude:\n\n  %s\n\nThen paste the code shown after approval.\n", authURL)
	if opts.OpenBrowser != nil {
		_ = opts.OpenBrowser(authURL)
	}
	fmt.Fprint(out, "Paste code here: ")

	line, err := readTrimmedLine(ctx, in)
	if err != nil {
		return oauth.Token{}, fmt.Errorf("claude code login: read code: %w", err)
	}
	code, returnedState := line, ""
	if hash := strings.IndexByte(line, '#'); hash >= 0 {
		code, returnedState = line[:hash], line[hash+1:]
	}
	if strings.TrimSpace(code) == "" {
		return oauth.Token{}, fmt.Errorf("claude code login: empty code")
	}
	// The pasted blob carries the state back; a mismatch means the paste came
	// from a different (possibly attacker-initiated) authorization.
	if returnedState != "" && returnedState != state {
		return oauth.Token{}, fmt.Errorf("claude code login: state mismatch")
	}

	return exchangeClaudeCodeCode(ctx, code, state, verifier, opts)
}

func exchangeClaudeCodeCode(ctx context.Context, code, state, verifier string, opts ClaudeCodeLoginOptions) (oauth.Token, error) {
	endpoint := opts.Endpoint
	if endpoint == "" {
		endpoint = claudeCodeTokenEndpoint
	}
	client := opts.HTTPClient
	if client == nil {
		// Matches the 60s override callers like runAuthClaude/`zero auth claude`
		// already use for this same endpoint: third-party reports describe
		// 40-60s responses during platform degradation, and this default is also
		// on the silent per-request refresh path (agentclicreds.go), where a
		// false timeout surfaces as a mid-conversation auth error, not just a
		// slow login screen.
		client = &http.Client{Timeout: 60 * time.Second}
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	body, err := json.Marshal(map[string]string{
		"grant_type":    "authorization_code",
		"code":          code,
		"state":         state,
		"client_id":     claudeCodeClientID,
		"redirect_uri":  claudeCodeRedirectURI,
		"code_verifier": verifier,
	})
	if err != nil {
		return oauth.Token{}, fmt.Errorf("claude code login: encode exchange: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return oauth.Token{}, fmt.Errorf("claude code login: build exchange: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", claudeCodeUserAgent)

	response, err := client.Do(request)
	if err != nil {
		return oauth.Token{}, fmt.Errorf("claude code login: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return oauth.Token{}, fmt.Errorf("claude code login: read exchange response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return oauth.Token{}, fmt.Errorf("claude code login: token endpoint returned %s", response.Status)
	}

	var parsed struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		Account      struct {
			EmailAddress string `json:"email_address"`
		} `json:"account"`
	}
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return oauth.Token{}, fmt.Errorf("claude code login: parse exchange response: %w", err)
	}
	if strings.TrimSpace(parsed.AccessToken) == "" {
		return oauth.Token{}, fmt.Errorf("claude code login: response carried no access token")
	}
	token := oauth.Token{
		AccessToken:  parsed.AccessToken,
		RefreshToken: parsed.RefreshToken,
		TokenType:    "Bearer",
		Account:      parsed.Account.EmailAddress,
	}
	if parsed.ExpiresIn > 0 {
		token.ExpiresAt = now().Add(time.Duration(parsed.ExpiresIn) * time.Second)
	}
	return token, nil
}

// pkcePair returns a PKCE code_verifier and its S256 code_challenge.
func pkcePair(randSource io.Reader) (verifier, challenge string, err error) {
	verifier, err = randomURLSafe(randSource, 64)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

func randomURLSafe(randSource io.Reader, size int) (string, error) {
	buf := make([]byte, size)
	if _, err := io.ReadFull(randSource, buf); err != nil {
		return "", fmt.Errorf("entropy: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// readTrimmedLine reads one line from in, honoring ctx cancellation (stdin
// reads cannot otherwise be interrupted).
func readTrimmedLine(ctx context.Context, in io.Reader) (string, error) {
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		reader := bufio.NewReader(in)
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			ch <- result{"", err}
			return
		}
		ch <- result{strings.TrimSpace(line), nil}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case r := <-ch:
		return r.line, r.err
	}
}

// ClaudeCodeRefreshOptions configures RefreshClaudeCode.
type ClaudeCodeRefreshOptions struct {
	// HTTPClient performs the token exchange; nil => a client with a sane timeout.
	HTTPClient *http.Client
	// Endpoint overrides the token endpoint; "" => the real Anthropic endpoint.
	// Injected by tests.
	Endpoint string
	// Now is the time source used to compute ExpiresAt from expires_in; nil =>
	// time.Now. Injected by tests.
	Now func() time.Time
}

// RefreshClaudeCode exchanges a Claude Code refresh token for a fresh access
// token. Zero uses this when a profile authenticates via the claude CLI's
// stored login (profile.AuthCLI == "claude") and the extracted access token has
// expired: the CLI only refreshes its own store when IT runs, and newer CLI
// versions migrate their live token chain to per-install keychain entries that
// cannot be reliably discovered — so zero must be able to walk the refresh
// chain itself. Returning the token (rather than persisting it) keeps the
// function composable, mirroring ChatGPTLogin; the caller decides where the
// refreshed pair lives.
func RefreshClaudeCode(ctx context.Context, refreshToken string, opts ClaudeCodeRefreshOptions) (oauth.Token, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return oauth.Token{}, fmt.Errorf("claude code refresh: no refresh token")
	}
	endpoint := opts.Endpoint
	if endpoint == "" {
		endpoint = claudeCodeTokenEndpoint
	}
	client := opts.HTTPClient
	if client == nil {
		// Matches the 60s override callers like runAuthClaude/`zero auth claude`
		// already use for this same endpoint: third-party reports describe
		// 40-60s responses during platform degradation, and this default is also
		// on the silent per-request refresh path (agentclicreds.go), where a
		// false timeout surfaces as a mid-conversation auth error, not just a
		// slow login screen.
		client = &http.Client{Timeout: 60 * time.Second}
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	body, err := json.Marshal(map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     claudeCodeClientID,
	})
	if err != nil {
		return oauth.Token{}, fmt.Errorf("claude code refresh: encode request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return oauth.Token{}, fmt.Errorf("claude code refresh: build request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", claudeCodeUserAgent)

	response, err := client.Do(request)
	if err != nil {
		return oauth.Token{}, fmt.Errorf("claude code refresh: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return oauth.Token{}, fmt.Errorf("claude code refresh: read response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		// The body may carry an OAuth error code but could also echo request
		// material — surface only the status, which is enough to diagnose
		// (401/400 => revoked or rotated-away refresh token; re-login fixes it).
		return oauth.Token{}, fmt.Errorf("claude code refresh: token endpoint returned %s", response.Status)
	}

	var parsed struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return oauth.Token{}, fmt.Errorf("claude code refresh: parse response: %w", err)
	}
	if strings.TrimSpace(parsed.AccessToken) == "" {
		return oauth.Token{}, fmt.Errorf("claude code refresh: response carried no access token")
	}
	token := oauth.Token{
		AccessToken: parsed.AccessToken,
		// A rotated refresh token replaces the old one; when the server omits
		// it, the one we just used remains valid — keep it so the chain never
		// dead-ends.
		RefreshToken: parsed.RefreshToken,
		TokenType:    "Bearer",
	}
	if token.RefreshToken == "" {
		token.RefreshToken = refreshToken
	}
	if parsed.ExpiresIn > 0 {
		token.ExpiresAt = now().Add(time.Duration(parsed.ExpiresIn) * time.Second)
	}
	return token, nil
}
