package acp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"
)

type testReadCloser struct {
	read  func([]byte) (int, error)
	close func() error
}

func (r testReadCloser) Read(p []byte) (int, error) { return r.read(p) }
func (r testReadCloser) Close() error               { return r.close() }

type testReader func([]byte) (int, error)

func (r testReader) Read(p []byte) (int, error) { return r(p) }

type testWriter func([]byte) (int, error)

func (w testWriter) Write(p []byte) (int, error) { return w(p) }

// connPair wires two Conns together over in-memory pipes and serves both.
func connPair(t *testing.T) (a, b *Conn, stop func()) {
	t.Helper()
	ar, bw := io.Pipe() // b -> a
	br, aw := io.Pipe() // a -> b
	a = NewConn(ar, aw)
	b = NewConn(br, bw)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = a.Serve(ctx) }()
	go func() { _ = b.Serve(ctx) }()
	return a, b, func() {
		cancel()
		_ = aw.Close()
		_ = bw.Close()
	}
}

func TestConnServeCancellationInterruptsIdleRead(t *testing.T) {
	pipeReader, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })
	readStarted := make(chan struct{})
	closeCalls := make(chan struct{}, 2)
	reader := testReadCloser{
		read: func(p []byte) (int, error) {
			close(readStarted)
			return pipeReader.Read(p)
		},
		close: func() error {
			closeCalls <- struct{}{}
			return pipeReader.Close()
		},
	}
	conn := NewConn(reader, io.Discard)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- conn.Serve(ctx) }()

	<-readStarted
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned %v after cancellation, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not return after cancelling an idle connection")
	}
	if got := len(closeCalls); got != 1 {
		t.Fatalf("reader Close calls = %d, want 1", got)
	}
}

func TestConnServePreservesTerminalReadErrorDuringCancellation(t *testing.T) {
	for _, closable := range []bool{false, true} {
		name := "non-closable"
		if closable {
			name = "closable"
		}
		t.Run(name, func(t *testing.T) {
			wantErr := errors.New("read failed")
			read := testReader(func(p []byte) (int, error) {
				return copy(p, "not json"), wantErr
			})
			closeCalls := make(chan struct{}, 1)
			var reader io.Reader = read
			if closable {
				reader = testReadCloser{
					read: read,
					close: func() error {
						closeCalls <- struct{}{}
						return nil
					},
				}
			}
			writeStarted := make(chan struct{})
			releaseWrite := make(chan struct{})
			writer := testWriter(func(p []byte) (int, error) {
				close(writeStarted)
				<-releaseWrite
				return len(p), nil
			})
			conn := NewConn(reader, writer)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- conn.Serve(ctx) }()

			<-writeStarted
			cancel()
			close(releaseWrite)
			select {
			case err := <-done:
				if !errors.Is(err, wantErr) {
					t.Fatalf("Serve returned %v, want terminal read error %v", err, wantErr)
				}
			case <-time.After(time.Second):
				t.Fatal("Serve did not return after terminal read error")
			}
			if got := len(closeCalls); got != 0 {
				t.Fatalf("reader Close calls = %d, want 0 after terminal read", got)
			}
		})
	}
}

func TestConnRequestResponse(t *testing.T) {
	a, b, stop := connPair(t)
	defer stop()

	b.Handle("add", func(_ context.Context, params json.RawMessage) (any, error) {
		var in struct{ X, Y int }
		if err := json.Unmarshal(params, &in); err != nil {
			return nil, RPCError(codeInvalidParams, "bad params")
		}
		return map[string]int{"sum": in.X + in.Y}, nil
	})

	var out struct{ Sum int }
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := a.Call(ctx, "add", map[string]int{"X": 2, "Y": 3}, &out); err != nil {
		t.Fatalf("call: %v", err)
	}
	if out.Sum != 5 {
		t.Fatalf("sum = %d, want 5", out.Sum)
	}
}

