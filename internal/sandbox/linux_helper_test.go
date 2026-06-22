package sandbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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
	if len(sequence) == 0 {
		return true
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
			return true
		}
	}
	return false
}
