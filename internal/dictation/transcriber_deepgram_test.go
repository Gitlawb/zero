package dictation

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/coder/websocket"
)

func TestDeepgramStreamTranscribeErrorRedaction(t *testing.T) {
	url := wsTestServer(t, func(ctx context.Context, c *websocket.Conn) {
		// Drain the client's audio frames, then wait for CloseStream so the
		// client has flushed its writes before we reject the connection. Closing
		// immediately (before CloseStream) risks the client failing on a write
		// error and never observing this API-key-bearing close reason.
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageText && strings.Contains(string(data), "CloseStream") {
				break
			}
		}
		c.Close(websocket.StatusPolicyViolation, "invalid key sk-test-key")
	})

	tr, err := NewDeepgramTranscriber(DeepgramConfig{APIKey: "sk-test-key", BaseURL: url})
	if err != nil {
		t.Fatal(err)
	}

	chunks := make(chan []byte, 1)
	chunks <- make([]byte, 320)
	close(chunks)
	_, ferr := tr.StreamTranscribe(context.Background(), chunks, func(string, bool) {})
	if ferr == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(ferr.Error(), "sk-test-key") {
		t.Errorf("API key leaked: %v", ferr)
	}
}

func TestDeepgramStreamTranscribeCancelKeepsSentinel(t *testing.T) {
	firstFrame := make(chan struct{})
	url := wsTestServer(t, func(ctx context.Context, c *websocket.Conn) {
		// Hold the connection open, never answering, so the client blocks in
		// Read until its context is cancelled (the Esc-abort path).
		var once sync.Once
		for {
			if _, _, err := c.Read(ctx); err != nil {
				return
			}
			once.Do(func() { close(firstFrame) })
		}
	})

	tr, err := NewDeepgramTranscriber(DeepgramConfig{APIKey: "sk-test-key", BaseURL: url})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	chunks := make(chan []byte, 1)
	defer close(chunks)
	chunks <- make([]byte, 320)
	// The channel stays open: the session is live when the user aborts.
	errCh := make(chan error, 1)
	go func() {
		_, ferr := tr.StreamTranscribe(ctx, chunks, nil)
		errCh <- ferr
	}()

	select {
	case <-firstFrame:
		cancel()
		ferr := <-errCh
		if !errors.Is(ferr, context.Canceled) {
			t.Fatalf("cancelled stream error lost the context.Canceled sentinel: %v", ferr)
		}
	case ferr := <-errCh:
		t.Fatalf("StreamTranscribe failed early instead of blocking: %v", ferr)
	}
}
