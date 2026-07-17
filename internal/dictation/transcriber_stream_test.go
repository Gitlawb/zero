package dictation

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// wsTestServer runs handler over a real localhost websocket, returning a wss->ws
// base URL the transcribers can dial. Exercising the real coder/websocket client
// against a real server is the closest unit test to the wire protocol.
func wsTestServer(t *testing.T, handler func(ctx context.Context, c *websocket.Conn)) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		handler(r.Context(), c)
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func TestDeepgramStreamTranscribe(t *testing.T) {
	var gotAuth string
	url := wsTestServer(t, func(ctx context.Context, c *websocket.Conn) {
		defer c.Close(websocket.StatusNormalClosure, "")
		// Read audio frames until CloseStream, then emit interim + final results.
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageText && strings.Contains(string(data), "CloseStream") {
				break
			}
		}
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"type":"Results","is_final":false,"channel":{"alternatives":[{"transcript":"hello"}]}}`))
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"type":"Results","is_final":true,"channel":{"alternatives":[{"transcript":"hello world"}]}}`))
	})

	tr, err := NewDeepgramTranscriber(DeepgramConfig{APIKey: "dg-key", BaseURL: url})
	if err != nil {
		t.Fatal(err)
	}
	_ = gotAuth

	var partials []string
	chunks := make(chan []byte, 2)
	chunks <- make([]byte, 320)
	close(chunks)
	final, err := tr.StreamTranscribe(context.Background(), chunks, func(text string, _ bool) {
		partials = append(partials, text)
	})
	if err != nil {
		t.Fatalf("StreamTranscribe: %v", err)
	}
	if final != "hello world" {
		t.Errorf("final = %q, want 'hello world'", final)
	}
	if len(partials) == 0 || partials[0] != "hello" {
		t.Errorf("expected an interim 'hello' partial, got %v", partials)
	}
}

func TestDeepgramSampleRateDefault(t *testing.T) {
	tr, _ := NewDeepgramTranscriber(DeepgramConfig{APIKey: "k"})
	if RequiredSampleRate(tr) != DefaultSampleRate {
		t.Errorf("Deepgram sample rate = %d, want %d", RequiredSampleRate(tr), DefaultSampleRate)
	}
}

func TestDeepgramMissingKey(t *testing.T) {
	if _, err := NewDeepgramTranscriber(DeepgramConfig{}); err == nil {
		t.Fatal("expected setup error for missing key")
	}
}

func TestOpenAIRealtimeStreamTranscribe(t *testing.T) {
	url := wsTestServer(t, func(ctx context.Context, c *websocket.Conn) {
		defer c.Close(websocket.StatusNormalClosure, "")
		committed := false
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			if typ != websocket.MessageText {
				continue
			}
			var msg struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(data, &msg)
			switch msg.Type {
			case "transcription_session.update":
				// Emit incremental deltas as if audio were flowing.
				_ = c.Write(ctx, websocket.MessageText, []byte(`{"type":"conversation.item.input_audio_transcription.delta","delta":"the "}`))
				_ = c.Write(ctx, websocket.MessageText, []byte(`{"type":"conversation.item.input_audio_transcription.delta","delta":"answer"}`))
			case "input_audio_buffer.commit":
				committed = true
				_ = c.Write(ctx, websocket.MessageText, []byte(`{"type":"input_audio_buffer.committed"}`))
				_ = c.Write(ctx, websocket.MessageText, []byte(`{"type":"conversation.item.input_audio_transcription.completed","transcript":"the answer"}`))
			}
			_ = committed
		}
	})

	tr, err := NewOpenAIRealtimeTranscriber(OpenAIRealtimeConfig{APIKey: "sk-key", BaseURL: url})
	if err != nil {
		t.Fatal(err)
	}
	if RequiredSampleRate(tr) != openAIRealtimeSampleRate {
		t.Errorf("OpenAI Realtime sample rate = %d, want %d", RequiredSampleRate(tr), openAIRealtimeSampleRate)
	}

	chunks := make(chan []byte, 1)
	chunks <- make([]byte, 480)
	close(chunks)
	done := make(chan struct{})
	var final string
	var ferr error
	go func() {
		final, ferr = tr.StreamTranscribe(context.Background(), chunks, func(string, bool) {})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("StreamTranscribe did not complete")
	}
	if ferr != nil {
		t.Fatalf("StreamTranscribe: %v", ferr)
	}
	if final != "the answer" {
		t.Errorf("final = %q, want 'the answer'", final)
	}
}

