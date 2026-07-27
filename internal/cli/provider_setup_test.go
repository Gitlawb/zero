package cli

import (
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/config"
)

// Regression for issue #555's follow-up: `zero providers check` must not
// error that a no-auth custom endpoint requires an API key, matching what
// /model and /providers already treat as usable.
func TestValidateProviderRuntimeReadyCustomEndpoint(t *testing.T) {
	cases := []struct {
		name    string
		profile config.ProviderProfile
		wantErr bool
	}{
		{
			name: "custom openai compatible with no credential configured",
			profile: config.ProviderProfile{
				Name:      "local-llama",
				CatalogID: "custom-openai-compatible",
				BaseURL:   "http://192.168.1.50:8080/v1",
				Model:     "custom-model",
			},
			wantErr: false,
		},
		{
			name: "custom openai compatible with stale legacy default env",
			profile: config.ProviderProfile{
				Name:      "local-llama",
				CatalogID: "custom-openai-compatible",
				BaseURL:   "http://192.168.1.50:8080/v1",
				APIKeyEnv: "OPENAI_API_KEY",
				Model:     "custom-model",
			},
			wantErr: false,
		},
		{
			name: "custom openai compatible with explicit non-default env still requires it",
			profile: config.ProviderProfile{
				Name:      "local-llama",
				CatalogID: "custom-openai-compatible",
				BaseURL:   "http://192.168.1.50:8080/v1",
				APIKeyEnv: "LLAMA_CPP_API_KEY",
				Model:     "custom-model",
			},
			wantErr: true,
		},
		{
			name: "catalog provider missing key still errors",
			profile: config.ProviderProfile{
				Name:      "groq",
				CatalogID: "groq",
				Model:     "llama-3.3-70b-versatile",
			},
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateProviderRuntimeReady(c.profile)
			if (err != nil) != c.wantErr {
				t.Fatalf("validateProviderRuntimeReady() error = %v, wantErr %v", err, c.wantErr)
			}
		})
	}
}

// atomic-chat-local without --model would persist the catalog placeholder
// "local-model", which the Atomic Chat server never serves, so the first
// completion fails. Adding it must require a real model instead.
func TestProviderProfileForAddRequiresModelForAtomicChatLocal(t *testing.T) {
	if _, err := providerProfileForAdd(providerAddOptions{catalogID: "atomic-chat-local"}); err == nil {
		t.Fatalf("providerProfileForAdd(atomic-chat-local, no --model) = nil error, want a require-model error")
	} else if !strings.Contains(err.Error(), "--model") {
		t.Fatalf("error should tell the user to pass --model, got %v", err)
	}

	profile, err := providerProfileForAdd(providerAddOptions{catalogID: "atomic-chat-local", model: "unsloth/gemma-4-E2B-it-GGUF"})
	if err != nil {
		t.Fatalf("providerProfileForAdd(atomic-chat-local, --model) returned error: %v", err)
	}
	if profile.Model != "unsloth/gemma-4-E2B-it-GGUF" {
		t.Fatalf("profile.Model = %q, want the explicit model", profile.Model)
	}
	if profile.Model == "local-model" {
		t.Fatalf("profile persisted the catalog placeholder")
	}
}
