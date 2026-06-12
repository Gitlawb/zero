package notify

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// captureLog returns a thread-safe Logf and a getter for the joined output.
func captureLog() (func(string, ...any), func() string) {
	var mu sync.Mutex
	var lines []string
	logf := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, fmt.Sprintf(format, args...))
	}
	get := func() string {
		mu.Lock()
		defer mu.Unlock()
		return strings.Join(lines, "\n")
	}
	return logf, get
}

func TestWebhookSinkPostsExpectedJSON(t *testing.T) {
	type received struct {
		method      string
		contentType string
		body        webhookPayload
		raw         string
	}
	got := make(chan received, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var payload webhookPayload
		_ = json.Unmarshal(raw, &payload)
		got <- received{method: r.Method, contentType: r.Header.Get("Content-Type"), body: payload, raw: string(raw)}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sink := NewWebhookSink(WebhookConfig{URL: server.URL})
	sink.Emit(Completion, "Zero: ready")

	select {
	case r := <-got:
		if r.method != http.MethodPost {
			t.Fatalf("method = %q, want POST", r.method)
		}
		if !strings.HasPrefix(r.contentType, "application/json") {
			t.Fatalf("content-type = %q, want application/json", r.contentType)
		}
		if r.body.Type != "completion" {
			t.Fatalf("payload type = %q, want completion", r.body.Type)
		}
		if r.body.Message != "Zero: ready" {
			t.Fatalf("payload message = %q, want %q", r.body.Message, "Zero: ready")
		}
		// Slack incoming-webhook compatibility: a human-readable "text" field must
		// be present so the message renders in a Slack channel.
		if strings.TrimSpace(r.body.Text) == "" {
			t.Fatalf("payload text was empty; raw=%s", r.raw)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("webhook server never received a request")
	}
}

func TestWebhookSinkIncludesSummaryAndLinks(t *testing.T) {
	got := make(chan webhookPayload, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload webhookPayload
		_ = json.NewDecoder(r.Body).Decode(&payload)
		got <- payload
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	sink := NewWebhookSink(WebhookConfig{
		URL:     server.URL,
		Summary: "verify failed after 3 retries",
		Links:   []WebhookLink{{Label: "Run", URL: "https://example.test/run/1"}},
	})
	sink.Emit(AwaitingInput, "Zero: needs input")

	select {
	case payload := <-got:
		if payload.Type != "awaiting_input" {
			t.Fatalf("type = %q, want awaiting_input", payload.Type)
		}
		if payload.Summary != "verify failed after 3 retries" {
			t.Fatalf("summary = %q", payload.Summary)
		}
		if len(payload.Links) != 1 || payload.Links[0].URL != "https://example.test/run/1" {
			t.Fatalf("links = %+v", payload.Links)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("webhook server never received a request")
	}
}

func TestWebhookSinkNon2xxDoesNotPanicAndLogs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	logf, getLog := captureLog()
	sink := NewWebhookSink(WebhookConfig{URL: server.URL, Logf: logf})
	// Must not panic and must not block the caller fatally.
	sink.Emit(Completion, "hi")

	if log := getLog(); !strings.Contains(strings.ToLower(log), "webhook") || !strings.Contains(log, "500") {
		t.Fatalf("expected a logged 500 webhook failure, got %q", log)
	}
}

func TestWebhookSinkTransportErrorIsLoggedNotFatal(t *testing.T) {
	logf, getLog := captureLog()
	// An unroutable URL forces a transport error; Emit must swallow it.
	sink := NewWebhookSink(WebhookConfig{
		URL:    "http://127.0.0.1:0/never",
		Logf:   logf,
		Client: &http.Client{Timeout: 200 * time.Millisecond},
	})
	sink.Emit(Completion, "hi")
	if log := getLog(); !strings.Contains(strings.ToLower(log), "webhook") {
		t.Fatalf("expected a logged webhook transport error, got %q", log)
	}
}

func TestWebhookSinkRedactsSecretsInPayloadAndLog(t *testing.T) {
	const token = "xoxb-123456789012-abcdefghijklmno"
	got := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		got <- string(raw)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	logf, getLog := captureLog()
	sink := NewWebhookSink(WebhookConfig{URL: server.URL, Logf: logf})
	sink.Emit(Completion, "leaking "+token+" oops")

	select {
	case raw := <-got:
		if strings.Contains(raw, token) {
			t.Fatalf("token leaked into webhook payload: %s", raw)
		}
		if !strings.Contains(raw, "[REDACTED]") {
			t.Fatalf("expected redaction marker in payload, got %s", raw)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("webhook server never received a request")
	}
	if log := getLog(); strings.Contains(log, token) {
		t.Fatalf("token leaked into log: %s", log)
	}
}

func TestWebhookSinkRedactsWebhookURLInLog(t *testing.T) {
	// A failing webhook whose URL itself carries a secret-looking token must not
	// echo that token when the failure is logged.
	logf, getLog := captureLog()
	const secretURL = "http://127.0.0.1:0/services/T000/B000/xoxb-123456789012-secrettoken"
	sink := NewWebhookSink(WebhookConfig{
		URL:    secretURL,
		Logf:   logf,
		Client: &http.Client{Timeout: 200 * time.Millisecond},
	})
	sink.Emit(Completion, "hi")
	if log := getLog(); strings.Contains(log, "xoxb-123456789012-secrettoken") {
		t.Fatalf("webhook URL token leaked into log: %s", log)
	}
}

func TestWebhookSinkEmptyURLIsNoop(t *testing.T) {
	// A sink with no URL configured must do nothing (and never panic).
	sink := NewWebhookSink(WebhookConfig{})
	sink.Emit(Completion, "hi") // no server, no panic, returns.
}

func TestWebhookSinkRedactsWebhookURLInPayload(t *testing.T) {
	// If the message or summary echoes the webhook URL (which carries a secret
	// token), that URL must be redacted before it is sent in the payload.
	got := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		got <- string(raw)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sink := NewWebhookSink(WebhookConfig{
		URL:     server.URL,
		Summary: "see " + server.URL + " for the run",
		Links:   []WebhookLink{{Label: "Hook", URL: server.URL}},
	})
	sink.Emit(Completion, "delivering to "+server.URL)

	select {
	case raw := <-got:
		if strings.Contains(raw, server.URL) {
			t.Fatalf("webhook URL leaked into payload: %s", raw)
		}
		if !strings.Contains(raw, "[REDACTED]") {
			t.Fatalf("expected redaction marker in payload, got %s", raw)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("webhook server never received a request")
	}
}

func TestWebhookSinkCopiesConfigSlices(t *testing.T) {
	// The sink must own its links/secrets so a caller mutating the config slices
	// after construction cannot alter (or race with) future payloads.
	const token = "xoxb-123456789012-abcdefghijklmno"
	got := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		got <- string(raw)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	links := []WebhookLink{{Label: "Run", URL: "https://example.test/run/1"}}
	secrets := []string{token}
	sink := NewWebhookSink(WebhookConfig{URL: server.URL, Links: links, ExtraSecrets: secrets})

	// Mutate the caller-owned slices after construction.
	links[0] = WebhookLink{Label: "Hijacked", URL: "https://attacker.test"}
	secrets[0] = "no-longer-redacted"

	sink.Emit(Completion, "leaking "+token+" oops")

	select {
	case raw := <-got:
		if strings.Contains(raw, "attacker.test") || strings.Contains(raw, "Hijacked") {
			t.Fatalf("mutated link leaked into payload: %s", raw)
		}
		if strings.Contains(raw, token) {
			t.Fatalf("token leaked despite original secret config: %s", raw)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("webhook server never received a request")
	}
}
