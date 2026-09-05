//go:build windows

package sandbox

import (
	"errors"
	"testing"

	"golang.org/x/sys/windows"
)

// The outer rollback must only delete principals this run created.
//
// Dual-role setup provisions offline then online. If the offline role adopts an
// account that already existed and anything afterwards fails, the outer rollback
// used to delete it anyway, turning a partial re-setup failure into the loss of
// a principal that was working before the run started. Two roles make that the
// common case rather than an unlucky one: the first role usually succeeds, so
// there is nearly always something for a later failure to destroy.
func TestDualRoleSetupRollbackSparesAdoptedPrincipals(t *testing.T) {
	for name, testCase := range map[string]struct {
		createdByRole map[windowsSandboxRole]bool
		wantRemoved   []windowsSandboxRole
	}{
		"offline adopted, online created": {
			createdByRole: map[windowsSandboxRole]bool{
				windowsSandboxRoleOffline: false,
				windowsSandboxRoleOnline:  true,
			},
			// Only the one this run made. Deleting the adopted offline principal
			// is the data loss this guards against.
			wantRemoved: []windowsSandboxRole{windowsSandboxRoleOnline},
		},
		"both adopted": {
			createdByRole: map[windowsSandboxRole]bool{
				windowsSandboxRoleOffline: false,
				windowsSandboxRoleOnline:  false,
			},
			wantRemoved: nil,
		},
		"both created": {
			createdByRole: map[windowsSandboxRole]bool{
				windowsSandboxRoleOffline: true,
				windowsSandboxRoleOnline:  true,
			},
			wantRemoved: []windowsSandboxRole{windowsSandboxRoleOffline, windowsSandboxRoleOnline},
		},
	} {
		t.Run(name, func(t *testing.T) {
			var removed []windowsSandboxRole
			restoreDualRoleSeams(t, testCase.createdByRole, &removed)

			// Fail on the SECOND role, not the first. ACL application happens per
			// role inside the loop, so failing the first aborts before the second
			// is provisioned at all and the rollback never has more than one
			// principal to consider. The case worth testing is the one review
			// described: the first role succeeds, the second fails, and the
			// question is whether the first gets destroyed on the way out.
			// Count GRANT plans, not ACL calls. applyWindowsPrincipalACLs revokes
			// this trustee's existing ACEs before applying the new plan, so there
			// are two calls per role now. The contract under test is "the second
			// ROLE fails"; counting raw calls would fail the first role's grant
			// instead and prove something else entirely.
			grants := 0
			applyWindowsACLPlanFn = func(plan WindowsACLPlan) (func() error, error) {
				if len(plan.Entries) > 0 && plan.Entries[0].Action == windowsACLRevoke {
					return func() error { return nil }, nil
				}
				grants++
				if grants < 2 {
					return func() error { return nil }, nil
				}
				return nil, errors.New("ACL apply refused")
			}

			if _, err := setupWindowsSandboxPrincipal(windowsSandboxTestConfig()); err == nil {
				t.Fatal("setup reported success despite an injected ACL failure")
			}
			assertRolesEqual(t, removed, testCase.wantRemoved)
		})
	}
}

// The same contract on the success path: the rollback handed back to the caller
// for a LATER setup step to invoke must be just as reluctant.
func TestDualRoleSetupReturnedRollbackSparesAdoptedPrincipals(t *testing.T) {
	var removed []windowsSandboxRole
	restoreDualRoleSeams(t, map[windowsSandboxRole]bool{
		windowsSandboxRoleOffline: false,
		windowsSandboxRoleOnline:  true,
	}, &removed)

	rollback, err := setupWindowsSandboxPrincipal(windowsSandboxTestConfig())
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("setup removed principals before anything failed: %v", removed)
	}
	if err := rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	assertRolesEqual(t, removed, []windowsSandboxRole{windowsSandboxRoleOnline})
}

func restoreDualRoleSeams(t *testing.T, createdByRole map[windowsSandboxRole]bool, removed *[]windowsSandboxRole) {
	t.Helper()
	prevProvision := provisionWindowsSandboxPrincipalForSetupFn
	prevApply := applyWindowsACLPlanFn
	prevRemove := removeWindowsSandboxPrincipalForSetupFn
	t.Cleanup(func() {
		provisionWindowsSandboxPrincipalForSetupFn = prevProvision
		applyWindowsACLPlanFn = prevApply
		removeWindowsSandboxPrincipalForSetupFn = prevRemove
	})

	provisionWindowsSandboxPrincipalForSetupFn = func(_ WindowsSandboxCommandConfig, role windowsSandboxRole) (windowsSandboxIdentity, bool, error) {
		sid, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
		if err != nil {
			return windowsSandboxIdentity{}, false, err
		}
		return windowsSandboxIdentity{Username: "zero-sbx-test", SID: sid}, createdByRole[role], nil
	}
	applyWindowsACLPlanFn = func(WindowsACLPlan) (func() error, error) {
		return func() error { return nil }, nil
	}
	removeWindowsSandboxPrincipalForSetupFn = func(_ WindowsSandboxCommandConfig, role windowsSandboxRole) error {
		*removed = append(*removed, role)
		return nil
	}
}

func windowsSandboxTestConfig() WindowsSandboxCommandConfig {
	return WindowsSandboxCommandConfig{
		SandboxHome:    `C:\sandboxhome`,
		CommandCWD:     `C:\ws`,
		WorkspaceRoots: []string{`C:\ws`},
		PermissionProfile: PermissionProfile{
			FileSystem: FileSystemPolicy{
				Kind:       FileSystemRestricted,
				WriteRoots: []WritableRoot{{Root: `C:\ws`}},
			},
		},
	}
}

func assertRolesEqual(t *testing.T, got []windowsSandboxRole, want []windowsSandboxRole) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("removed %v, want %v", got, want)
	}
	seen := map[windowsSandboxRole]bool{}
	for _, role := range got {
		seen[role] = true
	}
	for _, role := range want {
		if !seen[role] {
			t.Fatalf("removed %v, want %v", got, want)
		}
	}
}
