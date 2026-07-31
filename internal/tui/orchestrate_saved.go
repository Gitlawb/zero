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
		return m.runSavedPlan(rest, false)
	case "restart":
		return m.restartLastPlan()
	case "resume":
		// TWO MEANINGS OF ONE WORD, split by arity, and they are the same
		// intention at two scales: continue what was interrupted.
		//
		//   /plans resume         → un-pause the plan running right now
		//   /plans resume <name>  → pick up a saved plan where its last run
		//                           stopped
		//
		// Bare resume keeps the meaning it already had, so the pause control
		// shipped before this does not change under anyone.
		if strings.TrimSpace(rest) == "" {
			return m.appendPlansNotice(m.orchestrateControlText(args)), nil
		}
		return m.runSavedPlan(rest, true)
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
	// GRANTABLE, not read-only. These limits parse a plan that is being saved,
	// shown, restarted or resumed — plans that already ran, or are about to run
	// through the ordinary path with its own approval gate. Granting only the
	// read-only names here meant a plan that ParsePlan accepts at run time
	// (write tools may be named per task) failed to parse on every /plans verb,
	// so the entire durability surface silently excluded exactly the plans whose
	// work is most worth keeping.
	//
	// This widens no authority: the parent grant is intersected again when the
	// plan actually runs, and a write-capable plan still needs its approval and
	// its isolated worktree.
	return specialist.Limits{ParentTools: specialist.PlanGrantableToolNames()}
}

