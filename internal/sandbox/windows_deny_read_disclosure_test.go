package sandbox

import (
	"strings"
	"testing"
)

// THE DISCLOSURE HAS TO REACH THE PLANS THAT ACTUALLY BUILD THE TOKEN.
//
// The predicate keyed on request.CommandWrapped, read as "something already
// wrapped this". That is the opposite of what the field means:
// BuildExecutionRequest sets it TRUE for exactly the native and unelevated
// requests that then route to windowsRestrictedTokenCommandPlan. So the notice
// was suppressed on every plan that creates the restricted token and fired on
// none of them, while the old test passed because its hand-built request left
// the field false, which is a shape no real execution has.
//
// Built through the manager here rather than by hand, so the request carries the
// state the transition actually produces.
// denyRead goes on the POLICY, not on a hand-built profile. BuildExecutionRequest
// resolves the profile from the policy, so a profile passed in here is discarded
// and the request arrives with an empty DenyRead. That is how the first version
// of this test managed to fail against a working fix.
func windowsDisclosurePlan(t *testing.T, mode PolicyMode, denyRead []string, preference SandboxPreference) CommandPlan {
	t.Helper()
	workspace := t.TempDir()
	backend := windowsRestrictedTokenBackend()
	backend.CommandWrapping = true
	backend.Executable = `C:\Windows\System32\cmd.exe`
	manager := NewSandboxManager(SandboxManagerOptions{GOOS: "windows", Backend: backend})
	plan, err := manager.BuildCommandPlan(SandboxManagerRequest{
		WorkspaceRoot: workspace,
		Command:       CommandSpec{Name: "cmd.exe", Args: []string{"/c", "echo hi"}, Dir: workspace},
		Policy:        Policy{Mode: mode, EnforceWorkspace: true, DenyRead: denyRead},
		Preference:    preference,
	})
	if err != nil {
		t.Fatalf("BuildCommandPlan: %v", err)
	}
	return plan
}

func planNotes(plan CommandPlan) string {
	return strings.ToLower(strings.Join(plan.Notes, " "))
}

func TestEveryPlanThatBuildsTheRestrictedTokenCarriesTheDisclosure(t *testing.T) {
	withWindowsHost(t)
	denyRead := []string{`C:\Users\someone\.config\creds`}

	plan := windowsDisclosurePlan(t, ModeEnforce, denyRead, SandboxPreferenceAuto)
	if !plan.Wrapped {
		t.Skipf("this environment did not produce a wrapped Windows plan (backend %s, level %s)", plan.TargetBackend, plan.EnforcementLevel)
	}
	if len(plan.Notes) == 0 {
		t.Fatalf("a wrapped Windows plan carried no disclosure; every real deny_read execution gets the non-WRITE_RESTRICTED token and is told nothing (level %s)", plan.EnforcementLevel)
	}
	if !strings.Contains(planNotes(plan), "write") {
		t.Errorf("the note does not mention the write jail: %v", plan.Notes)
	}
}

// And the plans that build no token stay silent, or the assertion above would be
// satisfied by a notice attached to everything. A direct unwrapped plan carries
// the Windows backend and the same profile, so this is the case that made the
// original predicate look necessary.
func TestPlansThatBuildNoTokenStaySilent(t *testing.T) {
	withWindowsHost(t)
	denyRead := []string{`C:\Users\someone\.config\creds`}

	for _, testCase := range []struct {
		name       string
		mode       PolicyMode
		preference SandboxPreference
	}{
		{"sandbox forbidden, so the plan is direct", ModeEnforce, SandboxPreferenceForbid},
		{"sandbox disabled, so nothing is wrapped", ModeDisabled, SandboxPreferenceAuto},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			plan := windowsDisclosurePlan(t, testCase.mode, denyRead, testCase.preference)
			if plan.Wrapped {
				t.Fatalf("this case was supposed to produce an unwrapped plan (%s)", plan.EnforcementLevel)
			}
			if len(plan.Notes) != 0 {
				t.Errorf("an unwrapped plan claimed the write jail was traded away: %v", plan.Notes)
			}
		})
	}
}

// THE PROJECTION ITSELF, driven from a real plan.
//
// Everything else about notices in this PR is asserted by handing a constructor
// a Notices slice and checking it comes out the other side. That proves the
// consumers and not the producer: deleting the one line in EnforcementFor that
// puts plan.Notes into Enforcement.Notices left every notice test in the repo
// green, and that line is the whole reason hooks, plugins and MCP see anything.
//
// This starts from a plan the manager built, not a literal, so the chain
// profile -> plan.Notes -> Enforcement.Notices is covered end to end.
func TestEnforcementForCarriesThePlanNoticesToTheGenericContract(t *testing.T) {
	withWindowsHost(t)
	denyRead := []string{`C:\Users\someone\.config\creds`}

	plan := windowsDisclosurePlan(t, ModeEnforce, denyRead, SandboxPreferenceAuto)
	if !plan.Wrapped {
		t.Skipf("this environment did not produce a wrapped Windows plan (%s)", plan.EnforcementLevel)
	}
	if len(plan.Notes) == 0 {
		t.Fatal("SETUP INVALID: the plan carries no notes, so the projection has nothing to carry")
	}

	enforcement := EnforcementFor(plan)
	if len(enforcement.Notices) != len(plan.Notes) {
		t.Fatalf("EnforcementFor produced %d notices from %d plan notes; hooks, plugins and MCP read this field and would see nothing",
			len(enforcement.Notices), len(plan.Notes))
	}
	for index, note := range plan.Notes {
		if enforcement.Notices[index] != note {
			t.Errorf("notice %d = %q, want %q", index, enforcement.Notices[index], note)
		}
	}
}

// And a plan with nothing to disclose projects nothing, or the assertion above
// would be satisfied by a field that is never empty.
func TestEnforcementForCarriesNoNoticesFromASilentPlan(t *testing.T) {
	withWindowsHost(t)

	plan := windowsDisclosurePlan(t, ModeEnforce, nil, SandboxPreferenceAuto)
	if len(plan.Notes) != 0 {
		t.Fatalf("SETUP INVALID: a plan with no denyRead carries notes: %v", plan.Notes)
	}
	if notices := EnforcementFor(plan).Notices; len(notices) != 0 {
		t.Errorf("a silent plan projected notices: %v", notices)
	}
}
