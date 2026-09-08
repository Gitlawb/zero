package peermsg

import (
	"context"
	"io"
	"testing"
	"time"
)

func TestPlatformTransportRoundTrip(t *testing.T) {
	transport := platformTransport()
	endpoint, err := transport.Endpoint(t.TempDir(), "0123456789abcdef", 4242)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := transport.Listen(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
		_ = transport.Remove(endpoint)
	})

	serverErr := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverErr <- acceptErr
			return
		}
		defer conn.Close()
		payload := make([]byte, 4)
		if _, readErr := io.ReadFull(conn, payload); readErr != nil {
			serverErr <- readErr
			return
		}
		_, writeErr := conn.Write(payload)
		serverErr <- writeErr
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := transport.Dial(ctx, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 4)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatal(err)
	}
	if string(reply) != "ping" {
		t.Fatalf("reply = %q", reply)
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
}
