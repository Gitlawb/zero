package providers

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Gitlawb/zero/internal/agentcli"
	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/oauth"
	"github.com/Gitlawb/zero/internal/provideroauth"
	"github.com/Gitlawb/zero/internal/providers/anthropic"
	"github.com/Gitlawb/zero/internal/providers/openai"
	"github.com/Gitlawb/zero/internal/providers/providerio"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// newCLIAuthedProvider builds a provider that authenticates using credentials
// extracted live from a detected agent-CLI's local credential store
// (agentcli.ExtractCredentials) rather than a zero-managed API key or OAuth
// login. profile.AuthCLI names the harness ("claude", "codex", ...); the
// harness's CatalogID (fixed by the agentcli catalog, not user-editable)
// decides which provider flavor to build, mirroring isCodexCatalog/newCodexProvider
// for the zero-native OAuth path. Credentials are re-read from disk on EVERY
// call the resulting resolver makes — including the forced retry after a 401 —
// never cached here, so a refresh the harness CLI performs on disk between
// calls (its own token rotation) takes effect immediately without restarting
// zero.
func newCLIAuthedProvider(profile config.ProviderProfile, resolved resolvedProfile, authCLI string, options Options) (zeroruntime.Provider, error) {
	harness, ok := agentcli.Lookup(authCLI)
	if !ok {
		return nil, fmt.Errorf("provider %s: unknown auth CLI %q", profile.Name, authCLI)
	}
	if harness.CatalogID == "" {
		return nil, fmt.Errorf("provider %s: %s has no reusable provider credentials", profile.Name, harness.DisplayName)
	}
	deps := options.AgentCLIDeps

	switch harness.CatalogID {
	case "chatgpt":
		resolver := cliBearerResolver(harness, deps)
		accountResolver := cliAccountResolver(harness, deps)
		return openai.NewCodexProvider(openai.CodexOptions{
			Options: openai.Options{
				BaseURL:        resolved.baseURL,
				Model:          resolved.apiModel,
				OAuthResolver:  resolver,
				MaxTokens:      resolved.maxOutputTokens,
				HTTPClient:     options.HTTPClient,
				UserAgent:      options.UserAgent,
				ParseThinkTags: parseThinkTagsForProfile(profile, resolved),
			},
			AccountResolver: accountResolver,
		})
	case "anthropic":
		return anthropic.New(anthropic.Options{
			BaseURL:       resolved.baseURL,
			Model:         resolved.apiModel,
			OAuthResolver: claudeRefreshingBearerResolver(harness, deps, defaultCLIAuthTokenStore(), defaultClaudeRefresh(options)),
			// Claude Code's OAuth bearer authenticates with this beta flag
			// instead of the normal x-api-key credential. The resolver above
			// always supplies the bearer for THIS provider instance (its
			// APIKey is always empty — see providerWizardCLIProfile), so the
			// header is only ever sent on a CLI-authed request; the ordinary
			// x-api-key path (built with Options.Beta unset) is untouched.
			Beta: "oauth-2025-04-20",
			// A Claude Code subscription token is only served to requests that
			// identify as Claude Code (the claude-code beta + the "You are
			// Claude Code" system block); without it Anthropic 429s regardless
			// of the account's real quota. The anthropic provider injects both.
			ClaudeCodeIdentity: true,
			MaxTokens:          resolved.maxOutputTokens,
			HTTPClient:         options.HTTPClient,
			UserAgent:          options.UserAgent,
		})
	default:
		return nil, fmt.Errorf("provider %s: auth CLI %q (catalog %q) is not wired for CLI-reused credentials", profile.Name, authCLI, harness.CatalogID)
	}
}

// cliBearerResolver builds a providerio.TokenResolver over a harness's locally
// stored credentials. It is called once per outbound request (and again, with
// forceRefresh, on the 401 retry) and re-reads the credential store fresh every
// time — zero performs no refresh of its own here; the harness CLI (claude,
// codex, ...) is solely responsible for keeping its on-disk token current, and
// this resolver just picks up whatever it finds.
func cliBearerResolver(harness agentcli.Harness, deps agentcli.Deps) providerio.TokenResolver {
	return func(_ context.Context, _ bool) (string, string, bool, error) {
		creds, ok, err := agentcli.ExtractCredentials(harness, deps)
		if err != nil {
			return "", "", false, fmt.Errorf("%s credentials: %w", harness.DisplayName, err)
		}
		if !ok || strings.TrimSpace(creds.AccessToken) == "" {
			return "", "", false, cliLoginExpiredError(harness)
		}
		if !creds.ExpiresAt.IsZero() && !creds.ExpiresAt.After(time.Now()) {
			return "", "", false, cliLoginExpiredError(harness)
		}
		return "Authorization", "Bearer " + creds.AccessToken, true, nil
	}
}

// cliAuthTokenStoreKey is where zero caches the refresh chain it adopts from
// the claude CLI's store. It lives in ZERO's own oauth token store — never the
// CLI's files or keychain — so zero's rotations cannot disturb the CLI's login.
const cliAuthTokenStoreKey = "provider:authcli-claude"

// claudeRefreshBuffer is how close to expiry a token counts as "needs refresh":
// refreshing a few minutes early avoids racing a request against the boundary.
const claudeRefreshBuffer = 5 * time.Minute

// claudeRefreshFunc walks one link of the Claude Code OAuth refresh chain.
type claudeRefreshFunc func(ctx context.Context, refreshToken string) (oauth.Token, error)

// cliAuthTokenStore is the minimal token-store surface the refreshing resolver
// needs; *oauth.Store satisfies it and tests inject a fake.
type cliAuthTokenStore interface {
	Load(key string) (oauth.Token, bool, error)
	Save(key string, token oauth.Token) error
}