func (m model) savedPlansText() string {
	plans, problems := specialist.LoadPlans(m.planPaths)
	if len(plans) == 0 && len(problems) == 0 {
		return planControlNotice("info",
			"No saved plans.\nRun a plan, then keep it with /plans save <name>.")
	}
	// The BUNDLED plans do not count as "yours". A listing showing only the
	// shipped example while saying nothing about saving would read as though
	// the user already had plans of their own.
	saved := 0
	for _, plan := range plans {
		if plan.Scope != specialist.PlanScopeBuiltin {
			saved++
		}
	}
	var b strings.Builder
	b.WriteString("Plans\nstatus: info\n")
	if len(plans) == 0 {
		b.WriteString("No readable saved plans.")
	}
	for _, plan := range plans {
		fmt.Fprintf(&b, "  %-20s %2d tasks  %-8s", plan.Name, plan.TaskCount, plan.Scope)
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
	if saved == 0 {
		b.WriteString("\nNothing saved yet — run a plan, then keep it with /plans save <name>.")
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
//
// resume narrows the plan to the work the session log says has not succeeded.
func (m model) runSavedPlan(name string, resume bool) (tea.Model, tea.Cmd) {
	verb := "run"
	if resume {
		verb = "resume"
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return m.appendPlansNotice(planControlNotice("warning", "Name it: /plans "+verb+" <name>")), nil
	}
	// Resolved HERE so an unknown name is a plain refusal rather than a turn
	// spent discovering the plan does not exist.
	stored, err := specialist.FindSavedPlan(m.planPaths, name)
	if err != nil {
		return m.appendPlansNotice(planControlNotice("warning", err.Error())), nil
	}

	target := name
	instruction := "Run the saved plan %s by calling the orchestrate tool with the `saved` argument set to that name. Do not restate its tasks."
	if resume {
		remaining, notice, ok := m.resumeSavedPlan(stored)
		if !ok {
			return m.appendPlansNotice(notice), nil
		}
		target = remaining
		instruction = "Resume the plan by calling the orchestrate tool with the `saved` argument set to %s. " +
			"That is the saved plan narrowed to the tasks that had not finished. Do not restate its tasks."
		m = m.appendPlansNotice(notice)
	}
	encoded, err := json.Marshal(target)
	if err != nil {
		return m.appendPlansNotice(planControlNotice("warning", "Could not reference that plan: "+err.Error())), nil
	}
	return m.dispatchCommand(parsedCommand{
		kind: commandPrompt,
		text: fmt.Sprintf(instruction, string(encoded)),
	})
}

// restartLastPlan runs the plan that last ran, from the beginning.
//
// It stages the plan the BRIDGE holds, not the panel's rendering of it — the
// panel has ids, statuses and depths, which would restart something that merely
// resembled what ran. Staging it as a saved plan rather than replaying the
// original tool call is what makes restart inspectable: /plans show last_run
// reads exactly what is about to happen, and it travels the one execution path
// every other plan travels.
//
// A FIXED name, overwritten each time, for the same reason the resume staging
// uses one: a directory of last_run_1, _2, _3 is a directory nobody can tell
// apart, and only the newest is ever the right one.
func (m model) restartLastPlan() (tea.Model, tea.Cmd) {
	args, planName, ok := m.planProgress.LastPlan()
	if !ok {
		return m.appendPlansNotice(planControlNotice("warning",
			"No plan has run this session, so there is nothing to restart. "+
				"/plans list shows what is saved, and /plans run <name> starts one.")), nil
	}
	if m.planProgress.PlanRunningNow() {
		// Restarting under a running plan would leave two plans reporting into
		// one panel, and the panel is keyed by task id — the second would
		// overwrite the first's rows. Stop it first, deliberately, rather than
		// having the UI silently pick one.
		return m.appendPlansNotice(planControlNotice("warning",
			"A plan is still running. Stop it with /plans stop first, then restart.")), nil
	}
	plan, err := specialist.ParsePlan(args, m.savedPlanLimits())
	if err != nil {
		return m.appendPlansNotice(planControlNotice("warning",
			"That plan no longer validates, so it was not restarted: "+err.Error())), nil
	}
	dir := m.planPaths.ProjectDir
	if dir == "" {
		dir = m.planPaths.UserDir
	}
	if dir == "" {
		return m.appendPlansNotice(planControlNotice("warning",
			"There is nowhere to stage the plan in this run.")), nil
	}
	if _, err := specialist.SavePlan(dir, restartPlanName, plan); err != nil {
		return m.appendPlansNotice(planControlNotice("warning", "Could not stage the plan: "+err.Error())), nil
	}
	label := planName
	if label == "" {
		label = "the last plan"
	}
	m = m.appendPlansNotice(planControlNotice("info", fmt.Sprintf(
		"Restarting %s from the beginning (%d tasks). Staged as %q — /plans show %s to read it first.",
		label, plan.TaskCount(), restartPlanName, restartPlanName)))
	encoded, err := json.Marshal(restartPlanName)
	if err != nil {
		return m.appendPlansNotice(planControlNotice("warning", "Could not reference that plan: "+err.Error())), nil
	}
	return m.dispatchCommand(parsedCommand{
		kind: commandPrompt,
		text: "Run the saved plan " + string(encoded) + " by calling the orchestrate tool with the `saved` argument set to that name. Do not restate its tasks.",
	})
}

// restartPlanName is where a restart stages the last plan.
const restartPlanName = "last_run"

// resumeSavedPlan narrows a stored plan by the CURRENT session's plan events and
// saves the remainder under its own name.
//
// It writes a new saved plan rather than teaching the tool a second "skip these
// ids" argument. The remainder is a plan — it validates, it runs, it can be
// inspected with /plans show before anyone spends a token on it — and running it
// travels the one path every other plan travels. A skip list would have been a
// parallel notion of what a plan is, resolved somewhere other than ParsePlan.
func (m model) resumeSavedPlan(stored specialist.SavedPlan) (name string, notice string, ok bool) {
	if m.sessionStore == nil || m.activeSession.SessionID == "" {
		return "", planControlNotice("warning",
			"This session has no event log, so there is no record of what already ran."), false
	}
	events, err := m.sessionStore.ReadEvents(m.activeSession.SessionID)
	if err != nil {
		return "", planControlNotice("warning", "Could not read this session's events: "+err.Error()), false
	}
	progress, found := specialist.ReducePlanEvents(events)
	if !found {
		return "", planControlNotice("warning",
			"No plan has run in this session, so there is nothing to resume. Use /plans run "+stored.Name+" to run it from the start."), false
	}
	plan, err := specialist.ParsePlan(stored.Args, m.savedPlanLimits())
	if err != nil {
		return "", planControlNotice("warning", fmt.Sprintf("%s does not validate: %v", stored.Path, err)), false
	}
	// The reduction keeps only the LAST admitted plan's progress, so it may
	// belong to a different plan than the one being resumed. Narrowing across
	// that boundary silently drops work: ids like "tests" or "lint" collide
	// between plans routinely, and every task of this plan whose id sits in the
	// other plan's succeeded set would vanish from the remainder and never run.
	//
	// Compared against plan.Name(), not stored.Name: plan_admitted records the
	// plan's own name, which is independent of the name it was saved under.
	if progress.Name != plan.Name() {
		return "", planControlNotice("warning", fmt.Sprintf(
			"The last plan to run in this session was %q, not %q, so there is no record of what %q already did. Use /plans run %s to run it from the start.",
			progress.Name, plan.Name(), plan.Name(), stored.Name)), false
	}
	remaining, err := specialist.RemainingPlan(plan, progress, m.savedPlanLimits())
	if err != nil {
		return "", planControlNotice("info", err.Error()), false
	}

	dir := m.planPaths.ProjectDir
	if dir == "" {
		dir = m.planPaths.UserDir
	}
	// A FIXED name, overwritten each time. A resume that accumulated
	// sweep-resume-1, -2, -3 would leave a directory of near-identical plans
	// nobody can tell apart, and only the newest is ever the right one.
	resumeName := stored.Name + "_resume"
	if _, err := specialist.SavePlan(dir, resumeName, remaining); err != nil {
		return "", planControlNotice("warning", "Could not stage the remaining plan: "+err.Error()), false
	}
	return resumeName, planControlNotice("info", fmt.Sprintf(
		"Resuming %q: %d of %d tasks already succeeded, %d left.\nSaved the remainder as %q — /plans show %s to read it first.",
		stored.Name, len(progress.Succeeded), len(progress.Order), remaining.TaskCount(), resumeName, resumeName)), true
}
