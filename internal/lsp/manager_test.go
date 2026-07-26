package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// stubLSPServer is an lspServer backed by in-memory pipes. On each didOpen/
// didChange it publishes the next entry of a configured diagnostics sequence
// (repeating the last entry), or nothing when neverPublish is set.
type stubLSPServer struct {
	client       *Client
	serverWriter *io.PipeWriter
	clientWriter *io.PipeWriter
	closeOnce    sync.Once
}

func (s *stubLSPServer) Client() *Client { return s.client }

func (s *stubLSPServer) Shutdown(_ context.Context) error {
	s.closeOnce.Do(func() {
		_ = s.client.Close()
		_ = s.serverWriter.Close()
		_ = s.clientWriter.Close()
	})
	return nil
}

func (s *stubLSPServer) run(serverReader io.Reader, sequence [][]Diagnostic, neverPublish bool) {
	reader := bufio.NewReader(serverReader)
	count := 0
	for {
		body, err := readMessage(reader)
		if err != nil {
			return
		}
		var msg struct {
			Method string `json:"method"`
			Params struct {
				TextDocument struct {
					URI string `json:"uri"`
				} `json:"textDocument"`
			} `json:"params"`
		}
		_ = json.Unmarshal(body, &msg)
		if msg.Method != "textDocument/didOpen" && msg.Method != "textDocument/didChange" {
			continue
		}
		if neverPublish {
			continue
		}
		diags := []Diagnostic{}
		if count < len(sequence) {
			diags = sequence[count]
		} else if len(sequence) > 0 {
			diags = sequence[len(sequence)-1]
		}
		count++
		_ = writeMessage(s.serverWriter, map[string]any{
			"jsonrpc": "2.0",
			"method":  "textDocument/publishDiagnostics",
			"params":  PublishDiagnosticsParams{URI: msg.Params.TextDocument.URI, Diagnostics: diags},
		})
	}
}

func stubStarter(sequence [][]Diagnostic, neverPublish bool) serverStarter {
	return func(_ context.Context, _ []string, _ string) (lspServer, error) {
		clientReader, serverWriter := io.Pipe()
		serverReader, clientWriter := io.Pipe()
		stub := &stubLSPServer{
			client:       NewClient(clientReader, clientWriter),
			serverWriter: serverWriter,
			clientWriter: clientWriter,
		}
		go stub.run(serverReader, sequence, neverPublish)
		return stub, nil
	}
}

func fastManager(starter serverStarter) *Manager {
	m := newManagerWithStarter("/repo", starter)
	m.debounce = 15 * time.Millisecond // keep tests quick
	return m
}

func TestManagerCheckReturnsDiagnostics(t *testing.T) {
	errDiag := Diagnostic{
		Range:    Range{Start: Position{Line: 2, Character: 0}},
		Severity: SeverityError,
		Message:  "undefined: foo",
	}
	m := fastManager(stubStarter([][]Diagnostic{{errDiag}}, false))
	defer m.Shutdown(context.Background())

	diags, err := m.Check(context.Background(), "main.go", "package main")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(diags) != 1 || diags[0].Message != "undefined: foo" {
		t.Fatalf("diagnostics = %#v", diags)
	}
	if !m.HasErrors("main.go") {
		t.Fatal("HasErrors should be true after an error diagnostic")
	}
}

func TestManagerCheckClearsDiagnosticsOnChange(t *testing.T) {
	errDiag := Diagnostic{Severity: SeverityError, Message: "boom"}
	// First sync publishes an error; second publishes an empty list (fixed).
	m := fastManager(stubStarter([][]Diagnostic{{errDiag}, {}}, false))
	defer m.Shutdown(context.Background())

	if _, err := m.Check(context.Background(), "main.go", "broken"); err != nil {
		t.Fatal(err)
	}
	if !m.HasErrors("main.go") {
		t.Fatal("expected errors after first check")
	}
	diags, err := m.Check(context.Background(), "main.go", "fixed")
	if err != nil {
		t.Fatal(err)
	}
	if len(diags) != 0 || m.HasErrors("main.go") {
		t.Fatalf("expected diagnostics cleared, got %#v", diags)
	}
}

