package sandbox

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestPermissionProfileFromPolicyBuildsWorkspaceWriteProfile(t *testing.T) {
	workspace := t.TempDir()
	extra := t.TempDir()
	denyRead := filepath.Join(workspace, "private")
	denyWrite := filepath.Join(workspace, "readonly")
	if err := mkdirAll(denyRead, denyWrite); err != nil {
		t.Fatal(err)
	}
	scope, err := NewScope(workspace, []string{extra})
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	policy := DefaultPolicy()
	policy.DenyRead = []string{denyRead}
	policy.DenyWrite = []string{denyWrite}

	profile := PermissionProfileFromPolicy(workspace, policy, scope)
	if profile.FileSystem.Kind != FileSystemRestricted {
		t.Fatalf("filesystem kind = %q, want restricted", profile.FileSystem.Kind)
	}
	roots := scope.Roots()
	if len(profile.FileSystem.WriteRoots) != len(roots) {
		t.Fatalf("write roots = %#v, want scope roots %#v", profile.FileSystem.WriteRoots, roots)
	}
	for i, root := range roots {
		if profile.FileSystem.WriteRoots[i].Root != root {
			t.Fatalf("write roots = %#v, want scope roots %#v", profile.FileSystem.WriteRoots, roots)
		}
	}
	if !stringSliceContains(profile.FileSystem.ReadRoots, profileRootPath()) {
		t.Fatalf("read roots = %#v, want full read root %q", profile.FileSystem.ReadRoots, profileRootPath())
	}
	if !stringSliceContains(profile.FileSystem.WriteRoots[0].ProtectedMetadataNames, ".zero") || !stringSliceContains(profile.FileSystem.WriteRoots[0].ProtectedMetadataNames, ".agents") {
		t.Fatalf("protected metadata names = %#v, want workspace metadata protected", profile.FileSystem.WriteRoots[0].ProtectedMetadataNames)
	}
	resolvedRoot := profile.FileSystem.WriteRoots[0].Root
	wantGitCarveouts := []string{filepath.Join(resolvedRoot, ".git", "hooks"), filepath.Join(resolvedRoot, ".git", "config")}
	for _, want := range wantGitCarveouts {
		if !stringSliceContains(profile.FileSystem.WriteRoots[0].ReadOnlySubpaths, want) {
			t.Fatalf("read-only subpaths = %#v, want git metadata carveout %q", profile.FileSystem.WriteRoots[0].ReadOnlySubpaths, want)
		}
	}
	// DenyRead may also carry default credential-store entries when the host
	// has them, so assert containment rather than an exact count. Compare the
	// normalized (symlink-resolved) form the profile stores.
	if !stringSliceContains(profile.FileSystem.DenyRead, normalizeProfilePaths([]string{denyRead})[0]) || len(profile.FileSystem.DenyWrite) != 1 {
		t.Fatalf("deny paths = %#v / %#v, want configured entries present", profile.FileSystem.DenyRead, profile.FileSystem.DenyWrite)
	}
	if profile.Network.Mode != NetworkDeny {
		t.Fatalf("network profile = %#v, want deny", profile.Network)
	}
	if !profile.RequiresPlatformSandbox() {
		t.Fatal("workspace-write profile must require a platform sandbox")
	}
}

func TestPermissionProfileFromPolicyIncludesDefaultTempWriteRoots(t *testing.T) {
	tmpdir := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("TEMP", tmpdir)
		t.Setenv("TMP", tmpdir)
	} else {
		t.Setenv("TMPDIR", tmpdir)
	}
	workspace := t.TempDir()

	profile := PermissionProfileFromPolicy(workspace, DefaultPolicy(), nil)
	if !writeRootsContain(profile.FileSystem.WriteRoots, workspace) {
		t.Fatalf("write roots = %#v, want workspace %q", profile.FileSystem.WriteRoots, workspace)
	}
	if !writeRootsContain(profile.FileSystem.WriteRoots, tmpdir) {
		t.Fatalf("write roots = %#v, want temp root %q", profile.FileSystem.WriteRoots, tmpdir)
	}
	// /tmp is a default temp write root on POSIX only (see
	// defaultTempWriteRootCandidatesForGOOS); on Windows the bare path resolves
	// against the current drive, so a stray C:\tmp must not turn this on.
	if runtime.GOOS != "windows" && pathExists("/tmp") && !writeRootsContain(profile.FileSystem.WriteRoots, "/tmp") {
		t.Fatalf("write roots = %#v, want /tmp", profile.FileSystem.WriteRoots)
	}
}

