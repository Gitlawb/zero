package provideroauth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// roundTripFunc lets a test intercept outbound requests without a network.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func jsonResponse(status int, body any) *http.Response {
	payload, _ := json.Marshal(body)
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(string(payload))),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

func TestMintCopilotTokenSuccess(t *testing.T) {
	var gotAuth, gotEditor string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != copilotTokenEndpoint {
			t.Fatalf("unexpected URL %q", r.URL.String())
		}
		if r.Method != http.MethodGet {
			t.Fatalf("method = %q, want GET", r.Method)
		}
		gotAuth = r.Header.Get("Authorization")
		gotEditor = r.Header.Get("Editor-Version")
		return jsonResponse(http.StatusOK, map[string]any{
			"token":      "tid=abc;exp=123",
			"expires_at": time.Now().Add(30 * time.Minute).Unix(),
			"refresh_in": 1500,
		}), nil
	})}

	token, expiresAt, err := MintCopilotToken(context.Background(), client, "gho_test")
	if err != nil {
		t.Fatalf("MintCopilotToken: %v", err)
	}
	if token != "tid=abc;exp=123" {
		t.Fatalf("token = %q", token)
	}
	if expiresAt.IsZero() {
		t.Fatal("expiresAt should be set")
	}
	// The GitHub token is sent with the "token" scheme, not "Bearer".
	if gotAuth != "token gho_test" {
		t.Fatalf("Authorization = %q, want %q", gotAuth, "token gho_test")
	}
	if gotEditor == "" {
		t.Fatal("Editor-Version header must be sent")
	}
}

func TestMintCopilotTokenForbidden(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusForbidden, map[string]any{"message": "no access"}), nil
	})}
	_, _, err := MintCopilotToken(context.Background(), client, "gho_test")
	if err == nil {
		t.Fatal("expected an error on HTTP 403")
	}
	if strings.Contains(err.Error(), "no access") {
		t.Fatalf("error must not echo the response body: %v", err)
	}
	if !strings.Contains(err.Error(), "403") {
		t.Fatalf("error should mention the status code: %v", err)
	}
}

func TestMintCopilotTokenEmptyGitHubToken(t *testing.T) {
	if _, _, err := MintCopilotToken(context.Background(), http.DefaultClient, "  "); err == nil {
		t.Fatal("expected an error for an empty GitHub token")
	}
}

func TestCopilotTokenSourceCachesUntilExpiry(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	mints := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		mints++
		return jsonResponse(http.StatusOK, map[string]any{
			"token":      "copilot-token",
			"expires_at": now.Add(30 * time.Minute).Unix(),
		}), nil
	})}

	source := &CopilotTokenSource{
		HTTPClient:  client,
		GitHubToken: func(context.Context) (string, error) { return "gho_test", nil },
		Now:         func() time.Time { return now },
	}

	for i := 0; i < 3; i++ {
		got, err := source.Bearer(context.Background(), false)
		if err != nil {
			t.Fatalf("Bearer: %v", err)
		}
		if got != "copilot-token" {
			t.Fatalf("Bearer = %q", got)
		}
	}
	if mints != 1 {
		t.Fatalf("minted %d times, want 1 (cached)", mints)
	}

	// forceRefresh (a 401 retry) re-mints even when the cache is still valid.
	if _, err := source.Bearer(context.Background(), true); err != nil {
		t.Fatalf("Bearer(forceRefresh): %v", err)
	}
	if mints != 2 {
		t.Fatalf("minted %d times after forceRefresh, want 2", mints)
	}
}

