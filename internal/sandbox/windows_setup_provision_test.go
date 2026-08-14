package sandbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The tests here drive PRODUCTION entry points rather than the helpers those entry
// points call. The distinction is the point: this runtime-root work shipped once
// with tests that called windowsSandboxProfileWithRuntime and
// ensureWindowsSandboxRuntimeCandidates directly, and they stayed green while the
// production call sites were missing outright. A test that reaches past the caller
// proves the helper works and says nothing about whether anything calls it.

// windowsRuntimeTestRoots returns a workspace plus the runtime candidates derived
// for it, with EVERY location the derivation reads pointed at test-owned
// directories.
//
// Stubbing sandboxUserCacheDir is not tidiness. windowsSandboxRuntimeCandidates
// reads the real user cache when it is left alone, so a test that then clears a
// candidate to prove provisioning recreates it was deleting the developer's own
// zero runtime tree under the real cache on every run, and failing outright on a
// read-only home. The test has no ownership claim on that path, so it must not
// derive one. windows_runner_marker_windows_test.go had this right already.
func windowsRuntimeTestRoots(t *testing.T) (string, []string) {
	t.Helper()
	workspaceRoot := t.TempDir()
	cacheRoot := t.TempDir()
	tempRoot := t.TempDir()

	originalCacheDir := sandboxUserCacheDir
	sandboxUserCacheDir = func() (string, error) { return cacheRoot, nil }
	t.Cleanup(func() { sandboxUserCacheDir = originalCacheDir })
	t.Setenv("TMP", tempRoot)
	t.Setenv("TEMP", tempRoot)

	candidates := windowsSandboxRuntimeRoots(PermissionProfile{}, []string{workspaceRoot})
	if len(candidates) == 0 {
		t.Skip("no runtime candidates derivable in this environment")
	}
	// Belt and braces: refuse to run rather than touch anything the test does not
	// own, so a later change to the derivation cannot quietly reintroduce this.
	//
	// BOTH SIDES CANONICALIZED, because pathWithinRoot compares spellings and the
	// candidate arrives canonical: the derivation runs the cache root through
	// canonicalSandboxWorkspaceRoot before joining. Measuring that against a raw
	// t.TempDir() is the same one-sided comparison this PR fixes in
	// fallbackSandboxRuntimeRoot, and it fired on exactly the machines that carry a
	// second spelling: macOS /var vs /private/var, and a CI Windows runner whose
	// profile has an 8.3 name (RUNNER~1 against runneradmin). It passed locally
	// because this box has neither.
	ownedCache := canonicalSandboxWorkspaceRoot(cacheRoot)
	ownedTemp := canonicalSandboxWorkspaceRoot(tempRoot)
	for _, candidate := range candidates {
		canonical := canonicalSandboxWorkspaceRoot(candidate)
		if !pathWithinRoot(ownedCache, canonical) && !pathWithinRoot(ownedTemp, canonical) {
			t.Fatalf("candidate %s (canonically %s) is outside the test-owned cache (%s) and temp (%s) roots; refusing to modify it", candidate, canonical, ownedCache, ownedTemp)
		}
	}
	return workspaceRoot, candidates
}

