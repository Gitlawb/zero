package dictation

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultServerPort is the localhost port the sherpa-onnx streaming server binds.
const DefaultServerPort = 6006

// ServerConfig configures the warm sherpa-onnx streaming server (§6a).
type ServerConfig struct {
	// Binary is the streaming server executable (default
	// "sherpa-onnx-online-websocket-server"); looked up on PATH.
	Binary string
	// ModelPath is the sherpa-onnx streaming model directory (transducer:
	// encoder/decoder/joiner + tokens).
	ModelPath  string
	Port       int
	NumThreads int

	// Test seams.
	starter     processStarter
	healthCheck func(ctx context.Context, port int) error
	aliveCheck  func(port int) bool
	now         func() time.Time
}

// ServerManager owns one long-lived sherpa-onnx streaming server, spawned lazily
// on first streaming use and kept warm for the session — a websocket server with
// ~1-2s startup latency can't be respawned per utterance (§6a). Its lifecycle
// mirrors internal/lsp's Manager: lazy start, health-checked reuse, one restart
// on a crashed process, torn down on exit. Safe for concurrent use.
type ServerManager struct {
	cfg ServerConfig

	mu   sync.Mutex
	proc processHandle
	url  string
}

// NewServerManager builds a manager. Construction is side-effect free; the
// server is only spawned on the first EnsureRunning.
func NewServerManager(cfg ServerConfig) *ServerManager {
	if cfg.Binary == "" {
		cfg.Binary = "sherpa-onnx-online-websocket-server"
	}
	if cfg.Port == 0 {
		cfg.Port = DefaultServerPort
	}
	if cfg.starter == nil {
		cfg.starter = startProcess
	}
	if cfg.healthCheck == nil {
		cfg.healthCheck = dialHealthCheck
	}
	if cfg.aliveCheck == nil {
		cfg.aliveCheck = func(port int) bool { return portListening(port) }
	}
	if cfg.now == nil {
		cfg.now = time.Now
	}
	return &ServerManager{cfg: cfg}
}

// EnsureRunning returns the websocket URL of a live server, starting one if
// needed. A previously-started server whose process has since died is reaped and
// replaced (mirroring the LSP manager's crashed-session eviction).
func (m *ServerManager) EnsureRunning(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.proc != nil {
		if m.processAlive() {
			return m.url, nil
		}
		// The cached server died; reap it so a fresh one starts below.
		_ = m.proc.Kill()
		m.proc = nil
		m.url = ""
	}

	spec, err := onlineServerCommand(m.cfg)
	if err != nil {
		return "", err
	}
	proc, _, err := m.cfg.starter(spec)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", &SetupError{
				Tool: m.cfg.Binary,
				Hint: "install sherpa-onnx and put its online-websocket-server binary on PATH (see docs/dictation.md)",
			}
		}
		return "", fmt.Errorf("starting sherpa-onnx server: %w", err)
	}

	// Health-check the port before handing back a URL — the server takes ~1-2s to
	// bind, and a client connecting too early would fail (§6a).
	if err := m.cfg.healthCheck(ctx, m.cfg.Port); err != nil {
		_ = proc.Kill()
		return "", fmt.Errorf("sherpa-onnx server did not become ready: %w", err)
	}
	m.proc = proc
	m.url = fmt.Sprintf("ws://127.0.0.1:%d", m.cfg.Port)
	return m.url, nil
}

// processAlive reports whether the tracked server is still reachable. Probing
// the port (rather than the process handle) reflects real reachability and is
// the seam tests replace.
func (m *ServerManager) processAlive() bool {
	return m.cfg.aliveCheck(m.cfg.Port)
}

// portListening reports whether something accepts TCP connections on the port.
func portListening(port int) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// Shutdown stops the running server. Called alongside the LSP manager's shutdown
// on exit — a leaked warm server holds a port and CPU.
func (m *ServerManager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	proc := m.proc
	m.proc = nil
	m.url = ""
	m.mu.Unlock()
	if proc == nil {
		return nil
	}
	if err := proc.StopGracefully(); err != nil {
		_ = proc.Kill()
	}
	done := make(chan error, 1)
	go func() { done <- waitWithTimeout(proc, stopGrace) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		_ = proc.Kill()
		return ctx.Err()
	}
}

// URL returns the current server URL without starting one ("" when not running).
func (m *ServerManager) URL() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.url
}

// SetModelPath updates the model directory the server launches with, for a
// mid-session config change (e.g. an F9 auto-download). It takes effect the next
// time the server (re)starts; a running server is left as-is.
func (m *ServerManager) SetModelPath(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if path != "" {
		m.cfg.ModelPath = path
	}
}

// onlineServerCommand builds the streaming-server argv from the model directory.
// The online server uses a streaming transducer (encoder/decoder/joiner) plus
// tokens — the shape the sherpa-onnx docs' example uses (§6a).
func onlineServerCommand(cfg ServerConfig) (commandSpec, error) {
	if strings.TrimSpace(cfg.ModelPath) == "" {
		return commandSpec{}, &SetupError{
			Tool: "local STT streaming model",
			Hint: "set stt.localModelPath to a sherpa-onnx streaming model directory (see docs/dictation.md)",
		}
	}
	files, err := onnxFilesIn(cfg.ModelPath)
	if err != nil {
		return commandSpec{}, &SetupError{Tool: "local STT streaming model", Hint: err.Error()}
	}
	tokens := findTokensFile(files)
	// A streaming model ships encoder/decoder/joiner, often BOTH an int8 and an
	// fp32 copy of each. Pick a CONSISTENT set — all int8 or all fp32 — because
	// mixing (int8 encoder + fp32 decoder) is invalid. Prefer int8 (smaller,
	// faster) when a full int8 set is present.
	set := consistentQuantizedSet(files, "encoder", "decoder", "joiner")
	var encoder, decoder, joiner string
	if set != nil {
		encoder, decoder, joiner = set[0], set[1], set[2]
	}
	if tokens == "" || encoder == "" || decoder == "" || joiner == "" {
		return commandSpec{}, &SetupError{
			Tool: "local STT streaming model",
			Hint: fmt.Sprintf("streaming needs a transducer model (encoder/decoder/joiner .onnx + tokens.txt) in %q; see docs/dictation.md", cfg.ModelPath),
		}
	}
	args := []string{
		"--port=" + strconv.Itoa(cfg.Port),
		"--tokens=" + tokens,
		"--encoder=" + encoder,
		"--decoder=" + decoder,
		"--joiner=" + joiner,
		"--decoding-method=greedy_search",
	}
	if cfg.NumThreads > 0 {
		args = append(args, "--num-threads="+strconv.Itoa(cfg.NumThreads))
	}
	return commandSpec{name: cfg.Binary, args: args}, nil
}

func onnxFilesIn(dir string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("stt.localModelPath %q is not readable: %v", dir, err)
	}
	files := map[string]string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		files[strings.ToLower(e.Name())] = dir + string(os.PathSeparator) + e.Name()
	}
	return files, nil
}

// dialHealthCheck polls the server's TCP port until it accepts a connection or
// the deadline passes.
func dialHealthCheck(ctx context.Context, port int) error {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	deadline := time.Now().Add(5 * time.Second)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("port %d not listening after 5s", port)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
