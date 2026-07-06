package cli

import (
	"testing"

	"github.com/Gitlawb/zero/internal/config"
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
