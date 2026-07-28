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

	mu          sync.Mutex
	open        map[string]bool            // uri -> didOpen sent
	versions    map[string]int             // uri -> current (committed) version
	diagnostics map[string][]Diagnostic    // uri -> latest published diagnostics
	lastPublish map[string]time.Time       // uri -> time of last publish
	publishSeq  map[string]int64           // uri -> receipt seq of latest publish (see Client.NotificationSeq)
	waiters     map[string][]chan struct{} // uri -> goroutines awaiting the next publish
	fileLocks   map[string]*sync.Mutex     // uri -> per-document sync serializer
}

func newSession(server lspServer) *session {
	s := &session{
		server:      server,
		client:      server.Client(),
		open:        map[string]bool{},
		versions:    map[string]int{},
		diagnostics: map[string][]Diagnostic{},
		lastPublish: map[string]time.Time{},
		publishSeq:  map[string]int64{},
		waiters:     map[string][]chan struct{}{},
		fileLocks:   map[string]*sync.Mutex{},
	}
	s.client.SetNotificationHandler(s.handleNotification)
	return s
}

func (s *session) handleNotification(method string, params json.RawMessage, seq int64) {
	if method != "textDocument/publishDiagnostics" {
		return
	}
	var payload PublishDiagnosticsParams
	if err := json.Unmarshal(params, &payload); err != nil {
		return
	}
	s.mu.Lock()
	// Drop a delayed publish for a superseded version: if we've already synced a
	// newer document version, an older version's diagnostics are stale and must
	// not move the cache backward. Many servers omit the version (0) — only skip
	// when the payload carries a strictly-older positive version.
	if payload.Version > 0 && s.versions[payload.URI] > payload.Version {
		s.mu.Unlock()
		return
	}
	s.diagnostics[payload.URI] = payload.Diagnostics
	s.lastPublish[payload.URI] = time.Now()
	s.publishSeq[payload.URI] = seq
	waiters := s.waiters[payload.URI]
	delete(s.waiters, payload.URI)
	s.mu.Unlock()
	for _, ch := range waiters {
		close(ch)
	}
}

// sync opens the document on first sight, otherwise sends a full-text change.
// It holds a per-URI lock across the whole compute+notify so two concurrent
// syncs of the same document can't race their didOpen/didChange writes onto the
// wire out of order, and only commits the new version/open state after the
// notification succeeds (a failed Notify leaves tracking unchanged).
func (s *session) sync(ctx context.Context, absPath, languageID, text string) error {
	uri := PathToURI(absPath)
	lock := s.fileLock(uri)
	lock.Lock()
	defer lock.Unlock()

	s.mu.Lock()
	first := !s.open[uri]
	nextVersion := 1
	if !first {
		nextVersion = s.versions[uri] + 1
	}
	s.mu.Unlock()

	var err error
	if first {
		err = s.client.Notify(ctx, "textDocument/didOpen", map[string]any{
			"textDocument": TextDocumentItem{URI: uri, LanguageID: languageID, Version: nextVersion, Text: text},
		})
	} else {
		err = s.client.Notify(ctx, "textDocument/didChange", map[string]any{
			"textDocument":   map[string]any{"uri": uri, "version": nextVersion},
			"contentChanges": []any{map[string]any{"text": text}}, // full-document sync
		})
	}
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.open[uri] = true
	s.versions[uri] = nextVersion
	s.mu.Unlock()
	return nil
}

func (s *session) fileLock(uri string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, ok := s.fileLocks[uri]
	if !ok {
		lock = &sync.Mutex{}
		s.fileLocks[uri] = lock
	}
	return lock
}

func (s *session) diagnosticsFor(uri string) []Diagnostic {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Diagnostic(nil), s.diagnostics[uri]...)
}

// publishBaseline snapshots the client's current notification receipt
// sequence, captured before a sync so waitForDiagnostics can require a publish
// that was RECEIVED after this point. This must baseline against receipt, not
// against session.publishSeq (which only advances once handleNotification
// actually runs): dispatch happens off a queue, so a publish for a since-
// superseded version can already be sitting in that queue, still unprocessed,
// at the moment a later Check captures its baseline. If baseline were instead
// "how many publishes has this session recorded so far", that stale publish
// would land afterward, still satisfy "more than baseline", and hand the new
// check diagnostics for the wrong text — silently, since many servers omit the
// version field the staleness check in handleNotification relies on.
// Baselining against receipt sequence closes that: a notification already
// enqueued before this call has seq <= baseline no matter when it is handled.
func (s *session) publishBaseline() int64 {
	return s.client.NotificationSeq()
}

// waitForDiagnostics blocks until a publish newer than baseline arrives for the
// URI and the server then goes quiet for the debounce window, or until ctx is
// done or the client closes. It returns true when a fresh publish was observed
// and false when the wait ended before any did — so a caller can avoid returning
// a stale prior result for newer text. Servers don't signal "analysis complete",
// so the debounce approximates it: once a fresh publish lands, wait debounce for
// a follow-up, resetting on each new publish.
func (s *session) waitForDiagnostics(ctx context.Context, uri string, debounce time.Duration, baseline int64) bool {
	for {
		s.mu.Lock()
		ch := make(chan struct{})
		s.waiters[uri] = append(s.waiters[uri], ch)
		seq := s.publishSeq[uri]
		last := s.lastPublish[uri]
		s.mu.Unlock()

		if seq <= baseline {
			select {
			case <-ctx.Done():
				s.cancelWaiter(uri, ch)
				// Cancellation and a publish can become ready together. As with
				// client closure below, re-check state rather than letting select's
				// random choice discard a publish already recorded by the handler.
				s.mu.Lock()
				fresh := s.publishSeq[uri] > baseline
				s.mu.Unlock()
				return fresh
			case <-s.client.closed:
				s.cancelWaiter(uri, ch)
				// Closing the transport precedes draining notifications already
				// accepted by the reader. Wait for that drain before deciding there
				// was no publish. Hand-built clients have no worker/channel, so skip
				// the wait for them rather than blocking forever.
				if drained := s.client.notificationsDone(); drained != nil {
					select {
					case <-drained:
					case <-ctx.Done():
					}
				}
				// Re-check even when ctx and the drain are simultaneously ready:
				// either may have won after the handler recorded the fresh publish.
				s.mu.Lock()
				fresh := s.publishSeq[uri] > baseline
				s.mu.Unlock()
				return fresh
			case <-ch:
				continue // a fresh publish arrived; loop into the debounce check
			}
		}

		remaining := debounce - time.Since(last)
		if remaining <= 0 {
			s.cancelWaiter(uri, ch)
			return true
		}
		timer := time.NewTimer(remaining)
		select {
		case <-ctx.Done():
			timer.Stop()
			s.cancelWaiter(uri, ch)
			return true // a fresh publish did arrive; ctx merely cut the debounce short
		case <-s.client.closed:
			timer.Stop()
			s.cancelWaiter(uri, ch)
			return true // preserve the fresh publish already received before failure
		case <-ch:
			timer.Stop()
			continue // a newer publish arrived; re-arm the debounce
		case <-timer.C:
			s.cancelWaiter(uri, ch)
			return true // quiet for the full window
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