func TestManagerCheckTimesOutWithoutPublish(t *testing.T) {
	m := fastManager(stubStarter(nil, true)) // server never publishes
	defer m.Shutdown(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	diags, err := m.Check(ctx, "main.go", "package main")
	if err != nil {
		t.Fatalf("Check should not error on timeout, got %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics, got %#v", diags)
	}
	if time.Since(start) > time.Second {
		t.Fatal("Check hung well past the context timeout")
	}
}

func TestManagerNoServerForExtension(t *testing.T) {
	calls := 0
	m := fastManager(func(_ context.Context, _ []string, _ string) (lspServer, error) {
		calls++
		return nil, nil
	})
	diags, err := m.Check(context.Background(), "notes.md", "hello")
	if err != nil || diags != nil {
		t.Fatalf("unconfigured extension should return (nil,nil), got (%#v,%v)", diags, err)
	}
	if calls != 0 {
		t.Fatal("no server should be started for an unconfigured extension")
	}
}

func TestManagerCheckConcurrentReusesOneServer(t *testing.T) {
	var mu sync.Mutex
	started := 0
	base := stubStarter([][]Diagnostic{{{Severity: SeverityWarning, Message: "w"}}}, false)
	m := fastManager(func(ctx context.Context, command []string, root string) (lspServer, error) {
		mu.Lock()
		started++
		mu.Unlock()
		return base(ctx, command, root)
	})
	defer m.Shutdown(context.Background())

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := m.Check(context.Background(), "main.go", "package main"); err != nil {
				t.Errorf("concurrent Check: %v", err)
			}
		}()
	}
	wg.Wait()

	// All four calls target the same .go server; only one session is retained.
	m.mu.Lock()
	sessions := len(m.sessions)
	m.mu.Unlock()
	if sessions != 1 {
		t.Fatalf("expected a single reused session, got %d", sessions)
	}
}

type errShutdownServer struct{}

func (errShutdownServer) Client() *Client { return NewClient(strings.NewReader(""), io.Discard) }
func (errShutdownServer) Shutdown(context.Context) error {
	return errors.New("server refused to exit")
}

func TestManagerShutdownPropagatesErrors(t *testing.T) {
	m := newManagerWithStarter("/repo", func(context.Context, []string, string) (lspServer, error) {
		return errShutdownServer{}, nil
	})
	if _, err := m.sessionFor(context.Background(), []string{"gopls"}); err != nil {
		t.Fatalf("sessionFor: %v", err)
	}
	if err := m.Shutdown(context.Background()); err == nil {
		t.Fatal("Shutdown must surface a server that refused to exit")
	}
}

func TestSessionDropsStaleVersionPublish(t *testing.T) {
	sess := newSession(errShutdownServer{})
	uri := PathToURI("/repo/main.go")
	sess.mu.Lock()
	sess.versions[uri] = 3
	sess.mu.Unlock()

	stale, _ := json.Marshal(PublishDiagnosticsParams{URI: uri, Version: 2, Diagnostics: []Diagnostic{{Message: "stale"}}})
	sess.handleNotification("textDocument/publishDiagnostics", stale, 1)
	if len(sess.diagnosticsFor(uri)) != 0 {
		t.Fatal("a publish for an older version must be ignored")
	}

	fresh, _ := json.Marshal(PublishDiagnosticsParams{URI: uri, Version: 3, Diagnostics: []Diagnostic{{Message: "fresh"}}})
	sess.handleNotification("textDocument/publishDiagnostics", fresh, 2)
	if d := sess.diagnosticsFor(uri); len(d) != 1 || d[0].Message != "fresh" {
		t.Fatalf("a current-version publish must apply, got %#v", d)
	}
}

