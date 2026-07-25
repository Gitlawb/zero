package dictation

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeModelDir(t *testing.T, files ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestLocalTranscribeMoonshineArgs(t *testing.T) {
	dir := writeModelDir(t,
		"preprocess.onnx", "encode.onnx", "uncached_decode.onnx", "cached_decode.onnx", "tokens.txt")
	var gotBin string
	var gotArgs []string
	runOutput := func(_ context.Context, name string, args ...string) ([]byte, error) {
		gotBin, gotArgs = name, args
		return []byte("Started\nDone!\n\n/tmp/a.wav\n{\"text\":\" transcribed speech \"}\n----"), nil
	}
	tr, err := NewLocalTranscriber(LocalConfig{ModelPath: dir, runOutput: runOutput, tempDir: func() (string, error) { return t.TempDir(), nil }})
	if err != nil {
		t.Fatalf("NewLocalTranscriber: %v", err)
	}
	text, err := tr.Transcribe(context.Background(), wavFixture())
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if text != "transcribed speech" {
		t.Errorf("text = %q", text)
	}
	if gotBin != "sherpa-onnx-offline" {
		t.Errorf("binary = %q", gotBin)
	}
	joined := strings.Join(gotArgs, " ")
	for _, want := range []string{"--moonshine-preprocessor=", "--moonshine-encoder=", "--moonshine-uncached-decoder=", "--moonshine-cached-decoder=", "--tokens="} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing flag %q in %q", want, joined)
		}
	}
}

func TestLocalTranscribeTransducerArgs(t *testing.T) {
	dir := writeModelDir(t, "encoder-epoch-99.onnx", "decoder-epoch-99.onnx", "joiner-epoch-99.onnx", "tokens.txt")
	var gotArgs []string
	runOutput := func(_ context.Context, name string, args ...string) ([]byte, error) {
		gotArgs = args
		return []byte(`{"text":"hi"}`), nil
	}
	tr, _ := NewLocalTranscriber(LocalConfig{ModelPath: dir, runOutput: runOutput, tempDir: func() (string, error) { return t.TempDir(), nil }})
	if _, err := tr.Transcribe(context.Background(), wavFixture()); err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	joined := strings.Join(gotArgs, " ")
	for _, want := range []string{"--encoder=", "--decoder=", "--joiner=", "--tokens="} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing flag %q in %q", want, joined)
		}
	}
}

func TestDetectWhisperArgs(t *testing.T) {
	// Sherpa Whisper models are named "<model>-encoder.onnx" (NOT "whisper-encoder")
	// and prefix the tokens file as "<model>-tokens.txt".
	dir := writeModelDir(t, "tiny.en-encoder.onnx", "tiny.en-encoder.int8.onnx",
		"tiny.en-decoder.onnx", "tiny.en-decoder.int8.onnx", "tiny.en-tokens.txt")
	args, err := detectModelArgs(dir)
	if err != nil {
		t.Fatalf("Whisper detection failed: %v", err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--whisper-encoder=") || !strings.Contains(joined, "--whisper-decoder=") {
		t.Errorf("expected whisper flags, got %q", joined)
	}
	// Must not emit transducer flags (Whisper has no joiner).
	if strings.Contains(joined, "--joiner=") || strings.Contains(joined, "--encoder=") {
		t.Errorf("Whisper must not be detected as a transducer: %q", joined)
	}
	// Consistent quantization: both int8 (preferred) or both fp32, never mixed.
	if strings.Contains(joined, "encoder.int8.onnx") != strings.Contains(joined, "decoder.int8.onnx") {
		t.Errorf("mixed encoder/decoder quantization: %q", joined)
	}
}

func TestDetectTransducerPrefersConsistentInt8(t *testing.T) {
	dir := writeModelDir(t,
		"encoder-epoch-99.onnx", "encoder-epoch-99.int8.onnx",
		"decoder-epoch-99.onnx", "decoder-epoch-99.int8.onnx",
		"joiner-epoch-99.onnx", "joiner-epoch-99.int8.onnx", "tokens.txt")
	args, err := detectModelArgs(dir)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	// All three int8 (preferred), consistently.
	for _, want := range []string{"--encoder=", "encoder-epoch-99.int8.onnx", "joiner-epoch-99.int8.onnx"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %q", want, joined)
		}
	}
}

