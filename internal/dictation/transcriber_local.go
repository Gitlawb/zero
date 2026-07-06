package dictation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// LocalConfig configures the offline sherpa-onnx batch transcriber. Every tier
// (default/standard/high-end, §7) is just a different ModelPath — there are no
// tier-specific code paths; the model family is detected from the files present.
type LocalConfig struct {
	// Binary is the sherpa-onnx-offline executable; looked up on PATH when a
	// bare name. Empty defaults to "sherpa-onnx-offline".
	Binary string
	// ModelPath is the directory holding the model's .onnx + tokens.txt files
	// (stt.localModelPath).
	ModelPath  string
	NumThreads int

	// Test seams.
	runOutput commandOutputRunner
	tempDir   func() (string, error)
}

type localTranscriber struct {
	cfg LocalConfig
}

// NewLocalTranscriber builds the offline transcriber. It does not touch the
// filesystem or PATH until Transcribe runs, so a missing binary/model degrades
// to a clear setup message at use time (matching internal/lsp).
func NewLocalTranscriber(cfg LocalConfig) (Transcriber, error) {
	if strings.TrimSpace(cfg.ModelPath) == "" {
		return nil, &SetupError{
			Tool: "local STT model",
			Hint: "set stt.localModelPath to a sherpa-onnx model directory (see docs/dictation.md)",
		}
	}
	if cfg.Binary == "" {
		cfg.Binary = "sherpa-onnx-offline"
	}
	if cfg.runOutput == nil {
		cfg.runOutput = runCommandOutput
	}
	if cfg.tempDir == nil {
		cfg.tempDir = audioSpillDir
	}
	return &localTranscriber{cfg: cfg}, nil
}

func (l *localTranscriber) Transcribe(ctx context.Context, audio []byte) (string, error) {
	if len(audio) == 0 {
		return "", errors.New("no audio to transcribe")
	}
	modelArgs, err := detectModelArgs(l.cfg.ModelPath)
	if err != nil {
		return "", err
	}

	dir, err := l.cfg.tempDir()
	if err != nil {
		return "", fmt.Errorf("preparing dictation temp dir: %w", err)
	}
	ext := ".wav"
	if SniffFormat(audio) == FormatM4A {
		ext = ".m4a"
	}
	tmp, err := os.CreateTemp(dir, "stt-*"+ext)
	if err != nil {
		return "", fmt.Errorf("staging audio for transcription: %w", err)
	}
	path := tmp.Name()
	defer os.Remove(path) // audio never lingers on disk (§12)
	if _, err := tmp.Write(audio); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}

	args := make([]string, 0, len(modelArgs)+3)
	args = append(args, modelArgs...)
	if l.cfg.NumThreads > 0 {
		args = append(args, "--num-threads="+strconv.Itoa(l.cfg.NumThreads))
	}
	args = append(args, path)

	// Fail fast if already cancelled; ctx also propagates into the exec below so a
	// wedged sherpa-onnx-offline is killed on cancel/timeout rather than hanging.
	if err := ctx.Err(); err != nil {
		return "", err
	}
	out, err := l.cfg.runOutput(ctx, l.cfg.Binary, args...)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", &SetupError{
				Tool: l.cfg.Binary,
				Hint: "install sherpa-onnx and put its offline binary on PATH (see docs/dictation.md)",
			}
		}
		return "", fmt.Errorf("sherpa-onnx transcription failed: %w (%s)", err, lastLines(out, 3))
	}
	text := parseSherpaOutput(out)
	if text == "" {
		return "", fmt.Errorf("sherpa-onnx produced no transcript (output: %s)", lastLines(out, 3))
	}
	return text, nil
}

