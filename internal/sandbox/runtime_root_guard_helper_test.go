package sandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// digestFor recomputes the fallback leaf name the way fallbackSandboxRuntimeRoot
// does, so the test moves with the implementation rather than pinning a literal.
func digestFor(workspaceRoot string, scope string) string {
	digest := sha256.Sum256([]byte(canonicalSandboxWorkspaceRoot(workspaceRoot) + "\x00" + scope))
	return hex.EncodeToString(digest[:8])
}

var _ = testing.Verbose

// assumeWindowsACLGrantsApplied holds the grant question constant.
//
// ValidateWindowsSandboxSetupMarker asks two independent things: whether setup's
// intent matches this command's, which is what the marker fields compare, and
// whether the objects still carry the grants, which reads real security
// descriptors. A test about the first would otherwise fail on Windows only,
// because it never applies an ACL and the descriptors honestly say so, while
// passing everywhere else. That platform-dependent result is worse than the
// stub: it hides the assertion the test was written for.
//
// The grant check has its own coverage, on both sides. Do not use this in a test
// that is about the grant.
func assumeWindowsACLGrantsApplied(t *testing.T) {
	t.Helper()
	previous := windowsACLPlanApplied
	windowsACLPlanApplied = func(WindowsACLPlan) bool { return true }
	t.Cleanup(func() { windowsACLPlanApplied = previous })
}
