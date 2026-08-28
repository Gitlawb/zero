package mcp

import (
	"context"
	"sync"
)

// launchSink carries the fact that a server's PROCESS STARTED out of the connect
// attempt, without waiting for that attempt to finish.
//
// The startup notices used to leave connectStdio only on the returned client, or
// on the returned error. Both require the attempt to return. Registration
// abandons a server that exceeds the connect timeout, so a server that started
// under the reduced write confinement and then hung in initialize or tools/list
// was recorded as skipped with nothing said about the confinement it ran under.
// The reaper that later collects the abandoned attempt runs after the serial
// commit phase has finished, so it cannot contribute without breaking the
// deterministic ordering that phase exists to provide.
//
// Publishing at Start splits the two facts apart, which is the point: launch and
// connection usability have different lifetimes. A sink that was never published
// to means Start never happened, so prepare, pipe, and Start failures stay silent
// exactly as before.
type launchSink struct {
	mu       sync.Mutex
	launched bool
	notices  []string
}

type launchSinkKey struct{}

// withLaunchSink attaches a sink to the context handed to the client factory.
// Carried on the context rather than added to the factory signature so an
// injected or third-party factory that knows nothing about it still works, and
// simply discloses nothing.
func withLaunchSink(ctx context.Context, sink *launchSink) context.Context {
	return context.WithValue(ctx, launchSinkKey{}, sink)
}

// publishLaunch records that the process for this connect attempt has started,
// along with the enforcement notices that applied to it. Safe on a context with
// no sink, which is every caller outside registration.
func publishLaunch(ctx context.Context, notices []string) {
	sink, _ := ctx.Value(launchSinkKey{}).(*launchSink)
	if sink == nil {
		return
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.launched = true
	sink.notices = append([]string(nil), notices...)
}

// observe reports whether Start was reached and what applied to it. Read from
// the registration goroutine while the connect goroutine may still be running,
// hence the lock.
func (sink *launchSink) observe() (bool, []string) {
	if sink == nil {
		return false, nil
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return sink.launched, append([]string(nil), sink.notices...)
}
