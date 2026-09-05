package sandbox

import (
	"strings"
	"testing"
)

// diagnosticWarnings renders what `zero sandbox policy` and `zero sandbox check`
// show, through the manager rather than by hand, so the request carries the state
// the resolution actually produces.
func diagnosticWarnings(t *testing.T, mode PolicyMode, denyRead []string, preference SandboxPreference) (SandboxExecutionRequest, []string) {
	t.Helper()
	workspace := t.TempDir()
	backend := windowsRestrictedTokenBackend()
	backend.CommandWrapping = true
	backend.Executable = `C:\Windows\System32\cmd.exe`
	manager := NewSandboxManager(SandboxManagerOptions{GOOS: "windows", Backend: backend})
	policy := Policy{Mode: mode, EnforceWorkspace: true, DenyRead: denyRead}
	request, err := manager.BuildExecutionRequest(SandboxManagerRequest{
		WorkspaceRoot: workspace,
		Command:       CommandSpec{Name: "cmd.exe", Args: []string{"/c", "echo hi"}, Dir: workspace},
		Policy:        policy,
		Preference:    preference,
	})
	if err != nil {
		t.Fatalf("BuildExecutionRequest: %v", err)
	}
	return request, request.BackendPlan(policy).Warnings
}

func denyReadWarning(warnings []string) string {
	for _, warning := range warnings {
		if strings.Contains(strings.ToLower(warning), "denyread") {
			return warning
		}
	}
	return ""
}

// THE DIAGNOSTICS HAVE TO DESCRIBE THE RESOLVED PLAN, NOT THE INSTALLED BACKEND.
//
// request.Backend is always the AVAILABLE backend, so on a Windows host it stays
// the restricted-token backend with NativeIsolation set even when the resolution
// disables sandboxing entirely. BackendPlan keyed the warning off that field and
// the requested profile, so `--sandbox forbid` with deny_read configured resolved
// to enforcement disabled and target none, built no token, enforced no read rule,
// and still reported that the write jail had been traded for read denial.
//
// The reassuring half is the one that was false: "reads are denied as requested"
// on a run where nothing is denied at all.
func TestForbiddenSandboxDoesNotClaimTheDenyReadTokenTrade(t *testing.T) {
	withWindowsHost(t)

	request, warnings := diagnosticWarnings(t, ModeEnforce, []string{`C:\Users\someone\.config\creds`}, SandboxPreferenceForbid)

	// The preconditions that make this the interesting case rather than a
	// vacuous pass: deny_read really did survive into the resolved profile, and
	// the plan really does build nothing.
	if len(normalizeProfilePaths(request.PermissionProfile.FileSystem.DenyRead)) == 0 {
		t.Fatal("deny_read did not reach the resolved profile, so this no longer exercises the diagnostic gate")
	}
	if request.EnforcementLevel != EnforcementDisabled || request.TargetBackend != BackendNone {
		t.Fatalf("expected a forbidden plan to resolve to disabled/none, got level %s target %s", request.EnforcementLevel, request.TargetBackend)
	}

	if warning := denyReadWarning(warnings); warning != "" {
		t.Errorf("a forbidden sandbox claimed the deny_read token trade on a plan that builds no token and denies no read: %q", warning)
	}
}

// And the warning still fires where the token is real, or the fix above is
// satisfied by never warning at all.
func TestDiagnosticsStillDiscloseTheTradeWhereTheTokenRuns(t *testing.T) {
	withWindowsHost(t)

	request, warnings := diagnosticWarnings(t, ModeEnforce, []string{`C:\Users\someone\.config\creds`}, SandboxPreferenceAuto)
	if !request.CommandWrapped || request.TargetBackend != BackendWindowsRestrictedToken {
		t.Skipf("this environment produced no wrapped Windows plan (target %s, level %s)", request.TargetBackend, request.EnforcementLevel)
	}
	if denyReadWarning(warnings) == "" {
		t.Fatalf("a plan that does build the restricted token disclosed nothing: %v", warnings)
	}
}

// THE EXECUTION-PATH GUARD MUST SURVIVE THE SHARED PREDICATE.
//
// willBuildWindowsRestrictedToken was split out of windowsRestrictedTokenWillRun
// so the diagnostics could reuse it. plan.Wrapped deliberately stayed behind with
// the caller, because the request cannot speak for the produced plan. Folding it
// in would buy symmetry and hand the execution path back the bug the request-side
// checks alone cannot catch.
func TestSharedPredicateDidNotSwallowTheWrappedPlanGuard(t *testing.T) {
	withWindowsHost(t)

	request := wrappedWindowsRequest()
	if !request.willBuildWindowsRestrictedToken() {
		t.Fatal("the request half of the predicate rejected a request that does build the token")
	}
	// Same request, unwrapped plan. The request half says yes; only plan.Wrapped
	// says no, so this is exactly the guard that must not have moved.
	if windowsRestrictedTokenWillRun(CommandPlan{Wrapped: false}, request) {
		t.Error("an unwrapped plan was reported as building the restricted token")
	}
	if !windowsRestrictedTokenWillRun(CommandPlan{Wrapped: true}, request) {
		t.Error("a wrapped plan that builds the token was reported as not building it")
	}
}
