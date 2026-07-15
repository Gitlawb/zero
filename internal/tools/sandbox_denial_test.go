package tools

import (
	"testing"

	zeroSandbox "github.com/Gitlawb/zero/internal/sandbox"
)

func TestLikelySandboxDeniedDetectsReferenceKeywords(t *testing.T) {
	plan := zeroSandbox.CommandPlan{
		Wrapped:       true,
		TargetBackend: zeroSandbox.BackendLinuxBwrap,
	}
	output := "touch: cannot touch '/home/user/.npm/cache': Read-only file system"
	if !likelySandboxDenied(plan, 1, output) {
		t.Fatalf("expected reference sandbox denial keyword to be classified as sandbox denied")
	}
}

func TestLikelySandboxDeniedDetectsNetworkDenialEvenWithZeroExit(t *testing.T) {
	plan := zeroSandbox.CommandPlan{
		Wrapped:       true,
		TargetBackend: zeroSandbox.BackendLinuxBwrap,
		Policy:        zeroSandbox.Policy{Network: zeroSandbox.NetworkDeny},
		PermissionProfile: zeroSandbox.PermissionProfile{
			Network: zeroSandbox.NetworkPolicy{Mode: zeroSandbox.NetworkDeny},
		},
	}
	if !likelySandboxDenied(plan, 0, "Cannot open a network socket.") {
		t.Fatal("network-denied socket output with exit 0 must be classified as sandbox denied")
	}
	meta := map[string]string{}
	markLikelySandboxDenial(meta, plan, 0, "Cannot open a network socket.")
	if meta[SandboxLikelyDeniedMeta] != "true" || meta[SandboxDenialKindMeta] != SandboxDenialKindNetwork {
		t.Fatalf("network denial meta = %#v", meta)
	}
}

func TestLikelySandboxDeniedIgnoresUnsandboxedFailure(t *testing.T) {
	plan := zeroSandbox.CommandPlan{Wrapped: false}
	if likelySandboxDenied(plan, 1, "permission denied") {
		t.Fatal("unsandboxed command output must not be classified as a sandbox denial")
	}
}

func TestLikelySandboxDeniedDetectsSilentWindowsWrappedFailure(t *testing.T) {
	for _, backend := range []zeroSandbox.BackendName{
		zeroSandbox.BackendWindowsRestrictedToken,
		zeroSandbox.BackendWindowsElevated,
	} {
		plan := zeroSandbox.CommandPlan{
			Wrapped:       true,
			TargetBackend: backend,
		}
		if !likelySandboxDenied(plan, 1, "", "  \n") {
			t.Fatalf("wrapped Windows command failing with empty output must be classified as sandbox denied (backend %s)", backend)
		}
		meta := map[string]string{}
		markLikelySandboxDenial(meta, plan, 1, "")
		if meta[SandboxLikelyDeniedMeta] != "true" || meta[SandboxDenialKindMeta] != SandboxDenialKindSandbox {
			t.Fatalf("silent windows denial meta = %#v (backend %s)", meta, backend)
		}
	}
}

func TestLikelySandboxDeniedIgnoresSilentWindowsWrappedSuccess(t *testing.T) {
	plan := zeroSandbox.CommandPlan{
		Wrapped:       true,
		TargetBackend: zeroSandbox.BackendWindowsRestrictedToken,
	}
	if likelySandboxDenied(plan, 0, "") {
		t.Fatal("wrapped Windows command exiting 0 with empty output must not be classified as sandbox denied")
	}
}

func TestLikelySandboxDeniedIgnoresSilentFailureOnOtherBackends(t *testing.T) {
	plan := zeroSandbox.CommandPlan{
		Wrapped:       true,
		TargetBackend: zeroSandbox.BackendLinuxBwrap,
	}
	if likelySandboxDenied(plan, 1, "") {
		t.Fatal("silent nonzero exit on non-Windows backends must not be classified as sandbox denied")
	}
}

func TestLikelySandboxDeniedIgnoresSilentWindowsFailureWithOutput(t *testing.T) {
	plan := zeroSandbox.CommandPlan{
		Wrapped:       true,
		TargetBackend: zeroSandbox.BackendWindowsRestrictedToken,
	}
	if likelySandboxDenied(plan, 1, "fatal: not a git repository") {
		t.Fatal("a wrapped Windows command that produced unrelated output must not be classified as sandbox denied")
	}
}
