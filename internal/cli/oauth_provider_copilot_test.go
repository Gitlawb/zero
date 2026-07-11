package cli

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/oauth"
)

// TestCopilotLoginCandidatesIgnoreStuffedKey verifies that a GitHub Copilot
// profile still yields login candidates even when a durable token has been
// stuffed into APIKey (as the TUI's profileWithCredential does on a /model
// switch). The generic OAuthLoginCandidates gate would return nil here; the
// Copilot-specific path must not, or the mint resolver is silently dropped.
func TestCopilotLoginCandidatesIgnoreStuffedKey(t *testing.T) {
	profile := config.ProviderProfile{
		Name:      "copilot",
		CatalogID: "copilot",
		APIKey:    "ghu_durable_github_token_not_a_bearer",
	}

	if got := profile.OAuthLoginCandidates(); len(got) != 0 {
		t.Fatalf("precondition: OAuthLoginCandidates with APIKey set = %v, want empty", got)
	}
	got := copilotLoginCandidates(profile)
	want := []string{"copilot"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("copilotLoginCandidates = %v, want %v", got, want)
	}
}

func TestCopilotLoginResolverReusesTokenSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	t.Setenv("ZERO_OAUTH_STORAGE", "file")
	t.Setenv("ZERO_OAUTH_TOKENS_PATH", path)
	store, err := oauth.NewStore(oauth.StoreOptions{FilePath: path})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.Save(oauth.ProviderKey("copilot"), oauth.Token{AccessToken: "github-token", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	logins := &oauthLoginResolver{}
	profile := config.ProviderProfile{Name: "copilot", CatalogID: "copilot"}
	if resolver, _ := logins.loginForProfile(profile); resolver == nil {
		t.Fatal("first provider build returned no resolver")
	}
	first := logins.copilotSources[oauth.ProviderKey("copilot")].source
	if resolver, _ := logins.loginForProfile(profile); resolver == nil {
		t.Fatal("second provider build returned no resolver")
	}
	if len(logins.copilotSources) != 1 {
		t.Fatalf("Copilot token sources = %d, want 1", len(logins.copilotSources))
	}
	if got := logins.copilotSources[oauth.ProviderKey("copilot")].source; got != first {
		t.Fatal("unchanged GitHub login did not reuse its Copilot token source")
	}

	if err := store.Save(oauth.ProviderKey("copilot"), oauth.Token{AccessToken: "different-github-token", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("replace login: %v", err)
	}
	if resolver, _ := logins.loginForProfile(profile); resolver == nil {
		t.Fatal("provider rebuild after account change returned no resolver")
	}
	if got := logins.copilotSources[oauth.ProviderKey("copilot")].source; got == first {
		t.Fatal("changed GitHub login reused the previous account's Copilot bearer cache")
	}

	if err := store.Save(oauth.ProviderKey("copilot-alt"), oauth.Token{AccessToken: "alternate-login-token", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("save alternate login: %v", err)
	}
	alternate := config.ProviderProfile{Name: "copilot-alt", CatalogID: "copilot"}
	if resolver, _ := logins.loginForProfile(alternate); resolver == nil {
		t.Fatal("alternate Copilot login returned no resolver")
	}
	if logins.activeCopilotKey != oauth.ProviderKey("copilot-alt") {
		t.Fatalf("active Copilot login = %q, want alternate key", logins.activeCopilotKey)
	}
}

// TestCopilotLoginCandidatesNameThenCatalog checks ordering and de-duplication:
// the profile name comes first, the catalog ID is a fallback, and duplicates
// collapse.
func TestCopilotLoginCandidatesNameThenCatalog(t *testing.T) {
	profile := config.ProviderProfile{Name: "my-copilot", CatalogID: "copilot"}
	got := copilotLoginCandidates(profile)
	want := []string{"my-copilot", "copilot"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("copilotLoginCandidates = %v, want %v", got, want)
	}

	same := config.ProviderProfile{Name: "copilot", CatalogID: "copilot"}
	if got := copilotLoginCandidates(same); len(got) != 1 || got[0] != "copilot" {
		t.Fatalf("copilotLoginCandidates (dedup) = %v, want [copilot]", got)
	}
}