func TestConnNotification(t *testing.T) {
	a, b, stop := connPair(t)
	defer stop()

	got := make(chan string, 1)
	b.HandleNotify("ping", func(_ context.Context, params json.RawMessage) {
		var in struct{ Msg string }
		_ = json.Unmarshal(params, &in)
		got <- in.Msg
	})

	if err := a.Notify("ping", map[string]string{"Msg": "hello"}); err != nil {
		t.Fatalf("notify: %v", err)
	}
	select {
	case msg := <-got:
		if msg != "hello" {
			t.Fatalf("got %q, want hello", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("notification not delivered")
	}
}

func TestConnMethodNotFound(t *testing.T) {
	a, _, stop := connPair(t)
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := a.Call(ctx, "does_not_exist", nil, nil)
	var re *rpcError
	if !asRPCError(err, &re) {
		t.Fatalf("expected rpcError, got %v", err)
	}
	if re.Code != codeMethodNotFound {
		t.Fatalf("code = %d, want %d", re.Code, codeMethodNotFound)
	}
}

func TestConnHandlerError(t *testing.T) {
	a, b, stop := connPair(t)
	defer stop()
	b.Handle("boom", func(_ context.Context, _ json.RawMessage) (any, error) {
		return nil, RPCError(codeInvalidParams, "nope")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := a.Call(ctx, "boom", nil, nil)
	var re *rpcError
	if !asRPCError(err, &re) || re.Code != codeInvalidParams {
		t.Fatalf("expected invalid-params rpcError, got %v", err)
	}
}

// TestConnBidirectionalDuringHandler proves that while one peer is inside a
// request handler it can issue an outbound request back to the caller and the
// caller answers it — exactly the session/prompt -> session/request_permission
// pattern. If the read loop blocked on the handler, this would deadlock.
func TestConnBidirectionalDuringHandler(t *testing.T) {
	a, b, stop := connPair(t)
	defer stop()

	// a answers an "approve?" callback.
	a.Handle("approve", func(_ context.Context, _ json.RawMessage) (any, error) {
		return map[string]bool{"ok": true}, nil
	})

	// b's "run" handler calls back to a mid-flight.
	b.Handle("run", func(ctx context.Context, _ json.RawMessage) (any, error) {
		var approval struct{ OK bool }
		if err := b.Call(ctx, "approve", nil, &approval); err != nil {
			return nil, err
		}
		return map[string]bool{"ran": approval.OK}, nil
	})

	var out struct{ Ran bool }
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := a.Call(ctx, "run", nil, &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !out.Ran {
		t.Fatal("expected ran=true via mid-handler callback")
	}
}

// TestConnSurvivesMalformedLine proves a single bad ndjson line yields a -32700
// and does NOT tear down the connection — a following valid request still works.
func TestConnSurvivesMalformedLine(t *testing.T) {
	clientR, serverW := io.Pipe() // server -> client
	serverR, clientW := io.Pipe() // client -> server
	server := NewConn(serverR, serverW)
	server.Handle("ping", func(_ context.Context, _ json.RawMessage) (any, error) { return "pong", nil })
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		_ = serverW.Close()
		_ = clientW.Close()
	}()
	go func() { _ = server.Serve(ctx) }()

	go func() {
		_, _ = clientW.Write([]byte("this is not json\n"))
		_, _ = clientW.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n"))
	}()

	dec := json.NewDecoder(clientR)
	var sawParseError, sawPong bool
	for i := 0; i < 2; i++ {
		var msg struct {
			Result any `json:"result"`
			Error  *struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		done := make(chan error, 1)
		go func() { done <- dec.Decode(&msg) }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("decode response %d: %v", i, err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for a response")
		}
		if msg.Error != nil && msg.Error.Code == codeParseError {
			sawParseError = true
		}
		if r, ok := msg.Result.(string); ok && r == "pong" {
			sawPong = true
		}
	}
	if !sawParseError {
		t.Error("expected a -32700 parse error for the malformed line")
	}
	if !sawPong {
		t.Error("expected the valid request to still be answered (connection survived the bad line)")
	}
}

func asRPCError(err error, target **rpcError) bool {
	re, ok := err.(*rpcError)
	if ok {
		*target = re
	}
	return ok
}
