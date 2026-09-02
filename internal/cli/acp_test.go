package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type acpTestReader func([]byte) (int, error)

func (r acpTestReader) Read(p []byte) (int, error) { return r(p) }

type acpTestWriter func([]byte) (int, error)

func (w acpTestWriter) Write(p []byte) (int, error) { return w(p) }

func installACPTestContext(t *testing.T) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	previous := acpSignalContext
	acpSignalContext = func() (context.Context, context.CancelFunc) {
		return ctx, cancel
	}
	t.Cleanup(func() {
		cancel()
		acpSignalContext = previous
	})
	return cancel
}

func TestRunACPCancellationPreservesTerminalReadError(t *testing.T) {
	cancel := installACPTestContext(t)
	wantErr := errors.New("transport read failed")
	reader := acpTestReader(func(p []byte) (int, error) {
		return copy(p, `{"jsonrpc":"1.0","id":1}`+"\n"), wantErr
	})
	writeStarted := make(chan struct{}, 1)
	releaseWrite := make(chan struct{})
	var stdout bytes.Buffer
	writer := acpTestWriter(func(p []byte) (int, error) {
		writeStarted <- struct{}{}
		<-releaseWrite
		return stdout.Write(p)
	})
	var stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- runACP(nil, writer, &stderr, fillAppDeps(appDeps{stdin: reader}))
	}()

	select {
	case <-writeStarted:
	case <-time.After(time.Second):
		t.Fatal("ACP did not reach the response write")
	}
	// Keep Serve in the synchronous write until cancellation is observable. The
	// regression requires the genuine read error to win over that cancellation.
	cancel()
	close(releaseWrite)

	select {
	case code := <-done:
		if code != exitCrash {
			t.Fatalf("exit code = %d, want crash %d", code, exitCrash)
		}
	case <-time.After(time.Second):
		t.Fatal("ACP did not exit after terminal read error")
	}
	if got := stderr.String(); !strings.Contains(got, "acp: "+wantErr.Error()) {
		t.Fatalf("stderr = %q, want terminal read error", got)
	}
}

func TestRunACPIdleCancellationExitsCleanly(t *testing.T) {
	cancel := installACPTestContext(t)
	pipeReader, pipeWriter := io.Pipe()
	t.Cleanup(func() { _ = pipeWriter.Close() })
	readStarted := make(chan struct{}, 1)
	reader := &acpNotifyingReadCloser{ReadCloser: pipeReader, readStarted: readStarted}
	var stdout, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- runACP(nil, &stdout, &stderr, fillAppDeps(appDeps{stdin: reader}))
	}()

	select {
	case <-readStarted:
	case <-time.After(time.Second):
		t.Fatal("ACP did not begin reading")
	}
	cancel()

	select {
	case code := <-done:
		if code != exitSuccess {
			t.Fatalf("exit code = %d, want success %d; stderr: %s", code, exitSuccess, stderr.String())
		}
	case <-time.After(time.Second):
		t.Fatal("ACP did not exit after cancellation")
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

type acpNotifyingReadCloser struct {
	io.ReadCloser
	readStarted chan<- struct{}
	once        sync.Once
}

func (r *acpNotifyingReadCloser) Read(p []byte) (int, error) {
	r.once.Do(func() { r.readStarted <- struct{}{} })
	return r.ReadCloser.Read(p)
}
