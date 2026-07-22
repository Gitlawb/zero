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

	caps, err := LoadOrCreateWindowsCapabilitySIDs(home)
	if err != nil {
		t.Fatalf("LoadOrCreateWindowsCapabilitySIDs: %v", err)
	}
	systemDrive, systemRoot, programData, publicDir := windowsSharedDenyPathsForTest(t)

	// The shared system-path denies name only the one stable read-only SID
	// that every broadened token carries. Naming the per-workspace/per-root
	// SIDs here instead would append four permanent deny ACEs to these
	// machine-wide DACLs for every distinct project ever sandboxed.
	for _, path := range []string{systemDrive + `\`, programData, systemRoot + `\Temp`, publicDir} {
		assertWindowsACLEntryInheritance(t, plan, WindowsACLDenyWrite, path, caps.ReadOnly, false, true)
		for _, entry := range plan.Entries {
			if entry.Action != WindowsACLDenyWrite || windowsCapabilityPathKey(entry.Path) != windowsCapabilityPathKey(path) {
				continue
			}
			if entry.Capability == workspaceSID || entry.Capability == cacheSID {
				t.Fatalf("shared deny path %q names per-root SID %q; machine DACLs must only carry the stable read-only SID", path, entry.Capability)
			}
		}
	}
}

// TestBuildWindowsACLPlanOmitsSharedDenyPathsWithoutDenyRead pins the
// scoping of the Users/Authenticated Users broadening: profiles without
// DenyRead run under a WRITE_RESTRICTED token, which reads with its normal
// identity and is never broadened, so their plans must not touch the shared
// system-path DACLs at all.
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
	systemDrive, systemRoot, programData, publicDir := windowsSharedDenyPathsForTest(t)
	for _, path := range []string{systemDrive + `\`, programData, systemRoot + `\Temp`, publicDir} {
		for _, entry := range plan.Entries {
			if entry.Action == WindowsACLDenyWrite && windowsCapabilityPathKey(entry.Path) == windowsCapabilityPathKey(path) {
				t.Fatalf("plan without DenyRead touches shared path %q: %#v", path, entry)
			}
		}
	}
}

// TestBuildWindowsACLPlanOmitsSharedDenyPathsWhenUnelevated pins the fix for
// the unelevated tier aborting every sandboxed command: BuildWindowsACLPlan
// must not add DenyWrite entries for C:\, C:\ProgramData, C:\Windows\Temp, or
// C:\Users\Public when SandboxLevel is WindowsSandboxLevelUnelevated, because
// SetNamedSecurityInfo on those system-owned paths requires WRITE_DAC that an
// ordinary (non-Administrator) user does not have. The unelevated tier never
// puts the Users/Authenticated Users SIDs on the token in the first place
// (see createWindowsRestrictedTokenFromBase), so it does not need these
// mitigating entries.
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
			},
			Network: NetworkPolicy{Mode: NetworkDeny},
		},
	})
	if err != nil {
		t.Fatalf("BuildWindowsACLPlan: %v", err)
	}
	systemDrive, systemRoot, programData, publicDir := windowsSharedDenyPathsForTest(t)
	for _, path := range []string{systemDrive + `\`, programData, systemRoot + `\Temp`, publicDir} {
		for _, entry := range plan.Entries {
			if entry.Action == WindowsACLDenyWrite && windowsCapabilityPathKey(entry.Path) == windowsCapabilityPathKey(path) {
				t.Fatalf("unelevated ACL plan = %#v, want no DenyWrite entry for shared path %q", plan.Entries, path)
			}
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
	if len(plan.Entries) != 5 {
		t.Fatalf("ACL entries = %#v, want five entries (1 deny-read, 4 deny-write)", plan.Entries)
	}
	assertWindowsACLEntry(t, plan, WindowsACLDenyRead, `C:\workspace\secret-read`, caps.ReadOnly, true)
	systemDrive, systemRoot, programData, publicDir := windowsSharedDenyPathsForTest(t)
	assertWindowsACLEntryInheritance(t, plan, WindowsACLDenyWrite, systemDrive+`\`, caps.ReadOnly, false, true)
	assertWindowsACLEntryInheritance(t, plan, WindowsACLDenyWrite, programData, caps.ReadOnly, false, true)
	assertWindowsACLEntryInheritance(t, plan, WindowsACLDenyWrite, systemRoot+`\Temp`, caps.ReadOnly, false, true)
	assertWindowsACLEntryInheritance(t, plan, WindowsACLDenyWrite, publicDir, caps.ReadOnly, false, true)
}

// TestBuildWindowsACLPlanMarksSharedDenyPathsForDescendantScan pins that the
// four shared-root DenyWrite entries (and ONLY those) request the apply-time
// existing-writable-descendant scan. That scan is the enforcement that keeps a
// pre-existing writable child of one of those roots (which a non-inherited deny
// on the root object alone does not cover) from becoming a write-jail escape
// once the fully restricted DenyRead token is broadened with the Users and
// Authenticated Users SIDs.
func TestBuildWindowsACLPlanMarksSharedDenyPathsForDescendantScan(t *testing.T) {
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
	systemDrive, systemRoot, programData, publicDir := windowsSharedDenyPathsForTest(t)
	sharedKeys := map[string]bool{}
	for _, path := range []string{systemDrive + `\`, programData, systemRoot + `\Temp`, publicDir} {
		sharedKeys[windowsCapabilityPathKey(path)] = true
	}
	for _, path := range []string{systemDrive + `\`, programData, systemRoot + `\Temp`, publicDir} {
		found := false
		for _, entry := range plan.Entries {
			if entry.Action == WindowsACLDenyWrite && windowsCapabilityPathKey(entry.Path) == windowsCapabilityPathKey(path) && entry.ScanDescendants {
				found = true
			}
		}
		if !found {
			t.Fatalf("shared deny path %q is not marked ScanDescendants; existing writable descendants would stay unenforced", path)
		}
	}
	for _, entry := range plan.Entries {
		if entry.ScanDescendants && !sharedKeys[windowsCapabilityPathKey(entry.Path)] {
			t.Fatalf("non-shared entry %#v requests descendant scan; only the four shared roots should", entry)
		}
	}
}

// TestBuildWindowsACLPlanSkipsSharedDenyPathNestedUnderWriteRoot pins the fix
// for a shared-path deny landing on a configured write root's own descendant:
// if a workspace's write root is (or contains) one of the four shared paths —
// here C:\Users contains the Public shared path — a DenyWrite there would sit
// ahead of that root's Allow for every broadened token and win under Windows'
// deny-before-allow evaluation, jailing a directory the user explicitly
// configured as writable. Only the shared paths NOT nested under any
// configured write root should get the compensating deny.
func TestBuildWindowsACLPlanSkipsSharedDenyPathNestedUnderWriteRoot(t *testing.T) {
	home := t.TempDir()
	systemDrive, systemRoot, programData, publicDir := windowsSharedDenyPathsForTest(t)
	// publicDir is a Windows-style path (backslash-separated) even when this
	// test runs on Linux/macOS, so its parent must be computed with a literal
	// backslash split, not filepath.Dir (which uses the native separator and
	// would treat the whole string as one component on non-Windows GOOS).
	lastSeparator := strings.LastIndex(publicDir, `\`)
	if lastSeparator <= 0 {
		t.Fatalf("test fixture assumes publicDir %q has a parent reachable via a backslash split", publicDir)
	}
	usersRoot := publicDir[:lastSeparator]
	plan, err := BuildWindowsACLPlan(WindowsSandboxCommandConfig{
		SandboxHome:    home,
		WorkspaceRoots: []string{usersRoot},
		SandboxLevel:   WindowsSandboxLevelRestrictedToken,
		PermissionProfile: PermissionProfile{
			FileSystem: FileSystemPolicy{
				Kind:       FileSystemRestricted,
				WriteRoots: []WritableRoot{{Root: usersRoot}},
				DenyRead:   []string{`C:\workspace\secret-read`},
			},
			Network: NetworkPolicy{Mode: NetworkDeny},
		},
	})
	if err != nil {
		t.Fatalf("BuildWindowsACLPlan: %v", err)
	}
	for _, entry := range plan.Entries {
		if entry.Action == WindowsACLDenyWrite && windowsCapabilityPathKey(entry.Path) == windowsCapabilityPathKey(publicDir) {
			t.Fatalf("plan denies write on %q, which is nested under configured write root %q: %#v", publicDir, usersRoot, entry)
		}
	}
	// The other three shared paths are untouched by this write root and must
	// still get their compensating deny.
	for _, path := range []string{systemDrive + `\`, programData, systemRoot + `\Temp`} {
		found := false
		for _, entry := range plan.Entries {
			if entry.Action == WindowsACLDenyWrite && windowsCapabilityPathKey(entry.Path) == windowsCapabilityPathKey(path) {
				found = true
			}
		}
		if !found {
			t.Fatalf("plan = %#v, want shared path %q still denied (unaffected by the %q write root)", plan.Entries, path, usersRoot)
		}
	}
}

// TestBuildWindowsACLPlanSkipsSharedDenyPathExactlyEqualToWriteRoot exercises
// the windowsPathUnderAnyRoot exact-match branch (as opposed to the
// nested-under-a-write-root case above): a write root configured AT one of
// the four shared paths themselves must get its Allow entry with no
// conflicting DenyWrite, or a broadened token could never write there despite
// the user explicitly configuring it as writable.
func TestBuildWindowsACLPlanSkipsSharedDenyPathExactlyEqualToWriteRoot(t *testing.T) {
	home := t.TempDir()
	_, systemRoot, _, _ := windowsSharedDenyPathsForTest(t)
	writeRoot := systemRoot + `\Temp`

	plan, err := BuildWindowsACLPlan(WindowsSandboxCommandConfig{
		SandboxHome:    home,
		WorkspaceRoots: []string{writeRoot},
		SandboxLevel:   WindowsSandboxLevelRestrictedToken,
		PermissionProfile: PermissionProfile{
			FileSystem: FileSystemPolicy{
				Kind:       FileSystemRestricted,
				WriteRoots: []WritableRoot{{Root: writeRoot}},
				DenyRead:   []string{`C:\workspace\secret-read`},
			},
			Network: NetworkPolicy{Mode: NetworkDeny},
		},
	})
	if err != nil {
		t.Fatalf("BuildWindowsACLPlan: %v", err)
	}
	writeRootSID, err := WindowsWorkspaceCapabilitySID(home, writeRoot)
	if err != nil {
		t.Fatalf("WindowsWorkspaceCapabilitySID: %v", err)
	}
	assertWindowsACLEntry(t, plan, WindowsACLAllowWrite, writeRoot, writeRootSID, false)
	for _, entry := range plan.Entries {
		if entry.Action == WindowsACLDenyWrite && windowsCapabilityPathKey(entry.Path) == windowsCapabilityPathKey(writeRoot) {
			t.Fatalf("plan denies write on %q, which IS the configured write root: %#v", writeRoot, entry)
		}
	}
}

// TestBuildWindowsACLPlanRevokesStaleSharedDenyOnPromotedWriteRoot pins the
// fix for jatmn's P2 finding: every write-root path gets an unconditional
// WindowsACLRevokeCapability entry for the stable read-only capability SID,
// so a stale shared/descendant DenyWrite ACE an earlier setup round applied
// there (before it became a write root) does not survive to win over the new
// Allow under Windows' deny-before-allow evaluation. This covers both ways a
// path can have been promoted: it IS one of the four shared paths (Public
// here), or it was a previously discovered writable descendant elsewhere in
// the tree that the user later configured directly as a write root.
func TestBuildWindowsACLPlanRevokesStaleSharedDenyOnPromotedWriteRoot(t *testing.T) {
	home := t.TempDir()
	caps, err := LoadOrCreateWindowsCapabilitySIDs(home)
	if err != nil {
		t.Fatalf("LoadOrCreateWindowsCapabilitySIDs: %v", err)
	}
	systemDrive, _, _, publicDir := windowsSharedDenyPathsForTest(t)
	promotedDescendant := systemDrive + `\Users\shared`

	plan, err := BuildWindowsACLPlan(WindowsSandboxCommandConfig{
		SandboxHome:    home,
		WorkspaceRoots: []string{publicDir, promotedDescendant},
		SandboxLevel:   WindowsSandboxLevelRestrictedToken,
		PermissionProfile: PermissionProfile{
			FileSystem: FileSystemPolicy{
				Kind:       FileSystemRestricted,
				WriteRoots: []WritableRoot{{Root: publicDir}, {Root: promotedDescendant}},
				DenyRead:   []string{`C:\workspace\secret-read`},
			},
			Network: NetworkPolicy{Mode: NetworkDeny},
		},
	})
	if err != nil {
		t.Fatalf("BuildWindowsACLPlan: %v", err)
	}
	for _, root := range []string{publicDir, promotedDescendant} {
		found := false
		for _, entry := range plan.Entries {
			if entry.Action == WindowsACLRevokeCapability &&
				windowsCapabilityPathKey(entry.Path) == windowsCapabilityPathKey(root) &&
				strings.EqualFold(entry.Capability, caps.ReadOnly) {
				found = true
			}
		}
		if !found {
			t.Fatalf("plan = %#v, want a WindowsACLRevokeCapability entry for write root %q naming the stable read-only SID %q", plan.Entries, root, caps.ReadOnly)
		}
	}
}

// TestBuildWindowsACLPlanOmitsRevokeCapabilityWithoutDenyRead pins that the
// reconciliation entry is scoped the same way the shared-path denies
// themselves are: a profile without DenyRead never touches the stable
// read-only SID at all (see TestBuildWindowsACLPlanOmitsSharedDenyPathsWithoutDenyRead),
// so it must not add a revoke entry either.
func TestBuildWindowsACLPlanOmitsRevokeCapabilityWithoutDenyRead(t *testing.T) {
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
	for _, entry := range plan.Entries {
		if entry.Action == WindowsACLRevokeCapability {
			t.Fatalf("plan without DenyRead = %#v, want no WindowsACLRevokeCapability entry", plan.Entries)
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
