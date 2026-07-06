package dictation

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeProcess is a processHandle whose stdout is driven from an in-memory
// buffer, mirroring how internal/lsp/manager_test.go stubs its subprocess over
// in-memory pipes rather than spawning a real one.
type fakeProcess struct {
	stopped   bool
	killed    bool
	waitErr   error
	stopErr   error
	closeOnce sync.Once
	done      chan struct{}
}

func newFakeProcess() *fakeProcess { return &fakeProcess{done: make(chan struct{})} }

func (f *fakeProcess) StopGracefully() error {
	f.stopped = true
	f.closeOnce.Do(func() { close(f.done) })
	return f.stopErr
}
func (f *fakeProcess) Wait() error {
	<-f.done
	return f.waitErr
}
func (f *fakeProcess) Kill() error {
	f.killed = true
	f.closeOnce.Do(func() { close(f.done) })
	return nil
}

// captured records the last spec a starter was asked to launch, so tests can
// assert the exact discrete argv per platform.
type captured struct {
	spec commandSpec
}

// fileWritingStarter simulates a record-to-file batch tool: on Start it writes
// fixture bytes to the output path (last positional arg for arecord/sox, or the
// -y target for ffmpeg) and returns a process that "records" until stopped.
func fileWritingStarter(t *testing.T, cap *captured, fixture []byte) processStarter {
	t.Helper()
	return func(spec commandSpec) (processHandle, io.ReadCloser, error) {
		cap.spec = spec
		path := outputPath(spec)
		if path == "" {
			t.Fatalf("could not determine output path from argv: %v", spec.args)
		}
		if err := os.WriteFile(path, fixture, 0o600); err != nil {
			t.Fatalf("fixture write: %v", err)
		}
		return newFakeProcess(), nil, nil
	}
}

func outputPath(spec commandSpec) string {
	for i, a := range spec.args {
		if a == "-y" && i+1 < len(spec.args) {
			return spec.args[i+1]
		}
	}
	// arecord/sox: the path is the last arg (sox appends trim/silence effects
	// after it, so scan from the front for the first existing-dir target).
	last := spec.args[len(spec.args)-1]
	if filepath.IsAbs(last) {
		return last
	}
	for _, a := range spec.args {
		if filepath.IsAbs(a) && filepath.Ext(a) != "" {
			return a
		}
	}
	return ""
}

func wavFixture() []byte {
	// Minimal valid RIFF/WAVE header + a little data, enough for SniffFormat.
	var b bytes.Buffer
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(36))
	b.WriteString("WAVE")
	b.WriteString("fmt ")
	binary.Write(&b, binary.LittleEndian, uint32(16))
	b.Write(make([]byte, 16))
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, uint32(4))
	b.Write([]byte{1, 2, 3, 4})
	return b.Bytes()
}

func testTempDir(t *testing.T) func() (string, error) {
	dir := t.TempDir()
	return func() (string, error) { return dir, nil }
}

func TestBatchRecordLinuxArgv(t *testing.T) {
	cap := &captured{}
	fixture := wavFixture()
	r := NewRecorder(RecorderOptions{
		Platform: PlatformLinux,
		starter:  fileWritingStarter(t, cap, fixture),
		tempDir:  testTempDir(t),
	})
	if err := r.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	got, err := r.Stop()
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !bytes.Equal(got, fixture) {
		t.Fatalf("audio mismatch: got %d bytes", len(got))
	}
	if cap.spec.name != "arecord" {
		t.Errorf("tool = %q, want arecord", cap.spec.name)
	}
	assertArgs(t, cap.spec.args, "-f", "S16_LE")
	assertArgs(t, cap.spec.args, "-r", "16000")
	assertArgs(t, cap.spec.args, "-c", "1")
}

