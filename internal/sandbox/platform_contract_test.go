package sandbox

import (
	"path/filepath"
	"strings"
	"testing"
)

// The implementation mechanism differs by OS, but these observable policy
// semantics must remain stable across adapters.
func TestPlatformAdaptersShareExecutionContract(t *testing.T) {
	root := t.TempDir()
	policy := DefaultPolicy()
	profile := PermissionProfileFromPolicy(root, policy, nil)
	if !profile.RequiresPlatformSandbox() {
		t.Fatal("baseline profile must require platform enforcement")
	}
	protected := strings.Join(profile.FileSystem.WriteRoots[0].ProtectedMetadataNames, "\x00")
	for _, name := range []string{".zero", ".agents"} {
		if !strings.Contains(protected, name) {
			t.Fatalf("baseline profile missing protected metadata %q: %#v", name, profile.FileSystem.WriteRoots[0].ProtectedMetadataNames)
		}
	}
	resolvedRoot := profile.FileSystem.WriteRoots[0].Root
	for _, subpath := range []string{filepath.Join(resolvedRoot, ".git", "hooks"), filepath.Join(resolvedRoot, ".git", "config")} {
		if !stringSliceContains(profile.FileSystem.WriteRoots[0].ReadOnlySubpaths, subpath) {
			t.Fatalf("baseline profile missing git metadata carveout %q: %#v", subpath, profile.FileSystem.WriteRoots[0].ReadOnlySubpaths)
		}
	}

	t.Run("macOS native plan", func(t *testing.T) {
		backend := Backend{Name: BackendMacOSSeatbelt, Available: true, Platform: "darwin", CommandWrapping: true, NativeIsolation: true, Executable: "/usr/bin/sandbox-exec"}
		request, err := NewSandboxManager(SandboxManagerOptions{GOOS: "darwin", Backend: backend}).BuildExecutionRequest(SandboxManagerRequest{
			WorkspaceRoot: root, Policy: policy, Profile: profile, Command: CommandSpec{Name: "/bin/sh", Args: []string{"-c", "true"}, Dir: root},
		})
		if err != nil {
			t.Fatal(err)
		}
		if request.TargetBackend != BackendMacOSSeatbelt || !request.CommandWrapped || request.EnforcementLevel != EnforcementNative {
			t.Fatalf("macOS request = %#v", request)
		}
		network := networkRuleForProfile(request.PermissionProfile.Network)
		if network != "(deny network*)" {
			t.Fatalf("macOS restricted network profile = %q, want strict deny", network)
		}
	})

	t.Run("Windows native plan", func(t *testing.T) {
		original := windowsSandboxInitialized
		windowsSandboxInitialized = func() bool { return true }
		t.Cleanup(func() { windowsSandboxInitialized = original })
		backend := Backend{Name: BackendWindowsRestrictedToken, Available: true, Platform: "windows", CommandWrapping: true, NativeIsolation: true, Executable: `C:\zero\zero.exe`}
		request, err := NewSandboxManager(SandboxManagerOptions{GOOS: "windows", Backend: backend}).BuildExecutionRequest(SandboxManagerRequest{
			WorkspaceRoot: root, Policy: policy, Profile: profile, Command: CommandSpec{Name: "cmd.exe", Args: WindowsShellArgs("echo ok"), Dir: root},
		})
		if err != nil {
			t.Fatal(err)
		}
		if request.TargetBackend != BackendWindowsRestrictedToken || !request.CommandWrapped || request.EnforcementLevel != EnforcementNative {
			t.Fatalf("Windows request = %#v", request)
		}
		if request.PermissionProfile.Network.Mode != NetworkDeny {
			t.Fatalf("Windows native plan lost network restriction: %#v", request.PermissionProfile.Network)
		}
	})
}

func TestUnavailablePlatformAdaptersFailClosedOnlyForExplicitDenies(t *testing.T) {
	root := t.TempDir()
	for _, goos := range []string{"darwin", "windows"} {
		t.Run(goos, func(t *testing.T) {
			backend := unavailableBackend(goos, "native sandbox unavailable")
			manager := NewSandboxManager(SandboxManagerOptions{GOOS: goos, Backend: backend})
			baseline := DefaultPolicy()
			request, err := manager.BuildExecutionRequest(SandboxManagerRequest{WorkspaceRoot: root, Policy: baseline, Command: CommandSpec{Name: "echo", Dir: root}})
			if err != nil || request.EnforcementLevel != EnforcementDegraded || request.CommandWrapped {
				t.Fatalf("baseline degraded request=%#v err=%v", request, err)
			}
			hardened := baseline
			hardened.DenyRead = []string{"secrets"}
			_, err = manager.BuildExecutionRequest(SandboxManagerRequest{WorkspaceRoot: root, Policy: hardened, Command: CommandSpec{Name: "echo", Dir: root}, ValidateExecution: true})
			if err == nil {
				t.Fatal("explicit deny on unavailable adapter must fail closed")
			}
		})
	}
}
