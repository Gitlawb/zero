package sandbox

import (
	"strings"
	"testing"
)

func withWindowsHost(t *testing.T) {
	t.Helper()
	previous := denyReadWarningHostGOOS
	denyReadWarningHostGOOS = "windows"
	t.Cleanup(func() { denyReadWarningHostGOOS = previous })
}

func windowsRestrictedTokenBackend() Backend {
	return Backend{
		Name:            BackendWindowsRestrictedToken,
		Platform:        "windows",
		Available:       true,
		NativeIsolation: true,
	}
}

func profileWithDenyRead(paths ...string) PermissionProfile {
	profile := PermissionProfile{}
	profile.FileSystem.DenyRead = paths
	return profile
}

// Setting denyRead on Windows silently costs the token's write jail, because it
// selects the shape without WRITE_RESTRICTED and that shape has to keep the World
// SID. The trade is defensible; making it invisible is not. Someone who asked for
// read-deny has no way to discover they gave up write confinement for it.
func TestDenyReadOnWindowsWarnsThatTheWriteJailIsGone(t *testing.T) {
	withWindowsHost(t)
	warnings := windowsDenyReadWarnings(windowsRestrictedTokenBackend(), profileWithDenyRead(`C:\Users\someone\.config\creds`))
	if len(warnings) == 0 {
		t.Fatal("configuring denyRead on Windows produced no warning, so the lost write jail stays invisible")
	}
	warning := strings.ToLower(strings.Join(warnings, " "))
	// It has to name the cause and the consequence. A warning that says only
	// "degraded" sends the reader to the source to find out what changed.
	for _, want := range []string{"denyread", "write", "#869"} {
		if !strings.Contains(warning, want) {
			t.Errorf("warning does not mention %q, so it does not explain the trade: %q", want, warning)
		}
	}
}

// The default Windows posture must stay quiet. Zero never populates denyRead on
// Windows itself, so warning unconditionally would train every user to ignore the
// one case that matters.
func TestDefaultWindowsProfileProducesNoDenyReadWarning(t *testing.T) {
	withWindowsHost(t)
	if warnings := windowsDenyReadWarnings(windowsRestrictedTokenBackend(), PermissionProfile{}); len(warnings) != 0 {
		t.Fatalf("the default Windows profile warned about denyRead it does not set: %v", warnings)
	}
	// Blank and whitespace-only entries are not a configured denyRead either.
	if warnings := windowsDenyReadWarnings(windowsRestrictedTokenBackend(), profileWithDenyRead("", "   ")); len(warnings) != 0 {
		t.Fatalf("empty denyRead entries produced a warning: %v", warnings)
	}
}

// The warning describes one specific token implementation, so it must not appear
// for backends that do not build that token.
func TestDenyReadWarningIsScopedToTheWindowsRestrictedToken(t *testing.T) {
	withWindowsHost(t)
	others := []Backend{
		{Name: BackendMacOSSeatbelt, Platform: "darwin", Available: true, NativeIsolation: true},
		{Name: BackendLinuxLandlock, Platform: "linux", Available: true, NativeIsolation: true},
		// Same backend name but no native isolation: no token is built, so the
		// warning would describe enforcement that is not running at all.
		{Name: BackendWindowsRestrictedToken, Platform: "windows", NativeIsolation: false},
	}
	for _, backend := range others {
		if warnings := windowsDenyReadWarnings(backend, profileWithDenyRead(`C:\secret`)); len(warnings) != 0 {
			t.Errorf("backend %q (nativeIsolation=%v) got the Windows token warning: %v", backend.Name, backend.NativeIsolation, warnings)
		}
	}
}

// A Windows-targeted plan built on a non-Windows host must stay silent.
//
// This is the case that broke CI rather than a hypothetical. credentialDenyReadPaths
// returns empty ON Windows and populates itself from the host everywhere else, so a
// Windows plan built on a Linux runner carries that machine's credential paths
// (/home/runner/.docker/config.json) and drew a warning about a token nothing would
// ever build. TestSelectBackendChoosesPlatformAdapterWithFallback asserts a Windows
// plan has no warnings, and it only builds Windows plans from other hosts.
func TestNoDenyReadWarningWhenTheHostIsNotWindows(t *testing.T) {
	for _, host := range []string{"linux", "darwin"} {
		previous := denyReadWarningHostGOOS
		denyReadWarningHostGOOS = host
		warnings := windowsDenyReadWarnings(windowsRestrictedTokenBackend(), profileWithDenyRead("/home/runner/.docker/config.json"))
		denyReadWarningHostGOOS = previous
		if len(warnings) != 0 {
			t.Errorf("a windows-targeted plan built on %s warned about a token that host will never build: %v", host, warnings)
		}
	}
}

