package provideroauth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// simulateBrowser returns an OpenBrowser func that authorizes by GETting the
// callback_url with the given code (or no code), mimicking OpenRouter's redirect.
// It also asserts the PKCE params are present on the authorize URL.
func simulateBrowser(t *testing.T, code string) func(string) error {
	t.Helper()
	return func(authURL string) error {
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		q := u.Query()
		if q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
			t.Fatalf("authorize URL missing PKCE: %s", authURL)
		}
		cb := q.Get("callback_url")
		if cb == "" {
			t.Fatalf("authorize URL missing callback_url: %s", authURL)
		}
		target := cb
		if code != "" {
			target = cb + "?code=" + url.QueryEscape(code)
		}
		resp, err := http.Get(target) //nolint:noctx // test loopback
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		return nil
	}
}

func TestOpenRouterLoginMintsKey(t *testing.T) {
	var gotCode, gotVerifier, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/keys" || r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body map[string]string
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body)
		gotCode, gotVerifier, gotMethod = body["code"], body["code_verifier"], body["code_challenge_method"]
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"key": "sk-or-mock-123"})
	}))
	defer srv.Close()

	key, err := OpenRouterLogin(context.Background(), OpenRouterOptions{
		BaseURL:     srv.URL,
		HTTPClient:  srv.Client(),
		OpenBrowser: simulateBrowser(t, "TESTCODE"),
	})
	if err != nil {
		t.Fatalf("OpenRouterLogin: %v", err)
	}
	if key != "sk-or-mock-123" {
		t.Fatalf("key = %q, want sk-or-mock-123", key)
	}
	if gotCode != "TESTCODE" || gotVerifier == "" || gotMethod != "S256" {
		t.Fatalf("exchange body: code=%q verifier=%q method=%q", gotCode, gotVerifier, gotMethod)
	}
}

func TestOpenRouterLoginMissingCodeErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	_, err := OpenRouterLogin(context.Background(), OpenRouterOptions{
		BaseURL:     srv.URL,
		HTTPClient:  srv.Client(),
		OpenBrowser: simulateBrowser(t, ""), // callback with no code
	})
	if err == nil {
		t.Fatal("a callback without a code should error")
	}
}

func TestOpenRouterLoginExchangeFailureErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden) // exchange rejected
	}))
	defer srv.Close()
	_, err := OpenRouterLogin(context.Background(), OpenRouterOptions{
		BaseURL:     srv.URL,
		HTTPClient:  srv.Client(),
		OpenBrowser: simulateBrowser(t, "C"),
	})
	if err == nil {
		t.Fatal("a non-2xx exchange should error")
	}
}

func TestOpenRouterLoginEmptyKeyErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"key":""}`)
	}))
	defer srv.Close()
	_, err := OpenRouterLogin(context.Background(), OpenRouterOptions{
		BaseURL:     srv.URL,
		HTTPClient:  srv.Client(),
		OpenBrowser: simulateBrowser(t, "C"),
	})
	if err == nil || !strings.Contains(err.Error(), "empty key") {
		t.Fatalf("empty key should error, got %v", err)
	}
}

func TestOpenRouterLoginBrowserErrorPropagates(t *testing.T) {
	_, err := OpenRouterLogin(context.Background(), OpenRouterOptions{
		BaseURL:     "http://127.0.0.1:1",
		OpenBrowser: func(string) error { return errors.New("no browser") },
	})
	if err == nil {
		t.Fatal("a browser-open failure should propagate")
	}
}
