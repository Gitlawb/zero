//go:build windows

package sandbox

import (
	"errors"
	"os"
	"testing"
)

// The diagnostic reads a path back out of an error string produced two
// functions away. That coupling is invisible to the compiler, so it is pinned
// here by driving the REAL producer rather than by hand-writing the message:
// if openWindowsACLTarget ever rewords its error, this fails instead of the
// diagnostic silently going quiet and users losing the one clue they had.
func TestDeniedPathIsRecoveredFromARealApplyFailure(t *testing.T) {
	// A directory no ordinary user can re-DACL. Exactly the shape that bricked a
	// workspace: present, in the plan, and impossible to apply.
	const target = `C:\Windows\System32`

	_, _, err := applyWindowsACLPathGroup(windowsACLPathGroup{
		Path: target,
		Entries: []WindowsACLEntry{{
			Action:     WindowsACLAllowWrite,
			Path:       target,
			Capability: testPrincipalSID,
		}},
	})
	if err == nil {
		t.Skip("this process can re-DACL System32, so it is elevated and cannot exercise the denial path")
	}
	if !errors.Is(err, os.ErrPermission) {
		t.Skipf("failed for a reason other than access denial, nothing to extract here: %v", err)
	}

	got := windowsACLPlanDeniedPath(err)
	if got == "" {
		t.Fatalf("no path recovered from a real access-denied apply failure, so the operator is told only that something was denied: %v", err)
	}
	if got != target {
		t.Errorf("recovered %q, want %q", got, target)
	}
}

// Anything that is not an access denial must return empty, so the caller falls
// back to the generic message rather than naming an innocent path.
func TestDeniedPathIgnoresUnrelatedErrors(t *testing.T) {
	for _, err := range []error{nil, errors.New(`open windows ACL target C:\somewhere: disk full`), os.ErrNotExist} {
		if got := windowsACLPlanDeniedPath(err); got != "" {
			t.Errorf("recovered %q from %v, want empty", got, err)
		}
	}
}
