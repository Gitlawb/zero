package oauth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestLoopbackCapturesCode(t *testing.T) {
	l, err := NewLoopbackListener("the-state")
	if err != nil {
		t.Fatalf("NewLoopbackListener: %v", err)
	}
	defer l.Close()
	if !strings.HasPrefix(l.RedirectURI(), "http://127.0.0.1:") {
		t.Fatalf("redirect URI not loopback: %q", l.RedirectURI())
	}
	go func() {
		_, _ = http.Get(l.RedirectURI() + "?code=auth-code&state=the-state")
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	code, err := l.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if code != "auth-code" {
		t.Fatalf("code = %q, want auth-code", code)
	}
}

func TestNewLoopbackListenerRejectsEmptyState(t *testing.T) {
	if _, err := NewLoopbackListener("   "); err == nil {
		t.Fatal("an empty CSRF state must be rejected (fail closed)")
	}
}

func TestRedirectURIWithHostUsesGivenHostAndPath(t *testing.T) {
	l, err := NewLoopbackListener("the-state")
	if err != nil {
		t.Fatalf("NewLoopbackListener: %v", err)
	}
	defer l.Close()
	got := l.RedirectURIWithHost("localhost", "/auth/callback")
	if !strings.HasPrefix(got, "http://localhost:") {
		t.Fatalf("redirect URI host = %q, want localhost", got)
	}
	if !strings.HasSuffix(got, "/auth/callback") {
		t.Fatalf("redirect URI path = %q, want /auth/callback suffix", got)
	}
}

func TestLoopbackCapturesCodeOnAuthCallbackPath(t *testing.T) {
	// The ChatGPT flow uses /auth/callback, not /callback; the listener must
	// capture the code on that path too.
	l, err := NewLoopbackListener("the-state")
	if err != nil {
		t.Fatalf("NewLoopbackListener: %v", err)
	}
	defer l.Close()
	uri := l.RedirectURIWithHost("127.0.0.1", "/auth/callback")
	go func() {
		_, _ = http.Get(uri + "?code=auth-code&state=the-state")
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	code, err := l.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if code != "auth-code" {
		t.Fatalf("code = %q, want auth-code", code)
	}
}

func TestLoopbackRejectsStateMismatch(t *testing.T) {
	l, err := NewLoopbackListener("expected-state")
	if err != nil {
		t.Fatalf("NewLoopbackListener: %v", err)
	}
	defer l.Close()
	go func() {
		_, _ = http.Get(l.RedirectURI() + "?code=x&state=WRONG")
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = l.Wait(ctx)
	if !errors.Is(err, ErrStateMismatch) {
		t.Fatalf("Wait err = %v, want ErrStateMismatch", err)
	}
}

func TestLoopbackTimesOut(t *testing.T) {
	l, err := NewLoopbackListener("s")
	if err != nil {
		t.Fatalf("NewLoopbackListener: %v", err)
	}
	defer l.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := l.Wait(ctx); err == nil {
		t.Fatal("Wait should time out cleanly")
	}
}

func TestLoopbackProviderError(t *testing.T) {
	l, err := NewLoopbackListener("s")
	if err != nil {
		t.Fatalf("NewLoopbackListener: %v", err)
	}
	defer l.Close()
	go func() {
		_, _ = http.Get(l.RedirectURI() + "?error=access_denied&error_description=nope&state=s")
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = l.Wait(ctx)
	if err == nil || !strings.Contains(err.Error(), "access_denied") {
		t.Fatalf("Wait err = %v, want provider error", err)
	}
}

func TestLoopbackCloseForceClosesSlowHeaderConnection(t *testing.T) {
	l, err := NewLoopbackListener("audit-state")
	if err != nil {
		t.Fatalf("NewLoopbackListener: %v", err)
	}
	addr := l.listener.Addr().String()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	// Send a request line and a header key with no terminating CRLF: Serve
	// accepts the connection but never finishes reading the header.
	fmt.Fprintf(conn, "GET /callback?code=audit-code&state=audit-state HTTP/1.1\r\nHost: %s\r\nX-Stall:", addr)
	time.Sleep(100 * time.Millisecond) // let Serve accept and start reading the partial header

	started := time.Now()
	l.Close()
	if elapsed := time.Since(started); elapsed < 900*time.Millisecond {
		t.Fatalf("Close returned after %s; expected it to wait out its one-second shutdown deadline first", elapsed)
	}

	conn.SetReadDeadline(time.Now().Add(time.Second))
	var one [1]byte
	_, readErr := conn.Read(one[:])
	if readErr == nil {
		t.Fatal("read after Close succeeded; wanted the stalled connection to be closed")
	}
	if netErr, ok := readErr.(net.Error); ok && netErr.Timeout() {
		t.Fatalf("read after Close timed out (%v); wanted the stalled connection closed rather than left open", readErr)
	}
}

func TestLoopbackCloseIsIdempotent(t *testing.T) {
	l, err := NewLoopbackListener("s")
	if err != nil {
		t.Fatalf("NewLoopbackListener: %v", err)
	}
	l.Close()
	l.Close() // must not panic or hang
}

func TestLoopbackCloseReturnsPromptlyAfterSuccessfulCallback(t *testing.T) {
	l, err := NewLoopbackListener("s")
	if err != nil {
		t.Fatalf("NewLoopbackListener: %v", err)
	}
	go func() {
		_, _ = http.Get(l.RedirectURI() + "?code=x&state=s")
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := l.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	started := time.Now()
	l.Close()
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("Close took %s after a completed, idle callback; want a prompt return", elapsed)
	}
}