// TestLocalStreamingAccumulatesSegments is the regression for the "text vanishes
// on stop" bug: the sherpa server reports text PER SEGMENT and resets it to "" when
// it endpoints, so the transcriber must accumulate across segments rather than let
// a new (empty) segment overwrite everything already recognized.
func TestLocalStreamingAccumulatesSegments(t *testing.T) {
	url := wsTestServer(t, func(ctx context.Context, c *websocket.Conn) {
		defer c.Close(websocket.StatusNormalClosure, "")
		go func() { // drain the client's audio + "Done"
			for {
				if _, _, err := c.Read(ctx); err != nil {
					return
				}
			}
		}()
		for _, m := range []string{
			`{"text":" hello","segment":0,"is_final":false}`,
			`{"text":" hello world","segment":0,"is_final":true}`,
			`{"text":"","segment":1,"is_final":false}`, // endpoint reset — must NOT wipe seg 0
			`{"text":" foo bar","segment":1,"is_final":true}`,
			`{"text":"","segment":2,"is_final":true}`, // trailing empty segment at stop
		} {
			_ = c.Write(ctx, websocket.MessageText, []byte(m))
		}
		_ = c.Write(ctx, websocket.MessageText, []byte("Done!"))
	})

	colon := strings.LastIndex(url, ":")
	port, err := strconv.Atoi(url[colon+1:])
	if err != nil {
		t.Fatalf("parsing test server port from %q: %v", url, err)
	}
	mgr := NewServerManager(ServerConfig{
		ModelPath:   streamingModelDir(t),
		Port:        port,
		starter:     func(commandSpec) (processHandle, io.ReadCloser, error) { return newFakeProcess(), nil, nil },
		healthCheck: func(context.Context, int) error { return nil },
		aliveCheck:  func(int) bool { return false },
	})
	tr := NewLocalStreamingTranscriber(mgr)

	chunks := make(chan []byte)
	close(chunks) // no audio; the writer immediately sends "Done"
	var partials []string
	text, err := tr.StreamTranscribe(context.Background(), chunks, func(s string, _ bool) {
		partials = append(partials, s)
	})
	if err != nil {
		t.Fatalf("StreamTranscribe: %v", err)
	}
	if text != "hello world foo bar" {
		t.Errorf("final text = %q, want %q", text, "hello world foo bar")
	}
	// Once text has appeared, no later partial may go empty — that would be the
	// segment-reset wiping the transcript, the exact bug this guards.
	seen := false
	for _, p := range partials {
		if p != "" {
			seen = true
		}
		if seen && p == "" {
			t.Errorf("transcript went empty after text appeared; partials=%v", partials)
		}
	}
}

func TestParseDeepgramResultIgnoresNonResults(t *testing.T) {
	if _, _, ok := parseDeepgramResult([]byte(`{"type":"Metadata"}`)); ok {
		t.Error("Metadata messages should be ignored")
	}
	tr, final, ok := parseDeepgramResult([]byte(`{"type":"Results","is_final":true,"channel":{"alternatives":[{"transcript":"hi"}]}}`))
	if !ok || tr != "hi" || !final {
		t.Errorf("parse = (%q,%v,%v)", tr, final, ok)
	}
}

func TestParseRealtimeEvent(t *testing.T) {
	if e := parseRealtimeEvent([]byte(`{"type":"conversation.item.input_audio_transcription.delta","delta":"x"}`)); e.kind != realtimeDelta || e.text != "x" {
		t.Errorf("delta parse: %+v", e)
	}
	if e := parseRealtimeEvent([]byte(`{"type":"error","error":{"message":"boom"}}`)); e.kind != realtimeError || e.text != "boom" {
		t.Errorf("error parse: %+v", e)
	}
}

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