func TestBatchRecordDarwinArgv(t *testing.T) {
	cap := &captured{}
	r := NewRecorder(RecorderOptions{
		Platform:        PlatformDarwin,
		SilenceAutoStop: true,
		starter:         fileWritingStarter(t, cap, wavFixture()),
		tempDir:         testTempDir(t),
	})
	if err := r.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := r.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if cap.spec.name != "sox" {
		t.Errorf("tool = %q, want sox", cap.spec.name)
	}
	if !containsSubseq(cap.spec.args, []string{"silence", "1", "0.1", "3%", "1", "2.0", "3%"}) {
		t.Errorf("expected SoX silence auto-stop effect in argv: %v", cap.spec.args)
	}
}

func TestBatchRecordWindowsArgv(t *testing.T) {
	cap := &captured{}
	r := NewRecorder(RecorderOptions{
		Platform:           PlatformWindows,
		WindowsAudioDevice: "Microphone (Realtek)",
		starter:            fileWritingStarter(t, cap, wavFixture()),
		tempDir:            testTempDir(t),
	})
	if err := r.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := r.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if cap.spec.name != "ffmpeg" {
		t.Errorf("tool = %q, want ffmpeg", cap.spec.name)
	}
	assertArgs(t, cap.spec.args, "-i", "audio=Microphone (Realtek)")
	if cap.spec.stopViaStdin != "q" {
		t.Errorf("ffmpeg should stop via stdin 'q', got %q", cap.spec.stopViaStdin)
	}
}

func TestBatchRecordTermuxUsesCommandRunner(t *testing.T) {
	dir := t.TempDir()
	var calls [][]string
	runOutput := func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{name}, args...))
		if len(args) > 0 && args[0] == "-l" {
			// The -f target is the last arg; write the fixture there.
			path := args[len(args)-1]
			_ = os.WriteFile(path, m4aFixture(), 0o600)
		}
		return nil, nil
	}
	r := NewRecorder(RecorderOptions{
		Platform: PlatformTermux,
		starter: func(commandSpec) (processHandle, io.ReadCloser, error) {
			t.Fatal("termux must not use the process starter")
			return nil, nil, nil
		},
		runOutput: runOutput,
		tempDir:   func() (string, error) { return dir, nil },
	})
	if err := r.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	got, err := r.Stop()
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if SniffFormat(got) != FormatM4A {
		t.Errorf("expected m4a audio, got format %q", SniffFormat(got))
	}
	if len(calls) != 2 || calls[0][0] != "termux-microphone-record" || calls[1][len(calls[1])-1] != "-q" {
		t.Errorf("unexpected termux calls: %v", calls)
	}
}

func TestStartMissingBinaryReturnsSetupError(t *testing.T) {
	r := NewRecorder(RecorderOptions{
		Platform: PlatformLinux,
		starter: func(commandSpec) (processHandle, io.ReadCloser, error) {
			return nil, nil, exec.ErrNotFound
		},
		tempDir: testTempDir(t),
	})
	err := r.Start()
	var setupErr *SetupError
	if !errors.As(err, &setupErr) {
		t.Fatalf("want *SetupError for missing binary, got %v", err)
	}
	if setupErr.Tool != "arecord" {
		t.Errorf("SetupError.Tool = %q, want arecord", setupErr.Tool)
	}
}

func TestDoubleStartRejected(t *testing.T) {
	r := NewRecorder(RecorderOptions{
		Platform: PlatformLinux,
		starter:  fileWritingStarter(t, &captured{}, wavFixture()),
		tempDir:  testTempDir(t),
	})
	if err := r.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := r.Start(); !errors.Is(err, errAlreadyRecording) {
		t.Errorf("second Start should be rejected, got %v", err)
	}
}

