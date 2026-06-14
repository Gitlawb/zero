package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	zeroSandbox "github.com/Gitlawb/zero/internal/sandbox"
)

type fakeSearchBackend struct {
	results  []searchResult
	err      error
	gotQuery string
	gotLimit int
}

func (f *fakeSearchBackend) Search(_ context.Context, query string, limit int) ([]searchResult, error) {
	f.gotQuery = query
	f.gotLimit = limit
	return f.results, f.err
}

func TestWebSearchFormatsResults(t *testing.T) {
	backend := &fakeSearchBackend{results: []searchResult{
		{Title: "Go errors", URL: "https://go.dev/blog/errors", Snippet: "Working with errors in Go."},
		{Title: "Wrapping", URL: "https://go.dev/blog/wrap", Snippet: "Error wrapping."},
	}}
	tool := newWebSearchToolWithBackend(backend)

	res := tool.Run(context.Background(), map[string]any{"query": "go errors"})

	if res.Status != StatusOK {
		t.Fatalf("status = %v, output = %q", res.Status, res.Output)
	}
	for _, want := range []string{
		"1. Go errors — https://go.dev/blog/errors",
		"   Working with errors in Go.",
		"2. Wrapping — https://go.dev/blog/wrap",
		"   Error wrapping.",
	} {
		if !strings.Contains(res.Output, want) {
			t.Fatalf("output missing %q:\n%s", want, res.Output)
		}
	}
	if backend.gotQuery != "go errors" {
		t.Fatalf("backend query = %q, want %q", backend.gotQuery, "go errors")
	}
}

func TestWebSearchClampsAndDefaultsLimit(t *testing.T) {
	backend := &fakeSearchBackend{}
	tool := newWebSearchToolWithBackend(backend)

	// Above the cap clamps to 10 rather than erroring.
	tool.Run(context.Background(), map[string]any{"query": "q", "limit": 50})
	if backend.gotLimit != maxWebSearchLimit {
		t.Fatalf("limit = %d, want clamp to %d", backend.gotLimit, maxWebSearchLimit)
	}
	// Missing limit falls back to the default.
	tool.Run(context.Background(), map[string]any{"query": "q"})
	if backend.gotLimit != defaultWebSearchLimit {
		t.Fatalf("default limit = %d, want %d", backend.gotLimit, defaultWebSearchLimit)
	}
}

func TestWebSearchRequiresQuery(t *testing.T) {
	tool := newWebSearchToolWithBackend(&fakeSearchBackend{})
	res := tool.Run(context.Background(), map[string]any{})
	if res.Status != StatusError {
		t.Fatalf("expected StatusError for missing query, got %v", res.Status)
	}
}

func TestWebSearchUnconfiguredBackend(t *testing.T) {
	tool := newWebSearchToolWithBackend(nil)
	res := tool.Run(context.Background(), map[string]any{"query": "q"})
	if res.Status != StatusError {
		t.Fatalf("expected StatusError, got %v", res.Status)
	}
	if !strings.Contains(res.Output, "no search backend configured") {
		t.Fatalf("output should explain the missing backend, got %q", res.Output)
	}
}

func TestWebSearchRedactsBackendError(t *testing.T) {
	secret := "sk-livesecret0123456789abcdef"
	backend := &fakeSearchBackend{err: fmt.Errorf("backend rejected key %s", secret)}
	tool := newWebSearchToolWithBackend(backend)

	res := tool.Run(context.Background(), map[string]any{"query": "q"})

	if res.Status != StatusError {
		t.Fatalf("expected StatusError, got %v", res.Status)
	}
	if strings.Contains(res.Output, secret) {
		t.Fatalf("backend error leaked the API key into output: %q", res.Output)
	}
}

func TestWebSearchRegisteredInCoreNetworkTools(t *testing.T) {
	found := false
	for _, tool := range CoreNetworkTools() {
		if tool.Name() == "web_search" {
			found = true
		}
	}
	if !found {
		t.Fatal("web_search should be registered in CoreNetworkTools()")
	}
}

