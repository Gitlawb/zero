package provideroauth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCopilotEndpointsAPIFormat(t *testing.T) {
	cases := []struct {
		name      string
		endpoints []string
		want      string
	}{
		{"no field defaults to chat", nil, copilotAPIFormatChat},
		{"chat only", []string{"/chat/completions"}, copilotAPIFormatChat},
		{"chat and responses prefers chat", []string{"/responses", "/chat/completions"}, copilotAPIFormatChat},
		{"responses only", []string{"/responses", "ws:/responses"}, copilotAPIFormatResponses},
		{"embeddings only falls back to chat", []string{"/embeddings"}, copilotAPIFormatChat},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := copilotEndpointsAPIFormat(tc.endpoints); got != tc.want {
				t.Fatalf("copilotEndpointsAPIFormat(%v) = %q, want %q", tc.endpoints, got, tc.want)
			}
		})
	}
}

func TestCopilotModelAPIFormatBlankInputsDefaultToChat(t *testing.T) {
	if got := CopilotModelAPIFormat(t.Context(), nil, "", "", "", "gpt-5.4-mini"); got != copilotAPIFormatChat {
		t.Fatalf("blank bearer = %q, want %q", got, copilotAPIFormatChat)
	}
	if got := CopilotModelAPIFormat(t.Context(), nil, "token", "", "", ""); got != copilotAPIFormatChat {
		t.Fatalf("blank model = %q, want %q", got, copilotAPIFormatChat)
	}
}

func resetCopilotModelCache(t *testing.T) {
	t.Helper()
	InvalidateCopilotModelCache()
}

func TestCopilotModelAPIFormatCacheIsScopedByHost(t *testing.T) {
	resetCopilotModelCache(t)
	newServer := func(endpoint string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprintf(w, `{"data":[{"id":"shared-model","supported_endpoints":[%q]}]}`, endpoint)
		}))
	}
	responsesHost := newServer("/responses")
	defer responsesHost.Close()
	chatHost := newServer("/chat/completions")
	defer chatHost.Close()

	if got := CopilotModelAPIFormat(t.Context(), responsesHost.Client(), "token-a", responsesHost.URL, "account-a", "shared-model"); got != copilotAPIFormatResponses {
		t.Fatalf("responses host format = %q, want %q", got, copilotAPIFormatResponses)
	}
	if got := CopilotModelAPIFormat(t.Context(), chatHost.Client(), "token-b", chatHost.URL, "account-b", "shared-model"); got != copilotAPIFormatChat {
		t.Fatalf("chat host format = %q, want %q", got, copilotAPIFormatChat)
	}
}

func TestCopilotModelAPIFormatCacheIsScopedByAccount(t *testing.T) {
	resetCopilotModelCache(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		endpoint := "/chat/completions"
		if req.Header.Get("Authorization") == "Bearer token-a" {
			endpoint = "/responses"
		}
		_, _ = fmt.Fprintf(w, `{"data":[{"id":"shared-model","supported_endpoints":[%q]}]}`, endpoint)
	}))
	defer server.Close()

	if got := CopilotModelAPIFormat(t.Context(), server.Client(), "token-a", server.URL, "account-a", "shared-model"); got != copilotAPIFormatResponses {
		t.Fatalf("first account format = %q, want %q", got, copilotAPIFormatResponses)
	}
	if got := CopilotModelAPIFormat(t.Context(), server.Client(), "token-b", server.URL, "account-b", "shared-model"); got != copilotAPIFormatChat {
		t.Fatalf("second account format = %q, want %q", got, copilotAPIFormatChat)
	}
}

func TestCopilotModelAPIFormatDoesNotLockAcrossHosts(t *testing.T) {
	resetCopilotModelCache(t)
	started := make(chan struct{})
	release := make(chan struct{})
	slowHost := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = fmt.Fprint(w, `{"data":[{"id":"slow","supported_endpoints":["/responses"]}]}`)
	}))
	defer slowHost.Close()
	fastHost := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"data":[{"id":"fast","supported_endpoints":["/responses"]}]}`)
	}))
	defer fastHost.Close()

	slowDone := make(chan struct{})
	go func() {
		defer close(slowDone)
		_ = CopilotModelAPIFormat(context.Background(), slowHost.Client(), "slow-token", slowHost.URL, "slow-account", "slow")
	}()
	<-started

	fastDone := make(chan string, 1)
	go func() {
		fastDone <- CopilotModelAPIFormat(context.Background(), fastHost.Client(), "fast-token", fastHost.URL, "fast-account", "fast")
	}()
	select {
	case got := <-fastDone:
		if got != copilotAPIFormatResponses {
			t.Fatalf("fast host format = %q, want %q", got, copilotAPIFormatResponses)
		}
	case <-time.After(time.Second):
		t.Fatal("fast host fetch was blocked by unrelated slow host")
	}
	close(release)
	<-slowDone
}
