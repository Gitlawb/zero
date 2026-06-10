package providermodeldiscovery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/providercatalog"
)

func TestDiscoverOpenAICompatibleModelsFetchesModelsEndpoint(t *testing.T) {
	const apiKey = "sk-live-secret"
	var gotPath string
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"object": "list",
			"data": [
				{"id": "model-b", "object": "model"},
				{"id": "model-a", "object": "model"},
				{"id": "model-a", "object": "model"},
				{"object": "model"}
			]
		}`))
	}))
	defer server.Close()

	models, err := Discover(context.Background(), config.ProviderProfile{
		Name:         "test",
		ProviderKind: config.ProviderKindOpenAICompatible,
		BaseURL:      server.URL + "/v1",
		APIKey:       apiKey,
	}, Options{HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("Discover returned error: %v", err)
	}
	if gotPath != "/v1/models" {
		t.Fatalf("requested path = %q, want /v1/models", gotPath)
	}
	if gotAuth != "Bearer "+apiKey {
		t.Fatalf("Authorization = %q, want bearer API key", gotAuth)
	}
	if got, want := modelIDs(models), []string{"model-a", "model-b"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("models = %#v, want %#v", got, want)
	}
}

func TestDiscoverOpenAICompatibleModelsHandlesBaseURLWithoutVersion(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"data":[{"id":"local-model"}]}`))
	}))
	defer server.Close()

	models, err := Discover(context.Background(), config.ProviderProfile{
		Name:         "local",
		ProviderKind: config.ProviderKindOpenAICompatible,
		BaseURL:      server.URL,
	}, Options{HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("Discover returned error: %v", err)
	}
	if gotPath != "/models" {
		t.Fatalf("requested path = %q, want /models for provider base URLs without /v1", gotPath)
	}
	if len(models) != 1 || models[0].ID != "local-model" {
		t.Fatalf("models = %#v, want local-model", models)
	}
}

func TestDiscoverOpenAICompatibleModelsRejectsUnsupportedProviders(t *testing.T) {
	_, err := Discover(context.Background(), config.ProviderProfile{
		Name:         "anthropic",
		ProviderKind: config.ProviderKindAnthropic,
		BaseURL:      "https://api.anthropic.com",
	}, Options{})
	if err == nil || !strings.Contains(err.Error(), "does not expose OpenAI-compatible model discovery") {
		t.Fatalf("Discover error = %v, want unsupported provider message", err)
	}
}

func TestDiscoverOpenAICompatibleModelsRedactsSecretsInErrors(t *testing.T) {
	const apiKey = "sk-live-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad key "+apiKey, http.StatusUnauthorized)
	}))
	defer server.Close()

	_, err := Discover(context.Background(), config.ProviderProfile{
		Name:         "test",
		ProviderKind: config.ProviderKindOpenAICompatible,
		BaseURL:      server.URL + "/v1",
		APIKey:       apiKey,
	}, Options{HTTPClient: server.Client()})
	if err == nil {
		t.Fatal("Discover should return an error for non-2xx status")
	}
	if strings.Contains(err.Error(), apiKey) {
		t.Fatalf("error leaked API key: %v", err)
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("error should contain redacted marker, got: %v", err)
	}
}

func TestDiscoverCatalogMergesLiveModelsWithModelsDevMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api.json":
			_, _ = w.Write([]byte(`{
				"openai": {
					"models": {
						"gpt-4.1": {
							"id": "gpt-4.1",
							"name": "GPT-4.1",
							"tool_call": true,
							"reasoning": true,
							"limit": {"context": 1048576}
						},
						"not-enabled": {"id": "not-enabled"}
					}
				}
			}`))
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4.1"},{"id":"custom-live"}]}`))
		default:
			t.Fatalf("unexpected request path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	provider := providercatalog.Descriptor{
		ID:             "openai",
		Transport:      providercatalog.TransportOpenAI,
		DefaultBaseURL: server.URL + "/v1",
		RequiresAuth:   true,
	}
	models, err := DiscoverCatalog(context.Background(), provider, config.ProviderProfile{
		CatalogID:    "openai",
		ProviderKind: config.ProviderKindOpenAI,
		BaseURL:      server.URL + "/v1",
		APIKey:       "sk-live",
	}, Options{HTTPClient: server.Client(), ModelsDevURL: server.URL + "/api.json"})
	if err != nil {
		t.Fatalf("DiscoverCatalog returned error: %v", err)
	}
	if got := strings.Join(modelIDs(models), ","); got != "custom-live,gpt-4.1" {
		t.Fatalf("models = %s, want live model IDs only", got)
	}
	for _, model := range models {
		if model.ID == "gpt-4.1" {
			if model.ContextWindow != 1048576 || !model.ToolCall || !model.Reasoning {
				t.Fatalf("gpt-4.1 metadata = %#v, want models.dev capabilities", model)
			}
			return
		}
	}
	t.Fatal("missing gpt-4.1")
}

func modelIDs(models []Model) []string {
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	return ids
}
