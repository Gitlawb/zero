package providerhealth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/netip"
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
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(`{"data":[{"id":"gpt-4.1"}]}`)),
			Header:     make(http.Header),
		}, nil
	})}

	result := Probe(context.Background(), Options{
		Profile: config.ProviderProfile{
			Name:         "openai-test",
			ProviderKind: config.ProviderKindOpenAICompatible,
			BaseURL:      "https://api.example.com/v1",
			APIKey:       "sk-test-secret",
			Model:        "gpt-4.1",
		},
		Connectivity: true,
		HTTPClient:   client,
		Resolver:     staticResolver{addr: netip.MustParseAddr("93.184.216.34")},
	})

	if result.Status != StatusPass {
		t.Fatalf("Status = %q, want pass: %#v", result.Status, result.Checks)
	}
	if gotPath != "/v1/models" {
		t.Fatalf("probe path = %q, want /v1/models", gotPath)
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
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Status:     "401 Unauthorized",
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"bad key sk-test-secret"}}`)),
			Header:     make(http.Header),
		}, nil
	})}

	result := Probe(context.Background(), Options{
		Profile: config.ProviderProfile{
			Name:         "openai-test",
			ProviderKind: config.ProviderKindOpenAICompatible,
			BaseURL:      "https://api.example.com/v1",
			APIKey:       "sk-test-secret",
			Model:        "custom-model",
		},
		Connectivity: true,
		HTTPClient:   client,
		Resolver:     staticResolver{addr: netip.MustParseAddr("93.184.216.34")},
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

func TestProbeResolvedKindRequiresAuthWhenUnset(t *testing.T) {
	called := false
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("network should not be reached")
	})}

	result := Probe(context.Background(), Options{
		Profile: config.ProviderProfile{
			Name:  "openai-test",
			Model: "gpt-4.1",
		},
		Connectivity: true,
		HTTPClient:   client,
	})

	if result.Status != StatusFail {
		t.Fatalf("Status = %q, want fail: %#v", result.Status, result.Checks)
	}
	check := result.Check("provider.auth")
	if check == nil || check.Status != StatusFail || check.Category != CategoryAuth {
		t.Fatalf("provider.auth = %#v, want auth failure", check)
	}
	if called {
		t.Fatal("HTTP client was called after missing credentials")
	}
}

func TestProbeResolvedKindConnectivityAuthErrorRedactsSecret(t *testing.T) {
	authHeader := make(chan string, 1)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		select {
		case authHeader <- r.Header.Get("Authorization"):
		default:
		}
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Status:     "401 Unauthorized",
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"bad key sk-test-secret"}}`)),
			Header:     make(http.Header),
		}, nil
	})}

	result := Probe(context.Background(), Options{
		Profile: config.ProviderProfile{
			Name:    "openai-test",
			BaseURL: "https://api.example.com/v1",
			APIKey:  "sk-test-secret",
			Model:   "custom-model",
		},
		Connectivity: true,
		HTTPClient:   client,
		Resolver:     staticResolver{addr: netip.MustParseAddr("93.184.216.34")},
	})

	if result.Status != StatusFail {
		t.Fatalf("Status = %q, want fail: %#v", result.Status, result.Checks)
	}
	select {
	case got := <-authHeader:
		if got != "Bearer sk-test-secret" {
			t.Fatalf("Authorization = %q", got)
		}
	default:
		t.Fatal("expected connectivity probe request")
	}
	check := result.Check("provider.connectivity")
	if check == nil || check.Category != CategoryAuth {
		t.Fatalf("provider.connectivity = %#v, want auth failure", check)
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
		Resolver:     staticResolver{addr: netip.MustParseAddr("93.184.216.34")},
		Timeout:      time.Millisecond,
	})

	check := result.Check("provider.connectivity")
	if check == nil || check.Category != CategoryTimeout {
		t.Fatalf("connectivity check = %#v, want timeout category", check)
	}
}