func TestDetectTransducerMixedQuantization(t *testing.T) {
	// Some models ship an int8 encoder+joiner with an fp32 decoder (e.g. the
	// x-asr streaming zipformers). Detection must accept that mix, not reject it.
	dir := writeModelDir(t, "encoder.int8.onnx", "decoder.onnx", "joiner.int8.onnx", "tokens.txt")
	args, err := detectModelArgs(dir)
	if err != nil {
		t.Fatalf("mixed-quantization transducer should be detected, got %v", err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "encoder.int8.onnx") || !strings.Contains(joined, "decoder.onnx") || !strings.Contains(joined, "joiner.int8.onnx") {
		t.Errorf("expected the actual mixed files, got %q", joined)
	}
	// The fp32 decoder must not be mistaken for an int8 one.
	if strings.Contains(joined, "decoder.int8") {
		t.Errorf("no int8 decoder exists; should use decoder.onnx: %q", joined)
	}
}

func TestDetectSenseVoiceByDirName(t *testing.T) {
	dir := t.TempDir() // rename won't help; use a dir whose base contains sense-voice
	sv := dir + "/sherpa-onnx-sense-voice-zh-en"
	if err := os.MkdirAll(sv, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"model.int8.onnx", "tokens.txt"} {
		os.WriteFile(sv+"/"+f, []byte("x"), 0o600)
	}
	args, err := detectModelArgs(sv)
	if err != nil {
		t.Fatalf("SenseVoice detection failed: %v", err)
	}
	if !strings.Contains(strings.Join(args, " "), "--sense-voice-model=") {
		t.Errorf("expected sense-voice flag, got %v", args)
	}
}

// writeNamedModelDir creates a model directory whose base name carries the family
// signature (how sherpa-onnx distributes single-file / CTC models), with the given
// files, and returns its path.
func writeNamedModelDir(t *testing.T, base string, files ...string) string {
	t.Helper()
	dir := t.TempDir() + "/" + base
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if err := os.WriteFile(dir+"/"+f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestDetectSingleFileCTCFamilies(t *testing.T) {
	// One generic model.onnx + tokens, family told apart by the directory name.
	cases := []struct{ base, wantFlag string }{
		{"sherpa-onnx-fire-red-asr2-ctc-zh_en-int8-2026-02-25", "--fire-red-asr-ctc="},
		{"sherpa-onnx-nemo-ctc-en-conformer-medium", "--nemo-ctc-model="},
		{"sherpa-onnx-zipformer-ctc-zh-2024", "--zipformer-ctc-model="},
		{"sherpa-onnx-telespeech-ctc-int8", "--telespeech-ctc="},
		{"sherpa-onnx-wenet-ctc-en", "--wenet-ctc-model="},
		{"sherpa-onnx-dolphin-base-ctc-multi-lang", "--dolphin-model="},
		{"sherpa-onnx-omnilingual-asr-ctc", "--omnilingual-asr-model="},
		{"sherpa-onnx-tdnn-yesno", "--tdnn-model="},
	}
	for _, tc := range cases {
		dir := writeNamedModelDir(t, tc.base, "model.int8.onnx", "tokens.txt")
		args, err := detectModelArgs(dir)
		if err != nil {
			t.Errorf("%s: detection failed: %v", tc.base, err)
			continue
		}
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, tc.wantFlag) || !strings.Contains(joined, "--tokens=") {
			t.Errorf("%s: got %q, want %s", tc.base, joined, tc.wantFlag)
		}
	}
}

func TestDetectEncoderDecoderFamiliesBeforeWhisper(t *testing.T) {
	// FireRedASR-AED / Canary share Whisper's encoder+decoder shape, so they must be
	// disambiguated by name — not fall through to --whisper-encoder.
	cases := []struct{ base, wantFlag string }{
		{"sherpa-onnx-fire-red-asr-large-zh_en", "--fire-red-asr-encoder="},
		{"sherpa-onnx-canary-180m-flash", "--canary-encoder="},
	}
	for _, tc := range cases {
		dir := writeNamedModelDir(t, tc.base, "encoder.int8.onnx", "decoder.int8.onnx", "tokens.txt")
		args, err := detectModelArgs(dir)
		if err != nil {
			t.Errorf("%s: detection failed: %v", tc.base, err)
			continue
		}
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, tc.wantFlag) {
			t.Errorf("%s: got %q, want %s", tc.base, joined, tc.wantFlag)
		}
		if strings.Contains(joined, "--whisper-") {
			t.Errorf("%s must not be detected as Whisper: %q", tc.base, joined)
		}
	}
}

func TestDetectUnknownFamilyFailsCleanly(t *testing.T) {
	// A model with tokens but no recognizable family → a clear SetupError, not a
	// crash or wrong flags.
	dir := writeModelDir(t, "some-random-model.onnx", "tokens.txt")
	_, err := detectModelArgs(dir)
	var setupErr *SetupError
	if !errors.As(err, &setupErr) {
		t.Fatalf("want a clean *SetupError, got %v", err)
	}
	if !strings.Contains(setupErr.Error(), "could not recognize") {
		t.Errorf("expected a recognizable failure message, got %q", setupErr.Error())
	}
}

func TestLocalTranscribeMissingTokens(t *testing.T) {
	dir := writeModelDir(t, "encoder.onnx", "decoder.onnx", "joiner.onnx") // no tokens.txt
	tr, _ := NewLocalTranscriber(LocalConfig{ModelPath: dir, runOutput: func(context.Context, string, ...string) ([]byte, error) { return nil, nil }, tempDir: func() (string, error) { return t.TempDir(), nil }})
	_, err := tr.Transcribe(context.Background(), wavFixture())
	var setupErr *SetupError
	if !errors.As(err, &setupErr) {
		t.Fatalf("want *SetupError for missing tokens, got %v", err)
	}
}

func TestLocalTranscribeMissingBinary(t *testing.T) {
	dir := writeModelDir(t, "preprocess.onnx", "encode.onnx", "uncached_decode.onnx", "cached_decode.onnx", "tokens.txt")
	runOutput := func(context.Context, string, ...string) ([]byte, error) { return nil, exec.ErrNotFound }
	tr, _ := NewLocalTranscriber(LocalConfig{ModelPath: dir, runOutput: runOutput, tempDir: func() (string, error) { return t.TempDir(), nil }})
	_, err := tr.Transcribe(context.Background(), wavFixture())
	var setupErr *SetupError
	if !errors.As(err, &setupErr) {
		t.Fatalf("want *SetupError for missing binary, got %v", err)
	}
}

func TestParseSherpaOutput(t *testing.T) {
	out := []byte("Started\nDone!\n\naudio.wav\n{\"lang\":\"\",\"text\":\" the quick brown fox \",\"tokens\":[]}\n----\nnum threads: 2")
	if got := parseSherpaOutput(out); got != "the quick brown fox" {
		t.Errorf("parsed %q", got)
	}
	if got := parseSherpaOutput([]byte("no json here")); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestNewLocalTranscriberRequiresModelPath(t *testing.T) {
	_, err := NewLocalTranscriber(LocalConfig{})
	var setupErr *SetupError
	if !errors.As(err, &setupErr) {
		t.Fatalf("want *SetupError, got %v", err)
	}
}