func writeRootsContain(roots []WritableRoot, want string) bool {
	want = normalizeProfilePath(want)
	for _, root := range roots {
		if normalizeProfilePath(root.Root) == want {
			return true
		}
	}
	return false
}

func TestUnknownNetworkModeFailsClosed(t *testing.T) {
	for _, mode := range []NetworkMode{"scoped", "proxy"} {
		if got := NormalizeNetworkMode(mode); got != NetworkDeny {
			t.Fatalf("NormalizeNetworkMode(%s) = %q, want %q", mode, got, NetworkDeny)
		}
	}
	profile := PermissionProfileFromPolicy(t.TempDir(), Policy{
		Mode:             ModeEnforce,
		Network:          NetworkMode("scoped"),
		EnforceWorkspace: true,
	}, nil)
	if profile.Network.Mode != NetworkDeny {
		t.Fatalf("unknown network mode profile = %#v, want deny", profile.Network)
	}
	if !shouldUnshareLinuxNetwork(NetworkPolicy{Mode: NetworkMode("scoped")}) {
		t.Fatal("unknown Linux network mode must unshare network")
	}
}

func TestPermissionProfileFromDisabledPolicyDoesNotRequirePlatformSandbox(t *testing.T) {
	policy := DefaultPolicy()
	policy.Mode = ModeDisabled
	profile := PermissionProfileFromPolicy(t.TempDir(), policy, nil)
	if profile.FileSystem.Kind != FileSystemUnrestricted || profile.Network.Mode != NetworkAllow {
		t.Fatalf("disabled profile = %#v, want unrestricted filesystem and allow network", profile)
	}
	if profile.RequiresPlatformSandbox() {
		t.Fatalf("disabled profile must not require platform sandbox: %#v", profile)
	}
}

func TestSandboxManagerBuildsExecutionRequestFromProfile(t *testing.T) {
	backend := Backend{Name: BackendLinuxBwrap, Available: true, Executable: "/usr/bin/zero-linux-sandbox", Platform: "linux"}
	policy := DefaultPolicy()
	profile := PermissionProfileFromPolicy("/workspace", policy, nil)
	request, err := NewSandboxManager(SandboxManagerOptions{GOOS: "linux", Backend: backend}).BuildExecutionRequest(SandboxManagerRequest{
		WorkspaceRoot:     "/workspace",
		Command:           CommandSpec{Name: "/bin/sh", Args: []string{"-c", "true"}, Dir: "/workspace"},
		Policy:            policy,
		Profile:           profile,
		Preference:        SandboxPreferenceAuto,
		ValidateExecution: true,
	})
	if err != nil {
		t.Fatalf("BuildExecutionRequest: %v", err)
	}
	if request.TargetBackend != BackendLinuxBwrap || !request.CommandWrapped || request.EnforcementLevel != EnforcementNative {
		t.Fatalf("execution request = %#v, want native linux-bwrap wrapping", request)
	}
	if request.PermissionProfile.FileSystem.Kind != FileSystemRestricted || !request.RequiresPlatformSandbox {
		t.Fatalf("execution request profile = %#v, requires=%t", request.PermissionProfile, request.RequiresPlatformSandbox)
	}
}

