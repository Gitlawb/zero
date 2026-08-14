package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// protectedTokenFixture writes a bridge token inside the workspace and points
// ZERO_DAEMON_REMOTE_TOKEN_FILE at it.
func protectedTokenFixture(t *testing.T) (string, string) {
	t.Helper()
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	token := filepath.Join(ws, "bridge-token")
	if err := os.WriteFile(token, []byte("secret\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	t.Setenv(daemonRemoteTokenEnv, "")
	t.Setenv(daemonRemoteTokenFileEnv, token)
	return ws, token
}

func TestProtectedCredentialPathsResolveLikeTheDaemonReader(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	token := filepath.Join(base, "token")
	if err := os.WriteFile(token, []byte("secret\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}

	t.Run("absent variable protects nothing", func(t *testing.T) {
		t.Setenv(daemonRemoteTokenEnv, "")
		t.Setenv(daemonRemoteTokenFileEnv, "")
		if got := protectedCredentialPaths(); len(got) != 0 {
			t.Fatalf("protected paths = %#v, want none", got)
		}
	})

	t.Run("inline token leaves the unused file pointer unprotected", func(t *testing.T) {
		t.Setenv(daemonRemoteTokenEnv, "from-env")
		t.Setenv(daemonRemoteTokenFileEnv, token)
		if got := protectedCredentialPaths(); len(got) != 0 {
			t.Fatalf("protected paths = %#v, want none when the inline token takes precedence", got)
		}
	})

	t.Run("relative value resolves against the working directory", func(t *testing.T) {
		// os.ReadFile — what the daemon uses — resolves a relative value against the
		// working directory, so the protected path must do the same.
		t.Chdir(base)
		t.Setenv(daemonRemoteTokenEnv, "")
		t.Setenv(daemonRemoteTokenFileEnv, "token")
		if got := protectedCredentialPaths(); !stringSliceContains(got, token) {
			t.Fatalf("protected paths = %#v, want %q", got, token)
		}
	})

	t.Run("a literal tilde is not home-expanded", func(t *testing.T) {
		// os.ReadFile treats "~" as an ordinary directory name; expanding it here
		// would protect a path the daemon never reads.
		t.Chdir(base)
		t.Setenv(daemonRemoteTokenEnv, "")
		t.Setenv(daemonRemoteTokenFileEnv, filepath.Join("~", "token"))
		want := filepath.Join(base, "~", "token")
		got := protectedCredentialPaths()
		if !stringSliceContains(got, want) {
			t.Fatalf("protected paths = %#v, want literal %q", got, want)
		}
		home, err := os.UserHomeDir()
		if err == nil && stringSliceContains(got, filepath.Join(home, "token")) {
			t.Fatalf("protected paths = %#v, must not home-expand the value", got)
		}
	})

	t.Run("filename whitespace is part of the pathname", func(t *testing.T) {
		// The daemon reads exactly the bytes the variable names, so trimming the
		// pointer here would protect "<dir>/token" while the bearer file is
		// "<dir>/token " — leaving the real credential readable and replaceable.
		if runtime.GOOS == "windows" {
			t.Skip("Windows filenames cannot end in a space")
		}
		spaced := filepath.Join(base, "token ")
		if err := os.WriteFile(spaced, []byte("secret\n"), 0o600); err != nil {
			t.Fatalf("write token: %v", err)
		}
		t.Setenv(daemonRemoteTokenEnv, "")
		t.Setenv(daemonRemoteTokenFileEnv, spaced)
		got := protectedCredentialPaths()
		if !stringSliceContains(got, spaced) {
			t.Fatalf("protected paths = %#v, want %q", got, spaced)
		}
		if stringSliceContains(got, token) {
			t.Fatalf("protected paths = %#v, must not protect the trimmed name %q", got, token)
		}
	})

	t.Run("a symlinked pathname protects the link and its target", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlink creation needs elevation on Windows")
		}
		link := filepath.Join(base, "token-link")
		if err := os.Symlink(token, link); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
		t.Setenv(daemonRemoteTokenEnv, "")
		t.Setenv(daemonRemoteTokenFileEnv, link)
		got := protectedCredentialPaths()
		for _, want := range []string{link, token} {
			if !stringSliceContains(got, want) {
				t.Fatalf("protected paths = %#v, want %q", got, want)
			}
		}
	})
}

// TestProtectedCredentialsSurviveAllowRead locks in the non-opt-out guarantee:
// the bridge token grants control of the daemon, so neither AllowRead, an
// AllowWrite root, nor a granted permission may re-include it.
func TestProtectedCredentialsSurviveAllowRead(t *testing.T) {
	ws, token := protectedTokenFixture(t)
	policy := Policy{
		Mode:             ModeEnforce,
		EnforceWorkspace: true,
		AllowRead:        []string{ws, token},
		AllowWrite:       []string{ws},
	}
	scope, err := NewScope(ws, nil)
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}

	for _, sideEffect := range []SideEffect{SideEffectRead, SideEffectWrite, SideEffectOutOfWorkspace} {
		block := validatePathWithPolicy(scope, policy, sideEffect, true, ws, token)
		if block == nil || !strings.Contains(block.Reason, "remote bridge token") {
			t.Fatalf("%s on the bridge token: block = %#v, want a bridge-token deny", sideEffect, block)
		}
	}

	// The search-walk matcher enforces the same exclusion without consulting
	// AllowRead, and it is active even though DenyRead is empty.
	engine := NewEngine(EngineOptions{WorkspaceRoot: ws, Policy: policy, Scope: scope})
	rx := engine.ReadExclusions()
	if !rx.Active() {
		t.Fatal("read exclusions must be active for the automatic credential deny")
	}
	if !rx.PathExcluded(token) {
		t.Fatalf("read exclusions must exclude the bridge token %q", token)
	}
	if rx.PathExcluded(filepath.Join(ws, "main.go")) {
		t.Fatal("read exclusions must not exclude ordinary workspace files")
	}
	if globs := ReadExclusionGlobs(policy, scope); !stringSliceContains(globs, "!bridge-token") {
		t.Fatalf("read exclusion globs = %#v, want the bridge token excluded", globs)
	}
}

func TestProtectedCredentialDirExcluded(t *testing.T) {
	ws, token := protectedTokenFixture(t)
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: ws,
		Policy:        Policy{Mode: ModeEnforce, EnforceWorkspace: true, AllowRead: []string{token}},
	})

	if !engine.ReadExclusions().DirExcluded(token) {
		t.Fatalf("DirExcluded must enforce the protected credential path %q", token)
	}
}

