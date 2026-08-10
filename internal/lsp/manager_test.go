package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"runtime"
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

	// The stale (version-less — the common case) publish is RECEIVED first: the
	// read loop stamps a frame's receipt before it decodes or queues it, so the
	// stamp here comes first too.
	stale, _ := json.Marshal(PublishDiagnosticsParams{URI: uri, Diagnostics: []Diagnostic{{Message: "stale"}}})
	client.enqueueNotification(notification{
		method: "textDocument/publishDiagnostics", params: stale, seq: client.stampReceipt(),
	})

	// A later Check captures its baseline only now — after the stale publish's
	// receipt, exactly as publishBaseline does between two Checks whose
	// notification queue has backed up.
	baseline := sess.publishBaseline()

	// Dispatch the stale item exactly as notificationLoop would: dequeue and
	// call the handler with the receipt seq it was actually stamped with.
	item, ok, _ := client.dequeueNotification()
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
	client.enqueueNotification(notification{
		method: "textDocument/publishDiagnostics", params: fresh, seq: client.stampReceipt(),
	})
	item2, ok, _ := client.dequeueNotification()
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

// TestPublishBaselineRejectsFrameReadBeforeBaseline is the regression test for
// jatmn's follow-up P1 finding on #759: stamping receipt at ENQUEUE left a
// window async dispatch had opened. The read loop consumes a frame, then decodes
// it (a full json.Unmarshal of a peer-sized payload) and only then enqueues it,
// all while another goroutine can capture a baseline. A publishDiagnostics for
// text the new Check is about to supersede could therefore be numbered above a
// baseline taken after it had already come off the wire, and — since most
// servers omit the version field that handleNotification's staleness check needs
// — reach the caller as if it answered the new text.
//
// The interleaving is driven at the seam itself: stamp (the frame leaves the
// wire), baseline (a concurrent Check), then enqueue and dispatch (the read loop
// finishes decoding). Stamping at enqueue put this publish above the baseline;
// stamping at read puts it at or below.
func TestPublishBaselineRejectsFrameReadBeforeBaseline(t *testing.T) {
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

	// The read loop pulls the stale frame off the wire and stamps it...
	seq := client.stampReceipt()

	// ...a concurrent Check baselines while that frame is still being decoded...
	baseline := sess.publishBaseline()

	// ...and only now does the frame reach the queue and the handler.
	stale, _ := json.Marshal(PublishDiagnosticsParams{URI: uri, Diagnostics: []Diagnostic{{Message: "stale"}}})
	client.enqueueNotification(notification{
		method: "textDocument/publishDiagnostics", params: stale, seq: seq,
	})
	item, ok, _ := client.dequeueNotification()
	if !ok {
		t.Fatal("stale notification was not queued")
	}
	sess.handleNotification(item.method, item.params, item.seq)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if sess.waitForDiagnostics(ctx, uri, 10*time.Millisecond, baseline) {
		t.Fatal("a publish whose frame was read before baseline must not satisfy waitForDiagnostics merely because it was decoded and queued afterward")
	}
	if d := sess.diagnosticsFor(uri); len(d) != 1 || d[0].Message != "stale" {
		t.Fatalf("diagnostics = %#v, want the stale publish still recorded (rejected as an answer, not dropped)", d)
	}
}

