//go:build windows

package sandbox

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// denyReadSetupConfig builds the production shape: read everywhere, and a
// denyRead that selects the fully restricted token.
func denyReadSetupConfig(t *testing.T) WindowsSandboxSetupConfig {
	t.Helper()
	workspace := t.TempDir()
	volumeRoot := filepath.VolumeName(workspace) + string(filepath.Separator)
	if !isWindowsVolumeRoot(volumeRoot) {
		t.Fatalf("SETUP INVALID: %q is not recognised as a volume root", volumeRoot)
	}
	return WindowsSandboxSetupConfig{
		SandboxHome:    t.TempDir(),
		WorkspaceRoots: []string{workspace},
		PermissionProfile: PermissionProfile{
			FileSystem: FileSystemPolicy{
				Kind:       FileSystemRestricted,
				WriteRoots: []WritableRoot{{Root: workspace}},
				ReadRoots:  []string{volumeRoot},
				DenyRead:   []string{filepath.Join(workspace, "secrets")},
			},
		},
	}
}

// ELEVATED SETUP CAN WRITE A VOLUME ROOT'S DACL, WHICH IS WHY IT MUST NOT.
//
// A profile that configures denyRead selects a fully restricted token, and that
// token applies its restricted-SID check to reads, so the plan grants the read
// capability at every read root. Production profiles seed ReadRoots with the bare
// filesystem root.
//
// The applier marks allow entries on a directory inheritable and calls
// SetSecurityInfo, and Windows propagates inheritable ACEs onto existing
// children. So applying that one entry is not a change to a sandbox-owned
// object: it walks and rewrites DACL inheritance across unrelated system,
// application and user trees on the drive, with the result depending on which
// descendants happen to be locked or exclusively open.
//
// It is not even sufficient afterwards. A bare root resolves on one volume, and
// the executables and libraries a command needs can live on another without
// being read roots of their own, so the strict token can still fail before its
// executable starts.
//
// The unelevated tier already refused, for the narrower reason that it lacks the
// rights. This pins the tier that HAS the rights, and pins that it refuses BEFORE
// the first mutation.
func TestElevatedSetupRefusesAVolumeRootReadGrant(t *testing.T) {
	config := denyReadSetupConfig(t)

	// SETUP: the plan really reaches the volume root, or the refusal below asserts
	// nothing.
	plan, err := BuildWindowsACLPlan(config.commandConfig())
	if err != nil {
		t.Fatalf("BuildWindowsACLPlan: %v", err)
	}
	if got := windowsPlanVolumeRootGrant(plan); got == "" {
		t.Fatalf("SETUP INVALID: this profile carries no volume-root entry: %+v", plan.Entries)
	}

	previousElevated := windowsProcessIsElevatedFn
	previousApply := applyWindowsACLPlanFn
	t.Cleanup(func() {
		windowsProcessIsElevatedFn = previousElevated
		applyWindowsACLPlanFn = previousApply
	})
	windowsProcessIsElevatedFn = func() bool { return true }
	applied := false
	applyWindowsACLPlanFn = func(WindowsACLPlan) (func() error, error) {
		applied = true
		return func() error { return nil }, nil
	}

	var stderr bytes.Buffer
	if code := runWindowsSandboxSetup(config, &stderr); code == 0 {
		t.Fatal("elevated setup accepted a plan that rewrites DACL inheritance across the whole volume")
	}
	if applied {
		t.Fatal("the plan was applied before the refusal, so the volume-wide edit already happened")
	}
	message := stderr.String()
	for _, want := range []string{"denyRead", "volume root", "#869"} {
		if !strings.Contains(message, want) {
			t.Errorf("the refusal does not mention %q, so the reader cannot act on it:\n%s", want, message)
		}
	}
}

// And a profile with no denyRead still sets up: it needs no volume-root grant, so
// refusing it would take the Windows sandbox away from everyone.
func TestElevatedSetupStillAcceptsAWriteJailOnlyProfile(t *testing.T) {
	workspace := t.TempDir()
	config := WindowsSandboxSetupConfig{
		SandboxHome:    t.TempDir(),
		WorkspaceRoots: []string{workspace},
		PermissionProfile: PermissionProfile{
			FileSystem: FileSystemPolicy{
				Kind:       FileSystemRestricted,
				WriteRoots: []WritableRoot{{Root: workspace}},
				ReadRoots:  []string{workspace},
			},
		},
	}
	plan, err := BuildWindowsACLPlan(config.commandConfig())
	if err != nil {
		t.Fatalf("BuildWindowsACLPlan: %v", err)
	}
	if refusal := WindowsACLPlanVolumeRootRefusal(plan); refusal != "" {
		t.Fatalf("a write-jail-only profile was refused: %s", refusal)
	}
}