func TestSandboxManagerBuildsCommandPlanThroughLinuxHelper(t *testing.T) {
	backend := Backend{Name: BackendLinuxBwrap, Available: true, Executable: "/usr/bin/zero-linux-sandbox", Platform: "linux"}
	policy := DefaultPolicy()
	policy.BlockUnixSockets = true
	manager := NewSandboxManager(SandboxManagerOptions{GOOS: "linux", Backend: backend})
	plan, err := manager.BuildCommandPlan(SandboxManagerRequest{
		WorkspaceRoot:     "/workspace",
		Command:           CommandSpec{Name: "/bin/sh", Args: []string{"-c", "pwd"}, Dir: "/workspace/nested"},
		Policy:            policy,
		Profile:           PermissionProfileFromPolicy("/workspace", policy, nil),
		Preference:        SandboxPreferenceAuto,
		ValidateExecution: true,
	})
	if err != nil {
		t.Fatalf("BuildCommandPlan: %v", err)
	}
	if !plan.Wrapped || plan.Name != "/usr/bin/zero-linux-sandbox" || plan.TargetBackend != BackendLinuxBwrap {
		t.Fatalf("command plan = %#v, want native linux helper wrapper", plan)
	}
	if plan.EnforcementLevel != EnforcementNative {
		t.Fatalf("command metadata = %#v, want helper backend with native enforcement", plan)
	}
	assertArgsContainSequence(t, plan.Args, "--sandbox-policy-cwd", "/workspace")
	assertArgsContainSequence(t, plan.Args, "--command-cwd", "/workspace/nested")
	assertArgsContainSequence(t, plan.Args, "--block-unix-sockets")
	assertArgsContainSequence(t, plan.Args, "--", "/bin/sh", "-c", "pwd")
}

func TestSandboxManagerBuildsCommandPlanThroughWindowsRunner(t *testing.T) {
	// This exercises the native wrapped path, which requires the workspace to be
	// sandbox-initialized; stub the marker present (otherwise it degrades).
	restore := windowsSandboxInitialized
	t.Cleanup(func() { windowsSandboxInitialized = restore })
	windowsSandboxInitialized = func() bool { return true }
	backend := Backend{Name: BackendWindowsRestrictedToken, Available: true, Executable: `C:\zero\zero-windows-command-runner.exe`, Platform: "windows"}
	policy := DefaultPolicy()
	manager := NewSandboxManager(SandboxManagerOptions{GOOS: "windows", Backend: backend})
	plan, err := manager.BuildCommandPlan(SandboxManagerRequest{
		WorkspaceRoot:     `C:\workspace`,
		Command:           CommandSpec{Name: "cmd.exe", Args: []string{"/d", "/s", "/c", "dir"}, Dir: `C:\workspace\src`, Env: []string{"PATH=C:\\Tools", "TERM=xterm"}},
		Policy:            policy,
		Profile:           PermissionProfileFromPolicy(`C:\workspace`, policy, nil),
		Preference:        SandboxPreferenceAuto,
		ValidateExecution: true,
	})
	if err != nil {
		t.Fatalf("BuildCommandPlan: %v", err)
	}
	if !plan.Wrapped || plan.Name != `C:\zero\zero-windows-command-runner.exe` || plan.TargetBackend != BackendWindowsRestrictedToken {
		t.Fatalf("command plan = %#v, want native windows command runner wrapper", plan)
	}
	if plan.EnforcementLevel != EnforcementNative {
		t.Fatalf("command metadata = %#v, want native restricted-token backend", plan)
	}
	assertArgsContainSequence(t, plan.Args, "--command-cwd", `C:\workspace\src`)
	assertArgsContainSequence(t, plan.Args, "--sandbox-home")
	assertArgsContainSequence(t, plan.Args, "--windows-sandbox-level", string(WindowsSandboxLevelRestrictedToken))
	assertArgsContainSequence(t, plan.Args, "--workspace-root", `C:\workspace`)
	assertArgsContainSequence(t, plan.Args, "--", "cmd.exe", "/d", "/s", "/c", "dir")

	config, err := ParseWindowsSandboxCommandArgs(plan.Args)
	if err != nil {
		t.Fatalf("ParseWindowsSandboxCommandArgs: %v", err)
	}
	if config.SandboxHome == "" || config.CommandCWD != `C:\workspace\src` || len(config.WorkspaceRoots) != 1 || config.WorkspaceRoots[0] != `C:\workspace` {
		t.Fatalf("parsed roots = %#v cwd=%q, want workspace root and command cwd", config.WorkspaceRoots, config.CommandCWD)
	}
	if config.PermissionProfile.FileSystem.Kind != FileSystemRestricted || config.PermissionProfile.Network.Mode != NetworkDeny {
		t.Fatalf("parsed permission profile = %#v, want restricted deny profile", config.PermissionProfile)
	}
	if config.Env[EnvSandboxed] != "1" || config.Env[EnvSandboxBackend] != string(BackendWindowsRestrictedToken) || config.Env["COMSPEC"] == "" {
		t.Fatalf("parsed env = %#v, want sandbox markers and COMSPEC", config.Env)
	}
}