func TestWaitForDiagnosticsReturnsWhenClientCloses(t *testing.T) {
	client := &Client{
		pending: make(map[int64]chan rpcResponse),
		closed:  make(chan struct{}),
	}
	uri := PathToURI("/repo/main.go")
	sess := &session{
		client:      client,
		diagnostics: map[string][]Diagnostic{},
		lastPublish: map[string]time.Time{},
		publishSeq:  map[string]int64{},
		waiters:     map[string][]chan struct{}{},
	}
	waitDone := make(chan bool, 1)
	go func() {
		waitDone <- sess.waitForDiagnostics(context.Background(), uri, time.Second, 0)
	}()

	deadline := time.Now().Add(time.Second)
	for {
		sess.mu.Lock()
		waiting := len(sess.waiters[uri]) == 1
		sess.mu.Unlock()
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("diagnostic wait was not registered")
		}
		time.Sleep(time.Millisecond)
	}

	client.failPending(errors.New("notification backlog exceeded"))
	select {
	case fresh := <-waitDone:
		if fresh {
			t.Fatal("client failure without a fresh publish must not report fresh diagnostics")
		}
	case <-time.After(time.Second):
		t.Fatal("diagnostic wait did not wake when the client failed")
	}
	sess.mu.Lock()
	waiters := len(sess.waiters[uri])
	sess.mu.Unlock()
	if waiters != 0 {
		t.Fatalf("client failure left %d diagnostic waiters registered", waiters)
	}
}

