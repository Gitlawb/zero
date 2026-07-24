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

// Esc can cancel while the WebSocket dial is still pending. The dial error is
// redacted with %s, so we must return ctx.Err() rather than a flat string.
func TestOpenAIRealtimeStreamTranscribeDialCancelKeepsSentinel(t *testing.T) {
	tr, err := NewOpenAIRealtimeTranscriber(OpenAIRealtimeConfig{
		APIKey:  "sk-test-key",
		BaseURL: "ws://127.0.0.1:1", // never reached; ctx is already cancelled
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	chunks := make(chan []byte)
	defer close(chunks)

	_, ferr := tr.StreamTranscribe(ctx, chunks, nil)
	if !errors.Is(ferr, context.Canceled) {
		t.Fatalf("dial-cancel lost the context.Canceled sentinel: %v", ferr)
	}
}

// Esc right after accept can hit the session-update write or the first Read;
// both redaction sites must still yield context.Canceled.
func TestOpenAIRealtimeStreamTranscribeStartupCancelKeepsSentinel(t *testing.T) {
	accepted := make(chan struct{})
	url := wsTestServer(t, func(ctx context.Context, c *websocket.Conn) {
		close(accepted)
		// Do not read: leave the client in session-update write or first Read.
		<-ctx.Done()
	})

	tr, err := NewOpenAIRealtimeTranscriber(OpenAIRealtimeConfig{APIKey: "sk-test-key", BaseURL: url})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	chunks := make(chan []byte, 1)
	defer close(chunks)
	errCh := make(chan error, 1)
	go func() {
		_, ferr := tr.StreamTranscribe(ctx, chunks, nil)
		errCh <- ferr
	}()

	select {
	case <-accepted:
		cancel()
		ferr := <-errCh
		if !errors.Is(ferr, context.Canceled) {
			t.Fatalf("startup-cancel lost the context.Canceled sentinel: %v", ferr)
		}
	case ferr := <-errCh:
		t.Fatalf("StreamTranscribe failed before accept: %v", ferr)
	}
}

// After a server event the client selects writeErrCh. Cancel while the writer
// is blocked on more chunks so that path observes a cancelled write and must
// still return context.Canceled rather than a redacted flat string.
func TestOpenAIRealtimeStreamTranscribeWriteCancelKeepsSentinel(t *testing.T) {
	sessionReceived := make(chan struct{})
	url := wsTestServer(t, func(ctx context.Context, c *websocket.Conn) {
		if _, _, err := c.Read(ctx); err != nil {
			return
		}
		close(sessionReceived)
		for {
			if _, _, err := c.Read(ctx); err != nil {
				return
			}
		}
	})

	tr, err := NewOpenAIRealtimeTranscriber(OpenAIRealtimeConfig{APIKey: "sk-test-key", BaseURL: url})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	chunks := make(chan []byte, 1)
	chunks <- make([]byte, 480)
	defer close(chunks)

	errCh := make(chan error, 1)
	go func() {
		_, ferr := tr.StreamTranscribe(ctx, chunks, nil)
		errCh <- ferr
	}()

	select {
	case <-sessionReceived:
		cancel()
		ferr := <-errCh
		if !errors.Is(ferr, context.Canceled) {
			t.Fatalf("write-path cancel lost the context.Canceled sentinel: %v", ferr)
		}
	case ferr := <-errCh:
		t.Fatalf("StreamTranscribe failed early instead of blocking: %v", ferr)
	}
}

// jatmn (review, PR #710): Esc can race an incoming OpenAI error event.
// conn.Read may already have the error frame in hand by the time the
// cancellation lands. The server fires a delta immediately followed by
// the error, back to back. Cancel from inside the delta callback (same
// path the TUI uses when a partial triggers Esc handling) so the cancel
// is observed before the next event is processed. The returned error
// must still be context.Canceled, not the redacted OpenAI error.
func TestOpenAIRealtimeStreamTranscribeErrorRaceKeepsSentinel(t *testing.T) {
	url := wsTestServer(t, func(ctx context.Context, c *websocket.Conn) {
		if _, _, err := c.Read(ctx); err != nil {
			return
		}
		_ = c.Write(ctx, websocket.MessageText, []byte(
			`{"type":"conversation.item.input_audio_transcription.delta","delta":"hi"}`,
		))
		_ = c.Write(ctx, websocket.MessageText, []byte(
			`{"type":"error","error":{"message":"invalid API key sk-test-key"}}`,
		))
		<-ctx.Done()
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
	var once sync.Once
	_, ferr := tr.StreamTranscribe(ctx, chunks, func(string, bool) {
		// Cancel synchronously from the partial callback, before the
		// stream loop continues to the already-buffered error frame.
		once.Do(cancel)
	})
	if !errors.Is(ferr, context.Canceled) {
		t.Fatalf("StreamTranscribe error = %v, want context.Canceled (cancel must win over a racing OpenAI error event)", ferr)
	}
}
