package providermodeldiscovery

import (
	"context"
	"io"
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

func TestDiscoverAIMLAPIModelsSendsAuthAndCustomHeadersWithoutAttribution(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for header, want := range map[string]string{
			"Authorization": "Bearer test-key",
			"X-Trace":       "test",
		} {
			if got := r.Header.Get(header); got != want {
				t.Errorf("%s = %q, want %q", header, got, want)
			}
		}
		// No first-party referral/attribution headers are injected for catalog
		// presets; aimlapi rides through CopyHeaders like every other provider.
		for _, header := range []string{
			"X-AIMLAPI-Partner-ID",
			"X-AIMLAPI-Integration-Repo",
			"X-AIMLAPI-Integration-Version",
		} {
			if got := r.Header.Get(header); got != "" {
				t.Errorf("%s = %q, want no attribution header", header, got)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"openai/gpt-5-chat"}]}`))
	}))
	defer server.Close()

	_, err := Discover(context.Background(), config.ProviderProfile{
		CatalogID:     "aimlapi",
		ProviderKind:  config.ProviderKindOpenAICompatible,
		BaseURL:       server.URL + "/v1",
		APIKey:        "test-key",
		CustomHeaders: map[string]string{"X-Trace": "test"},
	}, Options{HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("Discover returned error: %v", err)
	}
}

func TestDiscoverOpenAICompatibleModelsHonorsAuthHeaderValue(t *testing.T) {
	// A profile can authenticate via a raw auth-header value instead of APIKey;
	// discovery must send it rather than probe unauthenticated.
	const headerValue = "Bearer raw-header-secret"
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"model-a"}]}`))
	}))
	defer server.Close()

	if _, err := Discover(context.Background(), config.ProviderProfile{
		Name:            "test",
		ProviderKind:    config.ProviderKindOpenAICompatible,
		BaseURL:         server.URL + "/v1",
		AuthHeaderValue: headerValue,
	}, Options{HTTPClient: server.Client()}); err != nil {
		t.Fatalf("Discover returned error: %v", err)
	}
	if gotAuth != headerValue {
		t.Fatalf("Authorization = %q, want raw auth-header value %q", gotAuth, headerValue)
	}
}

func TestDiscoveryHasCredential(t *testing.T) {
	cases := []struct {
		name    string
		profile config.ProviderProfile
		want    bool
	}{
		{"api key", config.ProviderProfile{APIKey: "sk-x"}, true},
		{"auth header only", config.ProviderProfile{AuthHeaderValue: "Bearer t"}, true},
		{"both", config.ProviderProfile{APIKey: "sk-x", AuthHeaderValue: "Bearer t"}, true},
		{"neither", config.ProviderProfile{}, false},
		{"whitespace only", config.ProviderProfile{APIKey: "  ", AuthHeaderValue: "\t"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := discoveryHasCredential(tc.profile); got != tc.want {
				t.Fatalf("discoveryHasCredential = %v, want %v", got, tc.want)
			}
		})
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
		Name:         "google",
		ProviderKind: config.ProviderKindGoogle,
		BaseURL:      "https://generativelanguage.googleapis.com",
	}, Options{})
	if err == nil || !strings.Contains(err.Error(), "does not expose model discovery") {
		t.Fatalf("Discover error = %v, want unsupported provider message", err)
	}
}

