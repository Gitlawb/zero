package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Gitlawb/zero/internal/specialist"
)

// Saved plans, from the TUI: save the plan that just ran, list what is stored,
// read one back, and run one again.
//
// RUNNING ONE GOES THROUGH THE MODEL, not straight into the tool. `/plans run x`
// dispatches an ordinary prompt, the model calls orchestrate with `saved: "x"`,
// and the plan is re-admitted by the same ParsePlan every other plan goes
// through. A command that reached into tool execution directly would be a
// SECOND way to run a plan, with its own copy of the posture gate, the depth
// check, the grant intersection and the recorder wiring — and this feature's
// entire defect history is second call paths that carry less than the first.

// handlePlansCommand dispatches `/plans [verb] [args]`.
//
// Split from orchestrateControlText because `run` has to start a turn, which
// needs a tea.Cmd; the rest only produce text.
func (m model) handlePlansCommand(args string) (tea.Model, tea.Cmd) {
	verb, rest := splitPlansArgs(args)
	switch verb {
	case "save":
		return m.appendPlansNotice(m.savePlanText(rest)), nil
	case "list":
		return m.appendPlansNotice(m.savedPlansText()), nil
	case "show":
		return m.appendPlansNotice(m.showSavedPlanText(rest)), nil
	case "run":
		return m.runSavedPlan(rest)
	default:
		return m.appendPlansNotice(m.orchestrateControlText(args)), nil
	}
}

func (m model) appendPlansNotice(text string) model {
	m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: text})
	return m
}

func splitPlansArgs(args string) (verb, rest string) {
	fields := strings.Fields(strings.TrimSpace(args))
	if len(fields) == 0 {
		return "", ""
	}
	return strings.ToLower(fields[0]), strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(args), fields[0]))
}

// savePlanText writes the plan that ran this session under the given name.
//
// It saves to the PROJECT directory, and that is the useful default: a plan is a
// piece of team knowledge about a repo — "the pre-release sweep" — not a
// personal preference. It is also the scope a user can see and review in a diff.
func (m model) savePlanText(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return planControlNotice("warning", "Name it: /plans save <name>")
	}
	args, planName, ok := m.planProgress.LastPlan()
	if !ok {
		return planControlNotice("warning",
			"No plan has run this session, so there is nothing to save. Run one first — a saved plan is a plan that worked.")
	}
	dir := m.planPaths.ProjectDir
	if dir == "" {
		dir = m.planPaths.UserDir
	}
	if dir == "" {
		return planControlNotice("warning", "There is nowhere to save plans in this run.")
	}
	// This converts args back into a Plan because SavePlan takes a Plan: its
	// signature is what makes "only a validated plan can be written" true by
	// construction rather than by convention.
	//
	// THE ERROR BRANCH IS UNREACHABLE FROM HERE and is not pretending otherwise.
	// These args came from a Plan that ParsePlan already admitted, and
	// savedPlanLimits is deliberately wider than any run's, so nothing can
	// reject them. It is handled because ignoring a returned error is how a
	// reachable version of this goes wrong later — not because it guards
	// something today. The mutation sweep is what forced this to be stated: no
	// test can kill this branch, and a comment claiming it protects the file on
	// disk would have been the third wrong-mechanism comment in this feature.
	//
	// The branch that DOES fire is on the way back in — showSavedPlanText and
	// the tool's own load — where a hand-edited file is re-admitted and can be
	// refused.
	plan, err := specialist.ParsePlan(args, m.savedPlanLimits())
	if err != nil {
		return planControlNotice("warning", "That plan no longer validates, so it was not saved: "+err.Error())
	}
	path, err := specialist.SavePlan(dir, name, plan)
	if err != nil {
		return planControlNotice("warning", "Could not save the plan: "+err.Error())
	}
	label := planName
	if label == "" {
		label = "the last plan"
	}
	return planControlNotice("info", fmt.Sprintf(
		"Saved %s as %q (%d tasks)\n%s\n\nRun it again with /plans run %s.", label, name, plan.TaskCount(), path, name))
}

// savedPlanLimits are the limits used to re-admit a plan for SAVING and for
// SHOWING — deliberately permissive on the tool grant, because saving is not
// running. The grant that matters is the one applied when the plan is actually
// executed, by the tool, against the run that runs it.
//
// MaxTasks is left unset for the same reason: refusing to save a plan that
// already ran, because the tier moved since, would be refusing to record
// history.
func (m model) savedPlanLimits() specialist.Limits {
	return specialist.Limits{ParentTools: specialist.PlanReadOnlyToolNames()}
}

