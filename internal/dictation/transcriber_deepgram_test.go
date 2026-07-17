package dictation

import (
	"context"
	"strings"
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