func TestCopilotTokenSourceReMintsAfterExpiry(t *testing.T) {
	current := time.Unix(1_000_000, 0)
	mints := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		mints++
		return jsonResponse(http.StatusOK, map[string]any{
			"token":      "copilot-token",
			"expires_at": current.Add(5 * time.Minute).Unix(),
		}), nil
	})}
	source := &CopilotTokenSource{
		HTTPClient:  client,
		GitHubToken: func(context.Context) (string, error) { return "gho_test", nil },
		Now:         func() time.Time { return current },
	}
	if _, err := source.Bearer(context.Background(), false); err != nil {
		t.Fatalf("Bearer: %v", err)
	}
	// Advance past the cached token's expiry; the next call must re-mint.
	current = current.Add(10 * time.Minute)
	if _, err := source.Bearer(context.Background(), false); err != nil {
		t.Fatalf("Bearer: %v", err)
	}
	if mints != 2 {
		t.Fatalf("minted %d times, want 2 (re-mint after expiry)", mints)
	}
}

func TestCopilotBaseURLFromToken(t *testing.T) {
	cases := []struct {
		name  string
		token string
		want  string
	}{
		{"business", "tid=abc;exp=123;proxy-ep=proxy.business.githubcopilot.com;st=dotcom", "https://api.business.githubcopilot.com"},
		{"individual", "tid=abc;proxy-ep=proxy.individual.githubcopilot.com", "https://api.individual.githubcopilot.com"},
		{"enterprise host without proxy prefix", "tid=abc;proxy-ep=copilot.acme.ghe.com", "https://copilot.acme.ghe.com"},
		{"no proxy-ep falls back to default", "tid=abc;exp=123", "https://api.githubcopilot.com"},
		{"empty token falls back to default", "", "https://api.githubcopilot.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CopilotBaseURLFromToken(tc.token); got != tc.want {
				t.Fatalf("CopilotBaseURLFromToken(%q) = %q, want %q", tc.token, got, tc.want)
			}
		})
	}
}

func TestSetCopilotChatHeaders(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "https://api.githubcopilot.com/chat/completions", nil)
	SetCopilotChatHeaders(req)
	for _, h := range []string{"Editor-Version", "Editor-Plugin-Version", "Copilot-Integration-Id", "X-Github-Api-Version", "User-Agent"} {
		if req.Header.Get(h) == "" {
			t.Fatalf("header %q not set", h)
		}
	}
	if req.Header.Get("Copilot-Integration-Id") != "vscode-chat" {
		t.Fatalf("Copilot-Integration-Id = %q", req.Header.Get("Copilot-Integration-Id"))
	}
}

func newBodyReq(t *testing.T, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "https://api.githubcopilot.com/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	return req
}

func TestSetCopilotDynamicHeaders(t *testing.T) {
	cases := []struct {
		name          string
		body          string
		wantInitiator string
		wantVision    string
	}{
		{
			name:          "user last message",
			body:          `{"messages":[{"role":"system","content":"x"},{"role":"user","content":"hi"}]}`,
			wantInitiator: "user",
		},
		{
			name:          "assistant last message is agent-initiated",
			body:          `{"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"ok"}]}`,
			wantInitiator: "agent",
		},
		{
			name:          "responses input shape",
			body:          `{"input":[{"role":"user","content":"hi"},{"role":"tool","content":"res"}]}`,
			wantInitiator: "agent",
		},
		{
			name:          "vision request",
			body:          `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"x"}}]}]}`,
			wantInitiator: "user",
			wantVision:    "true",
		},
		{
			name:          "unparseable body defaults to user",
			body:          `not json`,
			wantInitiator: "user",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := newBodyReq(t, tc.body)
			SetCopilotDynamicHeaders(req)
			if got := req.Header.Get("Copilot-Integration-Id"); got != "vscode-chat" {
				t.Fatalf("static header missing: Copilot-Integration-Id = %q", got)
			}
			if got := req.Header.Get("Openai-Intent"); got != "conversation-edits" {
				t.Fatalf("Openai-Intent = %q, want conversation-edits", got)
			}
			if got := req.Header.Get("X-Initiator"); got != tc.wantInitiator {
				t.Fatalf("X-Initiator = %q, want %q", got, tc.wantInitiator)
			}
			if got := req.Header.Get("Copilot-Vision-Request"); got != tc.wantVision {
				t.Fatalf("Copilot-Vision-Request = %q, want %q", got, tc.wantVision)
			}
		})
	}
}