func TestStreamingChunksAndStop(t *testing.T) {
	// 300ms of PCM16 @16kHz = 6 chunks of 50ms. Feed a fixed buffer, then EOF.
	pcm := make([]byte, chunkSizeBytes(16000)*6)
	starter := func(spec commandSpec) (processHandle, io.ReadCloser, error) {
		if !spec.wantStdout {
			t.Fatal("streaming spec must request stdout")
		}
		return newFakeProcess(), io.NopCloser(bytes.NewReader(pcm)), nil
	}
	r := NewRecorder(RecorderOptions{
		Platform: PlatformLinux,
		starter:  starter,
		tempDir:  testTempDir(t),
	})
	chunks, stop, err := r.StartStreaming()
	if err != nil {
		t.Fatalf("StartStreaming: %v", err)
	}
	var total int
	for c := range chunks {
		total += len(c)
	}
	_ = stop()
	if total != len(pcm) {
		t.Errorf("streamed %d bytes, want %d", total, len(pcm))
	}
}

func TestStreamingStopIdempotent(t *testing.T) {
	// A blocking reader so the capture goroutine parks until we stop it.
	pr, pw := io.Pipe()
	defer pw.Close()
	starter := func(commandSpec) (processHandle, io.ReadCloser, error) {
		return newFakeProcess(), pr, nil
	}
	r := NewRecorder(RecorderOptions{Platform: PlatformLinux, starter: starter, tempDir: testTempDir(t)})
	chunks, stop, err := r.StartStreaming()
	if err != nil {
		t.Fatalf("StartStreaming: %v", err)
	}
	if err := stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := stop(); err != nil {
		t.Fatalf("second stop should be a no-op, got %v", err)
	}
	// Draining the channel must terminate now that we've stopped.
	drained := make(chan struct{})
	go func() {
		for range chunks {
		}
		close(drained)
	}()
	select {
	case <-drained:
	case <-time.After(2 * time.Second):
		t.Fatal("chunks channel did not close after stop")
	}
}

func TestBatchMaxDurationCapInArgv(t *testing.T) {
	cap := &captured{}
	r := NewRecorder(RecorderOptions{
		Platform:    PlatformLinux,
		MaxDuration: 90 * time.Second,
		starter:     fileWritingStarter(t, cap, wavFixture()),
		tempDir:     testTempDir(t),
	})
	if err := r.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := r.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// arecord's -d bounds a runaway recording even if Stop is never called.
	assertArgs(t, cap.spec.args, "-d", "90")
}

func TestRecordedTempFileDeletedAfterStop(t *testing.T) {
	dir := t.TempDir()
	cap := &captured{}
	r := NewRecorder(RecorderOptions{
		Platform: PlatformLinux,
		starter:  fileWritingStarter(t, cap, wavFixture()),
		tempDir:  func() (string, error) { return dir, nil },
	})
	if err := r.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := r.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("recorded audio should be deleted after Stop; found %d files", len(entries))
	}
}

func TestStreamingSilenceAutoStop(t *testing.T) {
	// Speech (loud) then a run of silence longer than silenceHold must auto-stop.
	rate := 16000
	chunk := chunkSizeBytes(rate)
	loud := make([]byte, chunk)
	for i := 0; i+1 < len(loud); i += 2 {
		loud[i], loud[i+1] = 0x00, 0x40 // +16384, well above threshold
	}
	silence := make([]byte, chunk) // zeros
	var buf []byte
	buf = append(buf, loud...)
	silentChunks := int(silenceHold/chunkInterval) + 3
	for i := 0; i < silentChunks; i++ {
		buf = append(buf, silence...)
	}
	starter := func(commandSpec) (processHandle, io.ReadCloser, error) {
		return newFakeProcess(), io.NopCloser(bytes.NewReader(buf)), nil
	}
	r := NewRecorder(RecorderOptions{
		Platform:        PlatformLinux,
		SampleRate:      rate,
		SilenceAutoStop: true,
		starter:         starter,
		tempDir:         testTempDir(t),
	})
	chunks, stop, err := r.StartStreaming()
	if err != nil {
		t.Fatalf("StartStreaming: %v", err)
	}
	defer stop()
	// Draining must terminate on its own once silence auto-stop fires.
	drained := make(chan struct{})
	go func() {
		for range chunks {
		}
		close(drained)
	}()
	select {
	case <-drained:
	case <-time.After(3 * time.Second):
		t.Fatal("silence auto-stop did not end the stream")
	}
}

