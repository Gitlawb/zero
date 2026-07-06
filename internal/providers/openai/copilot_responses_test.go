package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// TestCopilotResponsesProviderTargetsResponsesEndpointWithHeaders verifies that
// NewCopilotResponsesProvider drives the Responses transport at
// {baseURL}/responses and applies the caller-supplied editor headers (the
// GitHub Copilot identity headers) on every request — without the Codex-only
// originator / chatgpt-account-id headers.
func TestCopilotResponsesProviderTargetsResponsesEndpointWithHeaders(t *testing.T) {
	var gotPath, gotIntegration, gotEditor, gotOriginator, gotAccount string
	var gotBody map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/responses", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotIntegration = r.Header.Get("Copilot-Integration-Id")
		gotEditor = r.Header.Get("Editor-Version")
		gotOriginator = r.Header.Get(codexOriginatorHeader)
		gotAccount = r.Header.Get(codexAccountHeader)
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"r1\",\"status\":\"in_progress\"}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"Hello \"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"there\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"status\":\"completed\",\"usage\":{\"input_tokens\":5,\"output_tokens\":2}}}\n\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	provider, err := NewCopilotResponsesProvider(Options{
		APIKey:  "copilot-token",
		BaseURL: srv.URL,
		Model:   "gpt-5.4-mini",
		SetRequestExtra: func(req *http.Request) {
			req.Header.Set("Copilot-Integration-Id", "vscode-chat")
			req.Header.Set("Editor-Version", "vscode/1.99.0")
		},
	})
	if err != nil {
		t.Fatalf("NewCopilotResponsesProvider: %v", err)
	}

	stream, err := provider.StreamCompletion(context.Background(), zeroruntime.CompletionRequest{
		Messages: []zeroruntime.Message{{Role: zeroruntime.MessageRoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("StreamCompletion: %v", err)
	}
	var texts []string
	for ev := range stream {
		switch ev.Type {
		case zeroruntime.StreamEventText:
			texts = append(texts, ev.Content)
		case zeroruntime.StreamEventError:
			t.Fatalf("unexpected error event: %q", ev.Error)
		}
	}

	if gotPath != "/responses" {
		t.Fatalf("request path = %q, want /responses", gotPath)
	}
	if gotIntegration != "vscode-chat" {
		t.Fatalf("Copilot-Integration-Id = %q, want vscode-chat", gotIntegration)
	}
	if gotEditor != "vscode/1.99.0" {
		t.Fatalf("Editor-Version = %q, want vscode/1.99.0", gotEditor)
	}
	if gotOriginator != "" || gotAccount != "" {
		t.Fatalf("Codex-only headers leaked: originator=%q account=%q", gotOriginator, gotAccount)
	}
	if model, _ := gotBody["model"].(string); model != "gpt-5.4-mini" {
		t.Fatalf("request model = %v, want gpt-5.4-mini", gotBody["model"])
	}
	if _, ok := gotBody["input"]; !ok {
		t.Fatalf("request body missing Responses `input` array: %#v", gotBody)
	}
	if joined := strings.Join(texts, ""); joined != "Hello there" {
		t.Fatalf("assembled text = %q, want %q", joined, "Hello there")
	}
}
