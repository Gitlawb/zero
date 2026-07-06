package cli

import (
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