func TestProtectedCredentialPreventsUnsandboxedExecution(t *testing.T) {
	ws, _ := protectedTokenFixture(t)
	engine := NewEngine(EngineOptions{WorkspaceRoot: ws, Policy: DefaultPolicy()})

	if engine.UnsandboxedExecutionAllowed() {
		t.Fatal("UnsandboxedExecutionAllowed must stay false while a credential path is protected")
	}
}

// TestProtectedCredentialsRejectSessionPermissionProfile covers the other
// re-inclusion route: a session/turn permission profile that asks for the token
// path must not be auto-applicable.
func TestProtectedCredentialsRejectSessionPermissionProfile(t *testing.T) {
	ws, token := protectedTokenFixture(t)
	scope, err := NewScope(ws, nil)
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: ws,
		Policy:        Policy{Mode: ModeEnforce, EnforceWorkspace: true},
		Scope:         scope,
	})
	if engine.CoversRequestPermissions(RequestPermissionProfile{
		FileSystem: &FileSystemPermissions{Read: []string{token}},
	}) {
		t.Fatal("a permission request covering the bridge token must not read as already-granted")
	}
	if !engine.CoversRequestPermissions(RequestPermissionProfile{
		FileSystem: &FileSystemPermissions{Read: []string{filepath.Join(ws, "main.go")}},
	}) {
		t.Fatal("an ordinary workspace read request must stay covered by policy")
	}
}

