package hooks

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("connection reset mid-body") }

func TestDispatchPromptActionReturnsModelOutput(t *testing.T) {
	var gotModel, gotPrompt string
	var gotPayload []byte
	promptRunner := func(_ context.Context, model string, prompt string, payload []byte) (string, error) {
		gotModel, gotPrompt, gotPayload = model, prompt, payload
		return "looks good", nil
	}
	config := Config{Enabled: true, Hooks: []Definition{
		{ID: "p", Event: EventAfterTool, Type: ActionPrompt, Prompt: "Follows conventions?", Model: "sonnet", Enabled: true},
	}}
	dispatcher := NewDispatcher(DispatcherOptions{Config: config, PromptRunner: promptRunner})

	outcome := dispatcher.Dispatch(context.Background(), DispatchInput{
		Event:    EventAfterTool,
		ToolName: "edit_file",
		Payload:  map[string]any{"k": "v"},
	})

	if outcome.Ran != 1 {
		t.Fatalf("Ran = %d, want 1", outcome.Ran)
	}
	if len(outcome.Messages) != 1 || outcome.Messages[0] != "looks good" {
		t.Fatalf("Messages = %#v, want [looks good]", outcome.Messages)
	}
	if gotModel != "sonnet" || gotPrompt != "Follows conventions?" {
		t.Fatalf("prompt runner got model=%q prompt=%q", gotModel, gotPrompt)
	}
	if !strings.Contains(string(gotPayload), `"k":"v"`) {
		t.Fatalf("payload not forwarded to prompt runner: %s", gotPayload)
	}
}

func TestDispatchPromptActionWithoutRunnerErrorsWithoutBlocking(t *testing.T) {
	config := Config{Enabled: true, Hooks: []Definition{
		{ID: "p", Event: EventAfterTool, Type: ActionPrompt, Prompt: "x", Enabled: true},
	}}
	dispatcher := NewDispatcher(DispatcherOptions{Config: config})

	outcome := dispatcher.Dispatch(context.Background(), DispatchInput{Event: EventAfterTool, ToolName: "edit_file"})

	if outcome.Blocked {
		t.Fatalf("prompt action without a runner must not block: %#v", outcome)
	}
	if outcome.Ran != 1 {
		t.Fatalf("Ran = %d, want 1", outcome.Ran)
	}
}

func TestDispatchHTTPActionPostsToAllowlistedURL(t *testing.T) {
	var gotBody string
	var gotMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	config := Config{Enabled: true, Hooks: []Definition{
		{ID: "h", Event: EventNotification, Type: ActionHTTP, URL: server.URL, Enabled: true},
	}}
	dispatcher := NewDispatcher(DispatcherOptions{Config: config, AllowedHTTPURLs: []string{server.URL}})

	outcome := dispatcher.Dispatch(context.Background(), DispatchInput{
		Event:   EventNotification,
		Payload: map[string]any{"event": "notification", "msg": "hi"},
	})

	if outcome.Ran != 1 {
		t.Fatalf("Ran = %d, want 1", outcome.Ran)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q, want POST", gotMethod)
	}
	if !strings.Contains(gotBody, `"msg":"hi"`) {
		t.Fatalf("posted body = %q", gotBody)
	}
}

func TestDispatchHTTPActionDoesNotFollowRedirectToNonAllowlistedHost(t *testing.T) {
	var evilCalled bool
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		evilCalled = true
		w.WriteHeader(http.StatusOK)
	}))
	defer evil.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, evil.URL, http.StatusFound)
	}))
	defer redirector.Close()

	config := Config{Enabled: true, Hooks: []Definition{
		{ID: "h", Event: EventNotification, Type: ActionHTTP, URL: redirector.URL, Enabled: true},
	}}
	// Only the redirector is allowlisted; the redirect target is not.
	dispatcher := NewDispatcher(DispatcherOptions{Config: config, AllowedHTTPURLs: []string{redirector.URL}})

	dispatcher.Dispatch(context.Background(), DispatchInput{
		Event:   EventNotification,
		Payload: map[string]any{"secret": "payload"},
	})

	if evilCalled {
		t.Fatal("http hook followed a redirect to a non-allowlisted host (allowlist bypass / SSRF)")
	}
}

func TestDispatchHTTPActionTreatsTruncatedResponseAsFailure(t *testing.T) {
	const url = "https://allowed.example/hook"
	badClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		// 200 headers, but the body errors mid-read (server reset).
		return &http.Response{StatusCode: 200, Body: io.NopCloser(errReader{}), Header: make(http.Header)}, nil
	})}
	audit, err := NewAuditStore(AuditStoreOptions{AuditPath: filepath.Join(t.TempDir(), "audit.jsonl")})
	if err != nil {
		t.Fatalf("NewAuditStore: %v", err)
	}
	config := Config{Enabled: true, Hooks: []Definition{
		{ID: "h", Event: EventNotification, Type: ActionHTTP, URL: url, Enabled: true},
	}}
	dispatcher := NewDispatcher(DispatcherOptions{Config: config, Audit: audit, AllowedHTTPURLs: []string{url}, HTTPClient: badClient})

	dispatcher.Dispatch(context.Background(), DispatchInput{Event: EventNotification})

	events, err := audit.ReadEvents()
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	var status AuditStatus
	for _, e := range events {
		if e.Type == "hook_execution_completed" && e.HookID == "h" {
			status = e.Status
		}
	}
	if status != AuditError {
		t.Fatalf("a truncated/erroring http response must be recorded as an error, got status %q", status)
	}
}

func TestDispatchHTTPActionRejectsNonAllowlistedURL(t *testing.T) {
	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	config := Config{Enabled: true, Hooks: []Definition{
		{ID: "h", Event: EventNotification, Type: ActionHTTP, URL: server.URL, Enabled: true},
	}}
	// Allowlist is empty: the request must never be made.
	dispatcher := NewDispatcher(DispatcherOptions{Config: config})

	outcome := dispatcher.Dispatch(context.Background(), DispatchInput{Event: EventNotification})

	if called {
		t.Fatal("non-allowlisted URL must not be requested")
	}
	if outcome.Ran != 1 {
		t.Fatalf("Ran = %d, want 1", outcome.Ran)
	}
	if outcome.Blocked {
		t.Fatalf("a rejected http hook on an advisory event must not block: %#v", outcome)
	}
}