func defaultCLIAuthTokenStore() cliAuthTokenStore {
	store, err := oauth.NewStore(oauth.StoreOptions{})
	if err != nil {
		// Without a store the resolver still works (extract + refresh per
		// request); it just cannot cache rotations across calls.
		return nil
	}
	return store
}

func defaultClaudeRefresh(options Options) claudeRefreshFunc {
	return func(ctx context.Context, refreshToken string) (oauth.Token, error) {
		return provideroauth.RefreshClaudeCode(ctx, refreshToken, provideroauth.ClaudeCodeRefreshOptions{
			HTTPClient: options.HTTPClient,
		})
	}
}

// claudeRefreshingBearerResolver resolves the claude bearer with zero-side
// refresh. The claude CLI only refreshes its stored token when IT runs, and
// newer versions migrate the live chain to per-install keychain entries zero
// cannot reliably discover — so the extracted store often holds an expired
// access token plus a still-usable refresh token. Resolution order:
//
//  1. zero's own cached token (a chain zero already adopted), if fresh;
//  2. the CLI's extracted token, if fresh (a CLI-side refresh wins the race);
//  3. refresh, using the refresh token from whichever source carries the
//     LATER expiry — a later ExpiresAt marks the newer link of the chain —
//     persisting the rotated pair back to zero's store.
//
// forceRefresh (the 401 retry) skips 1 and 2: a token that just 401'd is not
// worth re-serving, however fresh it looks.
func claudeRefreshingBearerResolver(harness agentcli.Harness, deps agentcli.Deps, store cliAuthTokenStore, refresh claudeRefreshFunc) providerio.TokenResolver {
	var mu sync.Mutex
	return func(ctx context.Context, forceRefresh bool) (string, string, bool, error) {
		// Concurrent StreamCompletion calls (e.g. parallel tool calls) share this
		// resolver. Without serializing, two calls that both see a stale token
		// would race to refresh with the SAME refresh token — and OAuth refresh
		// tokens are commonly single-use/rotated server-side, so the losing call
		// fails with a spurious auth error instead of transparently reusing the
		// winner's freshly-rotated token.
		mu.Lock()
		defer mu.Unlock()
		now := time.Now()
		var cached oauth.Token
		if store != nil {
			if token, ok, err := store.Load(cliAuthTokenStoreKey); err == nil && ok {
				cached = token
			}
		}
		if !forceRefresh && cached.AccessToken != "" && !cached.NeedsRefresh(now, claudeRefreshBuffer) {
			return "Authorization", "Bearer " + cached.AccessToken, true, nil
		}

		creds, ok, err := agentcli.ExtractCredentials(harness, deps)
		if err != nil {
			return "", "", false, fmt.Errorf("%s credentials: %w", harness.DisplayName, err)
		}
		if !ok {
			creds = agentcli.Credentials{}
		}
		extractedFresh := strings.TrimSpace(creds.AccessToken) != "" &&
			!creds.ExpiresAt.IsZero() && creds.ExpiresAt.After(now.Add(claudeRefreshBuffer))
		if !forceRefresh && extractedFresh {
			return "Authorization", "Bearer " + creds.AccessToken, true, nil
		}

		refreshToken := newestClaudeRefreshToken(cached, creds)
		if refreshToken == "" {
			return "", "", false, cliLoginExpiredError(harness)
		}
		token, refreshErr := refresh(ctx, refreshToken)
		if refreshErr != nil {
			return "", "", false, fmt.Errorf("%w (auto-refresh failed: %v)", cliLoginExpiredError(harness), refreshErr)
		}
		if store != nil {
			// A failed save only costs a redundant refresh next call; the token
			// in hand is still good.
			_ = store.Save(cliAuthTokenStoreKey, token)
		}
		return "Authorization", "Bearer " + token.AccessToken, true, nil
	}
}

// newestClaudeRefreshToken picks the refresh token from whichever source holds
// the later access-token expiry: refresh chains rotate, and the later expiry
// marks the later (still-valid) link. A source without a refresh token never
// wins; ties go to zero's own cache, whose chain zero rotated most recently.
func newestClaudeRefreshToken(cached oauth.Token, extracted agentcli.Credentials) string {
	cachedToken := strings.TrimSpace(cached.RefreshToken)
	extractedToken := strings.TrimSpace(extracted.RefreshToken)
	switch {
	case cachedToken == "":
		return extractedToken
	case extractedToken == "":
		return cachedToken
	case extracted.ExpiresAt.After(cached.ExpiresAt):
		return extractedToken
	default:
		return cachedToken
	}
}

// cliAccountResolver mirrors cliBearerResolver for the Codex chatgpt-account-id
// header: it reads the harness's stored AccountID fresh on every call. Unlike
// the bearer resolver, a missing account id is not a hard error — ok=false just
// omits the header (openai.CodexProvider's own convention), since the bearer
// resolver above is the one place that surfaces a "you're logged out" error to
// the caller.
func cliAccountResolver(harness agentcli.Harness, deps agentcli.Deps) openai.CodexAccountResolver {
	return func(_ context.Context) (string, bool, error) {
		creds, ok, err := agentcli.ExtractCredentials(harness, deps)
		if err != nil || !ok {
			return "", false, nil
		}
		account := strings.TrimSpace(creds.AccountID)
		if account == "" {
			return "", false, nil
		}
		return account, true, nil
	}
}

// cliLoginExpiredError is the actionable error surfaced when a profile.AuthCLI
// provider's harness has no usable local credential (never logged in, or its
// token expired): it names the harness and the command that fixes it.
func cliLoginExpiredError(harness agentcli.Harness) error {
	return fmt.Errorf("%s login has expired — run %q to refresh, or switch auth with /provider", harness.ID, harness.LoginCommand())
}
