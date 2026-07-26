package sandbox

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestBuildLinuxSandboxCommandArgsSerializesPermissionProfile(t *testing.T) {
	profile := PermissionProfile{
		FileSystem: FileSystemPolicy{
			Kind:      FileSystemRestricted,
			ReadRoots: []string{"/workspace"},
			WriteRoots: []WritableRoot{{
				Root:                   "/workspace",
				ProtectedMetadataNames: []string{".git", ".zero"},
			}},
			IncludePlatformRoots: true,
			AllowTemp:            true,
		},
		Network: NetworkPolicy{Mode: NetworkDeny},
	}
	args, err := BuildLinuxSandboxCommandArgs(LinuxSandboxCommandArgsOptions{
		SandboxPolicyCWD:  "/workspace",
		CommandCWD:        "/workspace/app",
		PermissionProfile: profile,
		UseLandlock:       true,
		BlockUnixSockets:  true,
		Command:           []string{"/bin/sh", "-c", "pwd"},
	})
	if err != nil {
		t.Fatalf("BuildLinuxSandboxCommandArgs: %v", err)
	}

	wantPrefix := []string{"--sandbox-policy-cwd", "/workspace", "--command-cwd", "/workspace/app", "--permission-profile"}
	if len(args) < len(wantPrefix)+1 || !reflect.DeepEqual(args[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("args prefix = %#v, want %#v", args, wantPrefix)
	}
	var gotProfile PermissionProfile
	if err := json.Unmarshal([]byte(args[len(wantPrefix)]), &gotProfile); err != nil {
		t.Fatalf("permission profile JSON: %v", err)
	}
	if !reflect.DeepEqual(gotProfile, profile) {
		t.Fatalf("permission profile = %#v, want %#v", gotProfile, profile)
	}
	separator := indexString(args, "--")
	if separator < 0 {
		t.Fatalf("args missing command separator: %#v", args)
	}
	if !reflect.DeepEqual(args[separator+1:], []string{"/bin/sh", "-c", "pwd"}) {
		t.Fatalf("command args = %#v", args[separator+1:])
	}
	if !stringSliceContains(args, "--use-landlock") || !stringSliceContains(args, "--block-unix-sockets") {
		t.Fatalf("args missing helper feature flags: %#v", args)
	}
}

func TestParseLinuxSandboxHelperArgs(t *testing.T) {
	profile := DefaultPermissionProfile("/workspace")
	args, err := BuildLinuxSandboxCommandArgs(LinuxSandboxCommandArgsOptions{
		SandboxPolicyCWD:     "/workspace",
		PermissionProfile:    profile,
		ApplySeccompThenExec: true,
		BlockUnixSockets:     true,
		NoProc:               true,
		Command:              []string{"true"},
	})
	if err != nil {
		t.Fatalf("BuildLinuxSandboxCommandArgs: %v", err)
	}
	config, err := ParseLinuxSandboxHelperArgs(args)
	if err != nil {
		t.Fatalf("ParseLinuxSandboxHelperArgs: %v", err)
	}
	if config.SandboxPolicyCWD != "/workspace" || config.CommandCWD != "/workspace" {
		t.Fatalf("cwd config = %#v", config)
	}
	if !config.ApplySeccompThenExec || !config.BlockUnixSockets || !config.NoProc {
		t.Fatalf("feature config = %#v", config)
	}
	if !reflect.DeepEqual(config.PermissionProfile, profile) || !reflect.DeepEqual(config.Command, []string{"true"}) {
		t.Fatalf("parsed config = %#v", config)
	}
}

func TestBuildLinuxSandboxBwrapArgsWrapsInnerSeccompStage(t *testing.T) {
	helperPath := filepath.Join(t.TempDir(), LinuxSandboxHelperName)
	if err := os.WriteFile(helperPath, []byte("helper"), 0o755); err != nil {
		t.Fatalf("WriteFile helper: %v", err)
	}
	args, err := BuildLinuxSandboxCommandArgs(LinuxSandboxCommandArgsOptions{
		SandboxPolicyCWD:  "/workspace",
		PermissionProfile: DefaultPermissionProfile("/workspace"),
		BlockUnixSockets:  true,
		Command:           []string{"true"},
	})
	if err != nil {
		t.Fatalf("BuildLinuxSandboxCommandArgs: %v", err)
	}
	config, err := ParseLinuxSandboxHelperArgs(args)
	if err != nil {
		t.Fatalf("ParseLinuxSandboxHelperArgs: %v", err)
	}
	bwrapArgs, err := BuildLinuxSandboxBwrapArgs(LinuxSandboxBwrapOptions{
		Config:     config,
		HelperPath: helperPath,
	})
	if err != nil {
		t.Fatalf("BuildLinuxSandboxBwrapArgs: %v", err)
	}
	for _, want := range [][]string{
		{"--new-session"},
		{"--die-with-parent"},
		{"--unshare-user"},
		{"--unshare-pid"},
		{"--unshare-net"},
		{"--ro-bind", "/", "/"},
		{"--chdir", "/workspace"},
		{"--setenv", EnvSandboxBackend, string(BackendLinuxBwrap)},
		{"--ro-bind", helperPath, helperPath},
		{"--", helperPath},
		{"--apply-seccomp-then-exec"},
		{"--block-unix-sockets"},
		{"--", "true"},
	} {
		assertArgsContainSequence(t, bwrapArgs, want...)
	}
	if argsContainSequence(bwrapArgs, "--tmpfs", "/") {
		t.Fatalf("default workspace-write profile must not start from an empty root: %#v", bwrapArgs)
	}
	if argsContainSequence(bwrapArgs, "--tmpfs", "/tmp") {
		t.Fatalf("default workspace-write profile must not replace host /tmp: %#v", bwrapArgs)
	}
	if stringSliceContains(bwrapArgs, "--clearenv") {
		t.Fatalf("Linux bwrap args must preserve caller environment like upstream: %#v", bwrapArgs)
	}
	for _, unwanted := range []string{"--unshare-ipc", "--unshare-uts"} {
		if stringSliceContains(bwrapArgs, unwanted) {
			t.Fatalf("Linux bwrap args should match upstream namespace set; found %s in %#v", unwanted, bwrapArgs)
		}
	}
}

func TestBuildLinuxSandboxBwrapArgsKeepsHostNetworkWhenAllowed(t *testing.T) {
	helperPath := filepath.Join(t.TempDir(), LinuxSandboxHelperName)
	if err := os.WriteFile(helperPath, []byte("helper"), 0o755); err != nil {
		t.Fatalf("WriteFile helper: %v", err)
	}
	profile := DefaultPermissionProfile("/workspace")
	profile.Network = NetworkPolicy{Mode: NetworkAllow}
	args, err := BuildLinuxSandboxCommandArgs(LinuxSandboxCommandArgsOptions{
		SandboxPolicyCWD:  "/workspace",
		PermissionProfile: profile,
		BlockUnixSockets:  true,
		Command:           []string{"python3", "-m", "http.server", "8000"},
	})
	if err != nil {
		t.Fatalf("BuildLinuxSandboxCommandArgs: %v", err)
	}
	config, err := ParseLinuxSandboxHelperArgs(args)
	if err != nil {
		t.Fatalf("ParseLinuxSandboxHelperArgs: %v", err)
	}
	bwrapArgs, err := BuildLinuxSandboxBwrapArgs(LinuxSandboxBwrapOptions{
		Config:     config,
		HelperPath: helperPath,
	})
	if err != nil {
		t.Fatalf("BuildLinuxSandboxBwrapArgs: %v", err)
	}
	if indexString(bwrapArgs, "--unshare-net") >= 0 {
		t.Fatalf("network-allowed bwrap args must not isolate loopback: %#v", bwrapArgs)
	}
	assertArgsContainSequence(t, bwrapArgs, "--setenv", "ZERO_SANDBOX_NETWORK", string(NetworkAllow))
}

func TestLinuxBwrapRootReadUsesReadOnlyHostRoot(t *testing.T) {
	profile := PermissionProfile{
		FileSystem: FileSystemPolicy{
			Kind:                 FileSystemRestricted,
			ReadRoots:            []string{string(filepath.Separator)},
			WriteRoots:           []WritableRoot{{Root: "/workspace"}},
			IncludePlatformRoots: true,
			AllowTemp:            true,
		},
		Network: NetworkPolicy{Mode: NetworkAllow},
	}

	args := linuxBwrapFilesystemArgs(profile)
	assertArgsContainSequence(t, args, "--ro-bind", "/", "/")
	if argsContainSequence(args, "--tmpfs", "/") {
		t.Fatalf("root-read profile must not start from an empty root: %#v", args)
	}
}

func TestLinuxBwrapTempUsesHostWriteRoots(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux bwrap temp root assertions use Unix paths")
	}
	tmpdir := t.TempDir()
	t.Setenv("TMPDIR", tmpdir)
	workspace := filepath.Join(tmpdir, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll workspace: %v", err)
	}
	profile := PermissionProfile{
		FileSystem: FileSystemPolicy{
			Kind:       FileSystemRestricted,
			ReadRoots:  []string{string(filepath.Separator)},
			WriteRoots: []WritableRoot{{Root: workspace, ProtectedMetadataNames: []string{".git"}}},
			AllowTemp:  true,
		},
		Network: NetworkPolicy{Mode: NetworkAllow},
	}

	args := linuxBwrapFilesystemArgs(profile)
	if argsContainSequence(args, "--tmpfs", "/tmp") {
		t.Fatalf("workspace-write temp access must bind host /tmp, not create private tmpfs: %#v", args)
	}
	for _, tempRoot := range defaultTempWriteRoots() {
		if pathExists(tempRoot) {
			assertArgsContainSequence(t, args, "--bind", tempRoot, tempRoot)
		}
	}
	assertArgsContainSequence(t, args, "--bind", workspace, workspace)

	if runtime.GOOS == "linux" {
		tmpdirBind := argsSequenceIndex(args, "--bind", tmpdir, tmpdir)
		workspaceBind := argsSequenceIndex(args, "--bind", workspace, workspace)
		if tmpdirBind < 0 || workspaceBind < 0 || tmpdirBind > workspaceBind {
			t.Fatalf("broader temp root must be bound before nested workspace root; args=%#v", args)
		}
	}
}

