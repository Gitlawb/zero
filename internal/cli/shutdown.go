package cli

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// signalContext returns a context that is cancelled when the process receives an
// interrupt (Ctrl+C / SIGINT) or SIGTERM, plus a stop function that releases the
// signal handler (call it when the operation completes). The agent loop honors
// context cancellation, so wrapping a long run in this context turns a Ctrl+C
// into a clean shutdown — the in-flight provider call is cancelled and the run
// returns context.Canceled — instead of an abrupt process kill that leaks the
// HTTP request and any spawned children.
func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
