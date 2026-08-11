package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildWindowsACLPlanForWorkspaceWriteProfile(t *testing.T) {
	home := t.TempDir()
	config := WindowsSandboxCommandConfig{
		SandboxHome:    home,
		WorkspaceRoots: []string{`C:\workspace`},
		PermissionProfile: PermissionProfile{
			FileSystem: FileSystemPolicy{
				Kind: FileSystemRestricted,
				WriteRoots: []WritableRoot{
					{
						Root:                   `C:\workspace`,
						ReadOnlySubpaths:       []string{`C:\workspace\vendor`},
						ProtectedMetadataNames: []string{".git", ".zero"},
					},
					{Root: `D:\cache`},
				},
				DenyRead:  []string{`C:\workspace\secret-read`},
				DenyWrite: []string{`C:\workspace\secret-write`},
			},
			Network: NetworkPolicy{Mode: NetworkDeny},
		},
	}

	plan, err := BuildWindowsACLPlan(config)
	if err != nil {
		t.Fatalf("BuildWindowsACLPlan: %v", err)
	}
	workspaceSID, err := WindowsWorkspaceCapabilitySID(home, `c:/workspace`)
	if err != nil {
		t.Fatalf("WindowsWorkspaceCapabilitySID: %v", err)
	}
	cacheSID, err := WindowsWritableRootCapabilitySID(home, `d:/cache`)
	if err != nil {
		t.Fatalf("WindowsWritableRootCapabilitySID: %v", err)
	}

	assertWindowsACLEntry(t, plan, WindowsACLAllowWrite, `C:\workspace`, workspaceSID, false)
	assertWindowsACLEntry(t, plan, WindowsACLAllowWrite, `D:\cache`, cacheSID, false)
	assertWindowsACLEntry(t, plan, WindowsACLDenyWrite, `C:\workspace\vendor`, workspaceSID, false)
	assertWindowsACLEntry(t, plan, WindowsACLDenyWrite, `C:\workspace\.git`, workspaceSID, false)
	assertWindowsACLEntry(t, plan, WindowsACLDenyWrite, `C:\workspace\.zero`, workspaceSID, false)
	assertWindowsACLEntry(t, plan, WindowsACLDenyWrite, `C:\workspace\secret-write`, workspaceSID, false)
	assertWindowsACLEntry(t, plan, WindowsACLDenyWrite, `C:\workspace\secret-write`, cacheSID, false)
	assertWindowsACLEntry(t, plan, WindowsACLDenyRead, `C:\workspace\secret-read`, workspaceSID, true)
	assertWindowsACLEntry(t, plan, WindowsACLDenyRead, `C:\workspace\secret-read`, cacheSID, true)
}

func TestBuildWindowsACLPlanUsesReadOnlySIDWithoutWriteRoots(t *testing.T) {
	home := t.TempDir()
	caps, err := LoadOrCreateWindowsCapabilitySIDs(home)
	if err != nil {
		t.Fatalf("LoadOrCreateWindowsCapabilitySIDs: %v", err)
	}
	plan, err := BuildWindowsACLPlan(WindowsSandboxCommandConfig{
		SandboxHome: home,
		PermissionProfile: PermissionProfile{
			FileSystem: FileSystemPolicy{
				Kind:     FileSystemRestricted,
				DenyRead: []string{`C:\workspace\secret-read`},
			},
			Network: NetworkPolicy{Mode: NetworkDeny},
		},
	})
	if err != nil {
		t.Fatalf("BuildWindowsACLPlan: %v", err)
	}
	// Two deny entries, one per SID the token can carry here. ReadOnly is the
	// only capability a profile with no write roots gets, and ReadAllow is the
	// read grant a DenyRead profile's strict token carries: denying just one of
	// them leaves the carveout readable through the other.
	if len(plan.Entries) != 2 {
		t.Fatalf("ACL entries = %#v, want a deny-read entry per capability SID", plan.Entries)
	}
	assertWindowsACLEntry(t, plan, WindowsACLDenyRead, `C:\workspace\secret-read`, caps.ReadOnly, true)
	readSID, err := WindowsReadAllowSID(home)
	if err != nil {
		t.Fatalf("WindowsReadAllowSID: %v", err)
	}
	assertWindowsACLEntry(t, plan, WindowsACLDenyRead, `C:\workspace\secret-read`, readSID, true)
}

func TestBuildWindowsACLPlanRejectsUnrestrictedProfiles(t *testing.T) {
	_, err := BuildWindowsACLPlan(WindowsSandboxCommandConfig{
		SandboxHome: t.TempDir(),
		PermissionProfile: PermissionProfile{
			FileSystem: FileSystemPolicy{Kind: FileSystemUnrestricted},
			Network:    NetworkPolicy{Mode: NetworkAllow},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "restricted filesystem") {
		t.Fatalf("BuildWindowsACLPlan error = %v, want restricted filesystem error", err)
	}
}

func TestPlanWindowsDenyReadPathsIncludesCanonicalExistingPath(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	wantRealDir, err := filepath.EvalSymlinks(realDir)
	if err != nil {
		t.Fatalf("EvalSymlinks real dir: %v", err)
	}
	linkDir := filepath.Join(root, "link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	paths := planWindowsDenyReadPaths([]string{linkDir})
	if !windowsPathListContains(paths, linkDir) {
		t.Fatalf("deny-read paths = %#v, want lexical path %q", paths, linkDir)
	}
	if !windowsPathListContains(paths, wantRealDir) {
		t.Fatalf("deny-read paths = %#v, want canonical path %q", paths, wantRealDir)
	}
}

func assertWindowsACLEntry(t *testing.T, plan WindowsACLPlan, action WindowsACLAction, path string, capability string, materialize bool) {
	t.Helper()
	for _, entry := range plan.Entries {
		if entry.Action == action &&
			windowsCapabilityPathKey(entry.Path) == windowsCapabilityPathKey(path) &&
			strings.EqualFold(entry.Capability, capability) &&
			entry.Materialize == materialize {
			return
		}
	}
	t.Fatalf("ACL entries = %#v, want %s %q capability %q materialize=%v", plan.Entries, action, path, capability, materialize)
}

func windowsPathListContains(paths []string, want string) bool {
	wantKey := windowsCapabilityPathKey(want)
	for _, path := range paths {
		if windowsCapabilityPathKey(path) == wantKey {
			return true
		}
	}
	return false
}
