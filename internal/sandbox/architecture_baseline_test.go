package sandbox

import (
	"errors"
	"testing"
)

func TestTargetBackendForPlatformBaseline(t *testing.T) {
	tests := []struct {
		name string
		goos string
		wsl  bool
		want BackendName
	}{
		{name: "linux", goos: "linux", want: BackendLinuxBwrap},
		{name: "linux wsl", goos: "linux", wsl: true, want: BackendLinuxBwrap},
		{name: "macos", goos: "darwin", want: BackendMacOSSeatbelt},
		{name: "windows", goos: "windows", want: BackendWindowsRestrictedToken},
		{name: "unsupported", goos: "plan9", want: BackendUnavailable},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := TargetBackendForPlatform(test.goos, test.wsl); got != test.want {
				t.Fatalf("TargetBackendForPlatform(%q, %t) = %q, want %q", test.goos, test.wsl, got, test.want)
			}
		})
	}
}

func TestBackendPlanCarriesPhase0ManagerFields(t *testing.T) {
	linux := SelectBackend(BackendOptions{
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
	}).BuildPlan("/workspace", DefaultPolicy())

	if linux.TargetBackend != BackendLinuxBwrap || linux.EnforcementLevel != EnforcementNative || !linux.CommandWrapped {
		t.Fatalf("linux plan metadata = %#v, want linux-bwrap native wrapped", linux)
	}
	for _, marker := range []string{EnvSandboxed + "=1", EnvSandboxBackend + "=" + string(BackendLinuxBwrap), "ZERO_SANDBOX_NETWORK=deny"} {
		if !stringSliceContains(linux.SandboxEnvMarkers, marker) {
			t.Fatalf("linux plan markers = %#v, missing %q", linux.SandboxEnvMarkers, marker)
		}
	}

	windows := SelectBackend(BackendOptions{
		GOOS:             "windows",
		LookupExecutable: func(string) (string, error) { return "", errors.New("missing") },
	}).BuildPlan("/workspace", DefaultPolicy())

	if windows.TargetBackend != BackendWindowsRestrictedToken {
		t.Fatalf("windows target backend = %q, want %q", windows.TargetBackend, BackendWindowsRestrictedToken)
	}
	if windows.EnforcementLevel != EnforcementDegraded || windows.CommandWrapped || windows.DowngradeReason == "" {
		t.Fatalf("windows plan metadata = %#v, want degraded unwrapped plan with downgrade reason", windows)
	}
	if len(windows.SandboxEnvMarkers) != 0 {
		t.Fatalf("windows unavailable plan must not claim sandbox env markers: %#v", windows.SandboxEnvMarkers)
	}
}

func TestCommandPlanCarriesSandboxMetadata(t *testing.T) {
	root := t.TempDir()
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: root,
		Policy:        DefaultPolicy(),
		Backend: Backend{
			Name:            BackendLinuxBwrap,
			Available:       true,
			Platform:        "linux",
			Executable:      "/usr/bin/zero-linux-sandbox",
			CommandWrapping: true,
			NativeIsolation: true,
		},
	})
	plan, err := engine.BuildCommandPlan(CommandSpec{Name: "/bin/sh", Args: []string{"-c", "true"}, Dir: root})
	if err != nil {
		t.Fatalf("BuildCommandPlan: %v", err)
	}

	if plan.TargetBackend != BackendLinuxBwrap || !plan.Wrapped || plan.EnforcementLevel != EnforcementNative || plan.DowngradeReason != "" {
		t.Fatalf("wrapped command metadata = %#v, want native linux-bwrap", plan)
	}
	if !stringSliceContains(plan.SandboxEnvMarkers, EnvSandboxBackend+"="+string(BackendLinuxBwrap)) {
		t.Fatalf("wrapped command markers = %#v, missing backend marker", plan.SandboxEnvMarkers)
	}

	unavailable := NewEngine(EngineOptions{
		WorkspaceRoot: root,
		Policy:        DefaultPolicy(),
		Backend:       Backend{Name: BackendUnavailable, Platform: "linux", Fallback: true, Message: "native sandbox unavailable"},
	})
	if _, err := unavailable.BuildCommandPlan(CommandSpec{Name: "/bin/sh", Dir: root}); !errors.Is(err, errNativeSandboxUnavailable) {
		t.Fatalf("BuildCommandPlan unavailable error = %v, want native sandbox unavailable", err)
	}
}

func TestUnavailableFailClosedForTargetPlatforms(t *testing.T) {
	root := t.TempDir()
	policy := DefaultPolicy()
	tests := []struct {
		name    string
		backend Backend
	}{
		{
			name:    "linux",
			backend: Backend{Name: BackendUnavailable, Platform: "linux", Fallback: true},
		},
		{
			name:    "macos",
			backend: Backend{Name: BackendUnavailable, Platform: "darwin", Fallback: true},
		},
		{
			name:    "windows",
			backend: Backend{Name: BackendUnavailable, Platform: "windows", Fallback: true},
		},
		{
			name:    "wsl",
			backend: Backend{Name: BackendWSL, Platform: "linux", Fallback: true},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine := NewEngine(EngineOptions{WorkspaceRoot: root, Policy: policy, Backend: test.backend})
			_, err := engine.BuildCommandPlan(CommandSpec{Name: "/bin/sh", Dir: root})
			if !errors.Is(err, errNativeSandboxUnavailable) {
				t.Fatalf("BuildCommandPlan error = %v, want native sandbox unavailable", err)
			}
		})
	}
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
