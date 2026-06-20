package daemon

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func unusedLauncher(context.Context, WorkerSpec) (WorkerHandle, error) {
	return nil, errors.New("unused")
}

// TestHandleConnBoundsHandshake verifies the daemon control handshake is
// deadline-bounded: a peer that connects but never sends the hello must not pin
// the handler goroutine forever (AUDIT-M7 / AUDIT-I1).
func TestHandleConnBoundsHandshake(t *testing.T) {
	orig := handshakeTimeout
	handshakeTimeout = 100 * time.Millisecond
	defer func() { handshakeTimeout = orig }()

	srv, _ := newTestServer(t, unusedLauncher)
	client, server := net.Pipe()
	defer client.Close()

	done := make(chan struct{})
	go func() { srv.handleConn(server); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleConn ignored the handshake deadline — an idle peer would pin the goroutine")
	}
}

// TestServeConnClosedByShutdown verifies a remote-bridge connection (entered via
// ServeConn, not the local accept loop) is tracked so Shutdown can close it even
// while it is blocked in the pre-stream handshake read (AUDIT-M8). handshakeTimeout
// stays at its 10s default, so the sub-2s completion proves Shutdown closed it, not
// the deadline.
func TestServeConnClosedByShutdown(t *testing.T) {
	srv, _ := newTestServer(t, unusedLauncher)
	client, server := net.Pipe()
	defer client.Close()

	served := make(chan struct{})
	go func() { srv.ServeConn(server); close(served) }() // blocks in the handshake read

	srv.Shutdown() // must close the tracked bridge conn, unblocking the read
	select {
	case <-served:
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown did not close a ServeConn (bridge) connection")
	}
}