// TestProtectedCredentialsDenyReadAndWriteInSeatbeltProfile covers the macOS
// backend: a token under a writable root was read-denied but still truncatable
// through the broad write allow. A user-configured DenyRead entry keeps the write
// direction (see TestSeatbeltProfileProtectsMetadataAndDenyOrdering).
func TestProtectedCredentialsDenyReadAndWriteInSeatbeltProfile(t *testing.T) {
	ws, token := protectedTokenFixture(t)
	userDenied := filepath.Join(ws, "generated")
	profile := PermissionProfile{
		FileSystem: FileSystemPolicy{
			Kind:       FileSystemRestricted,
			ReadRoots:  []string{string(filepath.Separator)},
			WriteRoots: []WritableRoot{{Root: ws}},
			DenyRead:   []string{token, userDenied},
			AllowTemp:  true,
		},
		Network: NetworkPolicy{Mode: NetworkDeny},
	}
	sbpl := seatbeltProfileFromPermissionProfile(profile, Policy{Mode: ModeEnforce, DenyRead: []string{userDenied}}, "")
	escaped := sandboxProfileString(normalizeProfilePath(token))
	denyRead := `(deny file-read* (literal "` + escaped + `"))`
	denyWrite := `(deny file-write* (literal "` + escaped + `"))`
	for _, want := range []string{denyRead, denyWrite} {
		if !strings.Contains(sbpl, want) {
			t.Fatalf("Seatbelt profile missing %q:\n%s", want, sbpl)
		}
	}
	if strings.Contains(sbpl, `(deny file-write* (literal "`+sandboxProfileString(normalizeProfilePath(userDenied))+`"))`) {
		t.Fatalf("a user-configured DenyRead path must stay writable:\n%s", sbpl)
	}
	// Seatbelt is last-match-wins, so the denial must follow the broad allow.
	if allow := strings.Index(sbpl, "(allow file-write*"); allow < 0 || strings.Index(sbpl, denyWrite) < allow {
		t.Fatalf("the write denial must follow the broad write allow:\n%s", sbpl)
	}
}

func TestProtectedCredentialFilenameWhitespaceReachesOSSandbox(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows filenames cannot end in a space")
	}
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	token := filepath.Join(workspace, "bridge-token ")
	if err := os.WriteFile(token, []byte("secret"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	t.Setenv(daemonRemoteTokenEnv, "")
	t.Setenv(daemonRemoteTokenFileEnv, token)

	profile := PermissionProfileFromPolicy(workspace, DefaultPolicy(), nil)
	if !stringSliceContains(profile.FileSystem.DenyReadIfExists, token) {
		t.Fatalf("DenyReadIfExists = %#v, want exact token pathname %q", profile.FileSystem.DenyReadIfExists, token)
	}

	sbpl := seatbeltProfileFromPermissionProfile(profile, DefaultPolicy(), "")
	escaped := sandboxProfileString(token)
	for _, want := range []string{
		`(deny file-read* (literal "` + escaped + `"))`,
		`(deny file-write* (literal "` + escaped + `"))`,
	} {
		if !strings.Contains(sbpl, want) {
			t.Fatalf("Seatbelt profile missing %q:\n%s", want, sbpl)
		}
	}

	plan := mustBuildLinuxBwrapFilesystemPlan(t, profile)
	assertArgsContainSequence(t, plan.Args, "--ro-bind", "/dev/null", token)

	if !protectedCredentialLinkableIntoWritableMacOSRoot(profile, protectedCredentialPaths()) {
		t.Fatalf("spaced token %q under writable root %q must fail macOS preflight", token, workspace)
	}
}

// A mandatory token named by a symlink fails the plan outright: bubblewrap
// cannot bind-mask a mutable symlink destination, so masking the link's current
// target would be detached by a retarget mid-session.
func TestMandatoryLinuxTokenSymlinkFailsPlan(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs elevation on Windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "token")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "token-link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	t.Setenv(daemonRemoteTokenEnv, "")
	t.Setenv(daemonRemoteTokenFileEnv, link)
	profile := PermissionProfileFromPolicy(dir, DefaultPolicy(), nil)

	_, err := buildLinuxBwrapFilesystemPlan(profile)
	if err == nil {
		t.Fatal("buildLinuxBwrapFilesystemPlan succeeded for a symlinked mandatory token, want a fail-closed error")
	}
	if !strings.Contains(err.Error(), "mandatory credential symlink") {
		t.Fatalf("buildLinuxBwrapFilesystemPlan error = %v, want a mandatory credential symlink refusal", err)
	}
}