func TestWaitForDiagnosticsPreservesPublishHandledBeforeClientCloses(t *testing.T) {
	// Keep the test goroutine running while it closes both signals so the waiter
	// observes the publish and client closure as simultaneously ready. Without
	// re-checking publishSeq, select could randomly choose closure and discard the
	// fresh diagnostics that handleNotification had already recorded.
	previousProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previousProcs)

	params, err := json.Marshal(PublishDiagnosticsParams{
		URI:         PathToURI("/repo/main.go"),
		Diagnostics: []Diagnostic{{Message: "fresh"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		client := &Client{
			pending: make(map[int64]chan rpcResponse),
			closed:  make(chan struct{}),
		}
		uri := PathToURI("/repo/main.go")
		sess := &session{
			client:      client,
			diagnostics: map[string][]Diagnostic{},
			lastPublish: map[string]time.Time{},
			publishSeq:  map[string]int64{},
			waiters:     map[string][]chan struct{}{},
		}
		waitDone := make(chan bool, 1)
		go func() {
			waitDone <- sess.waitForDiagnostics(context.Background(), uri, time.Second, 0)
		}()

		deadline := time.Now().Add(time.Second)
		for {
			sess.mu.Lock()
			waiting := len(sess.waiters[uri]) == 1
			sess.mu.Unlock()
			if waiting {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("diagnostic wait was not registered")
			}
			time.Sleep(time.Millisecond)
		}

		sess.handleNotification("textDocument/publishDiagnostics", params, 1)
		client.failPending(errors.New("server exited after publishing"))
		select {
		case fresh := <-waitDone:
			if !fresh {
				t.Fatalf("iteration %d discarded diagnostics handled before client closure", i)
			}
		case <-time.After(time.Second):
			t.Fatal("diagnostic wait did not wake when the client failed")
		}
	}
}

func TestWaitForDiagnosticsDrainsAcceptedPublishAfterTransportEOF(t *testing.T) {
	clientReader, serverWriter := io.Pipe()
	client := NewClient(clientReader, io.Discard)
	uri := PathToURI("/repo/main.go")
	sess := &session{
		client:      client,
		diagnostics: map[string][]Diagnostic{},
		lastPublish: map[string]time.Time{},
		publishSeq:  map[string]int64{},
		waiters:     map[string][]chan struct{}{},
	}
	blocked := make(chan struct{})
	release := make(chan struct{})
	client.SetNotificationHandler(func(method string, params json.RawMessage, seq int64) {
		if method == "test/block" {
			close(blocked)
			<-release
			return
		}
		sess.handleNotification(method, params, seq)
	})

	if err := writeMessage(serverWriter, map[string]any{"jsonrpc": "2.0", "method": "test/block"}); err != nil {
		t.Fatal(err)
	}
	<-blocked
	params := PublishDiagnosticsParams{URI: uri, Diagnostics: []Diagnostic{{Message: "fresh"}}}
	if err := writeMessage(serverWriter, map[string]any{
		"jsonrpc": "2.0", "method": "textDocument/publishDiagnostics", "params": params,
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for client.ReceiptSeq() != 2 {
		if time.Now().After(deadline) {
			t.Fatal("publish was not accepted before EOF")
		}
		runtime.Gosched()
	}

	waitDone := make(chan bool, 1)
	go func() { waitDone <- sess.waitForDiagnostics(context.Background(), uri, 0, 0) }()
	if err := serverWriter.Close(); err != nil {
		t.Fatal(err)
	}
	// Fresh budget: the wait above may have consumed most of the previous one,
	// which would make this loop fail on nothing worse than slow scheduling.
	deadline = time.Now().Add(time.Second)
	for !client.IsClosed() {
		if time.Now().After(deadline) {
			t.Fatal("transport EOF did not close client")
		}
		runtime.Gosched()
	}
	select {
	case fresh := <-waitDone:
		t.Fatalf("wait returned %v before accepted notifications drained", fresh)
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	select {
	case fresh := <-waitDone:
		if !fresh {
			t.Fatal("wait missed publish accepted before transport EOF")
		}
	case <-time.After(time.Second):
		t.Fatal("wait did not finish after notification drain")
	}
	if got := sess.diagnosticsFor(uri); len(got) != 1 || got[0].Message != "fresh" {
		t.Fatalf("diagnostics = %#v, want fresh publish", got)
	}
}

// TestWaitForDiagnosticsCatchesUpOnContextCancellation covers jatmn's P2 finding
// (1): the cancellation arm used to re-check only what the handler had already
// RUN. A publishDiagnostics accepted off the wire but still queued behind a busy
// worker therefore read as "never arrived", and Manager.Check returned (nil,
// nil) for text the server had in fact answered. Cancellation now settles like
// closure does — let the worker finish what it accepted, then re-read.
// TestCatchUpNotificationsFollowsPublishesAcceptedWhileWaiting covers jatmn's
// #759 finding: catch-up snapshotted the accepted sequence once, so a publish
// the reader accepted DURING the wait was not waited for. The worker finishing
// the stale target satisfied the check, and the caller then read diagnosticsFor
// while a newer publish for the same URI was still queued — the same
// "newest received, not the one that woke the wait" failure the closed-client
// drain regression was written for, on the live path.
func TestCatchUpNotificationsFollowsPublishesAcceptedWhileWaiting(t *testing.T) {
	clientReader, serverWriter := io.Pipe()
	defer serverWriter.Close()
	client := NewClient(clientReader, io.Discard)
	uri := PathToURI("/repo/main.go")
	sess := &session{
		client:       client,
		catchUpGrace: 5 * time.Second,
		diagnostics:  map[string][]Diagnostic{},
		lastPublish:  map[string]time.Time{},
		publishSeq:   map[string]int64{},
		waiters:      map[string][]chan struct{}{},
	}

	publish := func(message string) {
		t.Helper()
		params := PublishDiagnosticsParams{URI: uri, Diagnostics: []Diagnostic{{Message: message}}}
		if err := writeMessage(serverWriter, map[string]any{
			"jsonrpc": "2.0", "method": "textDocument/publishDiagnostics", "params": params,
		}); err != nil {
			t.Error(err)
		}
	}

	blocked := make(chan struct{})
	release := make(chan struct{})
	secondEntered := make(chan struct{})
	releaseSecond := make(chan struct{})
	client.SetNotificationHandler(func(method string, params json.RawMessage, seq int64) {
		switch {
		case method == "test/block":
			close(blocked)
			<-release
			return
		case strings.Contains(string(params), `"first"`):
			// Handling "first" is what makes "second" arrive mid-catch-up: it goes
			// on the wire and is confirmed accepted before this handler returns, so
			// the worker's handled count only reaches "first" once a newer item is
			// already queued behind it.
			publish("second")
			deadline := time.Now().Add(time.Second)
			for client.acceptedNotificationSeq() < 3 {
				if time.Now().After(deadline) {
					t.Error("follow-up publish was not accepted while the worker was busy")
					break
				}
				runtime.Gosched()
			}
		case strings.Contains(string(params), `"second"`):
			// Hold the worker inside this handler so the assertion below observes
			// the state the bug produced: caught up by the stale target, with the
			// newer publish still unrecorded.
			close(secondEntered)
			<-releaseSecond
		}
		sess.handleNotification(method, params, seq)
	})

	if err := writeMessage(serverWriter, map[string]any{"jsonrpc": "2.0", "method": "test/block"}); err != nil {
		t.Fatal(err)
	}
	<-blocked
	publish("first")
	deadline := time.Now().Add(time.Second)
	for client.ReceiptSeq() != 2 {
		if time.Now().After(deadline) {
			t.Fatal("first publish was not accepted")
		}
		runtime.Gosched()
	}

	done := make(chan struct{})
	go func() {
		sess.catchUpNotifications()
		close(done)
	}()
	// Catch-up must be parked in its wait before the worker is released, so the
	// sequence it started from is the one that goes stale.
	select {
	case <-done:
		t.Fatal("catch-up returned while the worker was still behind")
	case <-time.After(25 * time.Millisecond):
	}
	close(release)

	// The worker is now inside the handler for "second", which means "first" is
	// recorded and its sequence — the one catch-up started from — is satisfied.
	// A snapshot-based catch-up returns here, with the newer publish unrecorded.
	select {
	case <-secondEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("worker never reached the follow-up publish")
	}
	select {
	case <-done:
		t.Fatalf("catch-up returned on a stale target; diagnostics = %#v", sess.diagnosticsFor(uri))
	case <-time.After(25 * time.Millisecond):
	}

	close(releaseSecond)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("catch-up did not finish")
	}
	if got := sess.diagnosticsFor(uri); len(got) != 1 || got[0].Message != "second" {
		t.Fatalf("diagnostics = %#v, want the publish accepted during catch-up", got)
	}
}

// TestCatchUpNotificationsGivesUpAfterTheLiveGrace spells out the tradeoff the
// live-client timeout represents: an unbounded wait would hang Check past a
// deadline the caller has already blown, so a worker that never catches up
// costs the grace and no more. Production sessions use a 50ms grace; this uses
// the same order of magnitude rather than the generous one the other tests pin.
func TestCatchUpNotificationsGivesUpAfterTheLiveGrace(t *testing.T) {
	clientReader, serverWriter := io.Pipe()
	defer serverWriter.Close()
	client := NewClient(clientReader, io.Discard)
	sess := &session{
		client:       client,
		catchUpGrace: 25 * time.Millisecond,
		diagnostics:  map[string][]Diagnostic{},
		lastPublish:  map[string]time.Time{},
		publishSeq:   map[string]int64{},
		waiters:      map[string][]chan struct{}{},
	}
	blocked := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	client.SetNotificationHandler(func(method string, _ json.RawMessage, _ int64) {
		if method == "test/block" {
			close(blocked)
			<-release
		}
	})
	if err := writeMessage(serverWriter, map[string]any{"jsonrpc": "2.0", "method": "test/block"}); err != nil {
		t.Fatal(err)
	}
	<-blocked

	done := make(chan struct{})
	start := time.Now()
	go func() {
		sess.catchUpNotifications()
		close(done)
	}()
	select {
	case <-done:
		if elapsed := time.Since(start); elapsed < sess.catchUpGrace {
			t.Fatalf("catch-up returned after %v, before its %v grace", elapsed, sess.catchUpGrace)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("catch-up on a permanently-behind worker must give up after the grace, not hang")
	}
}

func TestWaitForDiagnosticsCatchesUpOnContextCancellation(t *testing.T) {
	clientReader, serverWriter := io.Pipe()
	defer serverWriter.Close()
	client := NewClient(clientReader, io.Discard)
	uri := PathToURI("/repo/main.go")
	sess := &session{
		client: client,
		// Generous, so the catch-up is decided by the worker rather than by how
		// fast this test's goroutines happen to be scheduled.
		catchUpGrace: 5 * time.Second,
		diagnostics:  map[string][]Diagnostic{},
		lastPublish:  map[string]time.Time{},
		publishSeq:   map[string]int64{},
		waiters:      map[string][]chan struct{}{},
	}
	blocked := make(chan struct{})
	release := make(chan struct{})
	client.SetNotificationHandler(func(method string, params json.RawMessage, seq int64) {
		if method == "test/block" {
			close(blocked)
			<-release
			return
		}
		sess.handleNotification(method, params, seq)
	})

	// Occupy the sole worker, then let a publish queue up behind it.
	if err := writeMessage(serverWriter, map[string]any{"jsonrpc": "2.0", "method": "test/block"}); err != nil {
		t.Fatal(err)
	}
	<-blocked
	params := PublishDiagnosticsParams{URI: uri, Diagnostics: []Diagnostic{{Message: "fresh"}}}
	if err := writeMessage(serverWriter, map[string]any{
		"jsonrpc": "2.0", "method": "textDocument/publishDiagnostics", "params": params,
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for client.ReceiptSeq() != 2 {
		if time.Now().After(deadline) {
			t.Fatal("publish was not accepted before cancellation")
		}
		runtime.Gosched()
	}

	ctx, cancel := context.WithCancel(context.Background())
	waitDone := make(chan bool, 1)
	go func() { waitDone <- sess.waitForDiagnostics(ctx, uri, 0, 0) }()
	deadline = time.Now().Add(time.Second)
	for {
		sess.mu.Lock()
		waiting := len(sess.waiters[uri]) == 1
		sess.mu.Unlock()
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("diagnostic wait was not registered")
		}
		runtime.Gosched()
	}

	cancel()
	select {
	case fresh := <-waitDone:
		t.Fatalf("wait returned %v while an accepted publish was still queued", fresh)
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	select {
	case fresh := <-waitDone:
		if !fresh {
			t.Fatal("cancellation discarded a publish that was already off the wire")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("wait did not finish after the worker caught up")
	}
	if got := sess.diagnosticsFor(uri); len(got) != 1 || got[0].Message != "fresh" {
		t.Fatalf("diagnostics = %#v, want the publish accepted before cancellation", got)
	}
}

// TestWaitForDiagnosticsDrainsFollowUpPublishOnCloseDuringDebounce covers
// jatmn's P2 finding (2): the debounce-phase closure arm returned true without
// draining, so Manager.Check read diagnosticsFor while a NEWER publish was still
// queued and handed back the older one. It also pins the drain as
// ctx-independent — cancelling mid-drain (finding (1)'s second clause) must not
// cut it short, since after closure the backlog can no longer grow.
func TestWaitForDiagnosticsDrainsFollowUpPublishOnCloseDuringDebounce(t *testing.T) {
	clientReader, serverWriter := io.Pipe()
	client := NewClient(clientReader, io.Discard)
	uri := PathToURI("/repo/main.go")
	sess := &session{
		client: client,
		// Zero on purpose: the post-closure drain must not be bounded by the
		// live-client grace.
		catchUpGrace: 0,
		diagnostics:  map[string][]Diagnostic{},
		lastPublish:  map[string]time.Time{},
		publishSeq:   map[string]int64{},
		waiters:      map[string][]chan struct{}{},
	}
	blocked := make(chan struct{})
	release := make(chan struct{})
	client.SetNotificationHandler(func(method string, params json.RawMessage, seq int64) {
		if method == "test/block" {
			close(blocked)
			<-release
			return
		}
		sess.handleNotification(method, params, seq)
	})

	publish := func(message string) {
		t.Helper()
		params := PublishDiagnosticsParams{URI: uri, Diagnostics: []Diagnostic{{Message: message}}}
		if err := writeMessage(serverWriter, map[string]any{
			"jsonrpc": "2.0", "method": "textDocument/publishDiagnostics", "params": params,
		}); err != nil {
			t.Fatal(err)
		}
	}

	// A first publish lands and is handled, so the wait below starts out in its
	// debounce phase rather than waiting for a first result.
	publish("first")
	deadline := time.Now().Add(time.Second)
	for {
		if got := sess.diagnosticsFor(uri); len(got) == 1 && got[0].Message == "first" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first publish was never handled")
		}
		runtime.Gosched()
	}
	// Now occupy the worker and queue a newer publish behind it.
	if err := writeMessage(serverWriter, map[string]any{"jsonrpc": "2.0", "method": "test/block"}); err != nil {
		t.Fatal(err)
	}
	<-blocked
	publish("second")
	deadline = time.Now().Add(time.Second)
	for client.ReceiptSeq() != 3 {
		if time.Now().After(deadline) {
			t.Fatal("follow-up publish was not accepted before closure")
		}
		runtime.Gosched()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waitDone := make(chan bool, 1)
	go func() { waitDone <- sess.waitForDiagnostics(ctx, uri, 5*time.Second, 0) }()
	select {
	case fresh := <-waitDone:
		t.Fatalf("wait returned %v instead of debouncing", fresh)
	case <-time.After(25 * time.Millisecond):
	}

	if err := serverWriter.Close(); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(time.Second)
	for !client.IsClosed() {
		if time.Now().After(deadline) {
			t.Fatal("transport EOF did not close client")
		}
		runtime.Gosched()
	}
	// Cancelling now must not cut the drain short.
	cancel()
	select {
	case fresh := <-waitDone:
		t.Fatalf("wait returned %v before the accepted follow-up publish drained", fresh)
	case <-time.After(25 * time.Millisecond):
	}

	close(release)
	select {
	case fresh := <-waitDone:
		if !fresh {
			t.Fatal("wait dropped the publish it had already observed")
		}
	case <-time.After(time.Second):
		t.Fatal("wait did not finish after the notification drain")
	}
	if got := sess.diagnosticsFor(uri); len(got) != 1 || got[0].Message != "second" {
		t.Fatalf("diagnostics = %#v, want the follow-up publish accepted before closure", got)
	}
}

func TestWaitForDiagnosticsPreservesPublishHandledAtDeadline(t *testing.T) {
	previousProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previousProcs)
	params, err := json.Marshal(PublishDiagnosticsParams{
		URI: PathToURI("/repo/main.go"), Diagnostics: []Diagnostic{{Message: "fresh"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		client := &Client{closed: make(chan struct{})}
		uri := PathToURI("/repo/main.go")
		sess := &session{
			client:      client,
			diagnostics: map[string][]Diagnostic{},
			lastPublish: map[string]time.Time{},
			publishSeq:  map[string]int64{},
			waiters:     map[string][]chan struct{}{},
		}
		ctx, cancel := context.WithCancel(context.Background())
		waitDone := make(chan bool, 1)
		go func() {
			waitDone <- sess.waitForDiagnostics(ctx, uri, time.Second, 0)
		}()

		deadline := time.Now().Add(time.Second)
		for {
			sess.mu.Lock()
			waiting := len(sess.waiters[uri]) == 1
			sess.mu.Unlock()
			if waiting {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("diagnostic wait was not registered")
			}
			runtime.Gosched()
		}

		// handleNotification records the publish and closes the waiter. Cancel
		// before yielding under GOMAXPROCS(1), making both cases ready together.
		sess.handleNotification("textDocument/publishDiagnostics", params, 1)
		cancel()

		select {
		case fresh := <-waitDone:
			if !fresh {
				t.Fatalf("iteration %d: deadline discarded a publish handled before cancellation", i)
			}
		case <-time.After(time.Second):
			t.Fatalf("iteration %d: wait did not return", i)
		}
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
