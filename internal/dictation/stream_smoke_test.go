package dictation

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLocalStreamingRealServer drives the full local streaming path against a
// REAL sherpa-onnx-online-websocket-server + streaming model: spawn the server,
// connect, feed a wav's PCM in 50ms chunks, and check the transcript. Opt-in:
//
//	ZERO_STT_STREAM_SERVER=/path/to/sherpa-onnx-online-websocket-server \
//	ZERO_STT_STREAM_MODEL=/path/to/streaming-model-dir \
//	ZERO_STT_STREAM_WAV=/path/to/16k-mono.wav \
//	go test ./internal/dictation/ -run RealServer -v -timeout 5m
func TestLocalStreamingRealServer(t *testing.T) {
	server := os.Getenv("ZERO_STT_STREAM_SERVER")
	model := os.Getenv("ZERO_STT_STREAM_MODEL")
	wav := os.Getenv("ZERO_STT_STREAM_WAV")
	if server == "" || model == "" || wav == "" {
		t.Skip("set ZERO_STT_STREAM_SERVER, ZERO_STT_STREAM_MODEL, ZERO_STT_STREAM_WAV to run the streaming smoke test")
	}
	raw, err := os.ReadFile(wav)
	if err != nil {
		t.Fatal(err)
	}
	pcm := stripWavHeader(raw)

	mgr := NewServerManager(ServerConfig{Binary: server, ModelPath: model, Port: 6100})
	defer mgr.Shutdown(context.Background())
	tr := NewLocalStreamingTranscriber(mgr)

	chunks := make(chan []byte)
	go func() {
		defer close(chunks)
		size := chunkSizeBytes(DefaultSampleRate) // 50ms
		for off := 0; off < len(pcm); off += size {
			end := off + size
			if end > len(pcm) {
				end = len(pcm)
			}
			chunks <- pcm[off:end]
			time.Sleep(10 * time.Millisecond) // pace it a little, like a mic
		}
	}()

	var partials int
	final, err := tr.StreamTranscribe(context.Background(), chunks, func(string, bool) { partials++ })
	if err != nil {
		t.Fatalf("StreamTranscribe: %v", err)
	}
	if final == "" {
		t.Fatal("streaming returned empty transcript")
	}
	t.Logf("streaming transcript (%d partials): %q", partials, final)
}

// stripWavHeader drops a standard 44-byte PCM WAV header, returning the raw
// int16 samples. Good enough for the canonical 16kHz/mono/16-bit test wavs.
func stripWavHeader(b []byte) []byte {
	if len(b) > 44 && string(b[:4]) == "RIFF" {
		return b[44:]
	}
	return b
}
