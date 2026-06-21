package acp

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
)

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

func asRPCError(err error, target **rpcError) bool {
	re, ok := err.(*rpcError)
	if ok {
		*target = re
	}
	return ok
}
