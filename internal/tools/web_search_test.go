package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"
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