func (m model) savedPlansText() string {
	plans, problems := specialist.LoadPlans(m.planPaths)
	if len(plans) == 0 && len(problems) == 0 {
		return planControlNotice("info",
			"No saved plans.\nRun a plan, then keep it with /plans save <name>.")
	}
	var b strings.Builder
	b.WriteString("Plans\nstatus: info\n")
	if len(plans) == 0 {
		b.WriteString("No readable saved plans.")
	}
	for _, plan := range plans {
		scope := "user"
		if plan.Project {
			scope = "project"
		}
		fmt.Fprintf(&b, "  %-20s %2d tasks  %s", plan.Name, plan.TaskCount, scope)
		if plan.Description != "" {
			b.WriteString("  · " + plan.Description)
		}
		b.WriteString("\n")
	}
	// Unreadable files are NAMED. "No saved plans" while three sit on disk
	// unparseable is a lie by omission, and the user is the only one who can fix
	// the file.
	if len(problems) > 0 {
		b.WriteString("\ncould not be read:\n")
		for _, problem := range problems {
			b.WriteString("  " + problem + "\n")
		}
	}
	b.WriteString("\n/plans show <name> to read one · /plans run <name> to run it")
	return strings.TrimRight(b.String(), "\n")
}

func (m model) showSavedPlanText(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return planControlNotice("warning", "Name it: /plans show <name>")
	}
	stored, err := specialist.FindSavedPlan(m.planPaths, name)
	if err != nil {
		return planControlNotice("warning", err.Error())
	}
	// Shown through ParsePlan, so what is displayed is what would RUN — including
	// the execution order, which is the part a stored task list does not show.
	plan, err := specialist.ParsePlan(stored.Args, m.savedPlanLimits())
	if err != nil {
		return planControlNotice("warning", fmt.Sprintf("%s does not validate: %v", stored.Path, err))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Plans\nstatus: info\n%s · %d tasks · %s\n", name, plan.TaskCount(), stored.Path)
	if plan.Description() != "" {
		b.WriteString(plan.Description() + "\n")
	}
	byID := map[string]specialist.Task{}
	for _, task := range plan.Tasks() {
		byID[task.ID] = task
	}
	for _, id := range plan.Order() {
		task := byID[id]
		fmt.Fprintf(&b, "\n  %s", id)
		if len(task.DependsOn) > 0 {
			deps := append([]string(nil), task.DependsOn...)
			sort.Strings(deps)
			b.WriteString("  after " + strings.Join(deps, ", "))
		}
		if summary := planTaskSummaryLine(task.Prompt); summary != "" {
			b.WriteString("\n      " + summary)
		}
	}
	return b.String()
}

// planTaskSummaryLine is the first line of a prompt, bounded. The full prompt
// stays in the file — a display surface must not become the data path.
func planTaskSummaryLine(prompt string) string {
	summary := strings.TrimSpace(prompt)
	if index := strings.IndexAny(summary, "\r\n"); index >= 0 {
		summary = summary[:index]
	}
	return truncateRunes(summary, planTaskSummaryWidth)
}

// runSavedPlan asks the MODEL to run it. See the note at the top of this file:
// a command that called the tool directly would be a second execution path.
func (m model) runSavedPlan(name string) (tea.Model, tea.Cmd) {
	name = strings.TrimSpace(name)
	if name == "" {
		return m.appendPlansNotice(planControlNotice("warning", "Name it: /plans run <name>")), nil
	}
	// Resolved HERE so an unknown name is a plain refusal rather than a turn
	// spent discovering the plan does not exist.
	if _, err := specialist.FindSavedPlan(m.planPaths, name); err != nil {
		return m.appendPlansNotice(planControlNotice("warning", err.Error())), nil
	}
	encoded, err := json.Marshal(name)
	if err != nil {
		return m.appendPlansNotice(planControlNotice("warning", "Could not reference that plan: "+err.Error())), nil
	}
	return m.dispatchCommand(parsedCommand{
		kind: commandPrompt,
		text: "Run the saved plan " + string(encoded) + " by calling the orchestrate tool with the `saved` argument set to that name. Do not restate its tasks.",
	})
}