// The helper-level refusal above must also hold at the command-plan level, so a
// future change cannot restore the unsafe accepted-symlink behavior while only a
// helper test stays green.
func TestSandboxManagerRefusesLinuxShellForSymlinkedToken(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs elevation on Windows")
	}
	workspace := t.TempDir()
	dir := t.TempDir()
	target := filepath.Join(dir, "token")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "token-link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	t.Setenv(daemonRemoteTokenEnv, "")
	t.Setenv(daemonRemoteTokenFileEnv, link)

	policy := DefaultPolicy()
	backend := Backend{Name: BackendLinuxBwrap, Available: true, Executable: "/usr/bin/zero-linux-sandbox", Platform: "linux"}
	_, err := NewSandboxManager(SandboxManagerOptions{GOOS: "linux", Backend: backend}).BuildCommandPlan(SandboxManagerRequest{
		WorkspaceRoot:     workspace,
		Command:           CommandSpec{Name: "/bin/sh", Args: []string{"-c", "true"}, Dir: workspace},
		Policy:            policy,
		Profile:           PermissionProfileFromPolicy(workspace, policy, nil),
		Preference:        SandboxPreferenceAuto,
		ValidateExecution: true,
	})
	if err == nil {
		t.Fatal("BuildCommandPlan succeeded with a symlinked mandatory token, want the shell refused")
	}
	if !strings.Contains(err.Error(), "mandatory credential symlink") && !strings.Contains(err.Error(), "hard-link aliases") {
		t.Fatalf("BuildCommandPlan error = %v, want a fail-closed credential refusal", err)
	}
}

// A hard-link alias is a second directory entry for the token's inode, so the
// /dev/null bind over the configured pathname does not hide it. Plan
// construction must fail closed instead of running behind that mask.
func TestSandboxManagerRejectsLinuxTokenHardLinkAlias(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("hard-link inode probing is exercised on Linux")
	}
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tokenDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	token := filepath.Join(tokenDir, "bridge-token")
	if err := os.WriteFile(token, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(tokenDir, "token-alias")
	if err := os.Link(token, alias); err != nil {
		t.Skipf("fixture paths are not hard-linkable: %v", err)
	}

	t.Setenv(daemonRemoteTokenEnv, "")
	t.Setenv(daemonRemoteTokenFileEnv, token)

	if _, ok := pathHardLinkCount(token); !ok {
		t.Fatal("pathHardLinkCount could not inspect the token fixture")
	}
	if credential, linkable := protectedCredentialLinkableIntoLinuxShellRoot(
		PermissionProfileFromPolicy(workspace, DefaultPolicy(), nil),
		protectedCredentialPaths(),
	); !linkable || credential != token {
		t.Fatalf("linkable = %t credential = %q, want the aliased token %q reported", linkable, credential, token)
	}

	policy := DefaultPolicy()
	backend := Backend{Name: BackendLinuxBwrap, Available: true, Executable: "/usr/bin/zero-linux-sandbox", Platform: "linux"}
	_, err = NewSandboxManager(SandboxManagerOptions{GOOS: "linux", Backend: backend}).BuildCommandPlan(SandboxManagerRequest{
		WorkspaceRoot:     workspace,
		Command:           CommandSpec{Name: "/bin/sh", Args: []string{"-c", "cat " + alias}, Dir: workspace},
		Policy:            policy,
		Profile:           PermissionProfileFromPolicy(workspace, policy, nil),
		Preference:        SandboxPreferenceAuto,
		ValidateExecution: true,
	})
	if err == nil {
		t.Fatal("BuildCommandPlan succeeded with a hard-linked token alias, want plan construction to fail closed")
	}
	if !strings.Contains(err.Error(), "hard-link aliases") {
		t.Fatalf("BuildCommandPlan error = %v, want a hard-link alias refusal", err)
	}
}

