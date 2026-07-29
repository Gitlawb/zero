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
	runtime, err := registerSpecialistTools(registry, workspace, 0, enabled, disabled, nil, orchestrateWiring{
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

// The depth the plan tool measures must be the RUN's depth, not a constant.
// Left unset it was always 0, so the admission headroom check and the
// executor's own nesting check could never fire — an inert guard that reads
// like a live one.
func TestRegisteredOrchestrateToolCarriesTheRunsDepth(t *testing.T) {
	workspace := t.TempDir()
	registry := newCoreRegistry(workspace)
	runtime, err := registerSpecialistTools(registry, workspace, 0, nil, nil, nil, orchestrateWiring{
		Gate:        &specialist.PostureGate{},
		PlanContext: specialist.PlanTaskContext{Cwd: workspace, Depth: 3},
	})
	if err != nil {
		t.Fatalf("registerSpecialistTools: %v", err)
	}
	t.Cleanup(func() { closeSpecialistRuntime(nil, runtime) })

	registered, _ := registry.Get(specialist.OrchestrateToolName)
	tool, ok := registered.(*specialist.OrchestrateTool)
	if !ok {
		t.Fatalf("orchestrate is %T", registered)
	}
	if tool.Depth != 3 {
		t.Fatalf("Depth = %d, want the run's depth 3: an always-zero depth makes the headroom check inert", tool.Depth)
	}
}

// The plan recorder is a REQUIRED parameter, not a wiring field. As a field the
// TUI simply never set it, so a plan run there recorded none of its five
// lifecycle events — finding 9, and the same omission the parent grant
// suffered. Making it positional turns forgetting it into a compile error;
// this test pins that a supplied recorder actually reaches the tool.
func TestRegisteredOrchestrateToolCarriesTheSuppliedRecorder(t *testing.T) {
	workspace := t.TempDir()
	registry := newCoreRegistry(workspace)
	recorder := &countingPlanRecorder{}
	runtime, err := registerSpecialistTools(registry, workspace, 0, nil, nil, recorder, orchestrateWiring{
		Gate: &specialist.PostureGate{},
	})
	if err != nil {
		t.Fatalf("registerSpecialistTools: %v", err)
	}
	t.Cleanup(func() { closeSpecialistRuntime(nil, runtime) })

	registered, _ := registry.Get(specialist.OrchestrateToolName)
	tool, ok := registered.(*specialist.OrchestrateTool)
	if !ok {
		t.Fatalf("orchestrate is %T", registered)
	}
	if tool.Recorder != recorder {
		t.Fatal("the supplied recorder did not reach the tool, so plan events go nowhere")
	}
}

type countingPlanRecorder struct{ dispatched int }

func (r *countingPlanRecorder) TaskDispatched(specialist.Task)      { r.dispatched++ }
func (r *countingPlanRecorder) TaskCompleted(specialist.TaskResult) {}
func (r *countingPlanRecorder) TaskFailed(specialist.TaskResult)    {}