func TestLinuxBwrapFilesystemPlanPreservesMissingProtectedMetadata(t *testing.T) {
	workspace := t.TempDir()
	existing := filepath.Join(workspace, ".git")
	if err := os.Mkdir(existing, 0o755); err != nil {
		t.Fatalf("Mkdir existing metadata: %v", err)
	}
	missing := filepath.Join(workspace, ".zero")
	profile := PermissionProfile{
		FileSystem: FileSystemPolicy{
			Kind:      FileSystemRestricted,
			ReadRoots: []string{string(filepath.Separator)},
			WriteRoots: []WritableRoot{{
				Root:                   workspace,
				ProtectedMetadataNames: []string{".git", ".zero"},
			}},
		},
		Network: NetworkPolicy{Mode: NetworkDeny},
	}

	plan := buildLinuxBwrapFilesystemPlan(profile)
	assertArgsContainSequence(t, plan.Args, "--ro-bind", existing, existing)
	if argsContainSequence(plan.Args, "--tmpfs", missing) || argsContainSequence(plan.Args, "--ro-bind", missing, missing) {
		t.Fatalf("missing protected metadata must remain absent inside the sandbox: %#v", plan.Args)
	}
	if !reflect.DeepEqual(plan.ProtectedCreateTargets, []string{missing}) {
		t.Fatalf("protected create targets = %#v, want %#v", plan.ProtectedCreateTargets, []string{missing})
	}
}

