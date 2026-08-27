package sandbox

import (
	"strings"
	"testing"
)

// THE STAMP IS NOT THE GRANT.
//
// The elevated tier attested its runtime root with a stamp file, which proves
// the directory was not removed and recreated under the same pathname and that
// setup provisioned it for this configuration. An ordinary file survives an ACL
// edit untouched, so `icacls /reset`, an inheritance change on a parent, or a
// security product rewriting the DACL all leave a valid stamp over a runtime
// root the WRITE_RESTRICTED child cannot write. The marker fields agree, the
// stamp agrees, and the first write into TMP or a package cache returns
// ACCESS_DENIED with nothing having said why.
//
// The unelevated tier already reads the descriptors. This is the same question
// asked by the tier that runs under the restricted token after an elevated
// provisioning it cannot repeat, so it refuses and names the action rather than
// re-applying.
func TestTheElevatedMarkerRefusesAnUnappliedGrant(t *testing.T) {
	config := WindowsSandboxSetupConfig{
		SandboxHome:    t.TempDir(),
		CommandCWD:     `C:\workspace`,
		WorkspaceRoots: []string{`C:\workspace`},
		PermissionProfile: PermissionProfile{
			FileSystem: FileSystemPolicy{
				Kind:       FileSystemRestricted,
				WriteRoots: []WritableRoot{{Root: `C:\workspace`}},
			},
			Network: NetworkPolicy{Mode: NetworkDeny},
		},
	}
	if _, err := WriteWindowsSandboxSetupMarker(config); err != nil {
		t.Fatalf("WriteWindowsSandboxSetupMarker: %v", err)
	}

	// Everything the marker compares is identical in both halves below. Only the
	// answer to "do the objects still carry the grants" differs.
	previous := windowsACLPlanApplied
	t.Cleanup(func() { windowsACLPlanApplied = previous })

	windowsACLPlanApplied = func(WindowsACLPlan) bool { return true }
	if err := ValidateWindowsSandboxSetupMarker(config); err != nil {
		t.Fatalf("SETUP INVALID: the marker does not validate even with the grants intact: %v", err)
	}

	windowsACLPlanApplied = func(WindowsACLPlan) bool { return false }
	err := ValidateWindowsSandboxSetupMarker(config)
	if err == nil {
		t.Fatal("a runtime root that no longer carries its grants validated, so the command launches into a sandbox it cannot write")
	}
	// Actionable, and about the right thing: an operator told "permission roots
	// changed" goes looking at their policy for a problem that is not there.
	for _, want := range []string{"permissions", "zero sandbox setup"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "out of date") {
		t.Errorf("the refusal reads as a policy edit, which sends the operator to the wrong place: %v", err)
	}
}
