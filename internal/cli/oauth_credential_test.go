package cli

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/oauth"
)

func TestProviderHasOAuthLogin(t *testing.T) {
	xai := config.ProviderProfile{Name: "xai", CatalogID: "xai"}
	if providerHasOAuthLogin(xai, nil) {
		t.Fatal("no stored login must be false")
	}
	if !providerHasOAuthLogin(xai, map[string]bool{"xai": true}) {
		t.Fatal("a stored login keyed by name must be true")
	}
	if !providerHasOAuthLogin(config.ProviderProfile{Name: "grok", CatalogID: "xai"}, map[string]bool{"xai": true}) {
		t.Fatal("a stored login keyed by catalog id must be true")
	}
}

func TestSetupRequiredRecognizesOAuthLogin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tok.json")
	t.Setenv("ZERO_OAUTH_TOKENS_PATH", path)
	store, err := oauth.NewStore(oauth.StoreOptions{FilePath: path})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.Save(oauth.ProviderKey("xai"), oauth.Token{AccessToken: "tok", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("seed token: %v", err)
	}

	// xai has no inline key (env-only) but IS logged in via OAuth → no setup.
	loggedIn := config.ResolvedConfig{Provider: config.ProviderProfile{Name: "xai", CatalogID: "xai", APIKeyEnv: "XAI_API_KEY"}}
	if setupRequired(loggedIn) {
		t.Fatal("a provider with a stored OAuth login must not require onboarding")
	}

	// A keyless provider with no OAuth login still requires setup.
	noLogin := config.ResolvedConfig{Provider: config.ProviderProfile{Name: "openai", CatalogID: "openai", APIKeyEnv: "OPENAI_API_KEY"}}
	if !setupRequired(noLogin) {
		t.Fatal("a keyless provider with no OAuth login must require onboarding")
	}
}
