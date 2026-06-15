package cli

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/oauth"
	"github.com/Gitlawb/zero/internal/providers/providerio"
)

// providerOAuthName resolves the OAuth login name for a provider profile. It is
// the name a user logs in under (`zero auth login <name>`) whose token then
// authenticates this provider's model calls. For now it is the profile name;
// the provider presets map well-known kinds (openai/anthropic) to canonical
// names on top of this.
func providerOAuthName(profile config.ProviderProfile) string {
	return strings.TrimSpace(profile.Name)
}

// oauthResolverForProfile returns a TokenResolver that authenticates a provider's
// model calls with the user's OAuth login, or nil when no login exists for this
// provider. Returning nil for the no-login case keeps API-key users free of any
// per-request store lookups: the resolver is only attached once a login is
// present at construction time.
func oauthResolverForProfile(profile config.ProviderProfile) providerio.TokenResolver {
	name := providerOAuthName(profile)
	if name == "" {
		return nil
	}
	store, err := oauth.NewStore(oauth.StoreOptions{})
	if err != nil {
		return nil
	}
	key := oauth.ProviderKey(name)
	if _, ok, loadErr := store.Load(key); loadErr != nil || !ok {
		// No login (or an unreadable/invalid key) → API-key auth, no resolver.
		return nil
	}
	manager, err := oauth.NewManager(oauth.ManagerOptions{
		Store:      store,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	})
	if err != nil {
		return nil
	}
	return func(ctx context.Context, forceRefresh bool) (string, string, bool, error) {
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
}
