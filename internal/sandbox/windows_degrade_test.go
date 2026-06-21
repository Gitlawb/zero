package sandbox

import (
	"errors"
	"testing"
)

// On Windows, a not-yet-set-up sandbox must DEGRADE (run unwrapped with a
// downgrade reason) on the default preference — never brick the command — while
// a strict --sandbox require still errors with a setup hint. Once the marker
// exists, enforcement is native and the command wraps.
func TestWindowsDegradesWhenSandboxNotInitialized(t *testing.T) {
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

	// Marker missing + auto -> degrade (unwrapped, with a reason), no error.
	windowsSandboxInitialized = func() bool { return false }
	auto := base
	auto.Preference = SandboxPreferenceAuto
	req, err := mgr.BuildExecutionRequest(auto)
	if err != nil {
		t.Fatalf("auto must degrade, not error: %v", err)
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

	// Marker missing + require -> hard error pointing at setup.
	require := base
	require.Preference = SandboxPreferenceRequire
	if _, err := mgr.BuildExecutionRequest(require); !errors.Is(err, errWindowsSandboxNotInitialized) {
		t.Fatalf("require must error with the setup hint, got %v", err)
	}

	// Marker present -> native, wrapped.
	windowsSandboxInitialized = func() bool { return true }
	req, err = mgr.BuildExecutionRequest(auto)
	if err != nil {
		t.Fatalf("initialized auto: %v", err)
	}
	if req.EnforcementLevel != EnforcementNative || !req.CommandWrapped {
		t.Fatalf("initialized -> native wrapped, got enforcement=%v wrapped=%v", req.EnforcementLevel, req.CommandWrapped)
	}
}
