package dictation

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestEnsureLocalEngineRealDownload exercises the full auto-download against the
// REAL k2-fsa/sherpa-onnx GitHub API (resolve → verify digest → extract → run).
// It downloads ~130MB, so it is opt-in via ZERO_STT_DOWNLOAD_TEST=1.
//
//	ZERO_STT_DOWNLOAD_TEST=1 go test ./internal/dictation/ -run RealDownload -v -timeout 10m
func TestEnsureLocalEngineRealDownload(t *testing.T) {
	if os.Getenv("ZERO_STT_DOWNLOAD_TEST") == "" {
		t.Skip("set ZERO_STT_DOWNLOAD_TEST=1 to run the real ~130MB download smoke test")
	}
	dest := t.TempDir()
	comp, err := EnsureLocalEngine(context.Background(), DownloadOptions{
		DestRoot: dest,
		Progress: func(s string) { t.Logf("stage: %s", s) },
	})
	if err != nil {
		t.Fatalf("EnsureLocalEngine: %v", err)
	}
	if !fileExists(comp.BinaryPath) || !fileExists(filepath.Join(comp.ModelPath, "tokens.txt")) {
		t.Fatalf("missing components: %+v", comp)
	}

	// The downloaded engine + model must actually transcribe.
	wav := filepath.Join(comp.ModelPath, "test_wavs", "0.wav")
	if !fileExists(wav) {
		t.Skipf("no bundled test wav at %s", wav)
	}
	audio, err := os.ReadFile(wav)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := NewLocalTranscriber(LocalConfig{Binary: comp.BinaryPath, ModelPath: comp.ModelPath})
	if err != nil {
		t.Fatal(err)
	}
	text, err := tr.Transcribe(context.Background(), audio)
	if err != nil {
		t.Fatalf("Transcribe with downloaded engine: %v", err)
	}
	if text == "" {
		t.Fatal("downloaded engine returned empty transcript")
	}
	t.Logf("downloaded-engine transcript: %q", text)
}
