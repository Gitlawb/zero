package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeTokenSource drives the OAuth round tripper without a real provider. It
// hands out a current access token and, on refresh, advances to a new one. A
// failing refresh returns an error to exercise the actionable-error path.
type fakeTokenSource struct {
	access      atomic.Value // string
	refreshFunc func() (string, error)
	refreshes   int32
}

func (s *fakeTokenSource) AccessToken(context.Context) (string, error) {
	value, _ := s.access.Load().(string)
	return value, nil
}

func (s *fakeTokenSource) Refresh(context.Context) (string, error) {
	atomic.AddInt32(&s.refreshes, 1)
	token, err := s.refreshFunc()
	if err != nil {
		return "", err
	}
	s.access.Store(token)
	return token, nil
}

func TestOAuthRoundTripperAttachesBearer(t *testing.T) {
	var gotAuth atomic.Value
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	source := &fakeTokenSource{}
	source.access.Store("access-1")
	source.refreshFunc = func() (string, error) { return "access-2", nil }

	client := &http.Client{Transport: newOAuthRoundTripper(http.DefaultTransport, source, "demo")}
	resp, err := client.Get(upstream.URL)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	resp.Body.Close()
	if got, _ := gotAuth.Load().(string); got != "Bearer access-1" {
		t.Fatalf("Authorization = %q, want Bearer access-1", got)
	}
	if atomic.LoadInt32(&source.refreshes) != 0 {
		t.Fatal("refresh called without a 401")
	}
}

func TestOAuthRoundTripperRefreshesOn401AndRetriesOnce(t *testing.T) {
	source := &fakeTokenSource{}
	source.access.Store("access-stale")
	source.refreshFunc = func() (string, error) { return "access-fresh", nil }

	var attempts int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		auth := r.Header.Get("Authorization")
		if n == 1 {
			if auth != "Bearer access-stale" {
				t.Errorf("first attempt auth = %q", auth)
			}
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if auth != "Bearer access-fresh" {
			t.Errorf("retry auth = %q, want refreshed token", auth)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	client := &http.Client{Transport: newOAuthRoundTripper(http.DefaultTransport, source, "demo")}
	resp, err := client.Get(upstream.URL)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 after refresh", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("upstream attempts = %d, want exactly 2 (retry once)", got)
	}
	if got := atomic.LoadInt32(&source.refreshes); got != 1 {
		t.Fatalf("refreshes = %d, want 1", got)
	}
}

func TestOAuthRoundTripperRefreshFailureSurfacesActionableError(t *testing.T) {
	source := &fakeTokenSource{}
	source.access.Store("access-stale")
	source.refreshFunc = func() (string, error) { return "", context.DeadlineExceeded }

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer upstream.Close()

	client := &http.Client{Transport: newOAuthRoundTripper(http.DefaultTransport, source, "demo")}
	_, err := client.Get(upstream.URL)
	if err == nil {
		t.Fatal("Get() error = nil, want actionable refresh failure")
	}
	if !strings.Contains(err.Error(), "zero mcp oauth login demo") {
		t.Fatalf("error = %q, want actionable re-login message", err.Error())
	}
}

func TestOAuthRoundTripperDoesNotRetryAfterSuccessfulNon401(t *testing.T) {
	source := &fakeTokenSource{}
	source.access.Store("access-1")
	source.refreshFunc = func() (string, error) { return "access-2", nil }

	var attempts int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer upstream.Close()

	client := &http.Client{Transport: newOAuthRoundTripper(http.DefaultTransport, source, "demo")}
	resp, err := client.Get(upstream.URL)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 passed through", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("attempts = %d, want 1 (no retry on non-401)", got)
	}
	if got := atomic.LoadInt32(&source.refreshes); got != 0 {
		t.Fatalf("refreshes = %d, want 0", got)
	}
}

func TestNonOAuthServerIsUnaffected(t *testing.T) {
	// Regression: a server without auth must not get a bearer header or any
	// OAuth round tripper, and must connect normally.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); strings.HasPrefix(got, "Bearer") {
			t.Errorf("non-oauth server received bearer header %q", got)
		}
		message := readHTTPRPCMessage(t, r)
		switch message.Method {
		case "initialize":
			writeHTTPRPCResponse(t, w, message.ID, map[string]any{"protocolVersion": "2024-11-05"})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		default:
			writeHTTPRPCResponse(t, w, message.ID, map[string]any{})
		}
	}))
	defer upstream.Close()

	client, err := Connect(ctx, Server{Name: "plain", Type: ServerTypeHTTP, URL: upstream.URL})
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}
