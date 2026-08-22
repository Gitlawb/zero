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
		SandboxLevel:   WindowsSandboxLevelRestrictedToken,
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

	// SID broadening is disabled, so the plan must not stamp shared system-path
	// DenyWrite ACEs or revoke legacy capability SIDs. Revocation could weaken
	// the boundary of a command launched by an earlier build.
	assertNoSharedSystemDenyWrites(t, plan)
	assertNoWindowsACLRevokes(t, plan)
}

// TestBuildWindowsACLPlanOmitsSharedDenyPathsWithoutDenyRead pins that
// profiles without DenyRead never stamp shared system-path DenyWrite ACEs or
// revoke old capability-SID guards that a running sandbox may still require.
func TestBuildWindowsACLPlanOmitsSharedDenyPathsWithoutDenyRead(t *testing.T) {
	home := t.TempDir()
	plan, err := BuildWindowsACLPlan(WindowsSandboxCommandConfig{
		SandboxHome:    home,
		WorkspaceRoots: []string{`C:\workspace`},
		SandboxLevel:   WindowsSandboxLevelRestrictedToken,
		PermissionProfile: PermissionProfile{
			FileSystem: FileSystemPolicy{
				Kind:       FileSystemRestricted,
				WriteRoots: []WritableRoot{{Root: `C:\workspace`}},
			},
			Network: NetworkPolicy{Mode: NetworkDeny},
		},
	})
	if err != nil {
		t.Fatalf("BuildWindowsACLPlan: %v", err)
	}
	assertNoSharedSystemDenyWrites(t, plan)
	assertNoWindowsACLRevokes(t, plan)
}

// TestBuildWindowsACLPlanOmitsSharedDenyPathsWhenUnelevated pins that the
// unelevated tier never stamps shared system-path DenyWrite ACEs (it also
// never broadens the restricted-SID list).
func TestBuildWindowsACLPlanOmitsSharedDenyPathsWhenUnelevated(t *testing.T) {
	home := t.TempDir()
	plan, err := BuildWindowsACLPlan(WindowsSandboxCommandConfig{
		SandboxHome:    home,
		WorkspaceRoots: []string{`C:\workspace`},
		SandboxLevel:   WindowsSandboxLevelUnelevated,
		PermissionProfile: PermissionProfile{
			FileSystem: FileSystemPolicy{
				Kind:       FileSystemRestricted,
				WriteRoots: []WritableRoot{{Root: `C:\workspace`}},
				DenyRead:   []string{`C:\workspace\secret`},
			},
			Network: NetworkPolicy{Mode: NetworkDeny},
		},
	})
	if err != nil {
		t.Fatalf("BuildWindowsACLPlan: %v", err)
	}
	assertNoSharedSystemDenyWrites(t, plan)
	for _, entry := range plan.Entries {
		if entry.Action == WindowsACLRevokeCapability {
			t.Fatalf("unelevated plan = %#v, want no WindowsACLRevokeCapability entry", plan.Entries)
		}
	}
}

// windowsSharedDenyPathsForTest calls the same trusted-path resolution
// BuildWindowsACLPlan itself uses, rather than reimplementing the
// resolution logic independently, so this test cannot silently drift out of
// sync with (or mask a regression in) the production resolver.
func windowsSharedDenyPathsForTest(t *testing.T) (systemDrive, systemRoot, programData, publicDir string) {
	t.Helper()
	systemDrive, systemRoot, programData, publicDir, err := resolveWindowsSharedDenyPaths()
	if err != nil {
		t.Fatalf("resolveWindowsSharedDenyPaths: %v", err)
	}
	return systemDrive, systemRoot, programData, publicDir
}

