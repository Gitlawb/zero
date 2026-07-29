//go:build windows

package sandbox

import (
	"path/filepath"
	"strings"
	"testing"
)

// Policy deny-write has to reach the principal plan.
//
// The capability plan has always emitted these. The principal plan denied write
// only on protected metadata and read-only subpaths inside write roots, so once
// the runner used a principal token a policy deny sitting anywhere else was not
// enforced at the OS layer at all, and a shell child could write where the
// restricted-token backend would have stopped it.
func TestPrincipalACLPlanCarriesPolicyDenyWrite(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	denied := filepath.Join(root, "protected", "keep-out")

	plan, err := buildWindowsPrincipalACLPlan(windowsPrincipalACLInput{
		PrincipalSID: "S-1-5-21-0-0-0-1001",
		WriteRoots:   []WritableRoot{{Root: root}},
		DenyWrite:    []string{denied},
	})
	if err != nil {
		t.Fatalf("buildWindowsPrincipalACLPlan: %v", err)
	}

	entry, ok := findPrincipalACLEntry(plan, WindowsACLDenyWrite, denied)
	if !ok {
		t.Fatalf("no deny-write ACE for the policy path; plan = %+v", plan.Entries)
	}
	// Materialized for the same reason the capability plan does it: the applier
	// skips targets that do not exist, so a deny on a path created after setup
	// would never be written.
	if !entry.Materialize {
		t.Error("policy deny-write ACE is not materialized, so it is skipped when the path does not exist yet")
	}
}

// Deny-read has to be materialized too, which it was not.
func TestPrincipalACLPlanMaterializesDenyRead(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	secret := filepath.Join(t.TempDir(), "elsewhere", "creds")

	plan, err := buildWindowsPrincipalACLPlan(windowsPrincipalACLInput{
		PrincipalSID: "S-1-5-21-0-0-0-1001",
		WriteRoots:   []WritableRoot{{Root: root}},
		DenyRead:     []string{secret},
	})
	if err != nil {
		t.Fatalf("buildWindowsPrincipalACLPlan: %v", err)
	}
	entry, ok := findPrincipalACLEntry(plan, WindowsACLDenyRead, secret)
	if !ok {
		t.Fatalf("no deny-read ACE emitted; plan = %+v", plan.Entries)
	}
	if !entry.Materialize {
		t.Fatal("deny-read ACE is not materialized, so a path created after setup never gets one")
	}
}

// Deny entries must still precede the grants they carve out of, which is what
// makes them win under Windows DACL evaluation. Adding deny-write to the plan is
// only safe if it did not disturb that ordering.
func TestPrincipalACLPlanKeepsDeniesBeforeGrants(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	plan, err := buildWindowsPrincipalACLPlan(windowsPrincipalACLInput{
		PrincipalSID: "S-1-5-21-0-0-0-1001",
		WriteRoots:   []WritableRoot{{Root: root}},
		DenyWrite:    []string{filepath.Join(root, "nope")},
		DenyRead:     []string{filepath.Join(root, "secret")},
	})
	if err != nil {
		t.Fatalf("buildWindowsPrincipalACLPlan: %v", err)
	}
	firstGrant := -1
	for i, entry := range plan.Entries {
		switch entry.Action {
		case WindowsACLAllowWrite, WindowsACLAllowRead:
			if firstGrant == -1 {
				firstGrant = i
			}
		case WindowsACLDenyRead, WindowsACLDenyWrite:
			if firstGrant != -1 {
				t.Fatalf("deny entry at %d follows a grant at %d; the grant would win", i, firstGrant)
			}
		}
	}
}

func findPrincipalACLEntry(plan WindowsACLPlan, action WindowsACLAction, path string) (WindowsACLEntry, bool) {
	want := normalizeProfilePath(path)
	for _, entry := range plan.Entries {
		if entry.Action == action && entry.Path == want {
			return entry, true
		}
	}
	return WindowsACLEntry{}, false
}

// An adopted account must not have its password rotated during provisioning.
//
// Rotating there left every later step running against an account whose password
// had already been replaced with nothing on disk holding it. Any failure in
// between stranded a working principal: the rollback correctly declined to
// delete an account it had not created, so what remained was a live account
// authenticated by a password no longer stored anywhere, and the command path
// read the absent secret as "not provisioned" and quietly fell back to the
// weaker backend.
func TestProvisionWindowsSandboxIdentityDefersPasswordRotation(t *testing.T) {
	stubWindowsProvisioning(t, true, nil, nil)

	rotated := false
	previous := resetWindowsSandboxUserPasswordFn
	t.Cleanup(func() { resetWindowsSandboxUserPasswordFn = previous })
	resetWindowsSandboxUserPasswordFn = func(string, string) error {
		rotated = true
		return nil
	}

	if _, _, _, err := provisionWindowsSandboxIdentity("workspacekey"); err != nil {
		t.Fatalf("provisioning an adopted account: %v", err)
	}
	if rotated {
		t.Fatal("provisioning rotated the password; the window this closes lasts until the secret is committed")
	}
}

// The ownership comment carries the full workspace key, so two workspaces whose
// digests collide in the 11 characters the account name can hold are refused
// rather than silently sharing one account, one secret and one ACL identity.
func TestWindowsSandboxUserCommentDistinguishesWorkspaces(t *testing.T) {
	first := windowsSandboxUserCommentFor("aaaaaaaaaaaabbbbbbbb")
	second := windowsSandboxUserCommentFor("aaaaaaaaaaaacccccccc")
	if first == second {
		t.Fatal("two workspaces produced the same ownership comment, so a name collision would be adopted")
	}
	if !strings.HasPrefix(first, windowsSandboxUserComment) {
		t.Fatalf("comment %q lost the marker prefix that identifies it as ours", first)
	}
	// The names DO collide, which is the whole reason the comment has to carry
	// the key. If this stops being true the test is no longer covering anything.
	if windowsSandboxUserName("aaaaaaaaaaaabbbbbbbb") != windowsSandboxUserName("aaaaaaaaaaaacccccccc") {
		t.Skip("account names no longer collide for these keys; revisit what this test is for")
	}
}
