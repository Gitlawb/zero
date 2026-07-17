package dictation

import (
	"context"
	"strings"
	"testing"

	"github.com/coder/websocket"
)

func TestDeepgramStreamTranscribeErrorRedaction(t *testing.T) {
	url := wsTestServer(t, func(ctx context.Context, c *websocket.Conn) {
		// close immediately with an error that simulates connection issue with API key
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
