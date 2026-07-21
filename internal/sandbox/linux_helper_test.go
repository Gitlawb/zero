package sandbox

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
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
	profile := PermissionProfile{
		FileSystem: FileSystemPolicy{Kind: FileSystemRestricted, ReadRoots: []string{string(filepath.Separator)}, IncludePlatformRoots: true},
		Network:    NetworkPolicy{Mode: NetworkDeny},
	}
	args, err := BuildLinuxSandboxCommandArgs(LinuxSandboxCommandArgsOptions{
		SandboxPolicyCWD:  "/workspace",
		PermissionProfile: profile,
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
	profile := PermissionProfile{
		FileSystem: FileSystemPolicy{Kind: FileSystemRestricted, ReadRoots: []string{string(filepath.Separator)}, IncludePlatformRoots: true},
		Network:    NetworkPolicy{Mode: NetworkAllow},
	}
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

	args, err := linuxBwrapFilesystemArgs(profile)
	if err != nil {
		t.Fatal(err)
	}
	assertArgsContainSequence(t, args, "--ro-bind", "/", "/")
	if argsContainSequence(args, "--tmpfs", "/") {
		t.Fatalf("root-read profile must not start from an empty root: %#v", args)
	}
}

func TestLinuxBwrapPathCarveoutsFailClosedForMissingMountTargets(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "missing", "nested")
	if _, err := appendReadOnlyLinuxPathArgs(nil, missing); err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("missing read-only target error = %v, want path-specific failure", err)
	}
	if _, err := appendUnreadableLinuxPathArgs(nil, missing); err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("missing deny-read target error = %v, want path-specific failure", err)
	}
	if _, err := os.Stat(filepath.Dir(missing)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("building carveouts materialized a host path: %v", err)
	}

	deniedDir := filepath.Join(root, "denied")
	if err := os.Mkdir(deniedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	args, err := appendUnreadableLinuxPathArgs(nil, deniedDir)
	if err != nil {
		t.Fatal(err)
	}
	assertArgsContainSequence(t, args, "--perms", "000", "--tmpfs", deniedDir, "--remount-ro", deniedDir)

	deniedFile := filepath.Join(root, "secret.json")
	if err := os.WriteFile(deniedFile, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	args, err = appendUnreadableLinuxPathArgs(nil, deniedFile)
	if err != nil {
		t.Fatal(err)
	}
	assertArgsContainSequence(t, args, "--ro-bind", "/dev/null", deniedFile)
	args, err = appendReadOnlyLinuxPathArgs(nil, deniedFile)
	if err != nil {
		t.Fatal(err)
	}
	assertArgsContainSequence(t, args, "--ro-bind", deniedFile, deniedFile)
}

func TestLinuxBwrapSkipsMissingAutomaticBaselines(t *testing.T) {
	root := t.TempDir()
	missingCredential := filepath.Join(root, "home", ".config", "zero")
	profile := PermissionProfile{FileSystem: FileSystemPolicy{
		Kind:             FileSystemRestricted,
		ReadRoots:        []string{string(filepath.Separator)},
		WriteRoots:       []WritableRoot{{Root: root, ReadOnlySubpaths: []string{filepath.Join(root, ".git", "hooks")}, ProtectedMetadataNames: []string{".zero"}}},
		DenyReadIfExists: []string{missingCredential},
	}}

	args, err := linuxBwrapFilesystemArgs(profile)
	if err != nil {
		t.Fatalf("missing automatic baselines must not abort launch: %v", err)
	}
	if stringSliceContains(args, missingCredential) {
		t.Fatalf("missing automatic baseline unexpectedly emitted: %#v", args)
	}
	if err := os.MkdirAll(missingCredential, 0o700); err != nil {
		t.Fatal(err)
	}
	args, err = linuxBwrapFilesystemArgs(profile)
	if err != nil {
		t.Fatal(err)
	}
	assertArgsContainSequence(t, args, "--perms", "000", "--tmpfs", missingCredential, "--remount-ro", missingCredential)
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

	args, err := linuxBwrapFilesystemArgs(profile)
	if err != nil {
		t.Fatal(err)
	}
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

func TestLinuxBwrapUnrestrictedFilesystemUsesWritableHostRoot(t *testing.T) {
	profile := PermissionProfile{
		FileSystem: FileSystemPolicy{
			Kind:      FileSystemUnrestricted,
			AllowTemp: true,
		},
		Network: NetworkPolicy{Mode: NetworkDeny},
	}

	args, err := linuxBwrapFilesystemArgs(profile)
	if err != nil {
		t.Fatal(err)
	}
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
