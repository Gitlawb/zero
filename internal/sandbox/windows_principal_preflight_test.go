package sandbox

import (
	"errors"
	"strings"
	"testing"
)

// SETUP MUST NOT PROVISION A PRINCIPAL THE CALLER COULD NEVER LAUNCH.
//
// Launching a command as a separate account needs SeAssignPrimaryTokenPrivilege
// and SeIncreaseQuotaPrivilege, which an ordinary unelevated process does not
// hold, and elevated setup cannot supply them because the command runs later
// from the caller rather than from setup. Without this gate setup succeeded
// completely: a local account, its password, its logon rights, workspace ACEs,
// the recovery ledger and network filter state all landed, and then every
// principal-mode command refused before opening its executable.
//
// The check belongs in the caller's process, which is the one whose privileges
// decide the answer, and it has to run before anything crosses the UAC
// boundary.
func TestSetupRefusesAPrincipalThisCallerCannotLaunch(t *testing.T) {
	previous := windowsPrincipalLaunchPreflight
	t.Cleanup(func() { windowsPrincipalLaunchPreflight = previous })

	optIn := true
	options := WindowsSandboxSetupArgsOptions{
		CommandCWD:     t.TempDir(),
		SandboxHome:    t.TempDir(),
		PrincipalOptIn: &optIn,
	}

	windowsPrincipalLaunchPreflight = func() error {
		return errors.New("needs SeAssignPrimaryTokenPrivilege and SeIncreaseQuotaPrivilege, which this process does not hold")
	}
	args, err := BuildWindowsSandboxSetupArgs(options)
	if err == nil {
		t.Fatalf("setup args were built for a principal that can never be launched: %v", args)
	}
	if !strings.Contains(err.Error(), "SeAssignPrimaryTokenPrivilege") {
		t.Errorf("the refusal does not say what is missing: %v", err)
	}
	if !strings.Contains(err.Error(), "provision") {
		t.Errorf("the refusal does not say that provisioning was declined: %v", err)
	}

	// A caller that CAN launch still provisions, or the gate would have disabled
	// the feature rather than gated it.
	windowsPrincipalLaunchPreflight = func() error { return nil }
	if _, err := BuildWindowsSandboxSetupArgs(options); err != nil {
		t.Errorf("a caller holding the privileges was refused anyway: %v", err)
	}
}

// AND THE GATE IS ONLY FOR THE OPTED-IN PATH. Without the opt-in there is no
// principal to provision, so a caller lacking the privileges must be able to
// set up the ordinary restricted-token sandbox, which needs none of them.
func TestSetupWithoutTheOptInIgnoresTheLaunchPreflight(t *testing.T) {
	previous := windowsPrincipalLaunchPreflight
	t.Cleanup(func() { windowsPrincipalLaunchPreflight = previous })
	windowsPrincipalLaunchPreflight = func() error {
		return errors.New("this process cannot launch a principal")
	}

	optOut := false
	if _, err := BuildWindowsSandboxSetupArgs(WindowsSandboxSetupArgsOptions{
		CommandCWD:     t.TempDir(),
		SandboxHome:    t.TempDir(),
		PrincipalOptIn: &optOut,
	}); err != nil {
		t.Errorf("the restricted-token sandbox was refused for a privilege it does not need: %v", err)
	}
}

// allowPrincipalLaunchForTest stubs the launch preflight for tests that are
// about argument plumbing rather than about whether this machine can launch a
// principal. Without it they depend on the privileges of whoever runs the
// suite, and they only passed before because nothing checked.
func allowPrincipalLaunchForTest(t *testing.T) {
	t.Helper()
	previous := windowsPrincipalLaunchPreflight
	windowsPrincipalLaunchPreflight = func() error { return nil }
	t.Cleanup(func() { windowsPrincipalLaunchPreflight = previous })
}