// The optional (non-mandatory) credential candidates keep the older behavior:
// never bind over the link itself, and mask the resolved destination when it is
// the path that actually exists.
func TestOptionalLinuxCredentialSymlinkMasksResolvedDestination(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs elevation on Windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "token")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "token-link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("EvalSymlinks target: %v", err)
	}

	profile := PermissionProfile{
		FileSystem: FileSystemPolicy{
			Kind:             FileSystemRestricted,
			ReadRoots:        []string{dir},
			DenyReadIfExists: []string{link, resolvedTarget},
		},
	}
	plan := mustBuildLinuxBwrapFilesystemPlan(t, profile)
	if stringSliceContains(plan.Args, link) {
		t.Fatalf("bubblewrap plan attempted to mount over symlink destination %q: %#v", link, plan.Args)
	}
	assertArgsContainSequence(t, plan.Args, "--ro-bind", "/dev/null", resolvedTarget)
}

// TestProtectedCredentialsSurviveDisabledPolicy covers the one route that skips
// validatePathWithPolicy entirely: ModeDisabled drops every user-configured
// restriction, but the bridge token authenticates the caller driving these tools.
func TestProtectedCredentialsSurviveDisabledPolicy(t *testing.T) {
	ws, token := protectedTokenFixture(t)
	engine := NewEngine(EngineOptions{WorkspaceRoot: ws, Policy: Policy{Mode: ModeDisabled}})

	for _, sideEffect := range []SideEffect{SideEffectRead, SideEffectWrite} {
		decision := engine.Evaluate(context.Background(), Request{
			ToolName:      "read_file",
			WorkspaceRoot: ws,
			SideEffect:    sideEffect,
			Args:          map[string]any{"path": token},
		})
		if decision.Action != ActionDeny || !strings.Contains(decision.Reason, "remote bridge token") {
			t.Fatalf("%s under a disabled policy: action = %q reason = %q, want a bridge-token deny", sideEffect, decision.Action, decision.Reason)
		}
	}

	// Everything else stays allowed: a disabled sandbox is still disabled.
	decision := engine.Evaluate(context.Background(), Request{
		ToolName:      "read_file",
		WorkspaceRoot: ws,
		SideEffect:    SideEffectRead,
		Args:          map[string]any{"path": filepath.Join(ws, "main.go")},
	})
	if decision.Action != ActionAllow {
		t.Fatalf("ordinary read under a disabled policy: action = %q reason = %q, want allow", decision.Action, decision.Reason)
	}

	rx := engine.ReadExclusions()
	if !rx.Active() || !rx.PathExcluded(token) {
		t.Fatalf("read exclusions under a disabled policy must still exclude %q", token)
	}
	if rx.PathExcluded(filepath.Join(ws, "main.go")) {
		t.Fatal("read exclusions under a disabled policy must not exclude ordinary files")
	}
}

// TestDisabledPolicyLeavesShellOutsideTheTokenBoundary pins the boundary jatmn
// asked to see stated for #685: under ModeDisabled the bridge-token exclusion
// covers Zero's in-process file tools and nothing else. No OS wrapper is built
// at all in that mode, so a shell command is confined by nothing and an
// escalation has nothing to bypass. This test exists so that stops being an
// implicit property — changing any of it should mean changing this test on
// purpose, not discovering the behavior later.
func TestDisabledPolicyLeavesShellOutsideTheTokenBoundary(t *testing.T) {
	ws, token := protectedTokenFixture(t)
	engine := NewEngine(EngineOptions{WorkspaceRoot: ws, Policy: Policy{Mode: ModeDisabled}})

	shell := engine.Evaluate(context.Background(), Request{
		ToolName:      "bash",
		WorkspaceRoot: ws,
		SideEffect:    SideEffectShell,
		Args:          map[string]any{"command": "cat " + token},
	})
	if shell.Action != ActionAllow {
		t.Fatalf("shell under a disabled policy = %q (%s); the token boundary is documented as in-process only", shell.Action, shell.Reason)
	}

	// The same command's payload IS blocked when it arrives as a path-carrying
	// request, which is the whole of the guarantee.
	read := engine.Evaluate(context.Background(), Request{
		ToolName:      "read_file",
		WorkspaceRoot: ws,
		SideEffect:    SideEffectRead,
		Args:          map[string]any{"path": token},
	})
	if read.Action != ActionDeny {
		t.Fatalf("in-process read under a disabled policy = %q (%s), want deny", read.Action, read.Reason)
	}

	if !engine.UnsandboxedExecutionAllowed() {
		t.Fatal("escalation under a disabled policy must stay allowed: there is no wrapper for it to bypass")
	}

	// With the sandbox on, the same configured token flips both: the profile is
	// built, so escalating out of it would drop a real deny rule.
	enforcing := NewEngine(EngineOptions{WorkspaceRoot: ws, Policy: DefaultPolicy()})
	if enforcing.UnsandboxedExecutionAllowed() {
		t.Fatal("escalation must be refused while a bridge token is protected by an active profile")
	}
}