func TestDiscoverAnthropicCompatibleModelsFetchesModelsEndpoint(t *testing.T) {
	const apiKey = "sk-ant-secret"
	var gotPath string
	var gotAPIKey string
	var gotVersion string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAPIKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": [
				{"id": "claude-custom-b", "display_name": "Claude Custom B"},
				{"id": "claude-custom-a", "display_name": "Claude Custom A"},
				{"id": "claude-custom-a", "display_name": "Claude Custom A"},
				{}
			]
		}`))
	}))
	defer server.Close()

	models, err := Discover(context.Background(), config.ProviderProfile{
		Name:         "custom",
		ProviderKind: config.ProviderKindAnthropicCompat,
		BaseURL:      server.URL + "/anthropic",
		APIKey:       apiKey,
	}, Options{HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("Discover returned error: %v", err)
	}
	if gotPath != "/anthropic/v1/models" {
		t.Fatalf("requested path = %q, want /anthropic/v1/models", gotPath)
	}
	if gotAPIKey != apiKey {
		t.Fatalf("x-api-key = %q, want API key", gotAPIKey)
	}
	if gotVersion == "" {
		t.Fatal("anthropic-version header is required")
	}
	if got, want := modelIDs(models), []string{"claude-custom-a", "claude-custom-b"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("models = %#v, want %#v", got, want)
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
			_, _ = w.Write([]byte(`{"data":[
				{"id":"gpt-4.1"},
				{"id":"gpt-image-1"},
				{"id":"text-embedding-3-large"},
				{"id":"not-enabled"}
			]}`))
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
	if got := strings.Join(modelIDs(models), ","); got != "gpt-4.1" {
		t.Fatalf("models = %s, want live coding model IDs only", got)
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

func TestLiveModelAllowedWithoutCatalogChecksProviderGateFirst(t *testing.T) {
	// The ModelIDAllowedForProvider check runs before the others.
	// For the restricted provider (opencode-go-anthropic-compatible) a
	// non-allowed model returns false immediately, without reaching the
	// IsKnownNonCodingModelID, Local, or LooksLikeCodingModelID checks.
	restricted := providercatalog.Descriptor{
		ID:    "opencode-go-anthropic-compatible",
		Local: true, // would pass the Local check if we got past the gate
	}

	// A model that isn't qwen/minimax is blocked at the gate, even though
	// Local=true would let any model through on its own.
	if got := liveModelAllowedWithoutCatalog(restricted, "claude-sonnet-4"); got != false {
		t.Fatal("liveModelAllowedWithoutCatalog: want false for claude-sonnet-4 on opencode-go-anthropic-compatible (blocked by ModelIDAllowedForProvider)")
	}

	// A qwen model passes the gate and continues to the remaining checks;
	// it's not a known non-coding model and looks like a coding model, so
	// the result is true.
	if got := liveModelAllowedWithoutCatalog(restricted, "qwen-max"); got != true {
		t.Fatal("liveModelAllowedWithoutCatalog: want true for qwen-max on opencode-go-anthropic-compatible (passes all checks)")
	}

	// A minimax model also passes the gate.
	if got := liveModelAllowedWithoutCatalog(restricted, "minimax-text-01"); got != true {
		t.Fatal("liveModelAllowedWithoutCatalog: want true for minimax-text-01 on opencode-go-anthropic-compatible (passes all checks)")
	}

	// Unrestricted provider: all models pass the gate, so the other checks
	// decide the result. claude-sonnet-4 looks like a coding model → true.
	openAI := providercatalog.Descriptor{ID: "openai"}
	if got := liveModelAllowedWithoutCatalog(openAI, "claude-sonnet-4"); got != true {
		t.Fatal("liveModelAllowedWithoutCatalog: want true for claude-sonnet-4 on openai (unrestricted)")
	}

	// Non-coding model still filtered on an unrestricted provider.
	if got := liveModelAllowedWithoutCatalog(openAI, "text-embedding-3-large"); got != false {
		t.Fatal("liveModelAllowedWithoutCatalog: want false for embedding model on openai")
	}
}

func modelIDs(models []Model) []string {
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	return ids
}

// TestDiscoverOllamaContextWindowFetchesFromNativeShowEndpoint: the generic
// /v1/models probe never carries context-window metadata (parseModelsResponse
// only extracts id/description), so a custom/local Ollama model tag with no
// curated-catalog match has no other source for it. This exercises the
// Ollama-native /api/show fallback that fills that gap.
func TestDiscoverOllamaContextWindowFetchesFromNativeShowEndpoint(t *testing.T) {
	var gotPath, gotMethod, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"model_info": {
				"general.architecture": "qwen2",
				"qwen2.context_length": 131072
			}
		}`))
	}))
	defer server.Close()

	window, err := DiscoverOllamaContextWindow(context.Background(), server.URL+"/v1", "kimi-k2.7-code:cloud", Options{HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("DiscoverOllamaContextWindow returned error: %v", err)
	}
	if window != 131072 {
		t.Fatalf("context window = %d, want 131072", window)
	}
	if gotPath != "/api/show" {
		t.Fatalf("requested path = %q, want /api/show (not under /v1)", gotPath)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q, want POST", gotMethod)
	}
	if !strings.Contains(gotBody, `"kimi-k2.7-code:cloud"`) {
		t.Fatalf("request body = %q, want it to name the model", gotBody)
	}
}

func TestDiscoverOllamaContextWindowRequiresModelName(t *testing.T) {
	if _, err := DiscoverOllamaContextWindow(context.Background(), "http://localhost:11434/v1", "", Options{}); err == nil {
		t.Fatal("expected an error for an empty model name")
	}
}

func TestDiscoverOllamaContextWindowErrorsWhenShowOmitsContextLength(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model_info": {"general.architecture": "qwen2"}}`))
	}))
	defer server.Close()

	if _, err := DiscoverOllamaContextWindow(context.Background(), server.URL+"/v1", "some-model", Options{HTTPClient: server.Client()}); err == nil {
		t.Fatal("expected an error when no *.context_length key is present")
	}
}
