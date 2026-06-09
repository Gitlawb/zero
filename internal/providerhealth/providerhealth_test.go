package providerhealth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/config"
)

func TestProbeConfigOnlyMissingProviderFails(t *testing.T) {
	result := Probe(context.Background(), Options{})

	if result.Status != StatusFail {
		t.Fatalf("Status = %q, want %q", result.Status, StatusFail)
	}
	check := result.Check("provider.config")
	if check == nil || check.Status != StatusFail {
		t.Fatalf("missing provider.config failure: %#v", result.Checks)
	}
}

func TestProbeConnectivityOpenAIModelsEndpointPasses(t *testing.T) {
	var gotPath string
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4.1"}]}`))
	}))
	defer server.Close()

	result := Probe(context.Background(), Options{
		Profile: config.ProviderProfile{
			Name:         "openai-test",
			ProviderKind: config.ProviderKindOpenAICompatible,
			BaseURL:      server.URL,
			APIKey:       "sk-test-secret",
			Model:        "gpt-4.1",
		},
		Connectivity: true,
		HTTPClient:   server.Client(),
	})

	if result.Status != StatusPass {
		t.Fatalf("Status = %q, want pass: %#v", result.Status, result.Checks)
	}
	if gotPath != "/models" {
		t.Fatalf("probe path = %q, want /models", gotPath)
	}
	if gotAuth != "Bearer sk-test-secret" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	check := result.Check("provider.connectivity")
	if check == nil || check.Status != StatusPass {
		t.Fatalf("missing connectivity pass: %#v", result.Checks)
	}
}

func TestProbeConnectivityClassifiesAndRedactsAuthError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key sk-test-secret"}}`))
	}))
	defer server.Close()

	result := Probe(context.Background(), Options{
		Profile: config.ProviderProfile{
			Name:         "openai-test",
			ProviderKind: config.ProviderKindOpenAICompatible,
			BaseURL:      server.URL,
			APIKey:       "sk-test-secret",
			Model:        "custom-model",
		},
		Connectivity: true,
		HTTPClient:   server.Client(),
	})

	if result.Status != StatusFail {
		t.Fatalf("Status = %q, want fail: %#v", result.Status, result.Checks)
	}
	check := result.Check("provider.connectivity")
	if check == nil {
		t.Fatalf("missing connectivity check: %#v", result.Checks)
	}
	if check.Category != CategoryAuth {
		t.Fatalf("Category = %q, want %q", check.Category, CategoryAuth)
	}
	if strings.Contains(check.Message, "sk-test-secret") {
		t.Fatalf("secret leaked in message: %q", check.Message)
	}
}

func TestProbeConnectivityClassifiesTimeout(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})}

	result := Probe(context.Background(), Options{
		Profile: config.ProviderProfile{
			Name:         "openai-test",
			ProviderKind: config.ProviderKindOpenAICompatible,
			BaseURL:      "https://example.invalid/v1",
			APIKey:       "sk-test-secret",
			Model:        "custom-model",
		},
		Connectivity: true,
		HTTPClient:   client,
		Timeout:      time.Millisecond,
	})

	check := result.Check("provider.connectivity")
	if check == nil || check.Category != CategoryTimeout {
		t.Fatalf("connectivity check = %#v, want timeout category", check)
	}
}

func TestProbeConnectivityUnsupportedTransportWarnsWithoutNetwork(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network should not be reached")
	})}

	result := Probe(context.Background(), Options{
		Profile: config.ProviderProfile{
			Name:      "bedrock",
			CatalogID: "bedrock",
			Model:     "anthropic.claude-3-5-sonnet-20241022-v2:0",
		},
		Connectivity: true,
		HTTPClient:   client,
	})

	check := result.Check("provider.runtime")
	if check == nil || check.Category != CategoryUnsupported {
		t.Fatalf("runtime check = %#v, want unsupported category", check)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
