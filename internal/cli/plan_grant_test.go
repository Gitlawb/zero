package cli

import (
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/specialist"
)

// The plan tool's parent grant was declared, documented and validated against —
// and left nil by BOTH production call sites, which made the "a task may narrow
// the parent's grant, never widen it" rule inert. These tests drive
// registerSpecialistTools, the function both call sites go through, rather than
// the helper it uses: asserting the helper would have passed throughout the
// period the wiring was missing.

func registeredOrchestrateTool(t *testing.T, enabled, disabled []string) *specialist.OrchestrateTool {
	t.Helper()
	workspace := t.TempDir()
	registry := newCoreRegistry(workspace)
	runtime, err := registerSpecialistTools(registry, workspace, 0, enabled, disabled, orchestrateWiring{
		Gate: &specialist.PostureGate{},
	})
	if err != nil {
		t.Fatalf("registerSpecialistTools: %v", err)
	}
	t.Cleanup(func() { closeSpecialistRuntime(nil, runtime) })

	registered, found := registry.Get(specialist.OrchestrateToolName)
	if !found {
		t.Fatal("orchestrate was not registered")
	}
	tool, ok := registered.(*specialist.OrchestrateTool)
	if !ok {
		t.Fatalf("orchestrate is %T, not *specialist.OrchestrateTool", registered)
	}
	return tool
}

// An unfiltered run grants every read-only tool a plan task may hold. The
// failure this pins is a grant of length zero, which is what both call sites
// produced.
func TestRegisteredOrchestrateToolCarriesTheRunsGrant(t *testing.T) {
	tool := registeredOrchestrateTool(t, nil, nil)
	if len(tool.ParentTools) == 0 {
		t.Fatal("the registered orchestrate tool holds no parent grant, so the narrowing rule cannot fire")
	}
	want := specialist.PlanReadOnlyToolNames()
	if strings.Join(tool.ParentTools, ",") != strings.Join(want, ",") {
		t.Fatalf("unfiltered grant = %v, want the full read-only set %v", tool.ParentTools, want)
	}
}

// --enabled-tools narrows the grant. This is the reproduction from the audit,
// at the registration boundary: a parent holding only grep must not hand a plan
// task read_file.
func TestRegisteredOrchestrateGrantHonoursEnabledTools(t *testing.T) {
	tool := registeredOrchestrateTool(t, []string{"grep", specialist.OrchestrateToolName}, nil)
	if strings.Join(tool.ParentTools, ",") != "grep" {
		t.Fatalf("grant = %v, want exactly [grep]: a plan task must not hold what --enabled-tools denied the run", tool.ParentTools)
	}
}

// --disabled-tools narrows it too, through the same filter gate the agent loop
// uses, so the two cannot disagree about what the run holds.
func TestRegisteredOrchestrateGrantHonoursDisabledTools(t *testing.T) {
	tool := registeredOrchestrateTool(t, nil, []string{"read_file", "glob"})
	for _, name := range tool.ParentTools {
		if name == "read_file" || name == "glob" {
			t.Fatalf("grant %v still contains a tool --disabled-tools removed from the run", tool.ParentTools)
		}
	}
	if len(tool.ParentTools) == 0 {
		t.Fatal("grant collapsed to empty; only the two disabled tools should have been removed")
	}
}

// A run holding no read-only tools grants nothing. The grant must be EMPTY
// rather than falling back to a default set — an empty grant is refused
// downstream, and the bug was that it expanded instead.
func TestRegisteredOrchestrateGrantIsEmptyWhenTheRunHoldsNoReadTools(t *testing.T) {
	tool := registeredOrchestrateTool(t, []string{specialist.OrchestrateToolName}, nil)
	if len(tool.ParentTools) != 0 {
		t.Fatalf("grant = %v, want empty: this run holds no read-only tools at all", tool.ParentTools)
	}
}

// The candidate set comes from specialist's exported list, not a second copy
// maintained here. Two duplicated lists drift (invariant 5).
func TestPlanParentToolsNeverExceedsThePlanReadOnlySet(t *testing.T) {
	allowed := map[string]bool{}
	for _, name := range specialist.PlanReadOnlyToolNames() {
		allowed[name] = true
	}
	for _, name := range planParentTools(newCoreRegistry(t.TempDir()), nil, nil) {
		if !allowed[name] {
			t.Fatalf("grant contains %q, which is not a tool a plan task may ever hold", name)
		}
	}
}
