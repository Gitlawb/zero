package sandbox

import (
	"errors"
	"runtime"
	"strings"
	"testing"
)

func TestSelectBackendChoosesPlatformAdapterWithFallback(t *testing.T) {
	t.Run("linux helper available", func(t *testing.T) {
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
		if backend.Name != BackendLinuxBwrap || !backend.Available || backend.Executable != "/usr/bin/zero-linux-sandbox" {
			t.Fatalf("linux backend = %#v, want available Linux helper", backend)
		}
		if backend.Platform != "linux" || backend.Fallback || !backend.CommandWrapping || !backend.NativeIsolation {
			t.Fatalf("linux backend capabilities = %#v, want native wrapping", backend)
		}
		plan := backend.BuildPlan(t.TempDir(), DefaultPolicy())
		if plan.SupportLevel != BackendSupportNative || len(plan.Warnings) != 0 {
			t.Fatalf("linux plan = %#v, want native support without warnings", plan)
		}
		if capabilityStatus(plan.Capabilities, "native_process_isolation") != CapabilityNative {
			t.Fatalf("linux native isolation capability = %#v, want native", plan.Capabilities)
		}
	})

	t.Run("linux helper missing falls back explicitly", func(t *testing.T) {
		backend := SelectBackend(BackendOptions{
			GOOS: "linux",
			LookupExecutable: func(name string) (string, error) {
				if name == "bwrap" {
					return "/usr/bin/bwrap", nil
				}
				return "", errors.New("missing")
			},
		})
		if backend.Name != BackendPolicyOnly || backend.Available {
			t.Fatalf("linux backend = %#v, want policy-only fallback without Linux helper", backend)
		}
		if !strings.Contains(backend.Message, "Linux sandbox helper is not available") {
			t.Fatalf("linux fallback message = %q, want missing helper", backend.Message)
		}
	})

	t.Run("darwin sandbox exec available", func(t *testing.T) {
		backend := SelectBackend(BackendOptions{
			GOOS: "darwin",
			LookupExecutable: func(name string) (string, error) {
				if name == "sandbox-exec" {
					return "/usr/bin/sandbox-exec", nil
				}
				return "", errors.New("missing")
			},
		})
		if backend.Name != BackendMacOSSeatbelt || !backend.Available || backend.Executable != "/usr/bin/sandbox-exec" {
			t.Fatalf("darwin backend = %#v, want available macOS Seatbelt backend", backend)
		}
		if backend.Platform != "darwin" || backend.Fallback || !backend.CommandWrapping || !backend.NativeIsolation {
			t.Fatalf("darwin backend capabilities = %#v, want native wrapping", backend)
		}
	})

	t.Run("windows command runner available", func(t *testing.T) {
		backend := SelectBackend(BackendOptions{
			GOOS: "windows",
			LookupExecutable: func(name string) (string, error) {
				if name == WindowsSandboxCommandRunnerName {
					return `C:\zero\zero-windows-command-runner.exe`, nil
				}
				if name == WindowsSandboxSetupName {
					return `C:\zero\zero-windows-sandbox-setup.exe`, nil
				}
				return "", errors.New("missing")
			},
		})
		if backend.Name != BackendWindowsRestrictedToken || !backend.Available || backend.Executable != `C:\zero\zero-windows-command-runner.exe` {
			t.Fatalf("windows backend = %#v, want available restricted-token runner", backend)
		}
		if backend.Platform != "windows" || backend.Fallback || !backend.CommandWrapping || !backend.NativeIsolation {
			t.Fatalf("windows backend capabilities = %#v, want native wrapping", backend)
		}
		plan := backend.BuildPlan(t.TempDir(), DefaultPolicy())
		if plan.SupportLevel != BackendSupportNative || len(plan.Warnings) != 0 {
			t.Fatalf("windows plan = %#v, want native support without warnings", plan)
		}
		if capabilityStatus(plan.Capabilities, "native_process_isolation") != CapabilityNative {
			t.Fatalf("windows native isolation capability = %#v, want native", plan.Capabilities)
		}
		if capabilityStatus(plan.Capabilities, "network_guard") != CapabilityNative {
			t.Fatalf("windows network guard capability = %#v, want native WFP enforcement", plan.Capabilities)
		}
	})

	t.Run("windows setup helper missing", func(t *testing.T) {
		backend := SelectBackend(BackendOptions{
			GOOS: "windows",
			LookupExecutable: func(name string) (string, error) {
				if name == WindowsSandboxCommandRunnerName {
					return `C:\zero\zero-windows-command-runner.exe`, nil
				}
				return "", errors.New("missing")
			},
		})
		if backend.Name != BackendPolicyOnly || backend.Available || backend.Platform != "windows" {
			t.Fatalf("windows backend = %#v, want policy-only windows fallback", backend)
		}
		if !strings.Contains(backend.Message, "Windows sandbox setup helper is not available") {
			t.Fatalf("expected Windows setup helper fallback message, got %q", backend.Message)
		}
	})

	t.Run("windows falls back explicitly", func(t *testing.T) {
		backend := SelectBackend(BackendOptions{
			GOOS:             "windows",
			LookupExecutable: func(string) (string, error) { return "", errors.New("missing") },
		})
		if backend.Name != BackendPolicyOnly || backend.Available || backend.Platform != "windows" {
			t.Fatalf("windows backend = %#v, want policy-only windows fallback", backend)
		}
		if !backend.Fallback || backend.CommandWrapping || backend.NativeIsolation {
			t.Fatalf("windows backend capabilities = %#v, want no native wrapping", backend)
		}
		if !strings.Contains(backend.Message, "Windows sandbox command runner is not available") {
			t.Fatalf("expected Windows fallback message, got %q", backend.Message)
		}
		plan := backend.BuildPlan(t.TempDir(), DefaultPolicy())
		if plan.SupportLevel != BackendSupportPolicyOnly {
			t.Fatalf("windows support level = %q, want policy-only", plan.SupportLevel)
		}
		if capabilityStatus(plan.Capabilities, "native_process_isolation") != CapabilityUnavailable {
			t.Fatalf("windows native isolation capability = %#v, want unavailable", plan.Capabilities)
		}
		if !restrictionContains(plan.Warnings, "Windows sandbox command runner is not available") {
			t.Fatalf("windows warnings = %#v, want command runner warning", plan.Warnings)
		}
	})

	t.Run("unsupported platform falls back to policy only", func(t *testing.T) {
		backend := SelectBackend(BackendOptions{
			GOOS:             "plan9",
			LookupExecutable: func(string) (string, error) { return "", errors.New("missing") },
		})
		if backend.Name != BackendPolicyOnly || backend.Available {
			t.Fatalf("fallback backend = %#v, want policy-only unavailable adapter", backend)
		}
		if backend.Platform != "plan9" || !backend.Fallback || backend.CommandWrapping || backend.NativeIsolation {
			t.Fatalf("fallback backend capabilities = %#v, want explicit policy-only fallback", backend)
		}
		if !strings.Contains(backend.Message, "policy-only") {
			t.Fatalf("expected fallback message, got %q", backend.Message)
		}
	})
}

