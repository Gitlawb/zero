package providers

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/oauth"
)

func TestOAuthLoginForProfileBindsBearerAndAccountToSameLogin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oauth-tokens.json")
	t.Setenv("ZERO_OAUTH_STORAGE", "file")
	t.Setenv("ZERO_OAUTH_TOKENS_PATH", path)
	store, err := oauth.NewStore(oauth.StoreOptions{FilePath: path})
	if err != nil {
		t.Fatalf("oauth store: %v", err)
	}
	key := oauth.ProviderKey("chatgpt")
	if err := store.Save(key, oauth.Token{
		AccessToken: "subscription-token",
		Account:     "account-42",
		ExpiresAt:   time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("save OAuth token: %v", err)
	}

	resolver, loginKey := OAuthLoginForProfile(config.ProviderProfile{Name: "codex", CatalogID: "chatgpt"})
	if resolver == nil || loginKey != key {
		t.Fatalf("OAuth login = (%v, %q), want resolver bound to %q", resolver != nil, loginKey, key)
	}
	header, value, ok, err := resolver(context.Background(), false)
	if err != nil || !ok || header != "Authorization" || value != "Bearer subscription-token" {
		t.Fatalf("bearer resolution = (%q, %q, %v, %v)", header, value, ok, err)
	}
	account, ok, err := CodexAccountResolverForLogin(loginKey)(context.Background())
	if err != nil || !ok || account != "account-42" {
		t.Fatalf("account resolution = (%q, %v, %v)", account, ok, err)
	}
}
