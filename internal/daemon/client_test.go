package daemon

import (
	"net"
	"testing"
	"time"
)

// TestClientHandshakeTimesOut verifies the handshake is bounded: a peer that
// accepts the connection but never completes the version exchange makes
// NewClientConn fail promptly instead of blocking forever (D9).
func TestClientHandshakeTimesOut(t *testing.T) {
	orig := handshakeTimeout
	handshakeTimeout = 100 * time.Millisecond
	defer func() { handshakeTimeout = orig }()

	// net.Pipe is synchronous: with no reader on the server end the client's hello
	// write blocks, so only the handshake deadline can unblock it.
	client, server := net.Pipe()
	defer server.Close()

	done := make(chan error, 1)
	go func() {
		_, err := NewClientConn(client)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("NewClientConn must fail when the peer never completes the handshake")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("NewClientConn blocked past the handshake timeout — deadline not applied")
	}
}
