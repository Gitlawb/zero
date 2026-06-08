package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/config"
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