func TestLinuxBwrapSkipsMissingCredentialBaselines(t *testing.T) {
	root := t.TempDir()
	missingCredential := filepath.Join(root, "home", ".config", "zero")
	profile := PermissionProfile{
		FileSystem: FileSystemPolicy{
			Kind:             FileSystemRestricted,
			ReadRoots:        []string{string(filepath.Separator)},
			WriteRoots:       []WritableRoot{{Root: root}},
			DenyReadIfExists: []string{missingCredential},
		},
		Network: NetworkPolicy{Mode: NetworkDeny},
	}

	plan := buildLinuxBwrapFilesystemPlan(profile)
	if stringSliceContains(plan.Args, missingCredential) {
		t.Fatalf("absent credential baseline must not become a mount target: %#v", plan.Args)
	}
	if stringSliceContains(plan.ProtectedCreateTargets, missingCredential) {
		t.Fatalf("credential baselines are not workspace metadata create targets: %#v", plan.ProtectedCreateTargets)
	}
	if _, err := os.Stat(filepath.Dir(missingCredential)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("building the plan materialized a host path: %v", err)
	}

	if err := os.MkdirAll(missingCredential, 0o700); err != nil {
		t.Fatalf("MkdirAll credential dir: %v", err)
	}
	plan = buildLinuxBwrapFilesystemPlan(profile)
	assertArgsContainSequence(t, plan.Args, "--perms", "000", "--tmpfs", missingCredential, "--remount-ro", missingCredential)
}

