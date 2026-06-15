package notify

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// envFunc builds a deterministic env resolver from a map for the wiring tests.
func envFunc(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestMaybeAddWebhookSinkAttachesAndDelivers(t *testing.T) {
	var hits int32
	var gotSummary string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		var payload webhookPayload
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&payload)
		gotSummary = payload.Summary
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	n := New(&bytes.Buffer{}, Config{Mode: ModeBell, FocusMode: FocusAlways})
	MaybeAddWebhookSink(n, envFunc(map[string]string{
		EnvWebhookURL:     server.URL,
		EnvWebhookSummary: "nightly audit",
	}), nil)

	if got := len(n.sinks); got != 1 {
		t.Fatalf("expected 1 sink attached, got %d", got)
	}

	n.Notify(Completion, "Zero: ready")
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("webhook hit %d times, want 1", got)
	}
	if gotSummary != "nightly audit" {
		t.Fatalf("summary = %q, want %q", gotSummary, "nightly audit")
	}
}

func TestMaybeAddWebhookSinkNoopWhenURLBlank(t *testing.T) {
	n := New(&bytes.Buffer{}, Config{Mode: ModeBell, FocusMode: FocusAlways})

	// Unset.
	MaybeAddWebhookSink(n, envFunc(nil), nil)
	// Set but blank / whitespace only.
	MaybeAddWebhookSink(n, envFunc(map[string]string{EnvWebhookURL: "   "}), nil)

	if got := len(n.sinks); got != 0 {
		t.Fatalf("expected no sink attached, got %d", got)
	}
}

func TestMaybeAddWebhookSinkNilGuards(t *testing.T) {
	// Must not panic on a nil notifier or nil env resolver.
	MaybeAddWebhookSink(nil, envFunc(map[string]string{EnvWebhookURL: "https://example.test"}), nil)

	n := New(&bytes.Buffer{}, Config{Mode: ModeBell, FocusMode: FocusAlways})
	MaybeAddWebhookSink(n, nil, nil)
	if got := len(n.sinks); got != 0 {
		t.Fatalf("nil env must attach nothing, got %d sinks", got)
	}
}
