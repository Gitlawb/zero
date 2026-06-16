package cli

import (
	"testing"

	"github.com/Gitlawb/zero/internal/config"
)

func TestFirstUsableProviderPrefersRemoteKeyed(t *testing.T) {
	providers := []config.ProviderProfile{
		{Name: "ollama", CatalogID: "ollama", BaseURL: "http://localhost:11434/v1", APIKey: "k"},      // usable but local
		{Name: "moonshot", CatalogID: "moonshot", BaseURL: "https://api.moonshot.ai/v1", APIKey: "k"}, // usable, remote
		{Name: "xai", CatalogID: "xai", APIKeyEnv: "XAI_API_KEY"},                                     // not usable (env only, no inline key)
	}
	got, ok := firstUsableProvider(providers)
	if !ok || got.Name != "moonshot" {
		t.Fatalf("want remote keyed provider (moonshot), got %q ok=%v", got.Name, ok)
	}
}

func TestFirstUsableProviderFallsBackToLocal(t *testing.T) {
	providers := []config.ProviderProfile{
		{Name: "xai", CatalogID: "xai", APIKeyEnv: "XAI_API_KEY"},                                // not usable
		{Name: "ollama", CatalogID: "ollama", BaseURL: "http://localhost:11434/v1", APIKey: "k"}, // local, usable
	}
	got, ok := firstUsableProvider(providers)
	if !ok || got.Name != "ollama" {
		t.Fatalf("want local usable fallback (ollama), got %q ok=%v", got.Name, ok)
	}
}

func TestFirstUsableProviderNoneUsable(t *testing.T) {
	providers := []config.ProviderProfile{
		{Name: "xai", CatalogID: "xai", APIKeyEnv: "XAI_API_KEY"},
		{Name: "openai", CatalogID: "openai", APIKeyEnv: "OPENAI_API_KEY"},
	}
	if got, ok := firstUsableProvider(providers); ok {
		t.Fatalf("no provider has a credential, want ok=false, got %q", got.Name)
	}
}

// A keyless local proxy (chatgpt-proxy, RequiresAuth=false) is usable without a
// credential, so it can serve as a fallback rather than forcing onboarding.
func TestFirstUsableProviderAcceptsKeylessLocalProxy(t *testing.T) {
	providers := []config.ProviderProfile{
		{Name: "xai", CatalogID: "xai", APIKeyEnv: "XAI_API_KEY"},
		{Name: "chatgpt", CatalogID: "chatgpt-proxy", BaseURL: "http://localhost:10531/v1"},
	}
	got, ok := firstUsableProvider(providers)
	if !ok || got.Name != "chatgpt" {
		t.Fatalf("want keyless local proxy fallback, got %q ok=%v", got.Name, ok)
	}
}
