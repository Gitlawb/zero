package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestServer(t *testing.T, launcher Launcher) (*Server, Paths) {
	t.Helper()
	dir := t.TempDir()
	paths := Paths{
		Socket: filepath.Join(dir, "d.sock"),
		Lock:   filepath.Join(dir, "d.lock"),
		Status: filepath.Join(dir, "d.status"),
	}
	pool, err := NewPool(PoolOptions{Size: 2, Launcher: launcher, KillTimeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	mgr, err := NewSessionManager(SessionManagerOptions{Pool: pool})
	if err != nil {
		t.Fatalf("NewSessionManager: %v", err)
	}
	srv, err := NewServer(ServerOptions{Paths: paths, Manager: mgr, Pool: pool})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return srv, paths
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("file %s did not appear within timeout", path)
}

func TestServerEndToEnd(t *testing.T) {
	out := []string{`{"type":"event","seq":1}`, `{"type":"event","seq":2}`}
	launcher, _ := seqLauncher(&fakeWorker{pid: 1, out: out, exitCode: 0})
	srv, paths := newTestServer(t, launcher)

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve() }()
	waitForFile(t, paths.Status)

	// --- run a session and collect its stream-json output ---
	runClient, err := Dial(paths.Socket)
	if err != nil {
		t.Fatalf("Dial(run): %v", err)
	}
	var got []string
	if err := runClient.Run("s1", "", "hello", nil, func(line string) { got = append(got, line) }); err != nil {
		t.Fatalf("Run: %v", err)
	}
	runClient.Close()
	if len(got) != 2 || got[0] != out[0] || got[1] != out[1] {
		t.Fatalf("streamed lines = %v, want %v", got, out)
	}

	// --- status reflects the session ---
	statusClient, err := Dial(paths.Socket)
	if err != nil {
		t.Fatalf("Dial(status): %v", err)
	}
	report, err := statusClient.Status()
	statusClient.Close()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if report.PoolSize != 2 {
		t.Fatalf("status PoolSize = %d, want 2", report.PoolSize)
	}
	foundSession := false
	for _, s := range report.Sessions {
		if s.ID == "s1" {
			foundSession = true
			if s.State != string(SessionDone) {
				t.Fatalf("session s1 state = %s, want done", s.State)
			}
		}
	}
	if !foundSession {
		t.Fatalf("status missing session s1: %+v", report.Sessions)
	}

	// --- attach to the finished session sees the buffered history ---
	attachClient, err := Dial(paths.Socket)
	if err != nil {
		t.Fatalf("Dial(attach): %v", err)
	}
	var attached []string
	if err := attachClient.Attach("s1", func(line string) { attached = append(attached, line) }); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	attachClient.Close()
	if len(attached) != 2 {
		t.Fatalf("attach lines = %v, want 2 buffered", attached)
	}

	// --- graceful stop, daemon cleans up ---
	stopClient, err := Dial(paths.Socket)
	if err != nil {
		t.Fatalf("Dial(stop): %v", err)
	}
	if err := stopClient.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	stopClient.Close()

	select {
	case err := <-serveErr:
		if err != nil {
			t.Fatalf("Serve returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return after shutdown")
	}
	// Socket, status, and lock files are removed on exit.
	for _, p := range []string{paths.Socket, paths.Status, paths.Lock} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("file %s not cleaned up after shutdown: %v", p, err)
		}
	}
}

func TestServerSecondInstanceFails(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	launcher, _ := seqLauncher(&fakeWorker{pid: 1, waitCh: block})
	srv1, paths := newTestServer(t, launcher)

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv1.Serve() }()
	waitForFile(t, paths.Status)

	// A second server on the same paths must refuse to start (single instance).
	pool2, _ := NewPool(PoolOptions{Size: 1, Launcher: launcher})
	mgr2, _ := NewSessionManager(SessionManagerOptions{Pool: pool2})
	srv2, _ := NewServer(ServerOptions{Paths: paths, Manager: mgr2, Pool: pool2})
	if err := srv2.Serve(); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second Serve err = %v, want ErrAlreadyRunning", err)
	}

	srv1.Shutdown()
	<-serveErr
}

func TestServerRejectsUnknownCommand(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	launcher, _ := seqLauncher(&fakeWorker{pid: 1, waitCh: block})
	srv, paths := newTestServer(t, launcher)
	go func() { _ = srv.Serve() }()
	defer srv.Shutdown()
	waitForFile(t, paths.Status)

	client, err := Dial(paths.Socket)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()
	// Run with an empty session id is rejected with an error frame.
	err = client.Run("", "", "hi", nil, nil)
	if err == nil {
		t.Fatal("run with empty session id must return an error")
	}
}
