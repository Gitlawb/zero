//go:build plan9

package providerio

// Plan 9 has no BSD-style errno constants (it uses string errors), so the
// errno-based classification is unavailable here: isConnResetErrno returns false
// and dialPreSendErrnos is empty. retry.go still classifies via its string
// markers on this platform, the pre-send inclusions (connection refused /
// network unreachable / no route to host) and the separate post-send
// "connection reset" exclusion each keep working through the wording checks.
// Zero targets linux/darwin/windows, so this variant only keeps the package
// building on Plan 9 rather than adding real support.
var dialPreSendErrnos = []error{}

func isConnResetErrno(error) bool { return false }
