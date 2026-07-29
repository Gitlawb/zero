package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gitlawb/zero/internal/specialist"
)

func savedPlanModel(t *testing.T) (model, specialist.PlanPaths) {
	t.Helper()
	root := t.TempDir()
	paths := specialist.PlanPaths{
		ProjectDir: filepath.Join(root, ".zero", "plans"),
		UserDir:    filepath.Join(t.TempDir(), "zero", "plans"),
	}
	m := model{planProgress: NewPlanProgressBridge(), planPaths: paths}
	m.planProgress.Attach(func(tea.Msg) {}, 1, nil, "")
	return m, paths
}

// SAVE-FROM-RUN. The panel holds a rendering of a plan; the bridge holds the
// plan. Saving from the panel's copy would write something that merely
// resembles what ran.
func TestSavingKeepsThePlanThatActuallyRan(t *testing.T) {
	m, paths := savedPlanModel(t)
	m.planProgress.PlanAdmitted(samplePlan(t))

	text := m.savePlanText("sweep")
	if !strings.Contains(text, "status: info") {
		t.Fatalf("save failed: %s", text)
	}
	stored, err := specialist.FindSavedPlan(paths, "sweep")
	if err != nil {
		t.Fatalf("the saved plan is not findable: %v", err)
	}
	if stored.TaskCount != samplePlan(t).TaskCount() {
		t.Fatalf("saved %d tasks, the plan had %d", stored.TaskCount, samplePlan(t).TaskCount())
	}
	// It must be RUNNABLE, not merely present: re-admitted through the one
	// constructor, exactly as the tool will do.
	if _, err := specialist.ParsePlan(stored.Args, specialist.Limits{
		MaxTasks: 20, ParentTools: specialist.PlanReadOnlyToolNames()}); err != nil {
		t.Fatalf("the saved plan does not re-admit: %v", err)
	}
}

// Saving with nothing to save must refuse. An empty file named after a plan the
// user believes they kept is worse than a refusal.
func TestSavingWithNoPlanRefuses(t *testing.T) {
	m, paths := savedPlanModel(t)
	text := m.savePlanText("sweep")
	if !strings.Contains(text, "status: warning") || !strings.Contains(text, "nothing to save") {
		t.Fatalf("save must refuse with a reason: %s", text)
	}
	if plans, _ := specialist.LoadPlans(paths); len(plans) != 0 {
		t.Fatalf("a file was written anyway: %+v", plans)
	}
}

func TestSavingRequiresAName(t *testing.T) {
	m, _ := savedPlanModel(t)
	m.planProgress.PlanAdmitted(samplePlan(t))
	if text := m.savePlanText("  "); !strings.Contains(text, "/plans save <name>") {
		t.Fatalf("an unnamed save must say how: %s", text)
	}
}

// An invalid name is refused by the STORE, and the surface reports it rather
// than swallowing it.
func TestSavingAnInvalidNameIsReported(t *testing.T) {
	m, _ := savedPlanModel(t)
	m.planProgress.PlanAdmitted(samplePlan(t))
	text := m.savePlanText("../escape")
	if !strings.Contains(text, "status: warning") {
		t.Fatalf("a traversal name must be refused: %s", text)
	}
}

func TestListingShowsScopeAndUnreadableFiles(t *testing.T) {
	m, paths := savedPlanModel(t)
	m.planProgress.PlanAdmitted(samplePlan(t))
	if text := m.savePlanText("sweep"); !strings.Contains(text, "status: info") {
		t.Fatal(text)
	}
	if err := os.WriteFile(filepath.Join(paths.ProjectDir, "broken.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	text := m.savedPlansText()
	if !strings.Contains(text, "sweep") || !strings.Contains(text, "project") {
		t.Fatalf("the listing must name the plan and its scope: %s", text)
	}
	if !strings.Contains(text, "could not be read") || !strings.Contains(text, "broken.json") {
		t.Fatalf("an unreadable file must be named, not hidden: %s", text)
	}
}

func TestListingWithNothingSavedSaysHowToSave(t *testing.T) {
	m, _ := savedPlanModel(t)
	text := m.savedPlansText()
	if !strings.Contains(text, "No saved plans") || !strings.Contains(text, "/plans save") {
		t.Fatalf("an empty listing must say how to fill it: %s", text)
	}
}

// SHOW renders through ParsePlan, so what is displayed is what would run —
// including the execution order, which a stored task list does not carry.
func TestShowRendersTheExecutionOrder(t *testing.T) {
	m, _ := savedPlanModel(t)
	m.planProgress.PlanAdmitted(samplePlan(t))
	if text := m.savePlanText("sweep"); !strings.Contains(text, "status: info") {
		t.Fatal(text)
	}

	text := m.showSavedPlanText("sweep")
	if !strings.Contains(text, "sweep") {
		t.Fatalf("show did not name the plan: %s", text)
	}
	for _, task := range samplePlan(t).Tasks() {
		if !strings.Contains(text, task.ID) {
			t.Fatalf("show omitted task %q: %s", task.ID, text)
		}
	}
}

func TestShowAndRunRefuseAnUnknownName(t *testing.T) {
	m, _ := savedPlanModel(t)
	if text := m.showSavedPlanText("nope"); !strings.Contains(text, "no saved plan named") {
		t.Fatalf("show must refuse by name: %s", text)
	}
	updated, cmd := m.runSavedPlan("nope")
	if cmd != nil {
		t.Fatal("running an unknown plan started a turn")
	}
	rendered := transcriptText(updated.(model).transcript)
	if !strings.Contains(rendered, "no saved plan named") {
		t.Fatalf("run must refuse by name: %s", rendered)
	}
}

// A hand-edited plan file that is VALID JSON but not a valid plan must be
// refused on the way back in, naming the file. This is the re-admit branch that
// actually fires — the one on the save side cannot, and says so.
func TestAHandEditedPlanIsRefusedOnTheWayBackIn(t *testing.T) {
	m, paths := savedPlanModel(t)
	if err := os.MkdirAll(paths.ProjectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Parseable JSON, impossible plan: a dependency on a task that is not there.
	body := `{"name":"edited","tasks":[{"id":"a","prompt":"x","depends_on":["ghost"]}],"budget":{"max_workers":1}}`
	if err := os.WriteFile(filepath.Join(paths.ProjectDir, "edited.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	text := m.showSavedPlanText("edited")
	if !strings.Contains(text, "status: warning") || !strings.Contains(text, "does not validate") {
		t.Fatalf("an invalid stored plan must be refused: %s", text)
	}
	if !strings.Contains(text, "edited.json") {
		t.Fatalf("the refusal must name the file: %s", text)
	}
	if !strings.Contains(text, "ghost") {
		t.Fatalf("the refusal must say what is wrong with it: %s", text)
	}
}
