package providerio

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// A stalled-but-open upstream must not block forever: the helper aborts after
// the idle timeout, cancels the request context, and returns ErrStreamIdle.
func TestScanSSEDataWithContextAbortsOnIdle(t *testing.T) {
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()

	// Send one event, then never send anything else and never close.
	go func() {
		_, _ = io.WriteString(pw, "data: first\n\n")
	}()

	cancelled := false
	cancel := func() { cancelled = true }

	var got []string
	done := make(chan error, 1)
	go func() {
		done <- ScanSSEDataWithContext(context.Background(), cancel, pr, 60*time.Millisecond, func(data string) bool {
			got = append(got, data)
			return true
		})
	}()

	select {
	case err := <-done:
		if !errors.Is(err, ErrStreamIdle) {
			t.Fatalf("err = %v, want ErrStreamIdle", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ScanSSEDataWithContext hung on a stalled stream")
	}

	if len(got) != 1 || got[0] != "first" {
		t.Fatalf("got payloads %#v, want [first]", got)
	}
	if !cancelled {
		t.Fatal("idle abort did not cancel the request context")
	}
}

// ctx cancellation must unblock a hung read and surface ctx.Err().
func TestScanSSEDataWithContextHonorsContextCancel(t *testing.T) {
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ScanSSEDataWithContext(ctx, cancel, pr, time.Hour, func(string) bool { return true })
	}()

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ScanSSEDataWithContext did not honor context cancellation")
	}
}

// Normal completion (EOF) must return nil after delivering all data payloads,
// matching ScanSSEData's multi-line accumulation semantics.
func TestScanSSEDataWithContextDeliversThenEOF(t *testing.T) {
	body := "data: line-a\ndata: line-b\n\ndata: [DONE]\n\n"
	var got []string
	err := ScanSSEDataWithContext(context.Background(), func() {}, strings.NewReader(body), time.Hour, func(data string) bool {
		got = append(got, data)
		return true
	})
	if err != nil {
		t.Fatalf("err = %v, want nil on EOF", err)
	}
	if len(got) != 1 || got[0] != "line-a\nline-b" {
		t.Fatalf("got %#v, want one accumulated payload", got)
	}
}
