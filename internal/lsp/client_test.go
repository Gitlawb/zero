package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
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
	client.SetNotificationHandler(func(method string, _ json.RawMessage) {
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
	client.SetNotificationHandler(func(_ string, _ json.RawMessage) {
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
	client.SetNotificationHandler(func(method string, _ json.RawMessage) {
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
	client.SetNotificationHandler(func(method string, _ json.RawMessage) {
		if method != "blocking" {
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
		for i := 0; i < notificationQueueSize+1; i++ {
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
}

func TestClientNotificationOverflowDropsOldestInOrder(t *testing.T) {
	client := &Client{notifyReady: make(chan struct{}, 1)}
	for i := 0; i < notificationQueueSize+2; i++ {
		client.enqueueNotification(notification{method: fmt.Sprintf("notification-%03d", i)})
	}

	for i := 2; i < notificationQueueSize+2; i++ {
		notification, ok := client.dequeueNotification()
		if !ok {
			t.Fatalf("notification %d missing", i)
		}
		want := fmt.Sprintf("notification-%03d", i)
		if notification.method != want {
			t.Fatalf("notification = %q, want %q", notification.method, want)
		}
	}
	if _, ok := client.dequeueNotification(); ok {
		t.Fatal("notification queue exceeded its bound")
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