// THE EXECUTION PATH, NOT JUST THE DIAGNOSTIC ONE.
//
// The warning above is reachable from BackendPlan, which is what `zero sandbox
// policy` and `zero sandbox check` render. An operator who never runs those
// sees nothing. A real tool call builds a CommandPlan instead, and the Windows
// runner picks the token shape from the resolved profile alone: DenyRead
// non-empty means no WRITE_RESTRICTED, which is the shape #869 is about. So
// approving file_system.deny_read for one command could cost the write jail
// with nothing said.
//
// withSandboxExecutionMetadata is the single funnel every plan passes through,
// including the Windows one, which is why the notice is derived there rather
// than at each caller.
func TestCommandPlanCarriesTheDenyReadDisclosure(t *testing.T) {
	withWindowsHost(t)

	// A request that will actually produce a restricted token. The notice follows
	// the token, so a fixture that only names the backend is not enough.
	plan := withSandboxExecutionMetadata(CommandPlan{}, wrappedWindowsRequest())

	if len(plan.Notes) == 0 {
		t.Fatal("a command plan resolved with denyRead carried no notice, so the operator loses the write jail without being told")
	}
	notice := strings.ToLower(strings.Join(plan.Notes, " "))
	for _, want := range []string{"denyread", "write", "#869"} {
		if !strings.Contains(notice, want) {
			t.Errorf("the execution-path notice does not mention %q: %q", want, notice)
		}
	}
}

// And it stays quiet for the ordinary profile, or every Windows command grows a
// notice about a trade nobody made.
func TestCommandPlanCarriesNoDisclosureWithoutDenyRead(t *testing.T) {
	withWindowsHost(t)

	plan := withSandboxExecutionMetadata(CommandPlan{}, SandboxExecutionRequest{
		Backend:       windowsRestrictedTokenBackend(),
		TargetBackend: BackendWindowsRestrictedToken,
	})
	if len(plan.Notes) != 0 {
		t.Errorf("a plan without denyRead carried notices: %v", plan.Notes)
	}
}

// wrappedWindowsRequest is the shape buildPlatformCommandPlan actually wraps in
// a restricted token: a platform sandbox is required, enforcement is native, the
// target is the Windows restricted-token backend, and nothing outside has
// wrapped the command already.
func wrappedWindowsRequest() SandboxExecutionRequest {
	return SandboxExecutionRequest{
		Backend:                 windowsRestrictedTokenBackend(),
		TargetBackend:           BackendWindowsRestrictedToken,
		PermissionProfile:       profileWithDenyRead(`C:\Users\someone\.config\creds`),
		RequiresPlatformSandbox: true,
		EnforcementLevel:        EnforcementNative,
	}
}

// THE NOTICE DESCRIBES A TOKEN, SO IT MUST FOLLOW THE TOKEN.
//
// Each case below carries the Windows backend and a DenyRead profile, and each
// takes the direct unwrapped plan rather than the restricted-token one. No token
// is created and the deny-read rule is not enforced, so claiming the write jail
// was traded away is false in both halves.
func TestNoDisclosureWhenNoRestrictedTokenIsCreated(t *testing.T) {
	withWindowsHost(t)

	for _, testCase := range []struct {
		name   string
		mutate func(*SandboxExecutionRequest)
	}{
		{name: "sandboxing disabled", mutate: func(r *SandboxExecutionRequest) { r.EnforcementLevel = EnforcementDisabled }},
		{name: "degraded to no native isolation", mutate: func(r *SandboxExecutionRequest) { r.EnforcementLevel = EnforcementDegraded }},
		{name: "already wrapped by an outer sandbox", mutate: func(r *SandboxExecutionRequest) { r.CommandWrapped = true }},
		{name: "command needs no platform sandbox", mutate: func(r *SandboxExecutionRequest) { r.RequiresPlatformSandbox = false }},
		{name: "no target backend", mutate: func(r *SandboxExecutionRequest) { r.TargetBackend = BackendNone }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := wrappedWindowsRequest()
			testCase.mutate(&request)
			plan := withSandboxExecutionMetadata(CommandPlan{}, request)
			if len(plan.Notes) != 0 {
				t.Errorf("claimed the write jail was traded away where no restricted token runs: %v", plan.Notes)
			}
		})
	}
}
