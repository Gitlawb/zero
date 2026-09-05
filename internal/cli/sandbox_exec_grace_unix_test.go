//go:build !windows

package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	zeroSandbox "github.com/Gitlawb/zero/internal/sandbox"
)

// THE GRACEFUL PHASE HAS TO ACTUALLY HAPPEN.
//
// The shutdown comments described a graceful signal followed by escalation while
// the code did neither: Cancel called KillProcessTree, which is SIGKILL here, and
// WaitDelay only starts counting after Cancel returns so it could never postpone
// a kill that had already happened. A directed SIGTERM therefore reached the
// child as 137 rather than 143, with no chance to flush output, drop a lock file,
// or run a shutdown handler.
//
// Driven with a child that traps the signal and records that its handler ran, so
// the assertion is about the observed lifecycle rather than about a configured
// duration.
func TestACancelledCommandGetsAChanceToCleanUp(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "cleanup-ran")
	script := "trap 'echo yes > " + marker + "; exit 0' TERM; while :; do sleep 0.05; done"

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		done <- runSandboxPlannedCommand(ctx, zeroSandbox.CommandPlan{
			Name: "/bin/sh",
			Args: []string{"-c", script},
			Dir:  t.TempDir(),
		}, io.Discard, io.Discard)
	}()

	// The trap has to be installed before the signal, or this proves nothing.
	time.Sleep(500 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("SETUP INVALID: the child exited on its own")
	default:
	}

	cancel()
	select {
	case <-done:
	case <-time.After(sandboxExecShutdownGrace + 10*time.Second):
		t.Fatal("the cancelled command never terminated")
	}

	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the child was killed outright instead of being asked to stop, so its cleanup handler never ran: %v", err)
	}
}

// And a child that ignores the request is still force-killed, after the bound
// rather than before it. Without this the fix could be "only ever ask nicely".
func TestAStubbornCancelledCommandIsStillKilled(t *testing.T) {
	// Traps TERM and keeps running: only escalation can end this.
	script := "trap '' TERM; while :; do sleep 0.05; done"

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		done <- runSandboxPlannedCommand(ctx, zeroSandbox.CommandPlan{
			Name: "/bin/sh",
			Args: []string{"-c", script},
			Dir:  t.TempDir(),
		}, io.Discard, io.Discard)
	}()
	time.Sleep(500 * time.Millisecond)

	started := time.Now()
	cancel()
	select {
	case <-done:
	case <-time.After(sandboxExecShutdownGrace + 15*time.Second):
		t.Fatal("a child ignoring the graceful request was never force-killed")
	}
	// After the bound, not before: a kill that lands immediately is the defect
	// this replaced.
	if elapsed := time.Since(started); elapsed < sandboxExecShutdownGrace {
		t.Fatalf("the child was killed after %s, before the %s grace it was owed", elapsed, sandboxExecShutdownGrace)
	}
}
