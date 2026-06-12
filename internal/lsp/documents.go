package lsp

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// lspServer is the subset of *Server the session needs, so a session can be
// driven by a stub over an in-memory pipe in tests.
type lspServer interface {
	Client() *Client
	Shutdown(ctx context.Context) error
}

// session wraps one language-server connection with per-document version
// tracking and a diagnostics store fed by textDocument/publishDiagnostics
// notifications. Safe for concurrent use.
type session struct {
	server lspServer
	client *Client

	mu           sync.Mutex
	open         map[string]bool            // uri -> didOpen sent
	versions     map[string]int             // uri -> current version
	diagnostics  map[string][]Diagnostic    // uri -> latest published diagnostics
	lastPublish  map[string]time.Time       // uri -> time of last publish
	publishCount map[string]int             // uri -> monotonic publish count
	waiters      map[string][]chan struct{} // uri -> goroutines awaiting the next publish
}

func newSession(server lspServer) *session {
	s := &session{
		server:       server,
		client:       server.Client(),
		open:         map[string]bool{},
		versions:     map[string]int{},
		diagnostics:  map[string][]Diagnostic{},
		lastPublish:  map[string]time.Time{},
		publishCount: map[string]int{},
		waiters:      map[string][]chan struct{}{},
	}
	s.client.SetNotificationHandler(s.handleNotification)
	return s
}

func (s *session) handleNotification(method string, params json.RawMessage) {
	if method != "textDocument/publishDiagnostics" {
		return
	}
	var payload PublishDiagnosticsParams
	if err := json.Unmarshal(params, &payload); err != nil {
		return
	}
	s.mu.Lock()
	s.diagnostics[payload.URI] = payload.Diagnostics
	s.lastPublish[payload.URI] = time.Now()
	s.publishCount[payload.URI]++
	waiters := s.waiters[payload.URI]
	delete(s.waiters, payload.URI)
	s.mu.Unlock()
	for _, ch := range waiters {
		close(ch)
	}
}

// sync opens the document on first sight, otherwise sends a full-text change.
func (s *session) sync(ctx context.Context, absPath, languageID, text string) error {
	uri := PathToURI(absPath)
	s.mu.Lock()
	first := !s.open[uri]
	if first {
		s.open[uri] = true
		s.versions[uri] = 1
	} else {
		s.versions[uri]++
	}
	version := s.versions[uri]
	s.mu.Unlock()

	if first {
		return s.client.Notify(ctx, "textDocument/didOpen", map[string]any{
			"textDocument": TextDocumentItem{URI: uri, LanguageID: languageID, Version: version, Text: text},
		})
	}
	return s.client.Notify(ctx, "textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": uri, "version": version},
		"contentChanges": []any{map[string]any{"text": text}}, // full-document sync
	})
}

func (s *session) didClose(ctx context.Context, absPath string) error {
	uri := PathToURI(absPath)
	s.mu.Lock()
	delete(s.open, uri)
	s.mu.Unlock()
	return s.client.Notify(ctx, "textDocument/didClose", map[string]any{
		"textDocument": map[string]any{"uri": uri},
	})
}

func (s *session) diagnosticsFor(uri string) []Diagnostic {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Diagnostic(nil), s.diagnostics[uri]...)
}

// publishBaseline snapshots the current publish count for a URI, captured before
// a sync so waitForDiagnostics can wait specifically for the publish that sync
// triggers (not be satisfied by a stale earlier publish).
func (s *session) publishBaseline(uri string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.publishCount[uri]
}

// waitForDiagnostics blocks until a publish newer than baseline arrives for the
// URI and the server then goes quiet for the debounce window, or until ctx is
// done. Servers don't signal "analysis complete", so the debounce approximates
// it: once a fresh publish lands, wait debounce for a follow-up, resetting on
// each new publish.
func (s *session) waitForDiagnostics(ctx context.Context, uri string, debounce time.Duration, baseline int) {
	for {
		s.mu.Lock()
		ch := make(chan struct{})
		s.waiters[uri] = append(s.waiters[uri], ch)
		count := s.publishCount[uri]
		last := s.lastPublish[uri]
		s.mu.Unlock()

		if count <= baseline {
			select {
			case <-ctx.Done():
				s.cancelWaiter(uri, ch)
				return
			case <-ch:
				continue // a fresh publish arrived; loop into the debounce check
			}
		}

		remaining := debounce - time.Since(last)
		if remaining <= 0 {
			s.cancelWaiter(uri, ch)
			return
		}
		timer := time.NewTimer(remaining)
		select {
		case <-ctx.Done():
			timer.Stop()
			s.cancelWaiter(uri, ch)
			return
		case <-ch:
			timer.Stop()
			continue // a newer publish arrived; re-arm the debounce
		case <-timer.C:
			s.cancelWaiter(uri, ch)
			return // quiet for the full window
		}
	}
}

// cancelWaiter removes a still-open waiter (one a publish has not already closed
// and cleared) so it can't leak or be closed twice.
func (s *session) cancelWaiter(uri string, target chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.waiters[uri]
	for i, ch := range list {
		if ch == target {
			s.waiters[uri] = append(list[:i], list[i+1:]...)
			return
		}
	}
}
