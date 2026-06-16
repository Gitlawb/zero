package providerio

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func drain(resp *http.Response) {
	if resp != nil {
		_ = resp.Body.Close()
	}
}

func TestSendWithAuthRetryAPIKeyFallbackWhenNilResolver(t *testing.T) {
	var gotKey, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-Api-Key")
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	resp, err := SendWithAuthRetry(context.Background(), srv.Client(), http.MethodPost, srv.URL, nil,
		AuthHeaders{APIKey: "KEY", DefaultAuthHeader: "X-Api-Key"}, nil, nil, 1)
	defer drain(resp)
	if err != nil {
		t.Fatalf("SendWithAuthRetry: %v", err)
	}
	if gotKey != "KEY" {
		t.Fatalf("X-Api-Key = %q, want KEY", gotKey)
	}
	if gotAuth != "" {
		t.Fatalf("Authorization should be unset for API-key auth, got %q", gotAuth)
	}
}

func TestSendWithAuthRetryResolverErrorDoesNotDispatch(t *testing.T) {
	var hit int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hit, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	resolver := func(context.Context, bool) (string, string, bool, error) {
		return "", "", false, errors.New("resolve failed")
	}
	resp, err := SendWithAuthRetry(context.Background(), srv.Client(), http.MethodPost, srv.URL, nil,
		AuthHeaders{APIKey: "KEY", DefaultAuthHeader: "X-Api-Key"}, resolver, nil, 1)
	drain(resp)
	if err == nil {
		t.Fatal("expected the resolver error to surface")
	}
	if resp != nil {
		t.Fatalf("no response should be returned on resolver error, got status %d", resp.StatusCode)
	}
	// The request must never reach the server when auth resolution failed.
	if n := atomic.LoadInt32(&hit); n != 0 {
		t.Fatalf("request dispatched %d times despite resolver error, want 0", n)
	}
}

func TestSendWithAuthRetryBearerWinsAndNeverBoth(t *testing.T) {
	var gotKey, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-Api-Key")
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	resolver := func(context.Context, bool) (string, string, bool, error) {
		return "Authorization", "Bearer TOK", true, nil
	}
	resp, err := SendWithAuthRetry(context.Background(), srv.Client(), http.MethodPost, srv.URL, nil,
		AuthHeaders{APIKey: "KEY", DefaultAuthHeader: "X-Api-Key"}, resolver, nil, 1)
	defer drain(resp)
	if err != nil {
		t.Fatalf("SendWithAuthRetry: %v", err)
	}
	if gotAuth != "Bearer TOK" {
		t.Fatalf("Authorization = %q, want Bearer TOK", gotAuth)
	}
	if gotKey != "" {
		t.Fatalf("API key must NOT be sent alongside a bearer token, got X-Api-Key=%q", gotKey)
	}
}

func TestSendWithAuthRetryOkFalseFallsBackToAPIKey(t *testing.T) {
	var gotKey, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-Api-Key")
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// resolver yields ok=false (no login) → API key path.
	resolver := func(context.Context, bool) (string, string, bool, error) { return "", "", false, nil }
	resp, err := SendWithAuthRetry(context.Background(), srv.Client(), http.MethodPost, srv.URL, nil,
		AuthHeaders{APIKey: "KEY", DefaultAuthHeader: "X-Api-Key"}, resolver, nil, 1)
	defer drain(resp)
	if err != nil {
		t.Fatalf("SendWithAuthRetry: %v", err)
	}
	if gotKey != "KEY" || gotAuth != "" {
		t.Fatalf("ok=false should use API key: X-Api-Key=%q Authorization=%q", gotKey, gotAuth)
	}
}

func TestSendWithAuthRetryRefreshesOn401(t *testing.T) {
	var count int32
	var first, second string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&count, 1)
		if n == 1 {
			first = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		second = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	var forced int32
	resolver := func(_ context.Context, forceRefresh bool) (string, string, bool, error) {
		if forceRefresh {
			atomic.AddInt32(&forced, 1)
			return "Authorization", "Bearer NEW", true, nil
		}
		return "Authorization", "Bearer OLD", true, nil
	}
	resp, err := SendWithAuthRetry(context.Background(), srv.Client(), http.MethodPost, srv.URL, nil,
		AuthHeaders{}, resolver, nil, 1)
	defer drain(resp)
	if err != nil {
		t.Fatalf("SendWithAuthRetry: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("final status = %d, want 200", resp.StatusCode)
	}
	if first != "Bearer OLD" || second != "Bearer NEW" {
		t.Fatalf("expected old→new bearer, got first=%q second=%q", first, second)
	}
	if atomic.LoadInt32(&forced) != 1 {
		t.Fatalf("force-refresh called %d times, want 1", forced)
	}
	if atomic.LoadInt32(&count) != 2 {
		t.Fatalf("server hit %d times, want 2", count)
	}
}

func TestSendWithAuthRetryStopsAfterOne401Retry(t *testing.T) {
	var count int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&count, 1)
		w.WriteHeader(http.StatusUnauthorized) // always 401
	}))
	defer srv.Close()

	resolver := func(context.Context, bool) (string, string, bool, error) {
		return "Authorization", "Bearer T", true, nil
	}
	resp, err := SendWithAuthRetry(context.Background(), srv.Client(), http.MethodPost, srv.URL, nil,
		AuthHeaders{}, resolver, nil, 1)
	defer drain(resp)
	if err != nil {
		t.Fatalf("SendWithAuthRetry: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("final status = %d, want 401", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&count); got != 2 {
		t.Fatalf("server hit %d times, want exactly 2 (initial + one refresh retry)", got)
	}
}

func TestSendWithAuthRetrySurfacesResolverError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	wantErr := errors.New("refresh failed")
	resolver := func(context.Context, bool) (string, string, bool, error) { return "", "", false, wantErr }
	resp, err := SendWithAuthRetry(context.Background(), srv.Client(), http.MethodPost, srv.URL, nil,
		AuthHeaders{}, resolver, nil, 1)
	defer drain(resp)
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}