// TestLinuxBwrapCreatesOwnedCredentialDirsBeforeMasking covers the long-lived
// session race: bubblewrap cannot mount over a path that does not exist, so a
// store written after the namespace was assembled would stay readable through
// the live read-only host-root bind. Zero's own directories are therefore
// created up front and masked, unlike third-party stores it must not create.
func TestLinuxBwrapCreatesOwnedCredentialDirsBeforeMasking(t *testing.T) {
	root := t.TempDir()
	ownedDir := filepath.Join(root, "config", "zero")
	thirdParty := filepath.Join(root, "home", ".aws")
	profile := PermissionProfile{
		FileSystem: FileSystemPolicy{
			Kind:               FileSystemRestricted,
			ReadRoots:          []string{string(filepath.Separator)},
			WriteRoots:         []WritableRoot{{Root: root}},
			DenyReadIfExists:   []string{ownedDir, thirdParty},
			EnsureDenyReadDirs: []string{ownedDir},
		},
		Network: NetworkPolicy{Mode: NetworkDeny},
	}

	plan := buildLinuxBwrapFilesystemPlan(profile)
	if info, err := os.Stat(ownedDir); err != nil || !info.IsDir() {
		t.Fatalf("owned credential dir was not created: err=%v", err)
	}
	assertArgsContainSequence(t, plan.Args, "--perms", "000", "--tmpfs", ownedDir, "--remount-ro", ownedDir)
	if stringSliceContains(plan.Args, thirdParty) {
		t.Fatalf("absent third-party store must stay unmounted and uncreated: %#v", plan.Args)
	}
	if _, err := os.Stat(thirdParty); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("third-party store must not be created by the sandbox: %v", err)
	}
}

