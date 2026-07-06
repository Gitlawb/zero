package cli

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/oauth"
	"github.com/Gitlawb/zero/internal/providercatalog"
	"github.com/Gitlawb/zero/internal/provideroauth"
	"github.com/Gitlawb/zero/internal/providers/providerio"
)

// oauthLoginForProfile resolves the user's OAuth login for a provider ONCE and
// returns both a TokenResolver that authenticates model calls with it and the
// credential-store key it bound to. It returns (nil, "") when no login exists —
// keeping API-key users free of any per-request store lookups, since the resolver
// is only attached when a login is present at construction time.
//
// The returned key is the single source of truth for "which login is this
// provider using": callers pass it to providers.Options.OAuthLoginKey so the
// Codex chatgpt-account-id header reads its account from the exact login that
// issued the bearer token, instead of doing a second, independent lookup that
// could select a different login (a backend-rejected mismatch).
//
// Candidate login names (profile name, then a catalog-ID fallback, both gated on
// the profile having no own configured credential) come from the shared
// ProviderProfile.OAuthLoginCandidates so the runtime resolver, the Codex account
// resolver, and the onboarding presence check never diverge.
func oauthLoginForProfile(profile config.ProviderProfile) (providerio.TokenResolver, string) {
	candidates := profile.OAuthLoginCandidates()
	if providercatalog.NormalizeID(profile.CatalogID) == "copilot" {
		// GitHub Copilot's credential is ALWAYS a durable GitHub login token that
		// must be exchanged (minted) for a short-lived Copilot bearer at request
		// time — it is never a usable bearer on its own. The TUI's
		// profileWithCredential stuffs that durable token into profile.APIKey on a
		// /model or provider switch, which flips HasConfiguredCredential() true and
		// makes OAuthLoginCandidates() return nil — silently dropping the mint
		// resolver so the rebuilt provider falls back to the generic host with the
		// raw token and loses premium models. Force the login candidates for
		// Copilot regardless of that gate so every rebuild keeps minting. (The
		// stuffed APIKey is harmless: withBearer clears it the instant the resolver
		// returns a minted token.)
		candidates = copilotLoginCandidates(profile)
	}
	if len(candidates) == 0 {
		return nil, ""
	}
	store, err := oauth.NewStore(oauth.StoreOptions{})
	if err != nil {
		return nil, ""
	}
	_, key, ok := oauth.FirstStored(store, candidates)
	if !ok {
		// No login under any candidate (or unreadable/invalid keys) → API-key
		// auth, no resolver.
		return nil, ""
	}
	manager, err := oauth.NewManager(oauth.ManagerOptions{
		Store:      store,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		// Refreshing a token the user logged into (possibly a preset provider like
		// xAI) re-resolves that provider's OAuth config, which needs the preset.
		AllowPresets: true,
	})
	if err != nil {
		return nil, ""
	}
	// GitHub Copilot's stored login is a durable GitHub user token, NOT the
	// bearer the model endpoint accepts. Wrap the resolver so it exchanges that
	// token for a short-lived Copilot token (minted + cached, re-minted on 401
	// or near expiry). The GitHub token is loaded from the same login key the
	// rest of this provider uses, so a `zero auth login copilot` refresh is
	// picked up without restarting the agent.
	if providercatalog.NormalizeID(profile.CatalogID) == "copilot" {
		source := &provideroauth.CopilotTokenSource{
			HTTPClient: &http.Client{Timeout: 30 * time.Second},
			GitHubToken: func(ctx context.Context) (string, error) {
				return manager.GetFresh(ctx, key)
			},
		}
		resolver := func(ctx context.Context, forceRefresh bool) (string, string, bool, error) {
			bearer, err := source.Bearer(ctx, forceRefresh)
			if errors.Is(err, oauth.ErrNoToken) {
				// The login was removed since construction → fall back to the API key.
				return "", "", false, nil
			}
			if err != nil {
				return "", "", false, err
			}
			return "Authorization", "Bearer " + bearer, true, nil
		}
		return resolver, key
	}
	resolver := func(ctx context.Context, forceRefresh bool) (string, string, bool, error) {
		var token string
		var rerr error
		if forceRefresh {
			token, rerr = manager.Handle401(ctx, key)
		} else {
			token, rerr = manager.GetFresh(ctx, key)
		}
		if errors.Is(rerr, oauth.ErrNoToken) {
			// The login was removed since construction → fall back to the API key.
			return "", "", false, nil
		}
		if rerr != nil {
			return "", "", false, rerr
		}
		return "Authorization", "Bearer " + token, true, nil
	}
	return resolver, key
}

// copilotLoginCandidates returns the OAuth login names to try for a GitHub
// Copilot profile — the profile name first, then the catalog ID fallback —
// WITHOUT the HasConfiguredCredential gate that OAuthLoginCandidates applies.
// Copilot always authenticates through a minted bearer, so a durable token
// parked in APIKey (e.g. by the TUI on a model switch) must not suppress the
// login lookup. Names are trimmed, blank-skipped, and de-duplicated
// case-sensitively to match the case-sensitive OAuth token store.
func copilotLoginCandidates(profile config.ProviderProfile) []string {
	var names []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		for _, existing := range names {
			if existing == s {
				return
			}
		}
		names = append(names, s)
	}
	add(profile.Name)
	add(profile.CatalogID)
	return names
}

// copilotProfileWithAPIFormat sets profile.APIFormat for a GitHub Copilot
// profile to the transport the chosen model requires ("responses" or
// "chat-completions"), consulting the live /models capability map (cached).
// This is called at the single provider-build choke point (cli/app.go
// newProvider) so every build — session start, `/model` switch, provider
// switch — routes Responses-only models (gpt-5.4-mini, gpt-5.3-codex,
// mai-code-1-flash-picker) to the Responses transport without threading the
// decision through the TUI. Non-Copilot profiles and the no-login / offline
// cases are returned unchanged (the factory then defaults to chat-completions).
func copilotProfileWithAPIFormat(profile config.ProviderProfile, resolver providerio.TokenResolver) config.ProviderProfile {
	if resolver == nil || providercatalog.NormalizeID(profile.CatalogID) != "copilot" {
		return profile
	}
	if strings.TrimSpace(profile.Model) == "" {
		return profile
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, headerValue, ok, err := resolver(ctx, false)
	if err != nil || !ok {
		return profile
	}
	bearer := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(headerValue), "Bearer "))
	if bearer == "" {
		return profile
	}
	// Honor the account-specific API host the backend baked into the token
	// (Individual / Business / Enterprise are each proxied through a different
	// host). Mirrors the Copilot editor plugins; without this a Business/Enterprise
	// account can be pinned to the wrong host and see a reduced model set.
	base := provideroauth.CopilotBaseURLFromToken(bearer)
	if base != "" {
		profile.BaseURL = base
	}
	profile.APIFormat = provideroauth.CopilotModelAPIFormat(ctx, &http.Client{Timeout: 10 * time.Second}, bearer, base, profile.Model)
	return profile
}
