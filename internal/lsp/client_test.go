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

	for range 2 {
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