// TestPublishBaselineRejectsAlreadyQueuedPublish is the regression test for
// jatmn's #759 P1 finding: publishBaseline used to snapshot how many publishes
// session.handleNotification had already RUN for a URI. Dispatch happens off a
// queue, though, so a publish for a version Check is about to supersede can
// already be sitting RECEIVED but undispatched at the moment a later Check
// captures its baseline — then run only afterward, incrementing the old
// count-based baseline and wrongly looking "new". Since many servers omit the
// version field, handleNotification's own staleness check (which only fires
// for a positive version) can't catch this either, so the stale result would
// reach the caller for the new text, and the debounce could finish before the
// real response even arrives.
//
// This drives the exact interleaving through the real production path: the
// stale publish is enqueued first (fixing its receipt seq), THEN a baseline is
// captured, THEN the stale item is dispatched — reproducing "received before
// baseline, handled after" without depending on goroutine scheduling luck. A
// bare Client (no background loops, like TestClientNotificationQueueIsLossless
// uses) makes the enqueue/dequeue ordering fully explicit.
func TestPublishBaselineRejectsAlreadyQueuedPublish(t *testing.T) {
	client := &Client{notifyReady: make(chan struct{}, 1)}
	sess := &session{
		client:      client,
		versions:    map[string]int{},
		diagnostics: map[string][]Diagnostic{},
		lastPublish: map[string]time.Time{},
		publishSeq:  map[string]int64{},
		waiters:     map[string][]chan struct{}{},
	}
	uri := PathToURI("/repo/main.go")

	// The stale (version-less — the common case) publish is RECEIVED first.
	stale, _ := json.Marshal(PublishDiagnosticsParams{URI: uri, Diagnostics: []Diagnostic{{Message: "stale"}}})
	client.enqueueNotification(notification{method: "textDocument/publishDiagnostics", params: stale})

	// A later Check captures its baseline only now — after the stale publish's
	// receipt, exactly as publishBaseline does between two Checks whose
	// notification queue has backed up.
	baseline := sess.publishBaseline()

	// Dispatch the stale item exactly as notificationLoop would: dequeue and
	// call the handler with the receipt seq it was actually stamped with.
	item, ok := client.dequeueNotification()
	if !ok {
		t.Fatal("stale notification was not queued")
	}
	sess.handleNotification(item.method, item.params, item.seq)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if sess.waitForDiagnostics(ctx, uri, 10*time.Millisecond, baseline) {
		t.Fatal("a publish received before baseline must not satisfy waitForDiagnostics merely because it was handled afterward")
	}

	// The real response — received (and handled) after baseline — must satisfy it.
	fresh, _ := json.Marshal(PublishDiagnosticsParams{URI: uri, Diagnostics: []Diagnostic{{Message: "fresh"}}})
	client.enqueueNotification(notification{method: "textDocument/publishDiagnostics", params: fresh})
	item2, ok := client.dequeueNotification()
	if !ok {
		t.Fatal("fresh notification was not queued")
	}
	sess.handleNotification(item2.method, item2.params, item2.seq)

	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	if !sess.waitForDiagnostics(ctx2, uri, 10*time.Millisecond, baseline) {
		t.Fatal("a publish received after baseline must satisfy waitForDiagnostics")
	}
	if d := sess.diagnosticsFor(uri); len(d) != 1 || d[0].Message != "fresh" {
		t.Fatalf("diagnostics = %#v, want the fresh publish", d)
	}
}

func TestManagerCheckDegradesWhenServerBinaryMissing(t *testing.T) {
	// A configured extension whose binary isn't on PATH (exec.ErrNotFound) must
	// degrade to no diagnostics, exactly like an unsupported extension.
	m := fastManager(func(context.Context, []string, string) (lspServer, error) {
		return nil, &exec.Error{Name: "gopls", Err: exec.ErrNotFound}
	})
	diags, err := m.Check(context.Background(), "main.go", "package main")
	if err != nil || diags != nil {
		t.Fatalf("missing server binary should degrade to (nil,nil), got (%#v, %v)", diags, err)
	}
}

func TestSessionForEvictsDeadSession(t *testing.T) {
	var starts int
	inner := stubStarter(nil, true) // neverPublish; this test only exercises session lifecycle
	m := fastManager(func(ctx context.Context, cmd []string, root string) (lspServer, error) {
		starts++
		return inner(ctx, cmd, root)
	})

	sess1, err := m.sessionFor(context.Background(), []string{"gopls"})
	if err != nil {
		t.Fatalf("sessionFor: %v", err)
	}
	if starts != 1 {
		t.Fatalf("starts = %d, want 1", starts)
	}

	// A live session is reused — no new server started.
	if sess2, _ := m.sessionFor(context.Background(), []string{"gopls"}); sess2 != sess1 || starts != 1 {
		t.Fatalf("live session should be reused: same=%v starts=%d", sess2 == sess1, starts)
	}

	// Simulate the language server crashing: its client closes.
	_ = sess1.client.Close()
	if !sess1.client.IsClosed() {
		t.Fatal("client should report closed after Close")
	}

	// sessionFor must now evict the dead session and start a fresh server (H4) —
	// otherwise every later diagnostic would fail forever against the dead one.
	sess3, err := m.sessionFor(context.Background(), []string{"gopls"})
	if err != nil {
		t.Fatalf("sessionFor after crash: %v", err)
	}
	if starts != 2 {
		t.Fatalf("a dead session must trigger a restart: starts=%d, want 2", starts)
	}
	if sess3 == sess1 {
		t.Fatal("should return a fresh session, not the dead one")
	}
}