func TestBuildWindowsACLPlanUsesReadOnlySIDWithoutWriteRoots(t *testing.T) {
	home := t.TempDir()
	caps, err := LoadOrCreateWindowsCapabilitySIDs(home)
	if err != nil {
		t.Fatalf("LoadOrCreateWindowsCapabilitySIDs: %v", err)
	}
	plan, err := BuildWindowsACLPlan(WindowsSandboxCommandConfig{
		SandboxHome:  home,
		SandboxLevel: WindowsSandboxLevelRestrictedToken,
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
	// Without write roots there is nothing to revoke; without SID broadening
	// there are no shared system-path DenyWrite entries either.
	if len(plan.Entries) != 1 {
		t.Fatalf("ACL entries = %#v, want one deny-read entry", plan.Entries)
	}
	assertWindowsACLEntry(t, plan, WindowsACLDenyRead, `C:\workspace\secret-read`, caps.ReadOnly, true)
	assertNoSharedSystemDenyWrites(t, plan)
}

// TestBuildWindowsACLPlanDisablesSharedDenyPathDescendantScan pins that SID
// broadening is off: the plan must not request ScanDescendants or shared-root
// DenyWrite entries that only existed to compensate for Users/AuthUsers SIDs.
func TestBuildWindowsACLPlanDisablesSharedDenyPathDescendantScan(t *testing.T) {
	home := t.TempDir()
	plan, err := BuildWindowsACLPlan(WindowsSandboxCommandConfig{
		SandboxHome:    home,
		WorkspaceRoots: []string{`C:\workspace`},
		SandboxLevel:   WindowsSandboxLevelRestrictedToken,
		PermissionProfile: PermissionProfile{
			FileSystem: FileSystemPolicy{
				Kind:       FileSystemRestricted,
				WriteRoots: []WritableRoot{{Root: `C:\workspace`}},
				DenyRead:   []string{`C:\workspace\secret-read`},
				DenyWrite:  []string{`C:\workspace\secret-write`},
			},
			Network: NetworkPolicy{Mode: NetworkDeny},
		},
	})
	if err != nil {
		t.Fatalf("BuildWindowsACLPlan: %v", err)
	}
	assertNoSharedSystemDenyWrites(t, plan)
	for _, entry := range plan.Entries {
		if entry.ScanDescendants {
			t.Fatalf("plan entry %#v requests descendant scan; shared DenyWrite compensation is disabled", entry)
		}
	}
}

// TestBuildWindowsACLPlanDoesNotRevokeLegacyGuards pins that a setup run does
// not remove persistent guards installed by an older build. A previously
// launched sandbox can still carry the legacy capability SID, so removing its
// deny would widen that process's access.
func TestBuildWindowsACLPlanDoesNotRevokeLegacyGuards(t *testing.T) {
	home := t.TempDir()
	plan, err := BuildWindowsACLPlan(WindowsSandboxCommandConfig{
		SandboxHome:    home,
		WorkspaceRoots: []string{`C:\workspace`},
		SandboxLevel:   WindowsSandboxLevelRestrictedToken,
		PermissionProfile: PermissionProfile{
			FileSystem: FileSystemPolicy{
				Kind:       FileSystemRestricted,
				WriteRoots: []WritableRoot{{Root: `C:\workspace`}},
			},
			Network: NetworkPolicy{Mode: NetworkDeny},
		},
	})
	if err != nil {
		t.Fatalf("BuildWindowsACLPlan: %v", err)
	}
	assertNoWindowsACLRevokes(t, plan)
}

func assertNoSharedSystemDenyWrites(t *testing.T, plan WindowsACLPlan) {
	t.Helper()
	systemDrive, systemRoot, programData, publicDir := windowsSharedDenyPathsForTest(t)
	for _, path := range []string{systemDrive + `\`, programData, systemRoot + `\Temp`, publicDir} {
		for _, entry := range plan.Entries {
			if entry.Action == WindowsACLDenyWrite && windowsCapabilityPathKey(entry.Path) == windowsCapabilityPathKey(path) {
				t.Fatalf("plan stamps shared system DenyWrite on %q = %#v; SID broadening is disabled so shared denies must not be planned", path, entry)
			}
		}
	}
}

func assertNoWindowsACLRevokes(t *testing.T, plan WindowsACLPlan) {
	t.Helper()
	for _, entry := range plan.Entries {
		if entry.Action == WindowsACLRevokeCapability {
			t.Fatalf("plan = %#v, want no WindowsACLRevokeCapability entries", plan.Entries)
		}
	}
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
	assertWindowsACLEntryInheritance(t, plan, action, path, capability, materialize, false)
}

func assertWindowsACLEntryInheritance(t *testing.T, plan WindowsACLPlan, action WindowsACLAction, path string, capability string, materialize bool, noInherit bool) {
	t.Helper()
	for _, entry := range plan.Entries {
		if entry.Action == action &&
			windowsCapabilityPathKey(entry.Path) == windowsCapabilityPathKey(path) &&
			strings.EqualFold(entry.Capability, capability) &&
			entry.Materialize == materialize &&
			entry.NoInherit == noInherit {
			return
		}
	}
	t.Fatalf("ACL entries = %#v, want %s %q capability %q materialize=%v noInherit=%v", plan.Entries, action, path, capability, materialize, noInherit)
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

// TestDedupeWindowsACLEntriesKeepsInheritanceVariants pins NoInherit as part
// of the entry identity: a direct-only deny and an inheritable deny on the
// same path and SID are different ACL shapes, and collapsing them could
// silently promote a deliberately non-inherited shared-path deny into an
// inheritable one that SetNamedSecurityInfo would propagate across a huge
// existing subtree.
func TestDedupeWindowsACLEntriesKeepsInheritanceVariants(t *testing.T) {
	entries := []WindowsACLEntry{
		{Action: WindowsACLDenyWrite, Path: `C:\shared`, Capability: "S-1-5-21-1", NoInherit: true},
		{Action: WindowsACLDenyWrite, Path: `C:\shared`, Capability: "S-1-5-21-1"},
		{Action: WindowsACLDenyWrite, Path: `C:\shared`, Capability: "S-1-5-21-1", NoInherit: true},
	}
	out := dedupeWindowsACLEntries(entries)
	if len(out) != 2 {
		t.Fatalf("dedupe = %#v, want the NoInherit and inheritable variants kept distinct", out)
	}
	if !out[0].NoInherit || out[1].NoInherit {
		t.Fatalf("dedupe order/shape = %#v, want first NoInherit then inheritable", out)
	}
}
