package provideronboarding

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLocalRuntimeCandidatesCoverOllamaAndLMStudio(t *testing.T) {
	candidates := LocalRuntimeCandidates()
	if len(candidates) == 0 {
		t.Fatalf("LocalRuntimeCandidates() returned no candidates")
	}
	byCatalog := map[string]LocalRuntime{}
	for _, candidate := range candidates {
		byCatalog[candidate.CatalogID] = candidate
	}
	ollama, ok := byCatalog["ollama"]
	if !ok {
		t.Fatalf("expected an ollama candidate, got %#v", candidates)
	}
	if !strings.Contains(ollama.BaseURL, "11434") {
		t.Fatalf("ollama candidate must probe default port 11434, got %q", ollama.BaseURL)
	}
	if ollama.RequiresKey {
		t.Fatalf("ollama candidate must not require an API key: %#v", ollama)
	}
	lmstudio, ok := byCatalog["lmstudio"]
	if !ok {
		t.Fatalf("expected an lmstudio candidate, got %#v", candidates)
	}
	if !strings.Contains(lmstudio.BaseURL, "1234") {
		t.Fatalf("lmstudio candidate must probe default port 1234, got %q", lmstudio.BaseURL)
	}
	if lmstudio.RequiresKey {
		t.Fatalf("lmstudio candidate must not require an API key: %#v", lmstudio)
	}
}

func TestDetectLocalRuntimesReportsReachableRuntime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[{"id":"llama3.1"}]}`))
	}))
	defer server.Close()

	detected := DetectLocalRuntimes(context.Background(), LocalDetectOptions{
		HTTPClient: server.Client(),
		Candidates: []LocalRuntime{{
			CatalogID: "ollama",
			Name:      "Ollama Local",
			BaseURL:   server.URL + "/v1",
		}},
	})
	if len(detected) != 1 {
		t.Fatalf("DetectLocalRuntimes() = %#v, want one reachable runtime", detected)
	}
	if !detected[0].Reachable {
		t.Fatalf("runtime should be reachable: %#v", detected[0])
	}
	if len(detected[0].Models) == 0 || detected[0].Models[0] != "llama3.1" {
		t.Fatalf("expected discovered model list, got %#v", detected[0].Models)
	}
}

func TestDetectLocalRuntimesSkipsUnreachableRuntime(t *testing.T) {
	// A client whose transport always fails simulates a closed local port.
	failing := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("connection refused")
	})}

	detected := DetectLocalRuntimes(context.Background(), LocalDetectOptions{
		HTTPClient: failing,
		Candidates: []LocalRuntime{{
			CatalogID: "ollama",
			Name:      "Ollama Local",
			BaseURL:   "http://127.0.0.1:11434/v1",
		}},
	})
	if len(detected) != 0 {
		t.Fatalf("DetectLocalRuntimes() = %#v, want no reachable runtimes", detected)
	}
}

func TestDetectLocalRuntimesIgnoresServerErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	detected := DetectLocalRuntimes(context.Background(), LocalDetectOptions{
		HTTPClient: server.Client(),
		Candidates: []LocalRuntime{{
			CatalogID: "lmstudio",
			Name:      "LM Studio",
			BaseURL:   server.URL + "/v1",
		}},
	})
	if len(detected) != 0 {
		t.Fatalf("a 5xx local response must not count as a reachable runtime: %#v", detected)
	}
}

func TestLocalRuntimeActionOffersNoKeySetup(t *testing.T) {
	runtime := DetectedLocalRuntime{LocalRuntime: LocalRuntime{
		CatalogID: "ollama",
		Name:      "Ollama Local",
		BaseURL:   "http://localhost:11434/v1",
	}, Reachable: true}

	action := runtime.SetupAction()
	if !strings.Contains(action.Command, "zero providers add ollama") {
		t.Fatalf("setup command should add the ollama provider, got %q", action.Command)
	}
	if strings.Contains(action.Command, "--api-key-env") {
		t.Fatalf("local runtime setup must not require an API key env, got %q", action.Command)
	}
	if !strings.Contains(strings.ToLower(action.Detail), "no api key") {
		t.Fatalf("setup detail should advertise the no-key path, got %q", action.Detail)
	}
}

func TestDetectLocalRuntimesAppliesDefaultTimeout(t *testing.T) {
	// A handler that blocks past the configured timeout must be treated as
	// unreachable rather than hanging the wizard.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	detected := DetectLocalRuntimes(context.Background(), LocalDetectOptions{
		HTTPClient: server.Client(),
		Timeout:    20 * time.Millisecond,
		Candidates: []LocalRuntime{{
			CatalogID: "ollama",
			Name:      "Ollama Local",
			BaseURL:   server.URL + "/v1",
		}},
	})
	if len(detected) != 0 {
		t.Fatalf("a runtime slower than the timeout must be skipped: %#v", detected)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
