package sandbox

import (
	"path/filepath"
	"strings"
	"testing"
)

// THE STRICT TOKEN MUST CARRY THE GRANT ITS READ ROOTS NAME.
//
// A profile with DenyRead drops WRITE_RESTRICTED, which puts reads under the
// restricted-SID check as well. Everyone used to satisfy that check, and taking
// it away without putting the read capability in its place leaves a token that
// cannot open cmd.exe: the command dies at launch with an access denial that
// says nothing about the profile.
//
// The ACL plan grants this same capability on every read root, so the two halves
// have to be decided together. This pins the token half; the plan half is pinned
// by the plan tests, and the kernel behaviour by the impersonation test beside
// this one.
func TestTheStrictTokenCarriesTheReadCapability(t *testing.T) {
	home := t.TempDir()
	want, err := WindowsReadAllowSID(home)
	if err != nil {
		t.Fatalf("SETUP INVALID: no read capability for %s: %v", home, err)
	}

	base := []string{"S-1-15-3-1024-workspace"}
	got, err := windowsRestrictedTokenSIDsForProfile(base, home, false)
	if err != nil {
		t.Fatalf("windowsRestrictedTokenSIDsForProfile: %v", err)
	}
	if !containsSID(got, want) {
		t.Fatalf("the strict token's restricted SIDs %v omit the read capability %s, so the command cannot open its own executable", got, want)
	}
	if !containsSID(got, base[0]) {
		t.Fatalf("the workspace capability was dropped: %v", got)
	}
}

// And the WRITE_RESTRICTED token does not get it: reads are already unrestricted
// there, so adding a SID would widen the write jail for nothing.
func TestTheWriteRestrictedTokenDoesNotCarryTheReadCapability(t *testing.T) {
	home := t.TempDir()
	readSID, err := WindowsReadAllowSID(home)
	if err != nil {
		t.Fatalf("SETUP INVALID: no read capability for %s: %v", home, err)
	}
	got, err := windowsRestrictedTokenSIDsForProfile([]string{"S-1-15-3-1024-workspace"}, home, true)
	if err != nil {
		t.Fatalf("windowsRestrictedTokenSIDsForProfile: %v", err)
	}
	if containsSID(got, readSID) {
		t.Fatalf("the WRITE_RESTRICTED token carries the read capability %s, widening the write jail to every read root", readSID)
	}
}

func containsSID(values []string, want string) bool {
	for _, value := range values {
		if strings.EqualFold(value, want) {
			return true
		}
	}
	return false
}

// THE PLAN AND THE TOKEN DECIDE THE READ CAPABILITY SEPARATELY.
//
// The ACL plan asks whether the profile configures DenyRead; the runner asks
// whether the token keeps WRITE_RESTRICTED. Both are read off the same field
// today, in two files, with nothing tying them together. Drift either way is
// silent and bad: a token carrying a SID no DACL names cannot open its own
// executable, and a plan granting a SID no token carries leaves reads confined
// by nothing.
//
// So this pins the equivalence rather than either half.
func TestThePlanAndTheTokenAgreeOnTheReadCapability(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		denyRead []string
	}{
		{name: "with denyRead", denyRead: []string{filepath.Join("secrets")}},
		{name: "without denyRead"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			workspace := t.TempDir()
			config := WindowsSandboxCommandConfig{
				SandboxHome:    t.TempDir(),
				CommandCWD:     workspace,
				WorkspaceRoots: []string{workspace},
				PermissionProfile: PermissionProfile{
					FileSystem: FileSystemPolicy{
						Kind:       FileSystemRestricted,
						WriteRoots: []WritableRoot{{Root: workspace}},
						ReadRoots:  []string{workspace},
						DenyRead:   prefixEach(workspace, testCase.denyRead),
					},
				},
			}

			planned, err := windowsReadAllowCapabilitySID(config)
			if err != nil {
				t.Fatalf("windowsReadAllowCapabilitySID: %v", err)
			}
			writeRestricted := len(config.PermissionProfile.FileSystem.DenyRead) == 0
			tokenSIDs, err := windowsRestrictedTokenSIDsForProfile(nil, config.SandboxHome, writeRestricted)
			if err != nil {
				t.Fatalf("windowsRestrictedTokenSIDsForProfile: %v", err)
			}

			inPlan := planned != ""
			inToken := len(tokenSIDs) > 0
			if inPlan != inToken {
				t.Fatalf("the plan grants the read capability = %t but the token carries it = %t; one side confines reads the other side never checks", inPlan, inToken)
			}
			if inPlan && !containsSID(tokenSIDs, planned) {
				t.Fatalf("the plan grants %s but the token carries %v, so the command cannot read what setup allowed", planned, tokenSIDs)
			}
		})
	}
}

func prefixEach(root string, relatives []string) []string {
	if len(relatives) == 0 {
		return nil
	}
	out := make([]string, 0, len(relatives))
	for _, relative := range relatives {
		out = append(out, filepath.Join(root, relative))
	}
	return out
}