// TestBuildWindowsSandboxSetupACLPlanCreatesTheRootsItGrants is the regression for
// the defect this PR shipped with: the plan named the runtime roots as AllowWrite
// targets and nothing created them. applyWindowsACLPlan materializes only DenyRead
// targets, so an absent AllowWrite target aborts the whole elevated setup with
// "windows ACL target does not exist".
func TestBuildWindowsSandboxSetupACLPlanCreatesTheRootsItGrants(t *testing.T) {
	workspaceRoot, candidates := windowsRuntimeTestRoots(t)
	for _, candidate := range candidates {
		if err := os.RemoveAll(candidate); err != nil {
			t.Fatalf("clear candidate %s: %v", candidate, err)
		}
	}

	config := WindowsSandboxSetupConfig{
		SandboxHome:    t.TempDir(),
		CommandCWD:     workspaceRoot,
		WorkspaceRoots: []string{workspaceRoot},
		// Augmented, because that is what the elevated helper parses off the wire:
		// BuildWindowsSandboxSetupArgs folds the runtime roots in before the re-exec,
		// so by the time runWindowsSandboxSetup builds this plan the roots are
		// already write roots. Feeding a bare profile here would test a shape
		// production never produces.
		PermissionProfile: WindowsSandboxProfileWithRuntimeRoots(
			PermissionProfileFromPolicy(workspaceRoot, DefaultPolicy(), nil),
			[]string{workspaceRoot},
		),
	}
	plan, err := buildWindowsSandboxSetupACLPlan(config)
	if err != nil {
		t.Fatalf("buildWindowsSandboxSetupACLPlan: %v", err)
	}

	granted := map[string]struct{}{}
	for _, entry := range plan.Entries {
		if entry.Action == WindowsACLAllowWrite {
			granted[windowsCapabilityPathKey(entry.Path)] = struct{}{}
		}
	}
	for _, candidate := range candidates {
		if _, ok := granted[windowsCapabilityPathKey(candidate)]; !ok {
			t.Fatalf("plan does not grant runtime root %s; the marker and the command plan will disagree", candidate)
		}
		info, err := os.Stat(candidate)
		if err != nil {
			t.Fatalf("runtime root %s is granted but absent: %v; applyWindowsACLPlan aborts the entire setup with \"windows ACL target does not exist\"", candidate, err)
		}
		if !info.IsDir() {
			t.Fatalf("runtime root %s exists but is not a directory", candidate)
		}
	}
}

// TestBuildWindowsSandboxSetupArgsCarriesEveryRuntimeCandidate closes the gap the
// reviewer named: every prior test fed BuildWindowsSandboxSetupArgs a profile the
// caller had already augmented, so deleting the augmentation inside the builder
// left the suite green. This one hands it a BARE profile and decodes the argument
// the elevated helper actually receives.
func TestBuildWindowsSandboxSetupArgsCarriesEveryRuntimeCandidate(t *testing.T) {
	workspaceRoot, candidates := windowsRuntimeTestRoots(t)
	bare := PermissionProfileFromPolicy(workspaceRoot, DefaultPolicy(), nil)
	for _, root := range bare.FileSystem.WriteRoots {
		for _, candidate := range candidates {
			if windowsCapabilityPathKey(root.Root) == windowsCapabilityPathKey(candidate) {
				t.Fatalf("the bare profile already contains runtime root %s, so this test could not detect the augmentation going missing", candidate)
			}
		}
	}

	args, err := BuildWindowsSandboxSetupArgs(WindowsSandboxSetupArgsOptions{
		SandboxHome: t.TempDir(),
		CommandCWD:  workspaceRoot,
		// Deliberately NOT pre-augmented: the builder folds the runtime roots in
		// itself, and passing them here would hide it if that ever stopped.
		PermissionProfile: bare,
		WorkspaceRoots:    []string{workspaceRoot},
	})
	if err != nil {
		t.Fatalf("BuildWindowsSandboxSetupArgs: %v", err)
	}

	encoded := ""
	for i, arg := range args {
		if arg == "--permission-profile" && i+1 < len(args) {
			encoded = args[i+1]
			break
		}
		if strings.HasPrefix(arg, "--permission-profile=") {
			encoded = strings.TrimPrefix(arg, "--permission-profile=")
			break
		}
	}
	if encoded == "" {
		t.Fatalf("no --permission-profile argument in %v", args)
	}
	var decoded PermissionProfile
	if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
		t.Fatalf("decode --permission-profile: %v", err)
	}

	present := map[string]struct{}{}
	for _, root := range decoded.FileSystem.WriteRoots {
		present[windowsCapabilityPathKey(root.Root)] = struct{}{}
	}
	for _, candidate := range candidates {
		if _, ok := present[windowsCapabilityPathKey(candidate)]; !ok {
			t.Fatalf("the setup args omit runtime root %s; setup would fingerprint a profile no command reproduces and every command would die on \"permission roots or deny lists changed\"", candidate)
		}
	}
}

