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

func TestNewUsesMiniMaxCompatibleEndpoints(t *testing.T) {
	tests := []struct {
		name         string
		catalogID    string
		providerKind config.ProviderKind
		baseURL      string
		responseBody string
		wantURL      string
	}{
		{
			name:         "global Anthropic",
			catalogID:    "minimax",
			providerKind: config.ProviderKindAnthropicCompat,
			responseBody: "data: {\"type\":\"message_stop\"}\n\n",
			wantURL:      "https://api.minimax.io/anthropic/v1/messages",
		},
		{
			name:         "China Anthropic",
			catalogID:    "minimaxi-cn",
			providerKind: config.ProviderKindAnthropicCompat,
			responseBody: "data: {\"type\":\"message_stop\"}\n\n",
			wantURL:      "https://api.minimaxi.com/anthropic/v1/messages",
		},
		{
			name:         "global OpenAI",
			catalogID:    "custom-openai-compatible",
			providerKind: config.ProviderKindOpenAICompatible,
			baseURL:      "https://api.minimax.io/v1",
			responseBody: "data: [DONE]\n\n",
			wantURL:      "https://api.minimax.io/v1/chat/completions",
		},
		{
			name:         "China OpenAI",
			catalogID:    "custom-openai-compatible",
			providerKind: config.ProviderKindOpenAICompatible,
			baseURL:      "https://api.minimaxi.com/v1",
			responseBody: "data: [DONE]\n\n",
			wantURL:      "https://api.minimaxi.com/v1/chat/completions",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := &captureTransport{responseBody: test.responseBody}
			provider, err := New(config.ProviderProfile{
				Name:         test.name,
				CatalogID:    test.catalogID,
				ProviderKind: test.providerKind,
				BaseURL:      test.baseURL,
				APIKey:       "sk-minimax",
				Model:        "MiniMax-M3",
			}, Options{HTTPClient: &http.Client{Transport: transport}})
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
			if got := transport.request.URL.String(); got != test.wantURL {
				t.Fatalf("request URL = %q, want %q", got, test.wantURL)
			}
		})
	}
}