// detectModelArgs inspects a sherpa-onnx model directory and builds the
// family-specific --*=path flags. Supports the families sherpa-onnx-offline
// accepts; Moonshine (the design's default/standard/high-end tiers) is the
// primary target. tokens.txt is required by every family.
func detectModelArgs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, &SetupError{
			Tool: "local STT model",
			Hint: fmt.Sprintf("stt.localModelPath %q is not readable: %v", dir, err),
		}
	}
	files := map[string]string{} // lowercased basename -> absolute path
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		files[strings.ToLower(e.Name())] = filepath.Join(dir, e.Name())
	}

	tokens := findTokensFile(files)
	if tokens == "" {
		return nil, &SetupError{
			Tool: "local STT model",
			Hint: fmt.Sprintf("no tokens.txt found in stt.localModelPath %q — is it a sherpa-onnx model directory?", dir),
		}
	}

	dirName := strings.ToLower(filepath.Base(dir))

	// Order matters — check the most specific signatures first so a model is
	// never mis-classified as a family it only partially resembles.

	// Moonshine: preprocessor + encoder + cached/uncached decoders. Its files are
	// "encode"/"decode" (no trailing "r"), so they never collide with the
	// "encoder"/"decoder" families below.
	if pre := findContains(files, "preprocess"); pre != "" {
		enc := findContains(files, "encode")
		// "uncached_decode" CONTAINS "cached_decode", so match the cached decoder
		// as "cached" AND not "uncached" to disambiguate (map order is random).
		unc := findOnnxMatching(files, "uncached", "")
		cac := findOnnxMatching(files, "cached", "uncached")
		if enc != "" && unc != "" && cac != "" {
			return []string{
				"--moonshine-preprocessor=" + pre,
				"--moonshine-encoder=" + enc,
				"--moonshine-uncached-decoder=" + unc,
				"--moonshine-cached-decoder=" + cac,
				"--tokens=" + tokens,
			}, nil
		}
	}

	// Transducer (Zipformer/Conformer): encoder + decoder + joiner, matched as a
	// consistent int8-or-fp32 set (mixing quantizations is invalid).
	if set := consistentQuantizedSet(files, "encoder", "decoder", "joiner"); set != nil {
		return []string{"--encoder=" + set[0], "--decoder=" + set[1], "--joiner=" + set[2], "--tokens=" + tokens}, nil
	}

	// Encoder+decoder families that share Whisper's file shape (encoder + decoder,
	// no joiner) but need their own flags — FireRedASR-AED, Canary, Cohere. They are
	// indistinguishable from Whisper by files alone, so disambiguate by name FIRST;
	// anything unnamed falls through to the Whisper default below.
	for _, fam := range encoderDecoderFamilies {
		if nameMatchesFamily(dirName, files, fam.match) {
			if set := consistentQuantizedSet(files, "encoder", "decoder"); set != nil {
				return []string{fam.encoderFlag + "=" + set[0], fam.decoderFlag + "=" + set[1], "--tokens=" + tokens}, nil
			}
		}
	}

	// Whisper: encoder + decoder, NO joiner. Sherpa's Whisper files are named
	// "<model>-encoder.onnx" / "<model>-decoder.onnx" (NOT "whisper-encoder"), so
	// match on the encoder/decoder roles — a consistent pair — after transducer
	// (which owns the encoder/decoder/joiner shape) has already been ruled out.
	if set := consistentQuantizedSet(files, "encoder", "decoder"); set != nil {
		return []string{"--whisper-encoder=" + set[0], "--whisper-decoder=" + set[1], "--tokens=" + tokens}, nil
	}

	// Single-model families: one generic model .onnx + tokens, indistinguishable by
	// file shape, so they are told apart by the model directory/file name. This
	// covers SenseVoice, Paraformer, and the CTC families (FireRedASR-CTC, NeMo,
	// TeleSpeech, WeNet, Zipformer-CTC, Dolphin, Omnilingual). Ordered most-specific
	// first so a compound name (e.g. "fire-red-asr2-ctc") matches its own family
	// before a generic token in it (e.g. bare "ctc") could steer it elsewhere.
	if m := singleModelOnnx(files); m != "" {
		for _, fam := range singleModelFamilies {
			if nameMatchesFamily(dirName, files, fam.match) {
				return []string{fam.flag + "=" + m, "--tokens=" + tokens}, nil
			}
		}
	}

	return nil, &SetupError{
		Tool: "local STT model",
		Hint: fmt.Sprintf("could not recognize the model family in %q (supported: Moonshine, Whisper, transducer/Zipformer, Paraformer, SenseVoice, and the CTC families FireRedASR/NeMo/TeleSpeech/WeNet/Zipformer-CTC/Dolphin/Omnilingual). See docs/dictation.md", dir),
	}
}

// singleModelFamilies maps a model's directory/file-name signature to the
// sherpa-onnx-offline flag for that single-file family. Each family ships exactly
// one generic model .onnx (+ tokens), so only the name tells them apart. Ordered
// so a specific compound name wins before a broader one.
var singleModelFamilies = []struct {
	match []string // name substrings (any hit); most-specific spellings first
	flag  string
}{
	{[]string{"fire-red-asr2-ctc", "fire-red-asr-ctc", "fire-red-asr", "firered"}, "--fire-red-asr-ctc"},
	{[]string{"nemo-ctc", "nemo_ctc"}, "--nemo-ctc-model"},
	{[]string{"zipformer2-ctc", "zipformer-ctc"}, "--zipformer-ctc-model"},
	{[]string{"telespeech"}, "--telespeech-ctc"},
	{[]string{"wenet"}, "--wenet-ctc-model"},
	{[]string{"dolphin"}, "--dolphin-model"},
	{[]string{"omnilingual"}, "--omnilingual-asr-model"},
	{[]string{"medasr"}, "--medasr"},
	{[]string{"tdnn"}, "--tdnn-model"},
	{[]string{"sense-voice", "sensevoice"}, "--sense-voice-model"},
	{[]string{"paraformer"}, "--paraformer"},
}

