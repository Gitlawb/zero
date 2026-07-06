package providers

import (
	"testing"

	"github.com/Gitlawb/zero/internal/config"
)

func TestCompactionModelIDResolutionOrder(t *testing.T) {
	anthropic := config.ProviderProfile{
		Name:         "anthropic",
		ProviderKind: config.ProviderKindAnthropic,
		Model:        "claude-sonnet-4.5",
	}

	t.Setenv(CompactionModelEnv, "my-env-model")
	if got := CompactionModelID(anthropic, "my-config-model"); got != "my-env-model" {
		t.Fatalf("env must win, got %q", got)
	}

	t.Setenv(CompactionModelEnv, "main")
	if got := CompactionModelID(anthropic, "my-config-model"); got != "" {
		t.Fatalf("env 'main' must force the main model, got %q", got)
	}

	t.Setenv(CompactionModelEnv, "")
	if got := CompactionModelID(anthropic, "my-config-model"); got != "my-config-model" {
		t.Fatalf("config preference must apply when env unset, got %q", got)
	}
	if got := CompactionModelID(anthropic, "main"); got != "" {
		t.Fatalf("config 'main' must force the main model, got %q", got)
	}
}

func TestCompactionModelIDCuratedDefaults(t *testing.T) {
	t.Setenv(CompactionModelEnv, "")

	anthropic := config.ProviderProfile{Name: "anthropic", ProviderKind: config.ProviderKindAnthropic, Model: "claude-sonnet-4.5"}
	if got := CompactionModelID(anthropic, ""); got != defaultAnthropicCompactionModel {
		t.Fatalf("anthropic default = %q, want %q", got, defaultAnthropicCompactionModel)
	}

	google := config.ProviderProfile{Name: "google", ProviderKind: config.ProviderKindGoogle, Model: "gemini-2.5-pro"}
	if got := CompactionModelID(google, ""); got != defaultGoogleCompactionModel {
		t.Fatalf("google default = %q, want %q", got, defaultGoogleCompactionModel)
	}

	// Custom/compatible endpoints: catalog unknowable, no default.
	compatible := config.ProviderProfile{
		Name:         "opengateway",
		ProviderKind: config.ProviderKindOpenAICompatible,
		BaseURL:      "https://opengateway.gitlawb.com/v1",
		Model:        "some-model",
	}
	if got := CompactionModelID(compatible, ""); got != "" {
		t.Fatalf("openai-compatible must have no default, got %q", got)
	}
}

func TestCompactionModelIDSkipsWhenMainIsAlreadyCheap(t *testing.T) {
	t.Setenv(CompactionModelEnv, "")
	haiku := config.ProviderProfile{
		Name:         "anthropic",
		ProviderKind: config.ProviderKindAnthropic,
		Model:        defaultAnthropicCompactionModel,
	}
	if got := CompactionModelID(haiku, ""); got != "" {
		t.Fatalf("already-cheap session must not get a dedicated summarizer, got %q", got)
	}
}