func TestProbeConnectivityBlocksLocalhostBeforeNetwork(t *testing.T) {
	called := false
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("network should not be reached")
	})}

	result := Probe(context.Background(), Options{
		Profile: config.ProviderProfile{
			Name:         "local",
			ProviderKind: config.ProviderKindOpenAICompatible,
			BaseURL:      "http://localhost:11434/v1",
			APIKey:       "sk-test-secret",
			Model:        "local-model",
		},
		Connectivity: true,
		HTTPClient:   client,
	})

	check := result.Check("provider.connectivity")
	if check == nil || check.Status != StatusFail || check.Category != CategoryNetwork {
		t.Fatalf("connectivity check = %#v, want blocked network failure", check)
	}
	if called {
		t.Fatal("HTTP client was called for a blocked localhost URL")
	}
}

func TestProbeConnectivityBlocksPrivateResolvedHostBeforeNetwork(t *testing.T) {
	called := false
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("network should not be reached")
	})}

	result := Probe(context.Background(), Options{
		Profile: config.ProviderProfile{
			Name:         "private",
			ProviderKind: config.ProviderKindOpenAICompatible,
			BaseURL:      "https://private.example/v1",
			APIKey:       "sk-test-secret",
			Model:        "custom-model",
		},
		Connectivity: true,
		HTTPClient:   client,
		Resolver:     staticResolver{addr: netip.MustParseAddr("10.0.0.5")},
	})

	check := result.Check("provider.connectivity")
	if check == nil || check.Status != StatusFail || check.Category != CategoryNetwork {
		t.Fatalf("connectivity check = %#v, want blocked private-network failure", check)
	}
	if called {
		t.Fatal("HTTP client was called for a blocked private resolved host")
	}
}

func TestProbeConnectivityServiceUnavailableFails(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Status:     "503 Service Unavailable",
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"maintenance"}}`)),
			Header:     make(http.Header),
		}, nil
	})}

	result := Probe(context.Background(), Options{
		Profile: config.ProviderProfile{
			Name:         "custom",
			ProviderKind: config.ProviderKindOpenAICompatible,
			BaseURL:      "https://api.example.com/v1",
			APIKey:       "sk-test-secret",
			Model:        "custom-model",
		},
		Connectivity: true,
		HTTPClient:   client,
		Resolver:     staticResolver{addr: netip.MustParseAddr("93.184.216.34")},
	})

	check := result.Check("provider.connectivity")
	if result.Status != StatusFail || check == nil || check.Category != CategoryProvider {
		t.Fatalf("result = %#v, connectivity = %#v, want provider failure", result, check)
	}
}

func TestProbeConnectivityContextCanceledIsNetworkFailure(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, context.Canceled
	})}

	result := Probe(context.Background(), Options{
		Profile: config.ProviderProfile{
			Name:         "custom",
			ProviderKind: config.ProviderKindOpenAICompatible,
			BaseURL:      "https://api.example.com/v1",
			APIKey:       "sk-test-secret",
			Model:        "custom-model",
		},
		Connectivity: true,
		HTTPClient:   client,
		Resolver:     staticResolver{addr: netip.MustParseAddr("93.184.216.34")},
	})

	check := result.Check("provider.connectivity")
	if check == nil || check.Category != CategoryNetwork {
		t.Fatalf("connectivity check = %#v, want network category for context.Canceled", check)
	}
}

func TestPrimaryCheckPrefersFailuresOverPassingConnectivity(t *testing.T) {
	result := Result{Checks: []Check{
		{ID: "provider.connectivity", Status: StatusPass, Message: "reachable"},
		{ID: "provider.auth", Status: StatusFail, Message: "missing auth"},
	}}

	if got := result.PrimaryCheck(); got == nil || got.ID != "provider.auth" {
		t.Fatalf("PrimaryCheck = %#v, want provider.auth failure", got)
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

type staticResolver struct {
	addr netip.Addr
	err  error
}

func (resolver staticResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	if resolver.err != nil {
		return nil, resolver.err
	}
	return []netip.Addr{resolver.addr}, nil
}