// TestLinuxBwrapKeepsCarveoutsReachableInsideMaskedDir covers the user plugin
// root inside the denied Zero config directory: the mask has to keep the
// traverse bit and re-bind the carveout, otherwise an installed user plugin
// cannot be executed through the sandbox at all.
func TestLinuxBwrapKeepsCarveoutsReachableInsideMaskedDir(t *testing.T) {
	root := t.TempDir()
	credentialDir := filepath.Join(root, "config", "zero")
	pluginRoot := filepath.Join(credentialDir, "plugins")
	if err := os.MkdirAll(pluginRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	profile := PermissionProfile{
		FileSystem: FileSystemPolicy{
			Kind:              FileSystemRestricted,
			ReadRoots:         []string{string(filepath.Separator)},
			WriteRoots:        []WritableRoot{{Root: root}},
			DenyReadIfExists:  []string{credentialDir},
			DenyReadCarveouts: []string{pluginRoot},
		},
		Network: NetworkPolicy{Mode: NetworkDeny},
	}

	plan := buildLinuxBwrapFilesystemPlan(profile)
	// 111 rather than 000: a 000 directory cannot be traversed, so the re-bound
	// subpath below it would be unreachable. Contents stay unlistable either way.
	assertArgsContainSequence(t, plan.Args, "--perms", "111", "--tmpfs", credentialDir)
	assertArgsContainSequence(t, plan.Args, "--ro-bind", pluginRoot, pluginRoot)
	assertArgsContainSequence(t, plan.Args, "--remount-ro", credentialDir)
	bindIdx := argsSequenceIndex(plan.Args, "--ro-bind", pluginRoot, pluginRoot)
	remountIdx := argsSequenceIndex(plan.Args, "--remount-ro", credentialDir)
	if bindIdx < 0 || remountIdx < 0 || bindIdx > remountIdx {
		t.Fatalf("carveout bind (%d) must precede the tmpfs remount-ro (%d): %#v", bindIdx, remountIdx, plan.Args)
	}
}

func TestLinuxBwrapUnrestrictedFilesystemUsesWritableHostRoot(t *testing.T) {
	profile := PermissionProfile{
		FileSystem: FileSystemPolicy{
			Kind:      FileSystemUnrestricted,
			AllowTemp: true,
		},
		Network: NetworkPolicy{Mode: NetworkDeny},
	}

	args := linuxBwrapFilesystemArgs(profile)
	assertArgsContainSequence(t, args, "--bind", "/", "/")
	if argsContainSequence(args, "--ro-bind", "/", "/") {
		t.Fatalf("unrestricted filesystem profile must not make host root read-only: %#v", args)
	}
	if argsContainSequence(args, "--tmpfs", "/tmp") {
		t.Fatalf("unrestricted filesystem profile must not replace host /tmp: %#v", args)
	}
	if argsContainSequence(args, "--dev", "/dev") {
		t.Fatalf("unrestricted filesystem profile must not replace host /dev: %#v", args)
	}
}

func TestLinuxHelperSandboxEnvironmentPreservesCallerEnv(t *testing.T) {
	env := linuxHelperSandboxEnvironment(
		PermissionProfile{Network: NetworkPolicy{Mode: NetworkDeny}},
		[]string{
			"PATH=/custom/bin",
			"HOME=/home/user",
			EnvSandboxed + "=0",
			EnvSandboxBackend + "=other",
		},
	)

	for _, want := range []string{
		"PATH=/custom/bin",
		"HOME=/home/user",
		EnvSandboxed + "=1",
		EnvSandboxBackend + "=" + string(BackendLinuxBwrap),
		"ZERO_SANDBOX_NETWORK=deny",
	} {
		if !stringSliceContains(env, want) {
			t.Fatalf("linux helper env = %#v, missing %q", env, want)
		}
	}
	if stringSliceContains(env, EnvSandboxed+"=0") || stringSliceContains(env, EnvSandboxBackend+"=other") {
		t.Fatalf("linux helper env did not replace stale sandbox markers: %#v", env)
	}
}

func indexString(values []string, want string) int {
	for index, value := range values {
		if value == want {
			return index
		}
	}
	return -1
}

func argsContainSequence(args []string, sequence ...string) bool {
	return argsSequenceIndex(args, sequence...) >= 0
}

func argsSequenceIndex(args []string, sequence ...string) int {
	if len(sequence) == 0 {
		return 0
	}
	for index := 0; index <= len(args)-len(sequence); index++ {
		matched := true
		for offset, want := range sequence {
			if args[index+offset] != want {
				matched = false
				break
			}
		}
		if matched {
			return index
		}
	}
	return -1
}
