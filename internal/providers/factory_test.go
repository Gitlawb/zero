package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/oauth"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

func TestNewCreatesOpenAIProviderWithFactoryOptions(t *testing.T) {
	transport := &captureTransport{
		responseBody: "data: [DONE]\n\n",
	}
	client := &http.Client{Transport: transport}

	provider, err := New(config.ProviderProfile{
		Name:         "custom",
		ProviderKind: config.ProviderKindOpenAICompatible,
		BaseURL:      "https://provider.example/v1/",
		APIKey:       "sk-factory",
		Model:        "factory-model",
	}, Options{
		HTTPClient: client,
		UserAgent:  "zero-factory-test",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	stream, err := provider.StreamCompletion(context.Background(), zeroruntime.CompletionRequest{
		Messages: []zeroruntime.Message{{Role: zeroruntime.MessageRoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("StreamCompletion() error = %v", err)
	}
	for range stream {
	}

	if transport.request == nil {
		t.Fatal("HTTP client was not used")
	}
	if transport.request.URL.String() != "https://provider.example/v1/chat/completions" {
		t.Fatalf("request URL = %q, want provider base URL", transport.request.URL.String())
	}
	if transport.request.Header.Get("Authorization") != "Bearer sk-factory" {
		t.Fatalf("Authorization = %q, want bearer token", transport.request.Header.Get("Authorization"))
	}
	if transport.request.Header.Get("User-Agent") != "zero-factory-test" {
		t.Fatalf("User-Agent = %q, want factory user agent", transport.request.Header.Get("User-Agent"))
	}
}

func TestNewThreadsCustomProviderHeaders(t *testing.T) {
	transport := &captureTransport{
		responseBody: "data: [DONE]\n\n",
	}
	client := &http.Client{Transport: transport}

	provider, err := New(config.ProviderProfile{
		Name:          "gateway",
		ProviderKind:  config.ProviderKindOpenAICompatible,
		BaseURL:       "https://gateway.example/v1",
		APIKey:        "sk-gateway",
		AuthHeader:    "X-API-Key",
		AuthScheme:    "Token",
		CustomHeaders: map[string]string{"HTTP-Referer": "https://zero.dev"},
		Model:         "gateway-model",
	}, Options{HTTPClient: client})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	stream, err := provider.StreamCompletion(context.Background(), zeroruntime.CompletionRequest{
		Messages: []zeroruntime.Message{{Role: zeroruntime.MessageRoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("StreamCompletion() error = %v", err)
	}
	for range stream {
	}

	if transport.request.Header.Get("Authorization") != "" {
		t.Fatalf("Authorization = %q, want empty when custom auth header is set", transport.request.Header.Get("Authorization"))
	}
	if transport.request.Header.Get("X-API-Key") != "Token sk-gateway" {
		t.Fatalf("X-API-Key = %q, want custom auth header", transport.request.Header.Get("X-API-Key"))
	}
	if transport.request.Header.Get("HTTP-Referer") != "https://zero.dev" {
		t.Fatalf("HTTP-Referer = %q, want custom provider header", transport.request.Header.Get("HTTP-Referer"))
	}
}

func TestNewSupportsOpenAIProviderKind(t *testing.T) {
	provider, err := New(config.ProviderProfile{
		Name:         "openai",
		ProviderKind: config.ProviderKindOpenAI,
		Model:        "gpt-test",
	}, Options{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if provider == nil {
		t.Fatal("New() returned nil provider")
	}
}

func TestParseThinkTagsForProfileUsesConservativeDefaultsAndOverride(t *testing.T) {
	openAICompatible := resolvedProfile{providerKind: config.ProviderKindOpenAICompatible, apiModel: "qwen3-coder:480b"}
	if !parseThinkTagsForProfile(config.ProviderProfile{}, openAICompatible) {
		t.Fatal("qwen3 OpenAI-compatible model should parse inline think tags by default")
	}

	generic := resolvedProfile{providerKind: config.ProviderKindOpenAICompatible, apiModel: "factory-model"}
	if parseThinkTagsForProfile(config.ProviderProfile{}, generic) {
		t.Fatal("generic OpenAI-compatible model should preserve literal think tags by default")
	}

	official := resolvedProfile{providerKind: config.ProviderKindOpenAI, apiModel: "gpt-4.1"}
	if parseThinkTagsForProfile(config.ProviderProfile{}, official) {
		t.Fatal("official OpenAI model should preserve literal think tags by default")
	}

	enabled := true
	if !parseThinkTagsForProfile(config.ProviderProfile{ParseThinkTags: &enabled}, generic) {
		t.Fatal("explicit parseThinkTags=true should enable inline think parsing")
	}

	disabled := false
	if parseThinkTagsForProfile(config.ProviderProfile{ParseThinkTags: &disabled}, openAICompatible) {
		t.Fatal("explicit parseThinkTags=false should disable inline think parsing")
	}
}

func TestNewResolvesKnownModelToAPIModelAndProvider(t *testing.T) {
	transport := &captureTransport{
		responseBody: "data: {\"type\":\"message_stop\"}\n\n",
	}
	client := &http.Client{Transport: transport}

	provider, err := New(config.ProviderProfile{
		Name:   "claude",
		APIKey: "sk-ant",
		Model:  "claude-sonnet-4.5",
	}, Options{
		HTTPClient: client,
		UserAgent:  "zero-factory-test",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	stream, err := provider.StreamCompletion(context.Background(), zeroruntime.CompletionRequest{
		Messages: []zeroruntime.Message{{Role: zeroruntime.MessageRoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("StreamCompletion() error = %v", err)
	}
	for range stream {
	}

	if transport.request == nil {
		t.Fatal("HTTP client was not used")
	}
	if transport.request.URL.String() != "https://api.anthropic.com/v1/messages" {
		t.Fatalf("request URL = %q, want Anthropic Messages API", transport.request.URL.String())
	}
	if transport.request.Header.Get("x-api-key") != "sk-ant" {
		t.Fatalf("x-api-key = %q, want Anthropic key", transport.request.Header.Get("x-api-key"))
	}
	if transport.request.Header.Get("User-Agent") != "zero-factory-test" {
		t.Fatalf("User-Agent = %q, want factory user agent", transport.request.Header.Get("User-Agent"))
	}
	var body map[string]any
	if err := json.NewDecoder(transport.body()).Decode(&body); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if body["model"] != "claude-sonnet-4-5-20250929" {
		t.Fatalf("model = %q, want registry API model", body["model"])
	}
	if body["max_tokens"] != float64(64000) {
		t.Fatalf("max_tokens = %#v, want registry output ceiling", body["max_tokens"])
	}
}

func TestNewCreatesGeminiProviderFromFactoryOptions(t *testing.T) {
	transport := &captureTransport{
		responseBody: "data: {}\n\n",
	}
	client := &http.Client{Transport: transport}

	provider, err := New(config.ProviderProfile{
		Name:         "google",
		ProviderKind: config.ProviderKindGoogle,
		APIKey:       "sk-google",
		Model:        "gemini-2.5-flash",
	}, Options{
		HTTPClient: client,
		UserAgent:  "zero-factory-test",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	stream, err := provider.StreamCompletion(context.Background(), zeroruntime.CompletionRequest{
		Messages: []zeroruntime.Message{{Role: zeroruntime.MessageRoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("StreamCompletion() error = %v", err)
	}
	for range stream {
	}

	if transport.request == nil {
		t.Fatal("HTTP client was not used")
	}
	wantURL := "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:streamGenerateContent?alt=sse"
	if transport.request.URL.String() != wantURL {
		t.Fatalf("request URL = %q, want %s", transport.request.URL.String(), wantURL)
	}
	if transport.request.Header.Get("x-goog-api-key") != "sk-google" {
		t.Fatalf("x-goog-api-key = %q, want Google key", transport.request.Header.Get("x-goog-api-key"))
	}
	var body map[string]any
	if err := json.NewDecoder(transport.body()).Decode(&body); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	generationConfig := body["generationConfig"].(map[string]any)
	if generationConfig["maxOutputTokens"] != float64(65536) {
		t.Fatalf("maxOutputTokens = %#v, want registry output ceiling", generationConfig["maxOutputTokens"])
	}
}

func TestNewRejectsMismatchedOfficialProviderAndKnownModel(t *testing.T) {
	_, err := New(config.ProviderProfile{
		Name:         "openai",
		ProviderKind: config.ProviderKindOpenAI,
		Model:        "claude-sonnet-4.5",
	}, Options{})
	if err == nil {
		t.Fatal("New() error = nil, want provider/model mismatch")
	}
	if !strings.Contains(err.Error(), "belongs to anthropic, not openai") {
		t.Fatalf("error = %q, want model/provider mismatch", err.Error())
	}
}

func TestNewRejectsUnsupportedProviderKind(t *testing.T) {
	_, err := New(config.ProviderProfile{
		Name:         "bad",
		ProviderKind: "bedrock",
		Model:        "model",
	}, Options{})
	if err == nil {
		t.Fatal("New() error = nil, want unsupported kind error")
	}
	if !strings.Contains(err.Error(), `unsupported provider kind "bedrock"`) {
		t.Fatalf("error = %q, want unsupported provider kind", err.Error())
	}
}

func TestNewRoutesChatGPTCatalogToCodexProvider(t *testing.T) {
	transport := &captureTransport{
		responseBody: "data: [DONE]\n\n",
	}
	client := &http.Client{Transport: transport}

	provider, err := New(config.ProviderProfile{
		Name:      "chatgpt",
		CatalogID: "chatgpt",
		Model:     "gpt-5",
	}, Options{
		HTTPClient: client,
		UserAgent:  "zero-factory-test",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	stream, err := provider.StreamCompletion(context.Background(), zeroruntime.CompletionRequest{
		Messages: []zeroruntime.Message{{Role: zeroruntime.MessageRoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("StreamCompletion() error = %v", err)
	}
	for range stream {
	}
	if transport.request == nil {
		t.Fatal("HTTP client was not used")
	}
	// The chatgpt catalog's baseURL is the Codex backend. The Codex
	// provider targets the Responses API at `{baseURL}/responses`, not
	// `/chat/completions` (a chat-completions body on this path would 404
	// or be misrouted by the Codex gateway).
	if !strings.HasSuffix(transport.request.URL.Path, "/responses") {
		t.Fatalf("request URL path = %q, want .../responses", transport.request.URL.Path)
	}
	wantHost := "chatgpt.com"
	if !strings.Contains(transport.request.URL.Host, wantHost) {
		t.Fatalf("request URL host = %q, want the Codex backend (chatgpt.com)", transport.request.URL.Host)
	}
	// The Codex-required headers must be present even when the OAuth token
	// has no account id (the AccountResolver returns ok=false in that case,
	// so the chatgpt-account-id header is just omitted, not wrongly set).
	if got := transport.request.Header.Get("originator"); got != "codex_cli_rs" {
		t.Fatalf("originator = %q, want codex_cli_rs", got)
	}
	if got := transport.request.Header.Get("chatgpt-account-id"); got != "" {
		t.Fatalf("chatgpt-account-id = %q, want empty when no OAuth login is stored", got)
	}
}

func TestNewRoutesChatGPTCatalogWithStoredAccountID(t *testing.T) {
	// The factory reads the stored OAuth token's Account field for the
	// chatgpt-account-id header. Seed a token in a temp store and point
	// ZERO_OAUTH_TOKENS_PATH at it so the factory picks it up.
	dir := t.TempDir()
	t.Setenv("ZERO_OAUTH_TOKENS_PATH", dir+"/tokens.json")
	store, err := newOAuthStoreForTest()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.Save(oauth.ProviderKey("chatgpt"), oauth.Token{
		AccessToken: "tok-1",
		Account:     "acc-stored-42",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	transport := &captureTransport{
		responseBody: "data: [DONE]\n\n",
	}
	provider, err := New(config.ProviderProfile{
		Name:      "chatgpt",
		CatalogID: "chatgpt",
		Model:     "gpt-5",
	}, Options{
		HTTPClient: &http.Client{Transport: transport},
		UserAgent:  "zero-factory-test",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	stream, err := provider.StreamCompletion(context.Background(), zeroruntime.CompletionRequest{
		Messages: []zeroruntime.Message{{Role: zeroruntime.MessageRoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("StreamCompletion() error = %v", err)
	}
	for range stream {
	}
	if got := transport.request.Header.Get("chatgpt-account-id"); got != "acc-stored-42" {
		t.Fatalf("chatgpt-account-id = %q, want acc-stored-42", got)
	}
}

func TestIsCodexCatalog(t *testing.T) {
	cases := []struct {
		catalogID string
		want      bool
	}{
		{"chatgpt", true},
		{"ChatGPT", true},
		{"openai", false},
		{"", false},
		{"chatgpt-proxy", false}, // the local proxy catalog stays on the openai path
	}
	for _, tc := range cases {
		got := isCodexCatalog(config.ProviderProfile{CatalogID: tc.catalogID}, resolvedProfile{})
		if got != tc.want {
			t.Errorf("isCodexCatalog(%q) = %v, want %v", tc.catalogID, got, tc.want)
		}
	}
}

type captureTransport struct {
	request      *http.Request
	requestBody  string
	responseBody string
}

func (transport *captureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.request = request
	if request.Body != nil {
		body, _ := io.ReadAll(request.Body)
		transport.requestBody = string(body)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(transport.responseBody)),
		Request:    request,
	}, nil
}

func (transport *captureTransport) body() io.Reader {
	return strings.NewReader(transport.requestBody)
}

// newOAuthStoreForTest builds a Store pointed at the current
// ZERO_OAUTH_TOKENS_PATH (set by the caller). It exists so the chatgpt
// factory tests can seed a token without copying the path-handling dance
// from internal/cli.
func newOAuthStoreForTest() (*oauth.Store, error) {
	return oauth.NewStore(oauth.StoreOptions{})
}
