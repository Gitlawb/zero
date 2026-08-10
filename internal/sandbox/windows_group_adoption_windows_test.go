//go:build windows

package sandbox

import (
	"errors"
	"strings"
	"testing"
)

// A NAME IS NOT PROOF OF PROVENANCE.
//
// "Already exists" was treated as plain success, so any local group that
// happened to be called ZeroSandboxUsers was adopted: its members, and every
// grant already keyed to it, silently became part of the sandbox's identity.
// An unprivileged user cannot create a local group, but an administrator, an
// installer or an earlier tool can, and the sandbox would then inherit it.
//
// Tested through the extracted decision rather than the syscall, so it needs no
// Administrator and leaves no group behind on the machine running the suite.
func TestAdoptingAForeignGroupOfOurNameIsRefused(t *testing.T) {
	for _, status := range []uintptr{nerrGroupExists, errorAliasExists} {
		err := resolveWindowsSandboxGroupAdd(status, func() (bool, error) { return false, nil })
		if err == nil {
			t.Fatalf("status %d adopted a group Zero did not create, handing the sandbox whatever it already grants", status)
		}
		if !strings.Contains(err.Error(), windowsSandboxGroupName) {
			t.Errorf("the refusal must name the group that is in the way, got %q", err)
		}
	}
}

// Our own group is still adopted, or re-running setup would fail on the group
// it created a moment ago and provisioning would never converge.
func TestAdoptingOurOwnGroupStillSucceeds(t *testing.T) {
	for _, status := range []uintptr{nerrGroupExists, errorAliasExists} {
		if err := resolveWindowsSandboxGroupAdd(status, func() (bool, error) { return true, nil }); err != nil {
			t.Errorf("status %d refused a group carrying Zero's own comment: %v", status, err)
		}
	}
}

// A freshly created group is ours by construction, so no ownership probe should
// run at all. Without this the check could be satisfied by an implementation
// that interrogates the group it just made, which would be a wasted syscall and
// a needless failure mode.
func TestCreatingTheGroupDoesNotProbeOwnership(t *testing.T) {
	probed := false
	if err := resolveWindowsSandboxGroupAdd(nerrSuccess, func() (bool, error) {
		probed = true
		return false, nil
	}); err != nil {
		t.Fatalf("a successful create was rejected: %v", err)
	}
	if probed {
		t.Error("ownership was probed for a group we had just created ourselves")
	}
}

// A failed ownership probe must not be read as "not ours" and must not be read
// as "ours" either. Neither guess is safe, so the error surfaces.
func TestAnUnreadableGroupIsNotGuessedEitherWay(t *testing.T) {
	sentinel := errors.New("NetLocalGroupGetInfo: status 5")
	err := resolveWindowsSandboxGroupAdd(nerrGroupExists, func() (bool, error) { return false, sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("a failed ownership probe was swallowed, got %v", err)
	}
}

// A real API failure is still a failure; the new branch must not mask it.
func TestARealGroupAddFailureStillFails(t *testing.T) {
	if err := resolveWindowsSandboxGroupAdd(errorAccessDenied32, func() (bool, error) { return true, nil }); err == nil {
		t.Fatal("access denied was reported as success")
	}
}