// TestProtectedCredentialsMatchCaseVariantOnCaseInsensitiveFilesystems covers the
// bypass a case-variant spelling opened: pathWithinRoot ends in filepath.Rel,
// which folds case on Windows but NOT on darwin, whose default APFS volume is
// case-insensitive — so `.../BRIDGE-TOKEN` missed the protected `.../bridge-token`
// while the OS opened the same bearer-token file. On a case-sensitive filesystem
// the variant is a genuinely different file and must stay unblocked.
func TestProtectedCredentialsMatchCaseVariantOnCaseInsensitiveFilesystems(t *testing.T) {
	ws, token := protectedTokenFixture(t)
	variant := filepath.Join(filepath.Dir(token), strings.ToUpper(filepath.Base(token)))
	if variant == token {
		t.Fatalf("fixture token %q has no case variant", token)
	}
	scope, err := NewScope(ws, nil)
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	policy := Policy{Mode: ModeEnforce, EnforceWorkspace: true, AllowRead: []string{ws}, AllowWrite: []string{ws}}
	wantDenied := protectedPathFoldsCase()

	for _, sideEffect := range []SideEffect{SideEffectRead, SideEffectWrite, SideEffectOutOfWorkspace} {
		block := validatePathWithPolicy(scope, policy, sideEffect, true, ws, variant)
		denied := block != nil && strings.Contains(block.Reason, "remote bridge token")
		if denied != wantDenied {
			t.Fatalf("%s on case variant %q: denied = %t, want %t (block = %#v)", sideEffect, variant, denied, wantDenied, block)
		}
	}

	engine := NewEngine(EngineOptions{WorkspaceRoot: ws, Policy: policy, Scope: scope})
	if excluded := engine.ReadExclusions().PathExcluded(variant); excluded != wantDenied {
		t.Fatalf("read exclusions on case variant %q: excluded = %t, want %t", variant, excluded, wantDenied)
	}
	// The exact spelling is denied on every platform regardless.
	if block := validatePathWithPolicy(scope, policy, SideEffectRead, true, ws, token); block == nil {
		t.Fatalf("the configured token path %q must always be denied", token)
	}
}

// TestProtectedCredentialsDoNotBlockUnrelatedRequests keeps the exclusion inert
// for everyone who does not run the remote bridge.
func TestProtectedCredentialsDoNotBlockUnrelatedRequests(t *testing.T) {
	t.Setenv(daemonRemoteTokenEnv, "")
	t.Setenv(daemonRemoteTokenFileEnv, "")
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: ws,
		Policy:        Policy{Mode: ModeEnforce, EnforceWorkspace: true},
	})
	if rx := engine.ReadExclusions(); rx.Active() {
		t.Fatal("read exclusions must stay inactive without a configured token file")
	}
	decision := engine.Evaluate(context.Background(), Request{
		ToolName:      "read_file",
		WorkspaceRoot: ws,
		SideEffect:    SideEffectRead,
		Args:          map[string]any{"path": filepath.Join(ws, "main.go")},
	})
	if decision.Action == ActionDeny {
		t.Fatalf("ordinary workspace read was denied: %q", decision.Reason)
	}
}
