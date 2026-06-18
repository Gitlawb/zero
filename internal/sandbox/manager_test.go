package sandbox

import (
	"errors"
	"os"
	"path/filepath"
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
	policy.Network = NetworkScoped
	policy.AllowedDomains = []string{"example.com", "api.example.com"}
	policy.DeniedDomains = []string{"blocked.example.com"}
	policy.DenyRead = []string{denyRead}
	policy.DenyWrite = []string{denyWrite}

	profile := PermissionProfileFromPolicy(workspace, policy, scope)
	if profile.FileSystem.Kind != FileSystemRestricted {
		t.Fatalf("filesystem kind = %q, want restricted", profile.FileSystem.Kind)
	}
	if len(profile.FileSystem.WriteRoots) != 2 {
		t.Fatalf("write roots = %#v, want workspace + extra", profile.FileSystem.WriteRoots)
	}
	if profile.FileSystem.WriteRoots[0].Root != scope.Roots()[0] || profile.FileSystem.WriteRoots[1].Root != scope.Roots()[1] {
		t.Fatalf("write roots = %#v, want scope roots %#v", profile.FileSystem.WriteRoots, scope.Roots())
	}
	if !stringSliceContains(profile.FileSystem.WriteRoots[0].ProtectedMetadataNames, ".git") || !stringSliceContains(profile.FileSystem.WriteRoots[0].ProtectedMetadataNames, ".zero") {
		t.Fatalf("protected metadata names = %#v, want workspace metadata protected", profile.FileSystem.WriteRoots[0].ProtectedMetadataNames)
	}
	if len(profile.FileSystem.DenyRead) != 1 || len(profile.FileSystem.DenyWrite) != 1 {
		t.Fatalf("deny paths = %#v / %#v, want one each", profile.FileSystem.DenyRead, profile.FileSystem.DenyWrite)
	}
	if profile.Network.Mode != NetworkScoped || !profile.Network.ProxyRequired {
		t.Fatalf("network profile = %#v, want scoped proxy-required", profile.Network)
	}
	if !profile.RequiresPlatformSandbox() {
		t.Fatal("workspace-write scoped profile must require a platform sandbox")
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

func TestSandboxManagerBuildsDegradedPolicyOnlyCommandPlan(t *testing.T) {
	policy := DefaultPolicy()
	backend := Backend{Name: BackendPolicyOnly, Platform: "windows", Fallback: true, Message: "policy-only fallback"}
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
	if plan.Wrapped || plan.Name != "cmd.exe" || plan.TargetBackend != BackendWindowsRestrictedToken {
		t.Fatalf("policy-only plan = %#v, want direct command targeting windows backend", plan)
	}
	if plan.EnforcementLevel != EnforcementDegraded || plan.DowngradeReason == "" {
		t.Fatalf("policy-only metadata = %#v, want degraded fallback", plan)
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
		{name: "unsupported", goos: "plan9", want: BackendPolicyOnly, wantTarget: BackendPolicyOnly},
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
	if backend != managerBackend {
		t.Fatalf("SelectBackend = %#v, manager backend = %#v", backend, managerBackend)
	}
}

func TestSandboxManagerFailsClosedWhenNativeRequiredAndPolicyOnlyDisabled(t *testing.T) {
	policy := DefaultPolicy()
	policy.AllowPolicyOnlyRunner = false
	profile := PermissionProfileFromPolicy("/workspace", policy, nil)
	_, err := NewSandboxManager(SandboxManagerOptions{
		GOOS:    "windows",
		Backend: Backend{Name: BackendPolicyOnly, Platform: "windows", Fallback: true},
	}).BuildExecutionRequest(SandboxManagerRequest{
		WorkspaceRoot:     "/workspace",
		Command:           CommandSpec{Name: "cmd.exe", Dir: "/workspace"},
		Policy:            policy,
		Profile:           profile,
		Preference:        SandboxPreferenceAuto,
		ValidateExecution: true,
	})
	if !errors.Is(err, errPolicyOnlyRunnerDisabled) {
		t.Fatalf("BuildExecutionRequest error = %v, want policy-only disabled", err)
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
