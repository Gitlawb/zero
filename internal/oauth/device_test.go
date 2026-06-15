package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRequestDeviceCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"device_code":"dc","user_code":"WXYZ-1234","verification_uri":"https://x/dev","verification_uri_complete":"https://x/dev?c=WXYZ","expires_in":600,"interval":3}`))
	}))
	defer server.Close()
	cfg := Config{ClientID: "c", DeviceAuthorizationEndpoint: server.URL, Scopes: []string{"a"}}
	auth, err := RequestDeviceCode(context.Background(), server.Client(), cfg, nil)
	if err != nil {
		t.Fatalf("RequestDeviceCode: %v", err)
	}
	if auth.DeviceCode != "dc" || auth.UserCode != "WXYZ-1234" {
		t.Fatalf("device auth = %+v", auth)
	}
	if auth.Interval != 3*time.Second {
		t.Fatalf("interval = %v, want 3s", auth.Interval)
	}
	if auth.VerificationURIComplete == "" {
		t.Fatal("missing verification_uri_complete")
	}
}

func TestRequestDeviceCodeDefaultsInterval(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"device_code":"dc","user_code":"U","verification_uri":"https://x"}`))
	}))
	defer server.Close()
	auth, err := RequestDeviceCode(context.Background(), server.Client(), Config{ClientID: "c", DeviceAuthorizationEndpoint: server.URL}, nil)
	if err != nil {
		t.Fatalf("RequestDeviceCode: %v", err)
	}
	if auth.Interval != 5*time.Second {
		t.Fatalf("default interval = %v, want 5s", auth.Interval)
	}
}

func TestPollDeviceTokenHonorsPendingThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"at","token_type":"Bearer","expires_in":3600}`))
	}))
	defer server.Close()
	cfg := Config{ClientID: "c", TokenEndpoint: server.URL}
	// Small interval + future expiry so the loop is fast.
	auth := DeviceAuth{DeviceCode: "dc", Interval: 5 * time.Millisecond, ExpiresAt: time.Now().Add(5 * time.Second)}
	tok, err := PollDeviceToken(context.Background(), server.Client(), cfg, auth, nil)
	if err != nil {
		t.Fatalf("PollDeviceToken: %v", err)
	}
	if tok.AccessToken != "at" {
		t.Fatalf("token = %+v", tok)
	}
	if calls.Load() != 3 {
		t.Fatalf("polled %d times, want 3 (2 pending + 1 success)", calls.Load())
	}
}

func TestPollDeviceTokenSlowDown(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"slow_down"}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"at"}`))
	}))
	defer server.Close()
	cfg := Config{ClientID: "c", TokenEndpoint: server.URL}
	auth := DeviceAuth{DeviceCode: "dc", Interval: 5 * time.Millisecond, ExpiresAt: time.Now().Add(5 * time.Second)}
	tok, err := PollDeviceToken(context.Background(), server.Client(), cfg, auth, nil)
	if err != nil {
		t.Fatalf("PollDeviceToken (slow_down): %v", err)
	}
	if tok.AccessToken != "at" {
		t.Fatalf("token = %+v", tok)
	}
}

func TestPollDeviceTokenExpired(t *testing.T) {
	cfg := Config{ClientID: "c", TokenEndpoint: "https://unused/token"}
	auth := DeviceAuth{DeviceCode: "dc", Interval: time.Millisecond, ExpiresAt: time.Now().Add(-time.Second)}
	_, err := PollDeviceToken(context.Background(), http.DefaultClient, cfg, auth, nil)
	if err == nil {
		t.Fatal("expected expired error")
	}
}

func TestPollDeviceTokenAccessDenied(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"access_denied"}`))
	}))
	defer server.Close()
	cfg := Config{ClientID: "c", TokenEndpoint: server.URL}
	auth := DeviceAuth{DeviceCode: "dc", Interval: 5 * time.Millisecond, ExpiresAt: time.Now().Add(5 * time.Second)}
	_, err := PollDeviceToken(context.Background(), server.Client(), cfg, auth, nil)
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("err = %v, want access denied", err)
	}
}
