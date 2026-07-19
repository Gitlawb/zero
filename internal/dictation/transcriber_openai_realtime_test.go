package dictation

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/coder/websocket"
)

func TestOpenAIRealtimeStreamTranscribeErrorRedaction(t *testing.T) {
	url := wsTestServer(t, func(ctx context.Context, c *websocket.Conn) {
		defer c.Close(websocket.StatusNormalClosure, "")
		for {
			typ, _, err := c.Read(ctx)
			if err != nil {
				return
			}
			if typ != websocket.MessageText {
				continue
			}
			_ = c.Write(ctx, websocket.MessageText, []byte(`{"type":"error","error":{"message":"invalid API key sk-test-key"}}`))
		}
	})

	tr, err := NewOpenAIRealtimeTranscriber(OpenAIRealtimeConfig{APIKey: "sk-test-key", BaseURL: url})
	if err != nil {
		t.Fatal(err)
	}

	chunks := make(chan []byte, 1)
	chunks <- make([]byte, 480)
	close(chunks)
	_, ferr := tr.StreamTranscribe(context.Background(), chunks, func(string, bool) {})
	if ferr == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(ferr.Error(), "sk-test-key") {
		t.Errorf("API key leaked: %v", ferr)
	}
}

func TestOpenAIRealtimeStreamTranscribeCancelKeepsSentinel(t *testing.T) {
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

	tr, err := NewOpenAIRealtimeTranscriber(OpenAIRealtimeConfig{APIKey: "sk-test-key", BaseURL: url})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	chunks := make(chan []byte, 1)
	defer close(chunks)
	chunks <- make([]byte, 480)
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
