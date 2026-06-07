package providerio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestShouldRetryStatus(t *testing.T) {
	cases := map[int]bool{
		http.StatusOK:                  false,
		http.StatusBadRequest:          false,
		http.StatusNotFound:            false,
		http.StatusUnauthorized:        false,
		http.StatusTooManyRequests:     true,
		http.StatusInternalServerError: true,
		http.StatusBadGateway:          true,
		http.StatusServiceUnavailable:  true,
	}
	for code, want := range cases {
		if got := ShouldRetryStatus(code); got != want {
			t.Errorf("ShouldRetryStatus(%d) = %v, want %v", code, got, want)
		}
	}
}

func TestRetryAfterParsesHeader(t *testing.T) {
	mk := func(value string) *http.Response {
		resp := &http.Response{Header: http.Header{}}
		if value != "" {
			resp.Header.Set("Retry-After", value)
		}
		return resp
	}
	if got := RetryAfter(mk("3")); got != 3*time.Second {
		t.Errorf("RetryAfter(\"3\") = %v, want 3s", got)
	}
	if got := RetryAfter(mk("")); got != 0 {
		t.Errorf("RetryAfter(absent) = %v, want 0", got)
	}
	if got := RetryAfter(mk("0")); got != 0 {
		t.Errorf("RetryAfter(\"0\") = %v, want 0", got)
	}
	if got := RetryAfter(mk("not-a-number")); got != 0 {
		t.Errorf("RetryAfter(garbage) = %v, want 0", got)
	}
	if got := RetryAfter(nil); got != 0 {
		t.Errorf("RetryAfter(nil) = %v, want 0", got)
	}
}

func TestBackoffReturnsFalseOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if Backoff(ctx, 5, 0) {
		t.Fatal("Backoff should return false when the context is already cancelled")
	}
}

func TestBackoffWaitsThenReturnsTrue(t *testing.T) {
	// retryAfter overrides the attempt-based wait, keeping the test fast.
	if !Backoff(context.Background(), 1, time.Millisecond) {
		t.Fatal("Backoff should return true after waiting out a short delay")
	}
}

func TestSendWithRetryRetriesThenSucceeds(t *testing.T) {
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.WriteHeader(http.StatusInternalServerError) // first attempt fails
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	resp, err := SendWithRetry(context.Background(), server.Client(), http.MethodPost, server.URL, []byte("{}"), nil, 3)
	if err != nil {
		t.Fatalf("SendWithRetry returned error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 after retry", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("server hit %d times, want 2 (one failure + one success)", got)
	}
}

func TestSendWithRetryReturnsNonRetryableImmediately(t *testing.T) {
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusBadRequest) // 400 is not retryable
	}))
	defer server.Close()

	resp, err := SendWithRetry(context.Background(), server.Client(), http.MethodPost, server.URL, []byte("{}"), nil, 3)
	if err != nil {
		t.Fatalf("SendWithRetry returned error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("server hit %d times, want 1 (no retry on 400)", got)
	}
}

func TestSendWithRetryReturnsLastResponseAfterMaxAttempts(t *testing.T) {
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusServiceUnavailable) // always retryable
	}))
	defer server.Close()

	resp, err := SendWithRetry(context.Background(), server.Client(), http.MethodPost, server.URL, []byte("{}"), nil, 2)
	if err != nil {
		t.Fatalf("SendWithRetry returned error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (exhausted retries surface the response)", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("server hit %d times, want 2 (maxAttempts)", got)
	}
}
