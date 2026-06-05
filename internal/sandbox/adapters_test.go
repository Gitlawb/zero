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
	})

	t.Run("unsupported platform falls back to policy only", func(t *testing.T) {
		backend := SelectBackend(BackendOptions{
			GOOS:             "plan9",
			LookupExecutable: func(string) (string, error) { return "", errors.New("missing") },
		})
		if backend.Name != BackendPolicyOnly || backend.Available {
			t.Fatalf("fallback backend = %#v, want policy-only unavailable adapter", backend)
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
}
