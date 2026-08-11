package sandbox

import (
	"strings"
	"testing"
)

// runtimeRootTestConfig is the shape every command reaches the Windows runner
// with: a restricted filesystem rooted at the workspace, which is what makes the
// runtime root necessary in the first place.
func runtimeRootTestConfig(t *testing.T) WindowsSandboxCommandConfig {
	t.Helper()
	workspace := t.TempDir()
	return WindowsSandboxCommandConfig{
		SandboxHome:    t.TempDir(),
		CommandCWD:     workspace,
		WorkspaceRoots: []string{workspace},
		PermissionProfile: PermissionProfile{
			FileSystem: FileSystemPolicy{
				Kind:       FileSystemRestricted,
				WriteRoots: []WritableRoot{{Root: workspace}},
			},
			Network: NetworkPolicy{Mode: NetworkDeny},
		},
	}
}

// A marker written by a fresh setup must accept the ordinary command, and the
// ordinary command is the RUNTIME-AUGMENTED one: Engine.run calls
// permissionProfileWithRuntime before the Windows runner ever sees the profile,
// so the profile presented at validation always carries the selected runtime
// root as an extra write root.
//
// Setup used to fingerprint the bare profile. The extra write root changed the
// ACL plan, the plan hash changed with it, and validation rejected a marker
// written seconds earlier with "permission roots or deny lists changed" — so on
// a restricted filesystem no command could run at all, including the very
// command that had just been set up for.
//
// Asserted for BOTH candidates because which one a process selects is not fixed:
// sandboxRuntimeRootFor prefers the cache-derived root and falls back to the
// temp-derived one, and a marker that only accepts the preferred root bricks
// every machine that falls back.
func TestWindowsSandboxSetupMarkerAcceptsRuntimeAugmentedCommand(t *testing.T) {
	config := runtimeRootTestConfig(t)
	setup := WindowsSandboxSetupConfigFromCommand(config)
	if _, err := WriteWindowsSandboxSetupMarker(setup); err != nil {
		t.Fatalf("WriteWindowsSandboxSetupMarker: %v", err)
	}

	candidates := windowsSandboxRuntimeCandidates(config.WorkspaceRoots)
	if len(candidates) == 0 {
		t.Fatal("windowsSandboxRuntimeCandidates returned none, so this test proves nothing")
	}
	for _, candidate := range candidates {
		augmented := config
		augmented.PermissionProfile = permissionProfileWithRuntime(
			config.PermissionProfile,
			SandboxRuntime{Root: candidate},
		)
		err := ValidateWindowsSandboxSetupMarker(WindowsSandboxSetupConfigFromCommand(augmented))
		if err != nil {
			t.Fatalf("ValidateWindowsSandboxSetupMarker with runtime root %s: %v", candidate, err)
		}
	}

	// The guard has to still bite, or the test above passes for the wrong reason
	// — a validator that accepts everything would satisfy it too.
	changed := config
	changed.PermissionProfile.FileSystem.DenyRead = []string{`C:\workspace\secret`}
	if err := ValidateWindowsSandboxSetupMarker(WindowsSandboxSetupConfigFromCommand(changed)); err == nil {
		t.Fatal("ValidateWindowsSandboxSetupMarker accepted a changed deny list, so it no longer detects drift")
	} else if !strings.Contains(err.Error(), "out of date") {
		t.Fatalf("ValidateWindowsSandboxSetupMarker changed error = %v, want out of date", err)
	}
}

// The runtime root needs BOTH sides of the write-restricted grant.
//
// A principal command runs on a token restricted to the capability SIDs, and a
// WRITE_RESTRICTED token grants a write only when the normal token check AND the
// restricting-SID check both pass. The runtime root used to be appended to the
// principal plan alone, so it carried the account ACE and no capability ACE: the
// normal check passed, the restricted check found nothing, and every cache and
// temp write was denied even once the marker agreed.
//
// This asserts the capability side, which is the side that was missing. Both
// candidates again, for the same reason as above.
func TestWindowsSandboxRuntimeRootsAreInTheCapabilityPlan(t *testing.T) {
	config := runtimeRootTestConfig(t)
	candidates := windowsSandboxRuntimeCandidates(config.WorkspaceRoots)
	if len(candidates) == 0 {
		t.Fatal("windowsSandboxRuntimeCandidates returned none, so this test proves nothing")
	}

	setup := WindowsSandboxSetupConfigFromCommand(config)
	plan, err := BuildWindowsACLPlan(setup.commandConfig())
	if err != nil {
		t.Fatalf("BuildWindowsACLPlan: %v", err)
	}
	granted := make(map[string]struct{}, len(plan.Entries))
	for _, entry := range plan.Entries {
		granted[windowsCapabilityPathKey(entry.Path)] = struct{}{}
	}
	for _, candidate := range candidates {
		if _, ok := granted[windowsCapabilityPathKey(candidate)]; !ok {
			t.Fatalf("capability ACL plan has no entry for runtime root %s; writes there fail the restricting-SID check", candidate)
		}
	}
}

// Both candidates are pure functions of the workspace root. Setup provisions the
// set and a later command selects from it in a different process, so a candidate
// that varied per process (a random or time-seeded fallback) would be granted by
// setup and never selected, or selected and never granted.
func TestWindowsSandboxRuntimeCandidatesAreDeterministic(t *testing.T) {
	config := runtimeRootTestConfig(t)
	first := windowsSandboxRuntimeCandidates(config.WorkspaceRoots)
	if len(first) == 0 {
		t.Fatal("windowsSandboxRuntimeCandidates returned none, so this test proves nothing")
	}
	second := windowsSandboxRuntimeCandidates(config.WorkspaceRoots)
	if len(first) != len(second) {
		t.Fatalf("candidate count = %d then %d, want stable", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("candidate %d = %q then %q, want stable", i, first[i], second[i])
		}
	}

	other := runtimeRootTestConfig(t)
	otherCandidates := windowsSandboxRuntimeCandidates(other.WorkspaceRoots)
	for _, candidate := range otherCandidates {
		for _, mine := range first {
			if candidate == mine {
				t.Fatalf("workspaces %s and %s share runtime root %s, so one workspace's grant covers the other",
					config.CommandCWD, other.CommandCWD, candidate)
			}
		}
	}
}
