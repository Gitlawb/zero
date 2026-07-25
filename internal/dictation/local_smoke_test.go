package dictation

import (
	"context"
	"os"
	"testing"
)

// TestLocalTranscribeRealBinary exercises the full local batch path against a
// REAL sherpa-onnx-offline binary and model — the manual smoke test §14 calls
// out as not CI-able. Skipped unless both env vars point at a real install:
//
//	ZERO_STT_TEST_BINARY=/path/to/sherpa-onnx-offline \
//	ZERO_STT_TEST_MODEL=/path/to/model-dir \
//	ZERO_STT_TEST_WAV=/path/to/16k-mono.wav \
//	go test ./internal/dictation/ -run TestLocalTranscribeRealBinary -v
func TestLocalTranscribeRealBinary(t *testing.T) {
	binary := os.Getenv("ZERO_STT_TEST_BINARY")
	model := os.Getenv("ZERO_STT_TEST_MODEL")
	wav := os.Getenv("ZERO_STT_TEST_WAV")
	if binary == "" || model == "" || wav == "" {
		t.Skip("set ZERO_STT_TEST_BINARY, ZERO_STT_TEST_MODEL, ZERO_STT_TEST_WAV to run the real-engine smoke test")
	}
	audio, err := os.ReadFile(wav)
	if err != nil {
		t.Fatalf("read wav: %v", err)
	}
	tr, err := NewLocalTranscriber(LocalConfig{Binary: binary, ModelPath: model})
	if err != nil {
		t.Fatalf("NewLocalTranscriber: %v", err)
	}
	text, err := tr.Transcribe(context.Background(), audio)
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if text == "" {
		t.Fatal("real engine returned an empty transcript")
	}
	t.Logf("transcript: %q", text)
}