func TestHTTPSearchBackendSendsProviderAndParsesResults(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"Title","url":"https://x.dev","snippet":"snip"}]}`))
	}))
	defer server.Close()

	backend := &httpSearchBackend{client: server.Client(), baseURL: server.URL, apiKey: "k", provider: "exa"}
	results, err := backend.Search(context.Background(), "q", 3)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].Title != "Title" || results[0].URL != "https://x.dev" {
		t.Fatalf("results = %#v", results)
	}
	// The configured provider and query must reach the backend.
	if gotBody["provider"] != "exa" {
		t.Fatalf("ZERO_WEBSEARCH_PROVIDER not forwarded: %#v", gotBody)
	}
	if gotBody["query"] != "q" {
		t.Fatalf("query not forwarded: %#v", gotBody)
	}
}

type fakeHostedBackend struct {
	host    string
	results []searchResult
	called  bool
}

func (b *fakeHostedBackend) Search(context.Context, string, int) ([]searchResult, error) {
	b.called = true
	return b.results, nil
}

func (b *fakeHostedBackend) endpointHost() string { return b.host }

func TestHTTPSearchBackendEndpointHost(t *testing.T) {
	backend := &httpSearchBackend{baseURL: "https://api.search.example:8443/v1/search"}
	if got := backend.endpointHost(); got != "api.search.example" {
		t.Fatalf("endpointHost = %q, want api.search.example", got)
	}
}

// TestWebSearchRunWithSandboxAllowsByDefaultUnderDeny verifies the default
// posture: a first-party in-process tool is NOT subject to the sandbox network
// policy unless EnforceToolNetwork is set, so web_search runs even under deny.
func TestWebSearchRunWithSandboxAllowsByDefaultUnderDeny(t *testing.T) {
	backend := &fakeHostedBackend{host: "search.example", results: []searchResult{{Title: "T", URL: "https://x.test"}}}
	tool := newWebSearchToolWithBackend(backend).(webSearchTool)
	engine := zeroSandbox.NewEngine(zeroSandbox.EngineOptions{
		Policy: zeroSandbox.Policy{Mode: zeroSandbox.ModeEnforce, Network: zeroSandbox.NetworkDeny},
	})
	res := tool.RunWithSandbox(context.Background(), map[string]any{"query": "hi"}, engine)
	if res.Status != StatusOK {
		t.Fatalf("web_search must run by default even under deny, got %q: %s", res.Status, res.Output)
	}
	if !backend.called {
		t.Fatal("search backend must be called when tool-network enforcement is off (default)")
	}
}

func TestWebSearchRunWithSandboxBlocksUnderDenyWhenEnforced(t *testing.T) {
	backend := &fakeHostedBackend{host: "search.example"}
	tool := newWebSearchToolWithBackend(backend).(webSearchTool)
	engine := zeroSandbox.NewEngine(zeroSandbox.EngineOptions{
		Policy: zeroSandbox.Policy{Mode: zeroSandbox.ModeEnforce, Network: zeroSandbox.NetworkDeny, EnforceToolNetwork: true},
	})
	res := tool.RunWithSandbox(context.Background(), map[string]any{"query": "hi"}, engine)
	if res.Status != StatusError || !strings.Contains(res.Output, "disabled") {
		t.Fatalf("expected deny block, got %q: %s", res.Status, res.Output)
	}
	if backend.called {
		t.Fatal("search backend must not be called under network deny when enforced")
	}
}

func TestWebSearchRunWithSandboxScopedBlocksUnlistedHost(t *testing.T) {
	backend := &fakeHostedBackend{host: "search.example"}
	tool := newWebSearchToolWithBackend(backend).(webSearchTool)
	engine := zeroSandbox.NewEngine(zeroSandbox.EngineOptions{
		Policy: zeroSandbox.Policy{Mode: zeroSandbox.ModeEnforce, Network: zeroSandbox.NetworkScoped, AllowedDomains: []string{"allowed.test"}, EnforceToolNetwork: true},
		Backend: zeroSandbox.Backend{
			Name: zeroSandbox.BackendSandboxExec, Available: true,
			Executable: "/usr/bin/sandbox-exec", ScopedEgress: true,
		},
	})
	res := tool.RunWithSandbox(context.Background(), map[string]any{"query": "hi"}, engine)
	if res.Status != StatusError || !strings.Contains(res.Output, "allowlist") {
		t.Fatalf("expected scoped block, got %q: %s", res.Status, res.Output)
	}
	if backend.called {
		t.Fatal("search backend must not be called for an unlisted host")
	}
}

func TestWebSearchRunWithSandboxScopedAllowsListedHost(t *testing.T) {
	backend := &fakeHostedBackend{host: "search.example", results: []searchResult{{Title: "T", URL: "https://x.test"}}}
	tool := newWebSearchToolWithBackend(backend).(webSearchTool)
	engine := zeroSandbox.NewEngine(zeroSandbox.EngineOptions{
		Policy: zeroSandbox.Policy{Mode: zeroSandbox.ModeEnforce, Network: zeroSandbox.NetworkScoped, AllowedDomains: []string{"search.example"}, EnforceToolNetwork: true},
		Backend: zeroSandbox.Backend{
			Name: zeroSandbox.BackendSandboxExec, Available: true,
			Executable: "/usr/bin/sandbox-exec", ScopedEgress: true,
		},
	})
	res := tool.RunWithSandbox(context.Background(), map[string]any{"query": "hi"}, engine)
	if res.Status != StatusOK {
		t.Fatalf("listed host must be allowed, got %q: %s", res.Status, res.Output)
	}
	if !backend.called {
		t.Fatal("search backend must be called when the host is allowlisted")
	}
}

func TestSameHostRedirectPolicy(t *testing.T) {
	orig, _ := http.NewRequest(http.MethodGet, "https://search.example/api", nil)
	same, _ := http.NewRequest(http.MethodGet, "https://search.example/v2/api", nil)
	cross, _ := http.NewRequest(http.MethodGet, "https://evil.test/x", nil)

	if err := sameHostRedirectPolicy(same, []*http.Request{orig}); err != nil {
		t.Fatalf("same-host redirect must be allowed, got %v", err)
	}
	if err := sameHostRedirectPolicy(cross, []*http.Request{orig}); err == nil {
		t.Fatal("cross-host redirect must be refused so scoped/deny can't be bypassed via a hop")
	}
	// A same-host HTTPS→HTTP downgrade must be refused (it would leak the query and
	// bearer token over plaintext).
	downgrade, _ := http.NewRequest(http.MethodGet, "http://search.example/api", nil)
	if err := sameHostRedirectPolicy(downgrade, []*http.Request{orig}); err == nil {
		t.Fatal("https→http downgrade redirect must be refused")
	}
	chain := make([]*http.Request, webSearchRedirectLimit)
	for i := range chain {
		chain[i] = orig
	}
	if err := sameHostRedirectPolicy(same, chain); err == nil {
		t.Fatal("redirect limit must be enforced")
	}
}