func TestSandboxManagerDegradesUnavailableCommandPlan(t *testing.T) {
	policy := DefaultPolicy()
	backend := Backend{Name: BackendUnavailable, Platform: "windows", Fallback: true, Message: "native sandbox unavailable"}
	manager := NewSandboxManager(SandboxManagerOptions{GOOS: "windows", Backend: backend})
	plan, err := manager.BuildCommandPlan(SandboxManagerRequest{
		WorkspaceRoot:     `C:\workspace`,
		Command:           CommandSpec{Name: "cmd.exe", Args: []string{"/c", "dir"}, Dir: `C:\workspace`},
		Policy:            policy,
		Profile:           PermissionProfileFromPolicy(`C:\workspace`, policy, nil),
		Preference:        SandboxPreferenceAuto,
		ValidateExecution: true,
	})
	if err != nil {
		t.Fatalf("BuildCommandPlan: %v", err)
	}
	if plan.Wrapped || plan.EnforcementLevel != EnforcementDegraded || plan.DowngradeReason != "native sandbox unavailable" {
		t.Fatalf("plan = %#v, want degraded direct plan", plan)
	}
}

func TestSandboxManagerSelectsPlatformBackend(t *testing.T) {
	tests := []struct {
		name       string
		goos       string
		lookupName string
		lookupPath string
		setupPath  string
		want       BackendName
		wantTarget BackendName
	}{
		{name: "linux", goos: "linux", lookupName: LinuxSandboxHelperName, lookupPath: "/usr/bin/zero-linux-sandbox", want: BackendLinuxBwrap, wantTarget: BackendLinuxBwrap},
		{name: "macos", goos: "darwin", lookupName: "sandbox-exec", lookupPath: "/usr/bin/sandbox-exec", want: BackendMacOSSeatbelt, wantTarget: BackendMacOSSeatbelt},
		{name: "windows", goos: "windows", lookupName: WindowsSandboxCommandRunnerName, lookupPath: `C:\zero\zero-windows-command-runner.exe`, setupPath: `C:\zero\zero-windows-sandbox-setup.exe`, want: BackendWindowsRestrictedToken, wantTarget: BackendWindowsRestrictedToken},
		{name: "unsupported", goos: "plan9", want: BackendUnavailable, wantTarget: BackendUnavailable},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := NewSandboxManager(SandboxManagerOptions{
				GOOS: test.goos,
				LookupExecutable: func(name string) (string, error) {
					if name == test.lookupName && test.lookupPath != "" {
						return test.lookupPath, nil
					}
					if test.goos == "linux" && name == "bwrap" {
						return "/usr/bin/bwrap", nil
					}
					if name == WindowsSandboxSetupName && test.setupPath != "" {
						return test.setupPath, nil
					}
					return "", errors.New("missing")
				},
			})
			backend := manager.Backend()
			if backend.Name != test.want {
				t.Fatalf("backend = %#v, want %q", backend, test.want)
			}
			if backend.TargetBackend() != test.wantTarget {
				t.Fatalf("target backend = %q, want %q for %#v", backend.TargetBackend(), test.wantTarget, backend)
			}
		})
	}
}