func TestNewPassesOpenGatewayHY3ModelThrough(t *testing.T) {
	transport := &captureTransport{
		responseBody: "data: [DONE]\n\n",
	}
	client := &http.Client{Transport: transport}

	provider, err := New(config.ProviderProfile{
		Name:         "opengateway",
		CatalogID:    "gitlawb-opengateway",
		ProviderKind: config.ProviderKindOpenAICompatible,
		APIKey:       "ogw_live_test",
		Model:        "tencent/hy3",
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

	if transport.request.URL.String() != "https://opengateway.gitlawb.com/v1/chat/completions" {
		t.Fatalf("request URL = %q, want OpenGateway chat completions", transport.request.URL.String())
	}
	var body map[string]any
	if err := json.NewDecoder(transport.body()).Decode(&body); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if body["model"] != "tencent/hy3" {
		t.Fatalf("model = %q, want tencent/hy3 passthrough", body["model"])
	}

	metadata, err := ResolveRuntimeMetadata(config.ProviderProfile{
		Name:         "opengateway",
		CatalogID:    "gitlawb-opengateway",
		ProviderKind: config.ProviderKindOpenAICompatible,
		Model:        "tencent/hy3",
	}, Options{})
	if err != nil {
		t.Fatalf("ResolveRuntimeMetadata() error = %v", err)
	}
	if metadata.ProviderKind != config.ProviderKindOpenAICompatible || metadata.APIModel != "tencent/hy3" {
		t.Fatalf("runtime metadata = %#v, want OpenAI-compatible tencent/hy3", metadata)
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

// TestPromptCacheKeyOnlyOnOfficialOpenAI locks in #624: session-backed TUI
// turns always carry a PromptCacheKey, but openai-compatible gateways (NVIDIA
// NIM, strict local proxies) reject the OpenAI-only prompt_cache_key field.
// The factory must omit it for openai-compatible profiles while still
// forwarding it for official OpenAI so multi-turn cache routing stays intact.
func TestPromptCacheKeyOnlyOnOfficialOpenAI(t *testing.T) {
	requestWithSession := zeroruntime.CompletionRequest{
		Messages:       []zeroruntime.Message{{Role: zeroruntime.MessageRoleUser, Content: "hello"}},
		PromptCacheKey: "sess_tui_123",
	}

	for _, tc := range []struct {
		name         string
		kind         config.ProviderKind
		wantCacheKey bool
	}{
		{name: "openai", kind: config.ProviderKindOpenAI, wantCacheKey: true},
		{name: "openai-compatible", kind: config.ProviderKindOpenAICompatible, wantCacheKey: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport := &captureTransport{responseBody: "data: [DONE]\n\n"}
			provider, err := New(config.ProviderProfile{
				Name:         "test",
				ProviderKind: tc.kind,
				BaseURL:      "https://provider.example/v1",
				APIKey:       "sk-test",
				Model:        "test-model",
			}, Options{HTTPClient: &http.Client{Transport: transport}})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			stream, err := provider.StreamCompletion(context.Background(), requestWithSession)
			if err != nil {
				t.Fatalf("StreamCompletion() error = %v", err)
			}
			for range stream {
			}
			var body map[string]any
			if err := json.NewDecoder(transport.body()).Decode(&body); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			_, hasKey := body["prompt_cache_key"]
			if hasKey != tc.wantCacheKey {
				t.Fatalf("prompt_cache_key present = %v, want %v; body = %#v", hasKey, tc.wantCacheKey, body)
			}
			if tc.wantCacheKey && body["prompt_cache_key"] != "sess_tui_123" {
				t.Fatalf("prompt_cache_key = %#v, want sess_tui_123", body["prompt_cache_key"])
			}
		})
	}
}

func TestParseThinkTagsForProfileUsesConservativeDefaultsAndOverride(t *testing.T) {
	openAICompatible := resolvedProfile{providerKind: config.ProviderKindOpenAICompatible, apiModel: "qwen3-coder:480b"}
	if !parseThinkTagsForProfile(config.ProviderProfile{}, openAICompatible) {
		t.Fatal("qwen3 OpenAI-compatible model should parse inline think tags by default")
	}
	minimaxM27 := resolvedProfile{providerKind: config.ProviderKindOpenAICompatible, apiModel: "MiniMax-M2.7"}
	if !parseThinkTagsForProfile(config.ProviderProfile{}, minimaxM27) {
		t.Fatal("MiniMax-M2.7 OpenAI-compatible model should parse inline think tags by default")
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
	// Isolate the OAuth token store to an empty temp path so the factory reads no
	// stored login — otherwise this test picks up the developer's real chatgpt
	// OAuth token and the "want empty chatgpt-account-id" assertion fails locally
	// (it still passes in CI, where no login is stored). Mirrors the isolation in
	// TestNewRoutesChatGPTCatalogWithStoredAccountID.
	t.Setenv("ZERO_OAUTH_STORAGE", "file")
	t.Setenv("ZERO_OAUTH_TOKENS_PATH", t.TempDir()+"/tokens.json")

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
	// chatgpt-account-id header, from the login key the CALLER supplies in
	// Options.OAuthLoginKey (the same key the bearer resolver bound). Seed a
	// token in an isolated temp store, then pass that key.
	store, err := newOAuthStoreForTest(t)
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
		HTTPClient:    &http.Client{Transport: transport},
		UserAgent:     "zero-factory-test",
		OAuthLoginKey: oauth.ProviderKey("chatgpt"),
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

// newOAuthStoreForTest pins the OAuth token store to a plain temp FILE and
// returns a Store on it. Pinning ZERO_OAUTH_STORAGE matters as much as the
// path: an inherited "keyring" value would send NewStore to the OS keychain
// and ignore ZERO_OAUTH_TOKENS_PATH entirely, making the test read/write the
// developer's real logins. Exists so the chatgpt factory tests can seed a
// token without copying the path-handling dance from internal/cli.
func newOAuthStoreForTest(t *testing.T) (*oauth.Store, error) {
	t.Helper()
	t.Setenv("ZERO_OAUTH_STORAGE", "file")
	t.Setenv("ZERO_OAUTH_TOKENS_PATH", t.TempDir()+"/tokens.json")
	return oauth.NewStore(oauth.StoreOptions{})
}

// codexAccountForKey reads the chatgpt-account-id from the token stored under a
// FIXED key — the key the caller (cli.oauthLoginForProfile) already bound for the
// bearer token, passed through providers.Options.OAuthLoginKey. The account is
// therefore always read from the same login that issued the bearer (no second,
// independent selection). It re-reads per call, so an in-place token refresh (new
// account claim under the SAME key) is picked up; an empty key (no OAuth login)
// or a missing/account-less token yields "".
func TestCodexAccountForKey(t *testing.T) {
	store, err := newOAuthStoreForTest(t)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.Save(oauth.ProviderKey("chatgpt"), oauth.Token{AccessToken: "tok-1", Account: "acc-catalog-7"}); err != nil {
		t.Fatalf("Save chatgpt: %v", err)
	}
	// A different login WITHOUT an account claim, to prove the read stays on the
	// requested key and never crosses to another login's account.
	if err := store.Save(oauth.ProviderKey("codex"), oauth.Token{AccessToken: "tok-codex"}); err != nil {
		t.Fatalf("Save codex: %v", err)
	}

	if got := codexAccountForKey(oauth.ProviderKey("chatgpt")); got != "acc-catalog-7" {
		t.Fatalf("account = %q, want acc-catalog-7", got)
	}
	// The bound key's token has no account → "", NOT the other login's account.
	if got := codexAccountForKey(oauth.ProviderKey("codex")); got != "" {
		t.Fatalf("account = %q, want empty (must not cross to another login's account)", got)
	}
	// Empty key (no OAuth login) → header omitted.
	if got := codexAccountForKey(""); got != "" {
		t.Fatalf("account for empty key = %q, want empty", got)
	}
	// Unknown key → "".
	if got := codexAccountForKey(oauth.ProviderKey("nope")); got != "" {
		t.Fatalf("account for unknown key = %q, want empty", got)
	}

	// An in-place refresh under the bound key IS reflected per request.
	if err := store.Save(oauth.ProviderKey("chatgpt"), oauth.Token{AccessToken: "tok-refreshed", Account: "acc-rotated"}); err != nil {
		t.Fatalf("refresh chatgpt: %v", err)
	}
	if got := codexAccountForKey(oauth.ProviderKey("chatgpt")); got != "acc-rotated" {
		t.Fatalf("account = %q, want acc-rotated (in-place refresh must be picked up)", got)
	}
}

// TestNewRejectsModelOutsideProviderAllowlist guards the PR #544 P2 fix:
// opencode-go-anthropic-compatible only accepts Qwen and MiniMax model IDs,
// so a Claude model routed through this catalog-backed profile must be rejected
// at runtime, not silently forwarded to https://opencode.ai/zen/go/v1/messages.
func TestNewRejectsModelOutsideProviderAllowlist(t *testing.T) {
	_, err := New(config.ProviderProfile{
		Name:      "opencode-go-anthropic",
		CatalogID: "opencode-go-anthropic-compatible",
		APIKey:    "sk-test",
		Model:     "claude-sonnet-4.5",
	}, Options{})
	if err == nil {
		t.Fatal("New() error = nil, want claude-sonnet-4.5 rejected for opencode-go-anthropic-compatible")
	}
	if !strings.Contains(err.Error(), "opencode-go-anthropic-compatible") {
		t.Fatalf("error = %q, want it to name the provider", err.Error())
	}
	if !strings.Contains(err.Error(), "claude-sonnet-4.5") {
		t.Fatalf("error = %q, want it to name the rejected model", err.Error())
	}
}

// TestNewRejectsRawModelOutsideProviderAllowlist covers the registry-miss
// branch of resolveProfile: an unknown raw model id (not in the model registry)
// typed by the user into a restricted-provider profile must still be rejected
// rather than passthrough-sent.
func TestNewRejectsRawModelOutsideProviderAllowlist(t *testing.T) {
	_, err := New(config.ProviderProfile{
		Name:      "opencode-go-anthropic",
		CatalogID: "opencode-go-anthropic-compatible",
		APIKey:    "sk-test",
		Model:     "totally-custom-not-in-allowlist-12345",
	}, Options{})
	if err == nil {
		t.Fatal("New() error = nil, want unknown raw model rejected for opencode-go-anthropic-compatible")
	}
	if !strings.Contains(err.Error(), "does not allow model") {
		t.Fatalf("error = %q, want allowlist rejection", err.Error())
	}
	if !strings.Contains(err.Error(), "totally-custom-not-in-allowlist-12345") {
		t.Fatalf("error = %q, want the raw model name to appear in the error", err.Error())
	}
}

// TestNewAllowsCuratedModelsForRestrictedProvider proves the gate is not
// over-eager: Qwen and MiniMax curated models pass through unchanged for
// opencode-go-anthropic-compatible.
func TestNewAllowsCuratedModelsForRestrictedProvider(t *testing.T) {
	for _, model := range []string{"minimax-m3", "qwen3.7-plus", "qwen3.7-max", "minimax-m2.7"} {
		_, err := New(config.ProviderProfile{
			Name:      "opencode-go-anthropic",
			CatalogID: "opencode-go-anthropic-compatible",
			APIKey:    "sk-test",
			Model:     model,
		}, Options{})
		if err != nil {
			t.Fatalf("New() error = %v, want curated model %q to be allowed", err, model)
		}
	}
}

// TestNewAllowsAnyModelForUnrestrictedProvider is the regression guard:
// unrestricted providers (no catalog or default catalog id) must continue to
// accept any model id, including those outside the opencode allowlist.
func TestNewAllowsAnyModelForUnrestrictedProvider(t *testing.T) {
	_, err := New(config.ProviderProfile{
		Name:         "anthropic",
		ProviderKind: config.ProviderKindAnthropic,
		APIKey:       "sk-ant",
		Model:        "claude-sonnet-4.5",
	}, Options{})
	if err != nil {
		t.Fatalf("New() error = %v, want unrestricted anthropic provider to accept claude-sonnet-4.5", err)
	}
}

// TestResolveRuntimeMetadataRejectsModelOutsideProviderAllowlist guards the
// read-only metadata path (called from TUI command_center, exec, provider
// health, context report). The metadata path must enforce the same gate as
// New() so a config-time override cannot escape through it.
func TestResolveRuntimeMetadataRejectsModelOutsideProviderAllowlist(t *testing.T) {
	_, err := ResolveRuntimeMetadata(config.ProviderProfile{
		Name:      "opencode-go-anthropic",
		CatalogID: "opencode-go-anthropic-compatible",
		APIKey:    "sk-test",
		Model:     "claude-sonnet-4.5",
	}, Options{})
	if err == nil {
		t.Fatal("ResolveRuntimeMetadata error = nil, want claude-sonnet-4.5 rejected")
	}
	if !strings.Contains(err.Error(), "opencode-go-anthropic-compatible") {
		t.Fatalf("error = %q, want it to name the provider", err.Error())
	}
	if !strings.Contains(err.Error(), "claude-sonnet-4.5") {
		t.Fatalf("error = %q, want it to name the rejected model", err.Error())
	}
}
