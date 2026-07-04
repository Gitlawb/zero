package daemon

import (
	"math/rand"
	"time"
)

// Backoff bounds. Mirrors reference-daemon-code-agent-js/worker-manager.js:
// backoffMs(consecutive) = jitter(min(1000 * 2**consecutive, 300000)).
const (
	backoffUnit = time.Second
	backoffCap  = 5 * time.Minute
)

// backoffBase returns the un-jittered exponential delay for a 1-based restart
// count: min(1s * 2^attempt, 5m). attempt<=0 yields the unit delay.
func backoffBase(attempt int) time.Duration {
	if attempt <= 0 {
		return backoffUnit
	}
	// Shift carefully: cap before overflow. 2^attempt * 1s exceeds the cap well
	// before any int64 overflow (cap is 5m), so clamp once it would exceed it.
	d := backoffUnit
	for range attempt {
		d *= 2
		if d >= backoffCap {
			return backoffCap
		}
	}
	return d
}

// BackoffWithJitter returns backoffBase(attempt) scaled by a uniform random
// factor in [0.5, 1.5), matching worker-manager.js jitter(base) =
// base*(0.5 + random()). The jitter de-synchronizes simultaneous restarts so a
// fleet of workers does not thunder back at the same instant.
func BackoffWithJitter(attempt int) time.Duration {
	base := backoffBase(attempt)
	factor := 0.5 + rand.Float64() //nolint:gosec // jitter, not security-sensitive
	return time.Duration(float64(base) * factor)
}