// encoderDecoderFamilies are offline families shaped like Whisper (an encoder +
// decoder .onnx, no joiner) but needing their own flags; matched by name before
// the Whisper fallback claims the shape.
var encoderDecoderFamilies = []struct {
	match       []string
	encoderFlag string
	decoderFlag string
}{
	{[]string{"fire-red-asr"}, "--fire-red-asr-encoder", "--fire-red-asr-decoder"},
	{[]string{"canary"}, "--canary-encoder", "--canary-decoder"},
	{[]string{"cohere"}, "--cohere-transcribe-encoder", "--cohere-transcribe-decoder"},
}

// nameMatchesFamily reports whether the model directory name or any of its .onnx
// filenames contain one of the family's signature substrings.
func nameMatchesFamily(dirName string, files map[string]string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(dirName, sub) {
			return true
		}
		for name := range files {
			if strings.HasSuffix(name, ".onnx") && strings.Contains(name, sub) {
				return true
			}
		}
	}
	return false
}

// consistentQuantizedSet returns one file per role (in order), preferring an
// int8 build of each but falling back to fp32 when a role has no int8 file.
// Returns nil when any role is entirely missing.
//
// Models legitimately SHIP mixed quantization — e.g. the x-asr streaming
// zipformers ship an int8 encoder and joiner with an fp32 decoder — and
// sherpa-onnx accepts that. So this must NOT require every role to share one
// quantization; it just needs one file per role, favoring the smaller int8 when
// a choice exists. (When a model ships both int8 and fp32 for every role, this
// still picks all-int8, since int8 is available for each.)
func consistentQuantizedSet(files map[string]string, roles ...string) []string {
	pick := func(role string, int8Only bool) string {
		for name, path := range files {
			if !strings.HasSuffix(name, ".onnx") || !strings.Contains(name, role) {
				continue
			}
			if int8Only && !strings.Contains(name, "int8") {
				continue
			}
			return path
		}
		return ""
	}
	out := make([]string, len(roles))
	for i, role := range roles {
		p := pick(role, true) // prefer int8 (smaller, faster)
		if p == "" {
			p = pick(role, false) // fall back to fp32 for this role
		}
		if p == "" {
			return nil // this role is genuinely absent
		}
		out[i] = p
	}
	return out
}

// findTokensFile returns the model's tokens file. It is usually "tokens.txt",
// but some families (e.g. Whisper) prefix it as "<model>-tokens.txt".
func findTokensFile(files map[string]string) string {
	if p := files["tokens.txt"]; p != "" {
		return p
	}
	for name, path := range files {
		if strings.HasSuffix(name, "tokens.txt") {
			return path
		}
	}
	return ""
}

// singleModelOnnx returns the model's single .onnx file (preferring an int8
// build), for families like SenseVoice/Paraformer that ship exactly one.
func singleModelOnnx(files map[string]string) string {
	best := ""
	for name, path := range files {
		if !strings.HasSuffix(name, ".onnx") {
			continue
		}
		if strings.Contains(name, "int8") {
			return path
		}
		best = path
	}
	return best
}

// findOnnxMatching returns an .onnx file whose name contains `must` and, when
// `exclude` is non-empty, does NOT contain `exclude` — used to tell the cached
// from the uncached Moonshine decoder.
func findOnnxMatching(files map[string]string, must, exclude string) string {
	for name, path := range files {
		if !strings.HasSuffix(name, ".onnx") || !strings.Contains(name, must) {
			continue
		}
		if exclude != "" && strings.Contains(name, exclude) {
			continue
		}
		return path
	}
	return ""
}

func findContains(files map[string]string, substr string) string {
	for name, path := range files {
		if strings.HasSuffix(name, ".onnx") && strings.Contains(name, substr) {
			return path
		}
	}
	return ""
}

// parseSherpaOutput extracts the transcript from sherpa-onnx-offline's output.
// The binary prints a JSON object with a "text" field (amid diagnostic lines);
// scan for the first line that parses as such.
func parseSherpaOutput(out []byte) string {
	for _, line := range bytes.Split(out, []byte("\n")) {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 || trimmed[0] != '{' {
			continue
		}
		var parsed struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(trimmed, &parsed); err == nil && strings.TrimSpace(parsed.Text) != "" {
			return strings.TrimSpace(parsed.Text)
		}
	}
	return ""
}

func lastLines(out []byte, n int) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}

// StreamTranscribe is unsupported for the offline binary; local streaming uses
// the sherpa-onnx websocket server transcriber instead.
func (l *localTranscriber) StreamTranscribe(context.Context, <-chan []byte, func(string, bool)) (string, error) {
	return "", ErrStreamingUnsupported
}