func TestSandboxManagerInfersPlatformFromExplicitBackend(t *testing.T) {
	tests := []struct {
		name     string
		backend  BackendName
		wantGOOS string
	}{
		{name: "linux helper", backend: BackendLinuxBwrap, wantGOOS: "linux"},
		{name: "macos seatbelt", backend: BackendMacOSSeatbelt, wantGOOS: "darwin"},
		{name: "windows runner", backend: BackendWindowsRestrictedToken, wantGOOS: "windows"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := NewSandboxManager(SandboxManagerOptions{
				Backend: Backend{Name: test.backend, Available: true, Executable: "sandbox-helper"},
			})
			if manager.goos != test.wantGOOS || manager.backend.Platform != test.wantGOOS {
				t.Fatalf("manager = %#v, want platform/goos %q", manager, test.wantGOOS)
			}
		})
	}
}

func TestSelectBackendDelegatesToSandboxManagerSelection(t *testing.T) {
	backend := SelectBackend(BackendOptions{
		GOOS: "linux",
		LookupExecutable: func(name string) (string, error) {
			if name == LinuxSandboxHelperName {
				return "/usr/bin/zero-linux-sandbox", nil
			}
			if name == "bwrap" {
				return "/usr/bin/bwrap", nil
			}
			return "", errors.New("missing")
		},
	})
	managerBackend := NewSandboxManager(SandboxManagerOptions{
		GOOS: "linux",
		LookupExecutable: func(name string) (string, error) {
			if name == LinuxSandboxHelperName {
				return "/usr/bin/zero-linux-sandbox", nil
			}
			if name == "bwrap" {
				return "/usr/bin/bwrap", nil
			}
			return "", errors.New("missing")
		},
	}).Backend()
	if !reflect.DeepEqual(backend, managerBackend) {
		t.Fatalf("SelectBackend = %#v, manager backend = %#v", backend, managerBackend)
	}
}

func TestSandboxManagerFailsClosedWhenNativeRequiredAndUnavailable(t *testing.T) {
	policy := DefaultPolicy()
	profile := PermissionProfileFromPolicy("/workspace", policy, nil)
	_, err := NewSandboxManager(SandboxManagerOptions{
		GOOS:    "windows",
		Backend: Backend{Name: BackendUnavailable, Platform: "windows", Fallback: true},
	}).BuildExecutionRequest(SandboxManagerRequest{
		WorkspaceRoot:     "/workspace",
		Command:           CommandSpec{Name: "cmd.exe", Dir: "/workspace"},
		Policy:            policy,
		Profile:           profile,
		Preference:        SandboxPreferenceRequire,
		ValidateExecution: true,
	})
	if !errors.Is(err, errNativeSandboxUnavailable) {
		t.Fatalf("BuildExecutionRequest error = %v, want native sandbox unavailable", err)
	}
}

