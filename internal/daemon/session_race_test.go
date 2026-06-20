package daemon

import (
	"sync"
	"testing"
)

// TestSessionLineRaceWithCancelAndFinish drives Session.Line concurrently with
// Subscribe/cancel and finish so the send-on-closed-channel invariant is exercised
// under -race. Line holds s.mu across its non-blocking sends precisely so cancel()
// and finish() can't close a subscriber channel mid-send (which would panic with
// "send on closed channel"). Reverting that lock-hold makes this test panic/race;
// with it, it passes. Run with: go test -race ./internal/daemon
func TestSessionLineRaceWithCancelAndFinish(t *testing.T) {
	for iter := 0; iter < 30; iter++ {
		sess := newSession("s", "", 0)
		sess.Started()

		stop := make(chan struct{})
		var pump sync.WaitGroup
		pump.Add(1)
		go func() {
			defer pump.Done()
			for {
				select {
				case <-stop:
					return
				default:
					sess.Line("x") // hammers the broadcast/send path
				}
			}
		}()

		// Repeatedly attach, spin up a drainer, and cancel while Line runs. Each
		// cancel closes the subscriber channel under s.mu, racing the pump's send.
		var drainers sync.WaitGroup
		for i := 0; i < 150; i++ {
			_, live, cancel := sess.Subscribe()
			drainers.Add(1)
			go func() {
				defer drainers.Done()
				for range live {
				}
			}()
			cancel()
		}

		// finish() also closes any remaining subscriber channels under s.mu; race it
		// against the still-running pump before tearing down.
		sess.finish(0, nil)
		close(stop)
		pump.Wait()
		drainers.Wait()
	}
}