// TestSetupMarkerSurvivesADifferentTempInALaterProcess is the regression for the
// finding that the marker depended on the caller's transient TEMP.
//
// The sequence is the real one and the old code could not survive it: elevated
// setup runs from one terminal, and a later command is planned by a parent
// process an IDE or service started with a different TEMP. The cache runtime is
// untouched and healthy throughout. While both candidates were folded in
// unconditionally, the second process derived a different temp-side path, the
// plan hashes disagreed, and every command died on "permission roots or deny
// lists changed" with nothing wrong.
func TestSetupMarkerSurvivesADifferentTempInALaterProcess(t *testing.T) {
	workspaceRoot := t.TempDir()
	cacheRoot := t.TempDir()
	originalCacheDir := sandboxUserCacheDir
	sandboxUserCacheDir = func() (string, error) { return cacheRoot, nil }
	t.Cleanup(func() { sandboxUserCacheDir = originalCacheDir })

	runtimeRootUnder := func(temp string) string {
		t.Helper()
		t.Setenv("TMP", temp)
		t.Setenv("TEMP", temp)
		roots := windowsSandboxRuntimeRoots(PermissionProfile{}, []string{workspaceRoot})
		if len(roots) != 1 {
			t.Fatalf("expected one runtime root under TEMP=%s, got %v", temp, roots)
		}
		return roots[0]
	}

	atSetup := runtimeRootUnder(t.TempDir())
	atLaterCommand := runtimeRootUnder(t.TempDir())

	if atSetup != atLaterCommand {
		t.Fatalf("the runtime root moved when only TEMP changed:\n  setup          %s\n  later process  %s\nboth feed the ACL plan the marker fingerprints, so every command would fail validation with \"permission roots or deny lists changed\" while the cache runtime sat there healthy",
			atSetup, atLaterCommand)
	}

	// Scope, stated rather than implied. This asserts the RUNTIME ROOT no longer
	// tracks TEMP, which is the part this PR introduced and this change removes.
	// The whole plan hash is still TEMP-dependent for a separate, older reason:
	// PermissionProfileFromPolicy grants os.TempDir() itself as a write root when
	// the policy allows temp, so the profile carries the caller's TEMP before any
	// runtime augmentation happens. Asserting on the full hash here would fail for
	// that pre-existing reason and read as though this fix were broken.
	base := PermissionProfileFromPolicy(workspaceRoot, DefaultPolicy(), nil)
	carriesAmbientTemp := false
	for _, root := range base.FileSystem.WriteRoots {
		if pathWithinRoot(canonicalSandboxWorkspaceRoot(os.TempDir()), canonicalSandboxWorkspaceRoot(root.Root)) {
			carriesAmbientTemp = true
		}
	}
	if !carriesAmbientTemp {
		t.Log("the base profile no longer grants the ambient temp dir; the wider TEMP dependency may now be closed and this note can go")
	}
}

// TestRuntimeRootsPinToTheProfileTheCommandActuallyHolds covers the other half:
// once a profile carries a runtime, the plan names THAT tree and does not
// re-derive one. prepareSandboxRuntime relocates on a lease failure, so a
// re-derivation would name the tree the command is not writing to.
func TestRuntimeRootsPinToTheProfileTheCommandActuallyHolds(t *testing.T) {
	workspaceRoot := t.TempDir()
	cacheRoot := t.TempDir()
	originalCacheDir := sandboxUserCacheDir
	sandboxUserCacheDir = func() (string, error) { return cacheRoot, nil }
	t.Cleanup(func() { sandboxUserCacheDir = originalCacheDir })
	tempRoot := t.TempDir()
	t.Setenv("TMP", tempRoot)
	t.Setenv("TEMP", tempRoot)

	derived := windowsSandboxRuntimeRoots(PermissionProfile{}, []string{workspaceRoot})
	if len(derived) != 1 {
		t.Fatalf("expected exactly one derived runtime root, got %v", derived)
	}

	// A runtime the process actually selected, deliberately NOT the derived one,
	// standing in for the lease-failure relocation.
	relocated := filepath.Join(t.TempDir(), "relocated-runtime")
	profile := PermissionProfile{Runtime: &SandboxRuntime{Root: relocated}}

	pinned := windowsSandboxRuntimeRoots(profile, []string{workspaceRoot})
	if len(pinned) != 1 || pinned[0] != relocated {
		t.Fatalf("the plan did not pin to the runtime the profile holds:\n  profile runtime %s\n  plan named      %v\nthe command would write to one tree while the plan grants another", relocated, pinned)
	}
	if pinned[0] == derived[0] {
		t.Fatalf("pinned and derived roots are identical (%s), so this test cannot tell them apart", pinned[0])
	}
}

