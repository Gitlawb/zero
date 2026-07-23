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

func TestLocalRuntimeCandidatesCoverOllamaLMStudioAndAtomicChat(t *testing.T) {
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
	atomicChat, ok := byCatalog["atomic-chat-local"]
	if !ok {
		t.Fatalf("expected an atomic-chat-local candidate, got %#v", candidates)
	}
	if atomicChat.BaseURL != "http://127.0.0.1:1337/v1" {
		t.Fatalf("atomic-chat-local candidate BaseURL = %q, want http://127.0.0.1:1337/v1", atomicChat.BaseURL)
	}
	if atomicChat.DefaultModel != "local-model" {
		t.Fatalf("atomic-chat-local candidate DefaultModel = %q, want local-model", atomicChat.DefaultModel)
	}
	if atomicChat.RequiresKey {
		t.Fatalf("atomic-chat-local candidate must not require an API key: %#v", atomicChat)
	}
	// The hosted atomic-chat preset stays remote and key-gated, so it must never
	// be probed as a local runtime.
	if _, ok := byCatalog["atomic-chat"]; ok {
		t.Fatalf("hosted atomic-chat must not be a local-runtime candidate, got %#v", candidates)
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
	if detected[0].Models == nil || len(detected[0].Models) == 0 || detected[0].Models[0] != "llama3.1" {
		t.Fatalf("expected discovered model list, got %#v", detected[0].Models)
	}
}

// A local runtime serves whichever model the user loaded, so the adopt command
// must pin the id the probe saw. Atomic Chat pulls its catalog from Hugging
// Face, so the served id is an arbitrary repo id and never the "local-model"
// placeholder the catalog carries as DefaultModel.
func TestSetupActionPinsProbedModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[{"id":"unsloth/Qwen3-Coder-30B-A3B-Instruct-GGUF"}]}`))
	}))
	defer server.Close()

	detected := DetectLocalRuntimes(context.Background(), LocalDetectOptions{
		HTTPClient: server.Client(),
		Candidates: []LocalRuntime{{
			CatalogID:    "atomic-chat-local",
			Name:         "Atomic Chat Local",
			BaseURL:      server.URL + "/v1",
			DefaultModel: "local-model",
		}},
	})
	if len(detected) != 1 {
		t.Fatalf("DetectLocalRuntimes() = %#v, want one reachable runtime", detected)
	}
	if got := detected[0].AdoptModel(); got != "unsloth/Qwen3-Coder-30B-A3B-Instruct-GGUF" {
		t.Fatalf("AdoptModel() = %q, want the probed id, not the catalog placeholder", got)
	}
	action := detected[0].SetupAction()
	if !strings.Contains(action.Command, "--model unsloth/Qwen3-Coder-30B-A3B-Instruct-GGUF") {
		t.Fatalf("SetupAction command must pin the probed model, got %q", action.Command)
	}
	if strings.Contains(action.Command, "local-model") {
		t.Fatalf("SetupAction command must not persist the catalog placeholder, got %q", action.Command)
	}
}

// When the probe returns no ids the command stays on the catalog default rather
// than inventing a model.
func TestSetupActionOmitsModelWhenProbeFoundNone(t *testing.T) {
	runtime := DetectedLocalRuntime{
		LocalRuntime: LocalRuntime{CatalogID: "atomic-chat-local", Name: "Atomic Chat Local", BaseURL: "http://127.0.0.1:1337/v1", DefaultModel: "local-model"},
		Reachable:    true,
	}
	if got := runtime.AdoptModel(); got != "" {
		t.Fatalf("AdoptModel() = %q, want empty", got)
	}
	if command := runtime.SetupAction().Command; strings.Contains(command, "--model") {
		t.Fatalf("SetupAction command must omit --model when nothing was probed, got %q", command)
	}
}

// jatmn's P1: the pinned model id comes from an untrusted /v1/models response,
// so the adopt command must render it shell-safely. A command-substitution id
// must be single-quoted, never left inside double quotes where it still runs.
func TestSetupActionQuotesShellMetacharactersInProbedModel(t *testing.T) {
	runtime := DetectedLocalRuntime{
		LocalRuntime: LocalRuntime{CatalogID: "atomic-chat-local", Name: "Atomic Chat Local", BaseURL: "http://127.0.0.1:1337/v1", DefaultModel: "local-model"},
		Reachable:    true,
		Models:       []string{"$(touch pwned)"},
	}
	command := runtime.SetupAction().Command
	if strings.Contains(command, `"$(touch pwned)"`) {
		t.Fatalf("model rendered inside double quotes still executes on paste: %q", command)
	}
	if !strings.Contains(command, `'$(touch pwned)'`) {
		t.Fatalf("model must be single-quoted so the shell cannot expand it, got %q", command)
	}
}

// A server that advertises the catalog default alongside other ids keeps the
// default, so an existing Ollama setup does not silently switch models.
func TestAdoptModelPrefersCatalogDefaultWhenServed(t *testing.T) {
	runtime := DetectedLocalRuntime{
		LocalRuntime: LocalRuntime{CatalogID: "ollama", Name: "Ollama Local", DefaultModel: "llama3.1"},
		Reachable:    true,
		Models:       []string{"qwen3:8b", "llama3.1"},
	}
	if got := runtime.AdoptModel(); got != "llama3.1" {
		t.Fatalf("AdoptModel() = %q, want the catalog default when the server serves it", got)
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