func TestLegacySandboxEntrypointsAreExplicitAndExist(t *testing.T) {
	t.Run("linux still requires bubblewrap behind helper", func(t *testing.T) {
		lookups := map[string]string{
			LinuxSandboxHelperName: "/usr/bin/" + LinuxSandboxHelperName,
			"bwrap":                "/usr/bin/bwrap",
		}
		backend := SelectBackend(BackendOptions{
			GOOS: "linux",
			LookupExecutable: func(name string) (string, error) {
				path, ok := lookups[name]
				if !ok {
					return "", errors.New("missing")
				}
				return path, nil
			},
		})

		if backend.Name != BackendLinuxBwrap || backend.Executable != lookups[LinuxSandboxHelperName] {
			t.Fatalf("linux backend = %#v, want Linux helper backed by bwrap", backend)
		}
	})

	t.Run("macos still uses sandbox-exec behind seatbelt backend", func(t *testing.T) {
		backend := SelectBackend(BackendOptions{
			GOOS: "darwin",
			LookupExecutable: func(name string) (string, error) {
				if name == "sandbox-exec" {
					return "/usr/bin/sandbox-exec", nil
				}
				return "", errors.New("missing")
			},
		})

		if backend.Name != BackendMacOSSeatbelt || backend.Executable != "/usr/bin/sandbox-exec" {
			t.Fatalf("darwin backend = %#v, want Seatbelt backend through sandbox-exec", backend)
		}
	})
}

func TestBackendBuildPlanDocumentsBestEffortIsolation(t *testing.T) {
	root := t.TempDir()
	policy := DefaultPolicy()
	plan := SelectBackend(BackendOptions{
		GOOS: runtime.GOOS,
		LookupExecutable: func(string) (string, error) {
			return "", errors.New("not installed")
		},
	}).BuildPlan(root, policy)

	if plan.WorkspaceRoot != root {
		t.Fatalf("workspace root = %q, want %q", plan.WorkspaceRoot, root)
	}
	if len(plan.Restrictions) == 0 {
		t.Fatalf("expected restrictions in build plan: %#v", plan)
	}
	if plan.Policy.Mode != policy.Mode {
		t.Fatalf("plan policy = %#v, want %#v", plan.Policy, policy)
	}
	if plan.Backend.Name == BackendPolicyOnly && !restrictionContains(plan.Restrictions, "native process isolation unavailable") {
		t.Fatalf("policy-only plan did not document native isolation fallback: %#v", plan.Restrictions)
	}
}

func TestBackendCapabilitiesReflectDisabledPolicy(t *testing.T) {
	backend := SelectBackend(BackendOptions{
		GOOS:             "windows",
		LookupExecutable: func(string) (string, error) { return "", errors.New("missing") },
	})
	plan := backend.BuildPlan(t.TempDir(), Policy{Mode: ModeDisabled})

	for _, key := range []string{"policy_evaluation", "workspace_write_guard", "network_guard", "destructive_shell_guard"} {
		if got := capabilityStatus(plan.Capabilities, key); got != CapabilityDisabled {
			t.Fatalf("capability %s = %q, want disabled in %#v", key, got, plan.Capabilities)
		}
	}
	if got := capabilityStatus(plan.Capabilities, "command_wrapping"); got != CapabilityUnavailable {
		t.Fatalf("command_wrapping = %q, want unavailable", got)
	}
}

func restrictionContains(restrictions []string, value string) bool {
	for _, restriction := range restrictions {
		if strings.Contains(restriction, value) {
			return true
		}
	}
	return false
}

func capabilityStatus(capabilities []BackendCapability, key string) CapabilityStatus {
	for _, capability := range capabilities {
		if capability.Key == key {
			return capability.Status
		}
	}
	return ""
}
