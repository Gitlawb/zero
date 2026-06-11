package mcp

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestMessageWriterEmitsNewlineDelimitedJSON(t *testing.T) {
	var out bytes.Buffer
	if err := newMessageWriter(&out).write(rpcMessage{Method: "ping"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := out.String()
	if strings.Contains(got, "Content-Length") {
		t.Fatalf("writer must use newline-delimited framing, got %q", got)
	}
	if !strings.HasSuffix(got, "\n") || strings.Count(got, "\n") != 1 {
		t.Fatalf("expected exactly one trailing newline, got %q", got)
	}
	if !strings.HasPrefix(strings.TrimSpace(got), "{") {
		t.Fatalf("expected a single JSON object line, got %q", got)
	}
}

func TestMessageReaderAcceptsNewlineDelimited(t *testing.T) {
	in := strings.NewReader(`{"jsonrpc":"2.0","method":"tools/list"}` + "\n")
	msg, err := newMessageReader(in).read()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if msg.Method != "tools/list" {
		t.Fatalf("method=%q want tools/list", msg.Method)
	}
}

func TestMessageReaderAcceptsFinalLineWithoutTrailingNewline(t *testing.T) {
	// A final message that ends at EOF without a trailing newline must still be
	// decoded (the read() EOF-tail branch), so a peer that doesn't terminate its
	// last line isn't dropped.
	in := strings.NewReader(`{"jsonrpc":"2.0","method":"tools/list"}`)
	msg, err := newMessageReader(in).read()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if msg.Method != "tools/list" {
		t.Fatalf("method=%q want tools/list", msg.Method)
	}
}

func TestMessageReaderAcceptsContentLengthFraming(t *testing.T) {
	body := `{"jsonrpc":"2.0","method":"initialize"}`
	framed := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
	msg, err := newMessageReader(strings.NewReader(framed)).read()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if msg.Method != "initialize" {
		t.Fatalf("method=%q want initialize", msg.Method)
	}
}

func TestMessageReaderRejectsOversizedContentLength(t *testing.T) {
	// A hostile Content-Length must be rejected before any allocation, not used
	// to size a make([]byte, n).
	framed := fmt.Sprintf("Content-Length: %d\r\n\r\n", maxMessageBytes+1)
	_, err := newMessageReader(strings.NewReader(framed)).read()
	if err == nil {
		t.Fatal("expected oversized Content-Length to be rejected")
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Fatalf("expected limit error, got %v", err)
	}
}

func TestMessageReaderWriterRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	w := newMessageWriter(&buf)
	for _, method := range []string{"a", "b", "c"} {
		if err := w.write(rpcMessage{Method: method}); err != nil {
			t.Fatalf("write %s: %v", method, err)
		}
	}
	r := newMessageReader(&buf)
	for _, want := range []string{"a", "b", "c"} {
		msg, err := r.read()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if msg.Method != want {
			t.Fatalf("method=%q want %q", msg.Method, want)
		}
	}
}