func mkdirAll(paths ...string) error {
	for _, path := range paths {
		if err := os.MkdirAll(path, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func TestCredentialDenyReadPathsIn(t *testing.T) {
	home := t.TempDir()
	awsDir := filepath.Join(home, ".aws")
	gcloudDir := filepath.Join(home, ".config", "gcloud")
	zeroDir := filepath.Join(home, "config", "zero")
	if err := mkdirAll(awsDir, gcloudDir, zeroDir); err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(home, "sa-key.json")
	oauthDir := filepath.Join(home, "oauth-store")
	mcpDir := filepath.Join(home, "mcp-store")
	if err := mkdirAll(oauthDir, mcpDir); err != nil {
		t.Fatal(err)
	}
	oauthOverride := filepath.Join(oauthDir, "tokens.json")
	mcpOverride := filepath.Join(mcpDir, "tokens.json")
	overrideFiles := []string{
		oauthOverride,
		oauthOverride + ".tmp",
		oauthOverride + ".lockfile",
		oauthOverride + ".secret",
		oauthOverride + ".secret.tmp",
		oauthOverride + ".secret.lock",
		mcpOverride,
		mcpOverride + ".tmp",
		mcpOverride + ".lockfile",
		mcpOverride + ".secret",
		mcpOverride + ".secret.tmp",
		mcpOverride + ".secret.lock",
		mcpOverride + ".migrated",
	}
	// The migrated legacy MCP token backup and the atomic-write temp siblings
	// every store publishes before its rename; none of these are itemized by
	// name, so they only stay protected if the whole zeroDir is denied.
	zeroFiles := []string{
		filepath.Join(zeroDir, "config.json"),
		filepath.Join(zeroDir, "credentials.json"),
		filepath.Join(zeroDir, "credentials.enc"),
		filepath.Join(zeroDir, "credentials.enc.secret"),
		filepath.Join(zeroDir, "oauth-tokens.json"),
		filepath.Join(zeroDir, "oauth-tokens.json.secret"),
		filepath.Join(zeroDir, "mcp-oauth-tokens.json"),
		filepath.Join(zeroDir, "mcp-oauth-tokens.json.secret"),
		filepath.Join(zeroDir, "mcp-oauth-tokens.json.migrated"),
		filepath.Join(zeroDir, "oauth-tokens.json.tmp-1234-5678"),
		filepath.Join(zeroDir, "credentials.enc.9-1.tmp"),
		filepath.Join(zeroDir, ".zero-config-1.tmp"),
	}
	for _, path := range append(append([]string{keyFile}, overrideFiles...), zeroFiles...) {
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	options := credentialPathOptions{
		Home:              home,
		GoogleCredentials: keyFile,
		ZeroConfigDir:     filepath.Join(home, "config"),
		OAuthTokens:       oauthOverride,
		MCPOAuthTokens:    mcpOverride,
	}
	paths := credentialDenyReadPathsIn(options, nil)
	wantPaths := append([]string{awsDir, gcloudDir, keyFile, zeroDir}, overrideFiles...)
	for _, want := range normalizeProfilePaths(wantPaths) {
		if !stringSliceContains(paths, want) {
			t.Errorf("credential deny paths = %#v, want %q included", paths, want)
		}
	}
	// zeroFiles is covered by the zeroDir subpath deny above, not by an
	// itemized entry — including the never-enumerated migrated backup and
	// temp-write siblings.
	for _, zeroFile := range zeroFiles {
		if stringSliceContains(paths, normalizeProfilePaths([]string{zeroFile})[0]) {
			t.Errorf("credential deny paths = %#v, want itemized %q dropped in favor of the zeroDir subpath rule", paths, zeroFile)
		}
	}

	// A default candidate absent from disk at profile-build time is still
	// emitted so pathname-policy backends can reserve it. Mount-based Linux
	// retains the same profile baseline but can mask only paths that exist when
	// Bubblewrap assembles the namespace.
	if !stringSliceContains(paths, filepath.Join(home, ".azure")) {
		t.Errorf("credential deny paths = %#v, want the not-yet-existing ~/.azure included", paths)
	}

	// An explicit AllowRead entry covering a store is an opt-out.
	optedOut := credentialDenyReadPathsIn(options, []string{awsDir, zeroDir})
	if stringSliceContains(optedOut, normalizeProfilePaths([]string{awsDir})[0]) {
		t.Errorf("credential deny paths = %#v, want AllowRead opt-out to drop ~/.aws", optedOut)
	}
	if stringSliceContains(optedOut, normalizeProfilePaths([]string{zeroDir})[0]) {
		t.Errorf("credential deny paths = %#v, want AllowRead opt-out to drop %q", optedOut, zeroDir)
	}
	if !stringSliceContains(optedOut, normalizeProfilePaths([]string{keyFile})[0]) {
		t.Errorf("credential deny paths = %#v, want unrelated entries kept after opt-out", optedOut)
	}

	if got := credentialDenyReadPathsIn(credentialPathOptions{}, nil); len(got) != 0 {
		t.Errorf("credential deny paths for blank home = %#v, want none", got)
	}

	// The GOOGLE_APPLICATION_CREDENTIALS target stays protected even when no
	// home directory is resolvable.
	homeless := credentialDenyReadPathsIn(credentialPathOptions{GoogleCredentials: keyFile}, nil)
	if !stringSliceContains(homeless, normalizeProfilePaths([]string{keyFile})[0]) {
		t.Errorf("credential deny paths without home = %#v, want key file included", homeless)
	}
}

// TestCredentialDenyReadPathsInOverrideMatchesStoreResolution reproduces the
// audit finding that a relative-and-tilde ZERO_OAUTH_TOKENS_PATH /
// ZERO_MCP_OAUTH_TOKENS_PATH override produced a deny rule for a DIFFERENT
// path than the one the token stores actually resolve (oauth.ResolveStorePath
// / mcp.ResolveTokenStorePath never expand "~"; they resolve a relative
// override literally against the working directory), leaving the real file
// unprotected.
func TestCredentialPathOptionsResolveAgainstCommandDirectory(t *testing.T) {
	commandDir := t.TempDir()
	override := "~/relative-tilde-tokens.json"
	options := credentialPathOptionsFromEnvironment(commandDir, []string{
		"HOME=",
		"USERPROFILE=" + filepath.Join(commandDir, "profile-home"),
		"XDG_CONFIG_HOME=~/literal-xdg",
		"ZERO_OAUTH_TOKENS_PATH=" + override,
		"ZERO_MCP_OAUTH_TOKENS_PATH=mcp/tokens.json",
	})
	paths := credentialDenyReadPathsIn(options, nil)

	wantHome := filepath.Join(commandDir, "profile-home")
	if options.Home != wantHome {
		t.Fatalf("home = %q, want USERPROFILE fallback %q", options.Home, wantHome)
	}
	wantConfig := filepath.Join(commandDir, "~", "literal-xdg")
	if options.ZeroConfigDir != wantConfig {
		t.Fatalf("config dir = %q, want command-relative literal XDG path %q", options.ZeroConfigDir, wantConfig)
	}
	for _, want := range []string{
		filepath.Join(wantConfig, "zero"),
		filepath.Join(commandDir, override),
		filepath.Join(commandDir, override) + ".tmp",
		filepath.Join(commandDir, override) + ".lockfile",
		filepath.Join(commandDir, override) + ".secret.lock",
		filepath.Join(commandDir, "mcp", "tokens.json"),
		filepath.Join(commandDir, "mcp", "tokens.json.tmp"),
		filepath.Join(commandDir, "mcp", "tokens.json.lockfile"),
		filepath.Join(commandDir, "mcp", "tokens.json.secret"),
		filepath.Join(commandDir, "mcp", "tokens.json.secret.tmp"),
		filepath.Join(commandDir, "mcp", "tokens.json.secret.lock"),
		filepath.Join(commandDir, "mcp", "tokens.json.migrated"),
	} {
		if !stringSliceContains(paths, want) {
			t.Errorf("credential deny paths = %#v, want command-relative root %q", paths, want)
		}
	}
}

func TestCredentialDenyReadPathsInConfigDirMatchesLiteralXDGResolution(t *testing.T) {
	configDir := "~/literal-xdg"
	commandDir := t.TempDir()
	resolvedConfigDir := credentialPathOptionsFromEnvironment(commandDir, []string{"XDG_CONFIG_HOME=" + configDir}).ZeroConfigDir
	paths := credentialDenyReadPathsIn(credentialPathOptions{ZeroConfigDir: resolvedConfigDir}, nil)

	want := filepath.Join(commandDir, configDir, "zero")
	if resolvedConfigDir != filepath.Dir(want) {
		t.Fatalf("zero credential config dir = %q, want literal XDG resolution %q", resolvedConfigDir, filepath.Dir(want))
	}
	if !stringSliceContains(paths, want) {
		t.Fatalf("credential deny paths = %#v, want literal XDG resolution %q", paths, want)
	}
	if expanded := normalizeProfilePaths([]string{filepath.Join(configDir, "zero")})[0]; expanded != want && stringSliceContains(paths, expanded) {
		t.Fatalf("credential deny paths = %#v, must not use tilde-expanded XDG path %q", paths, expanded)
	}
}

func TestBuildCommandPlanUsesCommandCredentialContext(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows credential deny-read is tracked separately")
	}
	workspace := t.TempDir()
	commandDir := filepath.Join(workspace, "nested")
	if err := os.MkdirAll(commandDir, 0o755); err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: workspace,
		Policy:        DefaultPolicy(),
		Backend:       Backend{Name: BackendUnavailable, Platform: runtime.GOOS},
	})
	plan, err := engine.BuildCommandPlan(CommandSpec{
		Name: "true",
		Dir:  commandDir,
		Env: []string{
			"HOME=" + filepath.Join(workspace, "home"),
			"ZERO_OAUTH_TOKENS_PATH=credentials/tokens.json",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(plan.Dir, "credentials", "tokens.json")
	if !stringSliceContains(plan.PermissionProfile.FileSystem.DenyReadIfExists, want) {
		t.Fatalf("DenyReadIfExists = %#v, want command-relative override %q", plan.PermissionProfile.FileSystem.DenyReadIfExists, want)
	}
	if stringSliceContains(plan.PermissionProfile.FileSystem.DenyReadIfExists, filepath.Dir(want)) {
		t.Fatalf("DenyReadIfExists = %#v, must not mask override parent %q", plan.PermissionProfile.FileSystem.DenyReadIfExists, filepath.Dir(want))
	}
}

func TestPermissionProfileIncludesZeroCredentialPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows credential deny-read is tracked separately")
	}
	// Resolve the temp base up front so macOS /var -> /private/var does not
	// diverge between the pre-mkdir Clean fallback and the post-mkdir
	// EvalSymlinks success path inside normalizeProfilePath.
	configHome := resolvedTempDir(t)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	zeroDir := filepath.Join(configHome, "zero")

	// Build the profile before the store directory exists to verify that profile
	// derivation retains the baseline candidate. Pathname-policy backends can
	// reserve it immediately; mount-based Linux applies it only if the path
	// exists when Bubblewrap assembles the namespace.
	profile := PermissionProfileFromPolicy(t.TempDir(), DefaultPolicy(), nil)
	want := normalizeProfilePaths([]string{zeroDir})[0]
	if !stringSliceContains(profile.FileSystem.DenyReadIfExists, want) {
		t.Fatalf("DenyReadIfExists = %#v, want Zero config directory %q even before it exists", profile.FileSystem.DenyReadIfExists, want)
	}

	if err := os.MkdirAll(zeroDir, 0o700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(zeroDir, "oauth-tokens.json")
	if err := os.WriteFile(secret, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	migrated := filepath.Join(zeroDir, "mcp-oauth-tokens.json.migrated")
	if err := os.WriteFile(migrated, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Re-derive after the files exist: the same directory rule covers both
	// the known store filename and the never-itemized migrated backup.
	// Recompute want once the directory exists so EvalSymlinks can resolve
	// the full path (macOS would otherwise compare a pre-mkdir Clean path
	// against a post-mkdir /private/var form).
	profile = PermissionProfileFromPolicy(t.TempDir(), DefaultPolicy(), nil)
	want = normalizeProfilePaths([]string{zeroDir})[0]
	if !stringSliceContains(profile.FileSystem.DenyReadIfExists, want) {
		t.Fatalf("DenyReadIfExists = %#v, want Zero config directory %q", profile.FileSystem.DenyReadIfExists, want)
	}
}
