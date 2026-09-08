package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMessageFramingRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	payload := outgoingRequest{JSONRPC: "2.0", ID: 7, Method: "initialize", Params: map[string]any{"rootUri": "file:///r"}}
	if err := writeMessage(&buf, payload); err != nil {
		t.Fatalf("writeMessage: %v", err)
	}
	body, err := readMessage(bufio.NewReader(&buf))
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	var got incomingMessage
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Method != "initialize" || string(got.ID) != "7" {
		t.Fatalf("round-trip mismatch: %s", body)
	}
}

func TestReadMessageIgnoresExtraHeaders(t *testing.T) {
	raw := "Content-Type: application/vscode-jsonrpc; charset=utf-8\r\nContent-Length: 17\r\n\r\n{\"jsonrpc\":\"2.0\"}"
	body, err := readMessage(bufio.NewReader(strings.NewReader(raw)))
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	if string(body) != `{"jsonrpc":"2.0"}` {
		t.Fatalf("body = %q", body)
	}
}

func TestReadMessageRejectsMissingContentLength(t *testing.T) {
	if _, err := readMessage(bufio.NewReader(strings.NewReader("X: y\r\n\r\n{}"))); err == nil {
		t.Fatal("expected error for missing Content-Length")
	}
}

func TestClientMatchesConcurrentResponsesByID(t *testing.T) {
	clientReader, serverWriter := io.Pipe()
	serverReader, clientWriter := io.Pipe()
	client := NewClient(clientReader, clientWriter)
	defer client.Close()
	defer serverWriter.Close()
	defer clientWriter.Close()

	// Stub server: read BOTH requests, then reply in REVERSE order so a broken
	// id router would deliver a response to the wrong caller.
	go func() {
		reader := bufio.NewReader(serverReader)
		type req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		var reqs []req
		for len(reqs) < 2 {
			body, err := readMessage(reader)
			if err != nil {
				return
			}
			var r req
			_ = json.Unmarshal(body, &r)
			reqs = append(reqs, r)
		}
		for i := len(reqs) - 1; i >= 0; i-- {
			_ = writeMessage(serverWriter, map[string]any{
				"jsonrpc": "2.0",
				"id":      reqs[i].ID,
				"result":  map[string]string{"method": reqs[i].Method},
			})
		}
	}()

	type outcome struct{ err error }
	results := make(chan outcome, 2)
	call := func(method string) {
		raw, err := client.Call(context.Background(), method, nil)
		if err != nil {
			results <- outcome{err: err}
			return
		}
		var got struct {
			Method string `json:"method"`
		}
		if err := json.Unmarshal(raw, &got); err != nil {
			results <- outcome{err: err}
			return
		}
		if got.Method != method {
			results <- outcome{err: fmt.Errorf("id mismatch: sent %q, got response for %q", method, got.Method)}
			return
		}
		results <- outcome{}
	}
	go call("alpha")
	go call("beta")

	for i := 0; i < 2; i++ {
		select {
		case r := <-results:
			if r.err != nil {
				t.Fatal(r.err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for responses")
		}
	}
}

func TestClientCallContextCancel(t *testing.T) {
	clientReader, serverWriter := io.Pipe()
	serverReader, clientWriter := io.Pipe()
	client := NewClient(clientReader, clientWriter)
	defer client.Close()
	defer serverWriter.Close()
	defer clientWriter.Close()
	// Drain requests but never reply, so the call must unblock via context.
	go func() {
		reader := bufio.NewReader(serverReader)
		for {
			if _, err := readMessage(reader); err != nil {
				return
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := client.Call(ctx, "initialize", nil); err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestPerformInitializeHandshake(t *testing.T) {
	clientReader, serverWriter := io.Pipe()
	serverReader, clientWriter := io.Pipe()
	client := NewClient(clientReader, clientWriter)
	defer client.Close()
	defer serverWriter.Close()
	defer clientWriter.Close()

	initialized := make(chan struct{}, 1)
	gotRootURI := make(chan string, 1)
	go func() {
		reader := bufio.NewReader(serverReader)
		for {
			body, err := readMessage(reader)
			if err != nil {
				return
			}
			var msg struct {
				ID     json.RawMessage  `json:"id"`
				Method string           `json:"method"`
				Params InitializeParams `json:"params"`
			}
			_ = json.Unmarshal(body, &msg)
			switch msg.Method {
			case "initialize":
				gotRootURI <- msg.Params.RootURI
				_ = writeMessage(serverWriter, map[string]any{
					"jsonrpc": "2.0",
					"id":      msg.ID,
					"result":  map[string]any{"capabilities": map[string]any{}},
				})
			case "initialized":
				initialized <- struct{}{}
			}
		}
	}()

	if err := performInitialize(context.Background(), client, "/repo/project"); err != nil {
		t.Fatalf("performInitialize: %v", err)
	}
	select {
	case uri := <-gotRootURI:
		if uri != PathToURI("/repo/project") {
			t.Fatalf("rootUri = %q, want %q", uri, PathToURI("/repo/project"))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server never received initialize params")
	}
	select {
	case <-initialized:
	case <-time.After(2 * time.Second):
		t.Fatal("server never received the initialized notification")
	}
}

func TestClientNotificationHandler(t *testing.T) {
	clientReader, serverWriter := io.Pipe()
	serverReader, clientWriter := io.Pipe()
	client := NewClient(clientReader, clientWriter)
	defer client.Close()
	defer serverWriter.Close()
	defer clientWriter.Close()
	_ = serverReader

	received := make(chan string, 1)
	client.SetNotificationHandler(func(method string, _ json.RawMessage, _ int64) {
		received <- method
	})
	_ = writeMessage(serverWriter, map[string]any{
		"jsonrpc": "2.0",
		"method":  "textDocument/publishDiagnostics",
		"params":  map[string]any{"uri": "file:///x", "diagnostics": []any{}},
	})

	select {
	case method := <-received:
		if method != "textDocument/publishDiagnostics" {
			t.Fatalf("notification method = %q", method)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("notification handler was not called")
	}
}

func TestClientNotificationHandlerCanCallClient(t *testing.T) {
	clientReader, serverWriter := io.Pipe()
	serverReader, clientWriter := io.Pipe()
	client := NewClient(clientReader, clientWriter)
	defer client.Close()
	defer serverWriter.Close()
	defer clientWriter.Close()

	serverDone := make(chan error, 1)
	go func() {
		body, err := readMessage(bufio.NewReader(serverReader))
		if err != nil {
			serverDone <- err
			return
		}
		var request incomingMessage
		if err := json.Unmarshal(body, &request); err != nil {
			serverDone <- err
			return
		}
		serverDone <- writeMessage(serverWriter, map[string]any{
			"jsonrpc": "2.0",
			"id":      request.ID,
			"result":  map[string]bool{"applied": true},
		})
	}()

	handlerDone := make(chan error, 1)
	client.SetNotificationHandler(func(_ string, _ json.RawMessage, _ int64) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, err := client.Call(ctx, "workspace/applyEdit", nil)
		handlerDone <- err
	})
	if err := writeMessage(serverWriter, map[string]any{
		"jsonrpc": "2.0",
		"method":  "workspace/requestEdit",
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-handlerDone:
		if err != nil {
			t.Fatalf("notification handler call failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("notification handler deadlocked waiting for its response")
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("server failed: %v", err)
	}
}

func TestClientNotificationHandlersPreserveOrder(t *testing.T) {
	clientReader, serverWriter := io.Pipe()
	client := NewClient(clientReader, io.Discard)
	defer client.Close()
	defer serverWriter.Close()

	received := make(chan string, 2)
	client.SetNotificationHandler(func(method string, _ json.RawMessage, _ int64) {
		received <- method
	})
	for _, method := range []string{"first", "second"} {
		if err := writeMessage(serverWriter, map[string]any{
			"jsonrpc": "2.0",
			"method":  method,
		}); err != nil {
			t.Fatal(err)
		}
	}

	for _, want := range []string{"first", "second"} {
		select {
		case got := <-received:
			if got != want {
				t.Fatalf("notification = %q, want %q", got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for notification %q", want)
		}
	}
}

func TestClientNotificationBurstDoesNotBlockResponse(t *testing.T) {
	clientReader, serverWriter := io.Pipe()
	serverReader, clientWriter := io.Pipe()
	client := NewClient(clientReader, clientWriter)
	defer client.Close()
	defer serverWriter.Close()
	defer clientWriter.Close()

	handlerStarted := make(chan struct{})
	handlerDone := make(chan error, 1)
	var mu sync.Mutex
	var queued []string
	allQueued := make(chan struct{})
	client.SetNotificationHandler(func(method string, _ json.RawMessage, _ int64) {
		if method != "blocking" {
			mu.Lock()
			queued = append(queued, method)
			complete := len(queued) == notificationBurstSize
			mu.Unlock()
			if complete {
				close(allQueued)
			}
			return
		}
		close(handlerStarted)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, err := client.Call(ctx, "workspace/applyEdit", nil)
		handlerDone <- err
	})

	serverDone := make(chan error, 1)
	go func() {
		reader := bufio.NewReader(serverReader)
		body, err := readMessage(reader)
		if err != nil {
			serverDone <- err
			return
		}
		var request incomingMessage
		if err := json.Unmarshal(body, &request); err != nil {
			serverDone <- err
			return
		}
		for i := 0; i < notificationBurstSize; i++ {
			if err := writeMessage(serverWriter, map[string]any{
				"jsonrpc": "2.0",
				"method":  fmt.Sprintf("queued-%03d", i),
			}); err != nil {
				serverDone <- err
				return
			}
		}
		serverDone <- writeMessage(serverWriter, map[string]any{
			"jsonrpc": "2.0",
			"id":      request.ID,
			"result":  nil,
		})
	}()

	if err := writeMessage(serverWriter, map[string]any{
		"jsonrpc": "2.0",
		"method":  "blocking",
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-handlerStarted:
	case <-time.After(time.Second):
		t.Fatal("blocking notification handler did not start")
	}
	select {
	case err := <-handlerDone:
		if err != nil {
			t.Fatalf("notification handler call failed under burst: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("notification burst blocked the response read loop")
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("server failed: %v", err)
	}
	// Not blocking the reader is only half the requirement: every notification the
	// server sent during the burst must still reach the handler, in order. A
	// bounded queue that drops the oldest would silently lose the head of this
	// sequence — and with it a publishDiagnostics the checker is waiting for.
	select {
	case <-allQueued:
	case <-time.After(5 * time.Second):
		mu.Lock()
		received := len(queued)
		mu.Unlock()
		t.Fatalf("received %d of %d burst notifications; the queue lost messages", received, notificationBurstSize)
	}
	mu.Lock()
	defer mu.Unlock()
	for i, method := range queued {
		if want := fmt.Sprintf("queued-%03d", i); method != want {
			t.Fatalf("burst notification %d = %q, want %q (order must survive the burst)", i, method, want)
		}
	}
}

// notificationBurstSize is far past any buffer the dispatch used to have, so a
// bounded queue would be visible as either a blocked read loop or a lost message.
const notificationBurstSize = 512

// TestClientNotificationQueueIsLossless is the regression test for the overflow
// policy: a queued notification must never be discarded. A dropped
// textDocument/publishDiagnostics is the server's only report for that URI, so
// losing it makes session.waitForDiagnostics time out and Manager.Check return
// nothing even though the server published findings.
func TestClientNotificationQueueIsLossless(t *testing.T) {
	client := &Client{notifyReady: make(chan struct{}, 1)}
	for i := 0; i < notificationBurstSize; i++ {
		client.enqueueNotification(notification{method: fmt.Sprintf("notification-%03d", i)})
	}

	for i := 0; i < notificationBurstSize; i++ {
		item, ok, _ := client.dequeueNotification()
		if !ok {
			t.Fatalf("notification %d was discarded by the queue", i)
		}
		want := fmt.Sprintf("notification-%03d", i)
		if item.method != want {
			t.Fatalf("notification = %q, want %q (queue must stay FIFO)", item.method, want)
		}
	}
	if _, ok, _ := client.dequeueNotification(); ok {
		t.Fatal("queue returned more notifications than were enqueued")
	}
	if client.notifyQueue != nil {
		t.Fatalf("drained queue retained %d slots of capacity", cap(client.notifyQueue))
	}
	if client.notifyBytes != 0 {
		t.Fatalf("drained queue retained byte accounting: %d", client.notifyBytes)
	}
}

func TestClientDrainsAcceptedNotificationsAfterTransportEOF(t *testing.T) {
	clientReader, serverWriter := io.Pipe()
	client := NewClient(clientReader, io.Discard)

	blockStarted := make(chan struct{})
	releaseBlock := make(chan struct{})
	handled := make(chan string, 2)
	client.SetNotificationHandler(func(method string, _ json.RawMessage, _ int64) {
		if method == "block" {
			close(blockStarted)
			<-releaseBlock
		}
		handled <- method
	})

	if err := writeMessage(serverWriter, map[string]any{"jsonrpc": "2.0", "method": "block"}); err != nil {
		t.Fatal(err)
	}
	<-blockStarted
	if err := writeMessage(serverWriter, map[string]any{
		"jsonrpc": "2.0", "method": "textDocument/publishDiagnostics",
	}); err != nil {
		t.Fatal(err)
	}
	// Wait until the reader has accepted the publish, then fail it while the
	// sole worker is still in the preceding handler and cannot dequeue it.
	deadline := time.Now().Add(time.Second)
	for client.ReceiptSeq() != 2 {
		if time.Now().After(deadline) {
			t.Fatal("publish was not accepted by the read loop")
		}
		runtime.Gosched()
	}
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
	close(releaseBlock)

	for _, want := range []string{"block", "textDocument/publishDiagnostics"} {
		select {
		case got := <-handled:
			if got != want {
				t.Fatalf("handled %q, want %q", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("accepted notification %q was not delivered after EOF", want)
		}
	}
	select {
	case <-client.notificationDrained:
	case <-time.After(time.Second):
		t.Fatal("notification worker did not report the accepted queue drained")
	}
}

// TestReceiptStampedAtReadNotEnqueue is the regression test for the review gap
// on #759 (pullrequestreview-4834747507): TestPublishBaselineRejectsFrameReadBeforeBaseline
// calls stampReceipt itself and never drives readLoop, so it pins the seq
// comparison but not WHERE the stamp is taken — a refactor that moved stamping
// back to the enqueue call site (reverting the read-time fix) left that test
// green.
//
// This test distinguishes the two call sites directly: a response frame is
// read off the wire but never reaches enqueueNotification (see the `hasID`
// branch in readLoop). Under read-time stamping every frame consumes a receipt
// number — "gaps in it are fine", per stampReceipt's doc comment — so
// ReceiptSeq must advance even though nothing was queued. Under enqueue-time
// stamping it would not, since stampReceipt would never run for this frame.
func TestReceiptStampedAtReadNotEnqueue(t *testing.T) {
	clientReader, serverWriter := io.Pipe()
	client := NewClient(clientReader, io.Discard)
	defer serverWriter.Close()

	// A plain response frame: has an id, no method, so it is delivered via
	// c.deliver and never passed to enqueueNotification.
	if err := writeMessage(serverWriter, map[string]any{
		"jsonrpc": "2.0", "id": 1, "result": nil,
	}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(time.Second)
	for client.ReceiptSeq() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("ReceiptSeq did not advance for a frame that was read but never enqueued as a notification")
		}
		runtime.Gosched()
	}
}

// TestReadMessageRejectsOversizedFrame covers jatmn's P3 finding on #759: the
// notification byte budget was enforced only at enqueue, but readMessage
// allocates the whole body before anything can tell a notification from a
// response. A single frame declaring a huge Content-Length was therefore
// materialized in full and only then measured against a budget it had already
// blown past. The header is now rejected before the allocation.
func TestReadMessageRejectsOversizedFrame(t *testing.T) {
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", maxFrameBytes+1)
	_, err := readMessage(bufio.NewReader(strings.NewReader(header)))
	if err == nil {
		t.Fatal("an oversized frame must be rejected, not allocated and read")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("error = %v, want the frame-size limit to reject it before reading the body", err)
	}
	// A frame at the limit is still legal: the cap rejects what is over it, not
	// what merely approaches it.
	body := strings.Repeat("x", 16)
	atLimit := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
	if _, err := readMessage(bufio.NewReader(strings.NewReader(atLimit))); err != nil {
		t.Fatalf("readMessage of an ordinary frame: %v", err)
	}
}

// TestClientFailsOnNotificationBacklogOverload is the regression test for
// jatmn's #759 P2 finding: the lossless queue above had no failure policy — a
// language server sustaining a higher notification rate than the single
// handler can drain (no permanently-stuck handler required, just a sustained
// producer faster than the consumer) would retain every full json.RawMessage
// payload on Zero's heap without bound. Hitting notifyQueueLimit must fail
// (and close) the client observably instead of continuing to grow.
func TestClientFailsOnNotificationBacklogOverload(t *testing.T) {
	client := &Client{
		notifyReady: make(chan struct{}, 1),
		pending:     make(map[int64]chan rpcResponse),
		closed:      make(chan struct{}),
	}
	for i := 0; i < notifyQueueLimit; i++ {
		client.enqueueNotification(notification{method: "spam"})
	}
	if client.IsClosed() {
		t.Fatal("client closed before the backlog limit was reached")
	}
	client.notifyMu.Lock()
	queued := len(client.notifyQueue)
	client.notifyMu.Unlock()
	if queued != notifyQueueLimit {
		t.Fatalf("queued = %d, want %d before the limit is exceeded", queued, notifyQueueLimit)
	}

	// One push past the limit must fail the client rather than growing the
	// queue further.
	client.enqueueNotification(notification{method: "spam"})
	if !client.IsClosed() {
		t.Fatal("client must be closed once the notification backlog exceeds notifyQueueLimit")
	}
	client.notifyMu.Lock()
	queuedAfter := len(client.notifyQueue)
	bytesAfter := client.notifyBytes
	client.notifyMu.Unlock()
	if queuedAfter != notifyQueueLimit {
		t.Fatalf("closed client retained %d accepted notifications, want %d pending worker drain", queuedAfter, notifyQueueLimit)
	}
	if bytesAfter == 0 {
		t.Fatal("accepted notifications were discarded instead of left for worker drain")
	}
}

func TestClientFailsOnNotificationBacklogByteOverload(t *testing.T) {
	client := &Client{
		notifyReady: make(chan struct{}, 1),
		pending:     make(map[int64]chan rpcResponse),
		closed:      make(chan struct{}),
	}
	const messageCount = 16
	const method = "spam"
	for i := 0; i < messageCount; i++ {
		client.enqueueNotification(notification{
			method: method,
			params: make(json.RawMessage, notifyQueueByteLimit/messageCount-len(method)),
		})
	}
	if client.IsClosed() {
		t.Fatal("client closed before the notification byte limit was exceeded")
	}
	client.notifyMu.Lock()
	queued := len(client.notifyQueue)
	queuedBytes := client.notifyBytes
	client.notifyMu.Unlock()
	if queued != messageCount {
		t.Fatalf("queued = %d, want %d at the byte limit", queued, messageCount)
	}
	if queuedBytes != notifyQueueByteLimit {
		t.Fatalf("queued bytes = %d, want limit %d", queuedBytes, notifyQueueByteLimit)
	}

	client.enqueueNotification(notification{method: "x"})
	if !client.IsClosed() {
		t.Fatal("client must close before retaining notification bytes past the limit")
	}
	client.notifyMu.Lock()
	queuedAfter := len(client.notifyQueue)
	bytesAfter := client.notifyBytes
	client.notifyMu.Unlock()
	if queuedAfter != messageCount || bytesAfter != notifyQueueByteLimit {
		t.Fatalf("accepted backlog changed during close: %d notifications and %d accounted bytes", queuedAfter, bytesAfter)
	}
}

func TestClientRejectsCallsAfterClose(t *testing.T) {
	clientReader, serverWriter := io.Pipe()
	serverReader, clientWriter := io.Pipe()
	client := NewClient(clientReader, clientWriter)
	defer serverWriter.Close()
	defer clientWriter.Close()
	_ = serverReader

	client.Close()
	if _, err := client.Call(context.Background(), "initialize", nil); err == nil {
		t.Fatal("Call after Close must return an error")
	}
	if err := client.Notify(context.Background(), "initialized", nil); err == nil {
		t.Fatal("Notify after Close must return an error")
	}
}

// TestClientDropsNotificationsAfterClose covers the shutdown path: Close stops
// the worker loop, but the read loop keeps reading until the transport ends —
// Server.Shutdown closes the client BEFORE closing stdin, so a server emitting
// notifications while it handles shutdown/exit would otherwise pile them into a
// queue nobody drains. Nothing may be handled or retained after Close.
func TestClientDropsNotificationsAfterClose(t *testing.T) {
	clientReader, serverWriter := io.Pipe()
	serverReader, clientWriter := io.Pipe()
	client := NewClient(clientReader, clientWriter)
	defer serverWriter.Close()
	defer clientWriter.Close()
	go func() {
		// Drain anything the client writes so no write can block the test.
		_, _ = io.Copy(io.Discard, serverReader)
	}()

	handled := make(chan string, 4)
	client.SetNotificationHandler(func(method string, _ json.RawMessage, _ int64) {
		handled <- method
	})

	// A notification before Close still reaches the handler.
	if err := writeMessage(serverWriter, map[string]any{"jsonrpc": "2.0", "method": "before-close"}); err != nil {
		t.Fatal(err)
	}
	select {
	case method := <-handled:
		if method != "before-close" {
			t.Fatalf("handled %q, want before-close", method)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("notification before Close was not handled")
	}

	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// The transport stays writable, exactly as it does between Client.Close and
	// stdin.Close in Server.Shutdown.
	for i := 0; i < 64; i++ {
		if err := writeMessage(serverWriter, map[string]any{
			"jsonrpc": "2.0",
			"method":  fmt.Sprintf("after-close-%03d", i),
		}); err != nil {
			t.Fatalf("write notification after Close: %v", err)
		}
	}

	select {
	case method := <-handled:
		t.Fatalf("handler ran for %q after Close", method)
	case <-time.After(200 * time.Millisecond):
	}

	// Give the read loop time to consume every frame it was handed, then require
	// the queue to hold nothing: dropping on enqueue is what keeps a closed client
	// from growing for as long as its reader stays open.
	deadline := time.Now().Add(2 * time.Second)
	for {
		client.notifyMu.Lock()
		queued := len(client.notifyQueue)
		closed := client.notifyClosed
		client.notifyMu.Unlock()
		if !closed {
			t.Fatal("Close did not mark the notification queue closed")
		}
		if queued != 0 {
			t.Fatalf("closed client retained %d queued notifications", queued)
		}
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}
