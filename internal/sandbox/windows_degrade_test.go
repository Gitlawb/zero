package sandbox

import (
	"errors"
	"strings"
	"testing"
)

// On Windows, a not-yet-set-up sandbox must fall back to the UNELEVATED tier
// (wrapped through the runner with the write-restricted token, network left to
// the approval gate) on the default preference — never brick the command —
// while a strict --sandbox require still errors with a setup hint. Once the
// marker exists, enforcement is native and the command wraps.
func TestWindowsFallsBackUnelevatedWhenSandboxNotInitialized(t *testing.T) {
	restore := windowsSandboxInitialized
	t.Cleanup(func() { windowsSandboxInitialized = restore })

	mgr := NewSandboxManager(SandboxManagerOptions{
		GOOS:    "windows",
		Backend: Backend{Name: BackendWindowsRestrictedToken, Available: true, Executable: "zero.exe", Platform: "windows"},
	})
	base := SandboxManagerRequest{
		WorkspaceRoot:     `C:\ws`,
		Command:           CommandSpec{Name: "bash", Args: []string{"-c", "echo hi"}},
		Policy:            DefaultPolicy(),
		ValidateExecution: true,
	}

	// Marker missing + auto -> unelevated tier: still wrapped, with a reason
	// documenting the missing network enforcement, no error.
	windowsSandboxInitialized = func() bool { return false }
	auto := base
	auto.Preference = SandboxPreferenceAuto
	req, err := mgr.BuildExecutionRequest(auto)
	if err != nil {
		t.Fatalf("auto must fall back, not error: %v", err)
	}
	if req.EnforcementLevel != EnforcementUnelevated {
		t.Fatalf("enforcement = %v, want unelevated", req.EnforcementLevel)
	}
	if !req.CommandWrapped {
		t.Fatal("unelevated command must still be wrapped through the sandbox runner")
	}
	if req.DowngradeReason == "" || !strings.Contains(req.DowngradeReason, "network") {
		t.Fatalf("downgrade reason = %q, want an explanation of the missing network enforcement", req.DowngradeReason)
	}

	// Marker missing + require -> hard error pointing at setup.
	require := base
	require.Preference = SandboxPreferenceRequire
	if _, err := mgr.BuildExecutionRequest(require); !errors.Is(err, errWindowsSandboxNotInitialized) {
		t.Fatalf("require must error with the setup hint, got %v", err)
	}

	// Marker present -> native, wrapped, no downgrade reason.
	windowsSandboxInitialized = func() bool { return true }
	req, err = mgr.BuildExecutionRequest(auto)
	if err != nil {
		t.Fatalf("initialized auto: %v", err)
	}
	if req.EnforcementLevel != EnforcementNative || !req.CommandWrapped {
		t.Fatalf("initialized -> native wrapped, got enforcement=%v wrapped=%v", req.EnforcementLevel, req.CommandWrapped)
	}
	if req.DowngradeReason != "" {
		t.Fatalf("native downgrade reason = %q, want empty", req.DowngradeReason)
	}
}

// A profile that needs the platform sandbox ONLY for network isolation gains
// nothing from the unelevated tier (which cannot enforce network), so it must
// keep the old degrade-to-policy-gate behavior.
func TestWindowsNetworkOnlyProfileStillDegradesWhenNotInitialized(t *testing.T) {
	restore := windowsSandboxInitialized
	t.Cleanup(func() { windowsSandboxInitialized = restore })
	windowsSandboxInitialized = func() bool { return false }

	mgr := NewSandboxManager(SandboxManagerOptions{
		GOOS:    "windows",
		Backend: Backend{Name: BackendWindowsRestrictedToken, Available: true, Executable: "zero.exe", Platform: "windows"},
	})
	req, err := mgr.BuildExecutionRequest(SandboxManagerRequest{
		WorkspaceRoot: `C:\ws`,
		Command:       CommandSpec{Name: "bash", Args: []string{"-c", "echo hi"}},
		Policy:        DefaultPolicy(),
		Profile: PermissionProfile{
			FileSystem: FileSystemPolicy{Kind: FileSystemUnrestricted, IncludePlatformRoots: true, AllowTemp: true},
			Network:    NetworkPolicy{Mode: NetworkDeny},
		},
		Preference: SandboxPreferenceAuto,
	})
	if err != nil {
		t.Fatalf("network-only profile must degrade, not error: %v", err)
	}
	if req.EnforcementLevel != EnforcementDegraded {
		t.Fatalf("enforcement = %v, want degraded", req.EnforcementLevel)
	}
	if req.CommandWrapped {
		t.Fatal("degraded command must NOT be wrapped through the sandbox runner")
	}
	if req.DowngradeReason == "" {
		t.Fatal("expected a downgrade reason explaining the missing setup")
	}
}
