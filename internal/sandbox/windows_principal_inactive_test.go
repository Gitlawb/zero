package sandbox

import (
	"strings"
	"testing"
)

// An opted-in principal that stands down must be reportable.
//
// This is the whole point of the predicate: under the DEFAULT network-deny
// policy the principal never runs, so an operator who set the opt-in to confine
// reads gets the same-user restricted token and no read confinement, with
// nothing anywhere saying so. The runner cannot warn per command, and the setup
// marker validates happily because the account really was provisioned.
func TestPrincipalReportsInactiveUnderTheDefaultDenyPolicy(t *testing.T) {
	reason := WindowsSandboxPrincipalInactiveReason(true, NetworkDeny)
	if reason == "" {
		t.Fatal("opted in under network-deny reported as active, so the standdown is invisible to doctor")
	}
	// The message has to say WHY, not just that something is off. An operator
	// reading it needs to know reads are not confined.
	for _, want := range []string{"restricted token", "reads"} {
		if !strings.Contains(reason, want) {
			t.Errorf("reason %q does not mention %q", reason, want)
		}
	}
}

// With the network allowed the principal genuinely runs, so there is nothing to
// report and doctor must stay quiet.
func TestPrincipalReportsActiveWhenNetworkIsAllowed(t *testing.T) {
	if reason := WindowsSandboxPrincipalInactiveReason(true, NetworkAllow); reason != "" {
		t.Errorf("opted in with network allowed reported inactive: %q", reason)
	}
}

// Not opting in is not a standdown. Warning every default install that a
// backend it never asked for is inactive would be noise, and noise that repeats
// gets filtered rather than acted on.
func TestNotOptingInIsNotReportedAsInactive(t *testing.T) {
	for _, mode := range []NetworkMode{NetworkDeny, NetworkAllow, ""} {
		if reason := WindowsSandboxPrincipalInactiveReason(false, mode); reason != "" {
			t.Errorf("opt-out with network %q reported inactive: %q", mode, reason)
		}
	}
}

// An unset network mode must normalize the same way the rest of the package
// treats it, so doctor and the runtime agree on a config that omits it.
func TestPrincipalInactiveHandlesAnUnsetNetworkMode(t *testing.T) {
	unset := WindowsSandboxPrincipalInactiveReason(true, "")
	normalized := WindowsSandboxPrincipalInactiveReason(true, NormalizeNetworkMode(""))
	if unset != normalized {
		t.Errorf("unset mode reported %q but its normalized form reported %q", unset, normalized)
	}
}
