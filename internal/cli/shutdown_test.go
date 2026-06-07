package cli

import (
	"testing"
	"time"
)

func TestSignalContextNotCancelledInitially(t *testing.T) {
	ctx, stop := signalContext()
	defer stop()
	if ctx.Err() != nil {
		t.Fatalf("signalContext should not be cancelled before a signal, got %v", ctx.Err())
	}
}

func TestSignalContextStopCancels(t *testing.T) {
	ctx, stop := signalContext()
	stop()
	select {
	case <-ctx.Done():
		// stop() releases the handler and cancels the context — expected.
	case <-time.After(time.Second):
		t.Fatal("stop() should cancel the signal context")
	}
}
