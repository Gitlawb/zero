//go:build plan9

package providerio

// Plan 9 has no BSD-style errno constants (it uses string errors), so the
// errno-based pre-send and reset classification is unavailable here; retry.go
// falls back to its string-marker heuristics (connection refused / reset /
// network unreachable / no route to host) on this platform. Zero targets
// linux/darwin/windows, so this variant only keeps the package building on
// Plan 9 rather than adding real support.
var dialPreSendErrnos = []error{}

func isConnResetErrno(error) bool { return false }
