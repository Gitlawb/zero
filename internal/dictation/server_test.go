package dictation

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

func streamingModelDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range []string{"encoder.onnx", "decoder.onnx", "joiner.onnx", "tokens.txt"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestServerManagerLazyStartAndReuse(t *testing.T) {
	var startCount int
	var mu sync.Mutex
	starter := func(spec commandSpec) (processHandle, io.ReadCloser, error) {
		mu.Lock()
		startCount++
		mu.Unlock()
		if spec.name != "sherpa-onnx-online-websocket-server" {
			t.Errorf("binary = %q", spec.name)
		}
		return newFakeProcess(), nil, nil
	}
	alive := true
	m := NewServerManager(ServerConfig{
		ModelPath:   streamingModelDir(t),
		Port:        6006,
		starter:     starter,
		healthCheck: func(context.Context, int) error { return nil },
		aliveCheck:  func(int) bool { return alive },
	})

	url, err := m.EnsureRunning(context.Background())
	if err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}
	if url != "ws://127.0.0.1:6006" {
		t.Errorf("url = %q", url)
	}
	// Second call reuses the warm server (alive) — no new start.
	if _, err := m.EnsureRunning(context.Background()); err != nil {
		t.Fatalf("EnsureRunning reuse: %v", err)
	}
	if startCount != 1 {
		t.Errorf("expected 1 start (warm reuse), got %d", startCount)
	}
}

func TestServerManagerRestartsCrashedServer(t *testing.T) {
	var startCount int
	starter := func(commandSpec) (processHandle, io.ReadCloser, error) {
		startCount++
		return newFakeProcess(), nil, nil
	}
	alive := true
	m := NewServerManager(ServerConfig{
		ModelPath:   streamingModelDir(t),
		starter:     starter,
		healthCheck: func(context.Context, int) error { return nil },
		aliveCheck:  func(int) bool { return alive },
	})
	if _, err := m.EnsureRunning(context.Background()); err != nil {
		t.Fatal(err)
	}
	alive = false // simulate a crashed server
	if _, err := m.EnsureRunning(context.Background()); err != nil {
		t.Fatal(err)
	}
	if startCount != 2 {
		t.Errorf("expected a restart (2 starts), got %d", startCount)
	}
}

func TestServerManagerMissingBinaryIsSetupError(t *testing.T) {
	m := NewServerManager(ServerConfig{
		ModelPath: streamingModelDir(t),
		starter:   func(commandSpec) (processHandle, io.ReadCloser, error) { return nil, nil, exec.ErrNotFound },
	})
	_, err := m.EnsureRunning(context.Background())
	var setupErr *SetupError
	if !errors.As(err, &setupErr) {
		t.Fatalf("want *SetupError, got %v", err)
	}
}

func TestServerManagerMissingModelIsSetupError(t *testing.T) {
	m := NewServerManager(ServerConfig{
		ModelPath: t.TempDir(), // empty dir, no onnx/tokens
		starter:   func(commandSpec) (processHandle, io.ReadCloser, error) { return newFakeProcess(), nil, nil },
	})
	_, err := m.EnsureRunning(context.Background())
	var setupErr *SetupError
	if !errors.As(err, &setupErr) {
		t.Fatalf("want *SetupError for missing streaming model, got %v", err)
	}
}

func TestServerManagerShutdown(t *testing.T) {
	proc := newFakeProcess()
	m := NewServerManager(ServerConfig{
		ModelPath:   streamingModelDir(t),
		starter:     func(commandSpec) (processHandle, io.ReadCloser, error) { return proc, nil, nil },
		healthCheck: func(context.Context, int) error { return nil },
		aliveCheck:  func(int) bool { return true },
	})
	if _, err := m.EnsureRunning(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if !proc.stopped && !proc.killed {
		t.Error("Shutdown should stop the server process")
	}
	if m.URL() != "" {
		t.Error("URL should be empty after shutdown")
	}
}

func TestOnlineServerCommandBuildsTransducerArgs(t *testing.T) {
	spec, err := onlineServerCommand(ServerConfig{Binary: "sherpa-onnx-online-websocket-server", ModelPath: streamingModelDir(t), Port: 6007})
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, a := range spec.args {
		joined += a + " "
	}
	for _, want := range []string{"--port=6007", "--tokens=", "--encoder=", "--decoder=", "--joiner=", "--decoding-method=greedy_search"} {
		if !contains(joined, want) {
			t.Errorf("missing %q in args: %s", want, joined)
		}
	}
}

func TestPCM16ToFloat32LE(t *testing.T) {
	// int16 max (32767) and min (-32768), little-endian.
	pcm := []byte{0xFF, 0x7F, 0x00, 0x80}
	out := pcm16ToFloat32LE(pcm)
	if len(out) != 8 {
		t.Fatalf("expected 8 bytes (2 float32), got %d", len(out))
	}
	f0 := math.Float32frombits(binary.LittleEndian.Uint32(out[0:4]))
	f1 := math.Float32frombits(binary.LittleEndian.Uint32(out[4:8]))
	if f0 < 0.99 || f0 > 1.0 {
		t.Errorf("32767 → %f, want ~1.0", f0)
	}
	if f1 != -1.0 {
		t.Errorf("-32768 → %f, want -1.0", f1)
	}
}

func TestParseSherpaStreamResult(t *testing.T) {
	text, segment, final, ok := parseSherpaStreamResult([]byte(`{"text":" hello world ","segment":2,"is_final":true}`))
	if !ok || text != "hello world" || segment != 2 || !final {
		t.Errorf("parsed = (%q, %d, %v, %v)", text, segment, final, ok)
	}
	if _, _, _, ok := parseSherpaStreamResult([]byte("not json")); ok {
		t.Error("non-JSON should not parse")
	}
}

func contains(hay, needle string) bool {
	return len(needle) == 0 || (len(hay) >= len(needle) && indexOf(hay, needle) >= 0)
}

func indexOf(hay, needle string) int {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
