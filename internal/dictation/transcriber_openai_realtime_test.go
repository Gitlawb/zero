package dictation

import (
	"context"
	"strings"
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