// TestFallbackSandboxRuntimeRootIsSpellingStable pins the canonicalization added
// for the aliased-TEMP finding. Two spellings of one directory must produce one
// runtime root; producing two is what let a root resolving inside the workspace
// pass the containment check.
func TestFallbackSandboxRuntimeRootIsSpellingStable(t *testing.T) {
	workspaceRoot := t.TempDir()
	tempRoot := t.TempDir()

	// An UPPER-CASED spelling is the one alias available without privilege or a
	// volume setting: 8.3 generation is disabled on many volumes and creating a
	// symlink needs a privilege the test process may not hold, while a
	// case-insensitive filesystem resolves this to the same directory and
	// GetLongPathName returns the on-disk casing. Skipped rather than passed
	// vacuously where the filesystem is case-sensitive, since there the two names
	// really are different directories.
	alias := strings.ToUpper(tempRoot)
	canonical := canonicalSandboxWorkspaceRoot(tempRoot)
	if alias == tempRoot || canonicalSandboxWorkspaceRoot(alias) != canonical {
		t.Skip("no distinct alias spelling of the temp dir is constructible here")
	}

	t.Setenv("TMP", tempRoot)
	t.Setenv("TEMP", tempRoot)
	viaReal, err := fallbackSandboxRuntimeRoot(workspaceRoot)
	if err != nil {
		t.Fatalf("fallbackSandboxRuntimeRoot(real spelling): %v", err)
	}

	t.Setenv("TMP", alias)
	t.Setenv("TEMP", alias)
	viaAlias, err := fallbackSandboxRuntimeRoot(workspaceRoot)
	if err != nil {
		t.Fatalf("fallbackSandboxRuntimeRoot(alias): %v", err)
	}

	if viaReal != viaAlias {
		t.Fatalf("two spellings of ONE temp directory produced two runtime roots:\n  via %s\n    -> %s\n  via %s\n    -> %s\nsetup and the command side derive from the same function, so they would grant and expect different paths, and pathWithinRoot would measure the workspace against a spelling it does not match", tempRoot, viaReal, alias, viaAlias)
	}
}

// TestWindowsSandboxRuntimeCandidatesUsesOneWorkspaceRoot pins the single-root
// contract rather than treating it as a defect. The marker compares plan hashes
// for EQUALITY and a command presents exactly one root, so deriving candidates for
// every root would put entries in the marker that no command reproduces. Whoever
// widens this has to change the marker comparison in the same change.
func TestWindowsSandboxRuntimeCandidatesUsesOneWorkspaceRoot(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()

	// Owned cache and temp even though this test only reads: the derivation would
	// otherwise depend on the developer's real cache directory, which makes the
	// result environment-dependent as well as impolite.
	cacheRoot := t.TempDir()
	originalCacheDir := sandboxUserCacheDir
	sandboxUserCacheDir = func() (string, error) { return cacheRoot, nil }
	t.Cleanup(func() { sandboxUserCacheDir = originalCacheDir })
	tempRoot := t.TempDir()
	t.Setenv("TMP", tempRoot)
	t.Setenv("TEMP", tempRoot)

	combined := windowsSandboxRuntimeRoots(PermissionProfile{}, []string{first, second})
	alone := windowsSandboxRuntimeRoots(PermissionProfile{}, []string{first})
	if len(combined) == 0 {
		t.Skip("no runtime candidates derivable in this environment")
	}
	separator := string(filepath.ListSeparator)
	if strings.Join(combined, separator) != strings.Join(alone, separator) {
		t.Fatalf("a second workspace root changed the candidate set:\n  [first, second] %v\n  [first]         %v\nsetup would grant roots no single command reproduces", combined, alone)
	}
}
