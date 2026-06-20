package config

import "testing"

func TestEnvKeyPreservesExistingCompatibleProviderKind(t *testing.T) {
	// A same-named openai-compatible provider (a proxy/gateway) must keep its kind
	// when the user exports OPENAI_API_KEY (credentials only, no baseURL). H2.
	cfg := &FileConfig{
		ActiveProvider: "openai",
		Providers: []ProviderProfile{{
			Name:         "openai",
			ProviderKind: ProviderKindOpenAICompatible,
			BaseURL:      "https://gateway.example/v1",
			Model:        "gpt-4o",
		}},
	}
	applyProviderEnv(cfg, ProviderKindOpenAI, envProfile{Name: "openai", APIKey: "sk-from-env"})
	p := cfg.Providers[0]
	if p.ProviderKind != ProviderKindOpenAICompatible {
		t.Errorf("transport kind clobbered: got %q, want %q", p.ProviderKind, ProviderKindOpenAICompatible)
	}
	if p.APIKey != "sk-from-env" {
		t.Errorf("env api key not applied: %q", p.APIKey)
	}
	if p.BaseURL != "https://gateway.example/v1" {
		t.Errorf("existing baseURL lost: %q", p.BaseURL)
	}
}

func TestEnvKeyCreatesStandardProviderWhenAbsent(t *testing.T) {
	// With no same-named provider, the env key still creates a standard provider.
	cfg := &FileConfig{}
	applyProviderEnv(cfg, ProviderKindOpenAI, envProfile{Name: "openai", APIKey: "sk-x"})
	if len(cfg.Providers) != 1 || cfg.Providers[0].ProviderKind != ProviderKindOpenAI {
		t.Fatalf("absent provider should be created as standard openai, got %+v", cfg.Providers)
	}
}
