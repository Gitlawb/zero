package sandbox

import (
	"bytes"
	"encoding/json"
	"reflect"
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
		SandboxPolicyCWD:     "/workspace",
		CommandCWD:           "/workspace/app",
		PermissionProfile:    profile,
		UseLandlock:          true,
		AllowNetworkForProxy: true,
		Command:              []string{"/bin/sh", "-c", "pwd"},
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
	if !stringSliceContains(args, "--use-landlock") || !stringSliceContains(args, "--allow-network-for-proxy") {
		t.Fatalf("args missing helper feature flags: %#v", args)
	}
}

func TestParseLinuxSandboxHelperArgs(t *testing.T) {
	profile := DefaultPermissionProfile("/workspace")
	args, err := BuildLinuxSandboxCommandArgs(LinuxSandboxCommandArgsOptions{
		SandboxPolicyCWD:     "/workspace",
		PermissionProfile:    profile,
		ApplySeccompThenExec: true,
		ProxyRouteSpec:       "proxy=127.0.0.1:9999",
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
	if !config.ApplySeccompThenExec || !config.NoProc || config.ProxyRouteSpec != "proxy=127.0.0.1:9999" {
		t.Fatalf("feature config = %#v", config)
	}
	if !reflect.DeepEqual(config.PermissionProfile, profile) || !reflect.DeepEqual(config.Command, []string{"true"}) {
		t.Fatalf("parsed config = %#v", config)
	}
}

func TestRunLinuxSandboxHelperFailsClosedUntilEnforcementIsWired(t *testing.T) {
	args, err := BuildLinuxSandboxCommandArgs(LinuxSandboxCommandArgsOptions{
		SandboxPolicyCWD:  "/workspace",
		PermissionProfile: DefaultPermissionProfile("/workspace"),
		Command:           []string{"true"},
	})
	if err != nil {
		t.Fatalf("BuildLinuxSandboxCommandArgs: %v", err)
	}
	var stderr bytes.Buffer
	if code := RunLinuxSandboxHelper(args, &stderr); code != 125 {
		t.Fatalf("exit code = %d, want 125; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "not implemented") {
		t.Fatalf("stderr = %q, want fail-closed message", stderr.String())
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
