package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSearxngBackendGETParsesResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/search" {
			t.Errorf("expected /search, got %s", r.URL.Path)
		}
		if r.URL.Query().Get("format") != "json" || r.URL.Query().Get("q") != "language" {
			t.Errorf("expected format=json&q=language, got %q", r.URL.RawQuery)
		}
		if r.Header.Get("Authorization") != "" {
			t.Errorf("SearXNG must be keyless; got auth header %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[
			{"title":"Go","url":"https://go.dev","content":"the language","score":0.9},
			{"title":"no-url","url":"","content":"skip"},
			{"title":"Rust","url":"https://rust-lang.org","content":"systems"}
		]}`))
	}))
	defer server.Close()

	backend := &httpSearchBackend{client: server.Client(), baseURL: server.URL, provider: "searxng"}
	results, err := backend.Search(context.Background(), "language", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 { // empty-URL entry skipped
		t.Fatalf("expected 2 results, got %d: %#v", len(results), results)
	}
	if results[0].URL != "https://go.dev" || results[0].Snippet != "the language" || results[0].Score != 0.9 {
		t.Fatalf("first result mapped wrong: %#v", results[0])
	}
}

func TestSearxngBackendRespectsLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"url":"https://a"},{"url":"https://b"},{"url":"https://c"}]}`))
	}))
	defer server.Close()
	backend := &httpSearchBackend{client: server.Client(), baseURL: server.URL, provider: "searxng"}
	results, err := backend.Search(context.Background(), "x", 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("limit=2 not respected: got %d", len(results))
	}
}
