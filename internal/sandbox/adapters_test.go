package sandbox

import (
	"errors"
	"runtime"
	"strings"
	"testing"
)

func TestSelectBackendChoosesPlatformAdapterWithFallback(t *testing.T) {
	t.Run("linux bubblewrap available", func(t *testing.T) {
		backend := SelectBackend(BackendOptions{
			GOOS: "linux",
			LookupExecutable: func(name string) (string, error) {
				if name == "bwrap" {
					return "/usr/bin/bwrap", nil
				}
				return "", errors.New("missing")
			},
		})
		if backend.Name != BackendBubblewrap || !backend.Available || backend.Executable != "/usr/bin/bwrap" {
			t.Fatalf("linux backend = %#v, want available bubblewrap", backend)
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
		if backend.Name != BackendSandboxExec || !backend.Available || backend.Executable != "/usr/bin/sandbox-exec" {
			t.Fatalf("darwin backend = %#v, want available sandbox-exec", backend)
		}
		if backend.Platform != "darwin" || backend.Fallback || !backend.CommandWrapping || !backend.NativeIsolation {
			t.Fatalf("darwin backend capabilities = %#v, want native wrapping", backend)
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
		if !strings.Contains(backend.Message, "Windows native sandbox adapter is not implemented") {
			t.Fatalf("expected Windows fallback message, got %q", backend.Message)
		}
		plan := backend.BuildPlan(t.TempDir(), DefaultPolicy())
		if plan.SupportLevel != BackendSupportPolicyOnly {
			t.Fatalf("windows support level = %q, want policy-only", plan.SupportLevel)
		}
		if capabilityStatus(plan.Capabilities, "native_process_isolation") != CapabilityUnavailable {
			t.Fatalf("windows native isolation capability = %#v, want unavailable", plan.Capabilities)
		}
		if !restrictionContains(plan.Warnings, "Windows native sandbox adapter is not implemented") {
			t.Fatalf("windows warnings = %#v, want adapter warning", plan.Warnings)
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