func TestChunkPeak(t *testing.T) {
	loud := []byte{0x00, 0x40} // +16384
	if got := chunkPeak(loud); got != 16384 {
		t.Errorf("peak = %d, want 16384", got)
	}
	quiet := []byte{0x10, 0x00} // +16
	if got := chunkPeak(quiet); got != 16 {
		t.Errorf("peak = %d, want 16", got)
	}
	min := []byte{0x00, 0x80} // -32768 -> clamped to 32767
	if got := chunkPeak(min); got != 32767 {
		t.Errorf("peak = %d, want 32767 (clamp)", got)
	}
}

func TestDetectWindowsAudioDevice(t *testing.T) {
	listing := `[dshow @ 0000] DirectShow audio devices
[dshow @ 0000]  "Microphone (USB Audio)" (audio)
[dshow @ 0000]     Alternative name "@device_cm_..."`
	runOutput := func(context.Context, string, ...string) ([]byte, error) { return []byte(listing), nil }
	got, err := detectWindowsAudioDevice(runOutput)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if got != "Microphone (USB Audio)" {
		t.Errorf("device = %q", got)
	}
}

func TestSniffFormat(t *testing.T) {
	if SniffFormat(wavFixture()) != FormatWAV {
		t.Error("wav not detected")
	}
	if SniffFormat(m4aFixture()) != FormatM4A {
		t.Error("m4a not detected")
	}
	if SniffFormat([]byte("nope")) != FormatUnknown {
		t.Error("unknown not detected")
	}
}

func m4aFixture() []byte {
	b := make([]byte, 16)
	copy(b[4:], []byte("ftyp"))
	copy(b[8:], []byte("M4A "))
	return b
}

func assertArgs(t *testing.T, args []string, want ...string) {
	t.Helper()
	if !containsSubseq(args, want) {
		t.Errorf("argv %v missing subsequence %v", args, want)
	}
}

func containsSubseq(hay, needle []string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(hay); i++ {
		if equalSlice(hay[i:i+len(needle)], needle) {
			return true
		}
	}
	return false
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestChunkLevel(t *testing.T) {
	// Silence → ~0.
	silence := make([]byte, 640)
	if lvl := ChunkLevel(silence); lvl > 0.01 {
		t.Errorf("silence level = %f, want ~0", lvl)
	}
	// A loud full-scale tone → clamped to 1.
	loud := make([]byte, 640)
	for i := 0; i+1 < len(loud); i += 2 {
		loud[i], loud[i+1] = 0xFF, 0x7F // +32767
	}
	if lvl := ChunkLevel(loud); lvl < 0.99 {
		t.Errorf("full-scale level = %f, want ~1", lvl)
	}
	// A normal speech-level signal (~-30 dBFS) should land mid-meter, not pinned
	// to the bottom (the whole point of the dB mapping).
	mid := make([]byte, 640)
	for i := 0; i+1 < len(mid); i += 2 {
		mid[i], mid[i+1] = byte(1000&0xFF), byte(1000>>8) // ≈ -30 dBFS
	}
	if lvl := ChunkLevel(mid); lvl < 0.4 || lvl > 0.75 {
		t.Errorf("normal-speech level = %f, want a visible mid-range value", lvl)
	}
	// A quiet signal (~-45 dBFS) should be low but non-zero.
	quiet := make([]byte, 640)
	for i := 0; i+1 < len(quiet); i += 2 {
		quiet[i], quiet[i+1] = byte(180&0xFF), byte(180>>8)
	}
	if lvl := ChunkLevel(quiet); lvl <= 0 || lvl > 0.35 {
		t.Errorf("quiet level = %f, want a small non-zero value", lvl)
	}
}
