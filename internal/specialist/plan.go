package specialist

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Gitlawb/zero/internal/config"
)

// ZeroMaxing Phase 2: plan capture, executed SEQUENTIALLY.
//
// The deliverable is DATA. A plan declares a dependency graph, Zero runs it in
// topological order one task at a time, and records how long each task took. If
// the recorded max_speedup says fan-out would not have paid for itself, Phase 3
// is not built — see PlanReport.MaxSpeedup.
//
// Fan-out, pipeline and phase are ONE structure: fan-out is tasks with no
// dependencies, a pipeline is a chain, a phase is a label plus a barrier. The
// prototype grew five separate hooks for these and never invoked any of them.

// Plan is a validated, executable plan.
//
// Its fields are UNEXPORTED and ParsePlan is the only constructor. That is not
// stylistic: it makes skipping validation impossible rather than merely wrong.
// The prototype's validator was reachable, correct, and never called from the
// production path; a type that cannot be constructed any other way removes the
// choice.
type Plan struct {
	name        string
	description string
	tasks       []Task
	budget      Budget
	// order is the topological order Kahn's algorithm emitted during
	// validation. The executor reuses it rather than recomputing, so admission
	// and execution cannot disagree about the order or about acyclicity.
	order []string
}

// Task is one unit of a plan.
type Task struct {
	ID        string
	Prompt    string
	DependsOn []string
	// Tools is the read-only subset this task may use. Empty means "the parent's
	// full read-only grant". It can only ever NARROW: validated here and
	// intersected again at dispatch, so a validator bug cannot widen authority.
	Tools []string
	// Phase is a display and ordering label only. It carries no execution
	// semantics in Phase 2 — a barrier is expressible as dependencies.
	Phase string
}

// Budget bounds a plan. Every field is required to be sane at admission and
// MaxTokens is enforced again at dispatch.
type Budget struct {
	// MaxWorkers is how many tasks the plan may run at once, 1 to maxPlanWorkers.
	//
	// It is what the PLAN asked for, not what it will get: the machine's own
	// capacity may be lower, and the report says which number actually applied
	// rather than letting the request stand as if it were honoured.
	MaxWorkers int
	MaxTokens  int
	MaxWall    time.Duration
	// MaxStall bounds how long a single task may emit NOTHING before it is
	// stopped. Distinct from MaxWall, which bounds the whole plan: a plan can
	// sit inside its wall budget while one task is wedged and the rest never
	// run. Zero means the default.
	MaxStall time.Duration
	// MaxRetries is how many EXTRA attempts a STALLED task gets. Resolved to its
	// effective value at parse time — 0 here means no retries, and an unset
	// max_retries has already become the default by the time anything reads it,
	// so nothing downstream re-derives it and 0 can never be mistaken for unset.
	//
	// Only stalls are retried. See runTaskWithRetries.
	MaxRetries int
}

// Limits are the caller-supplied hard caps a plan must fit inside.
type Limits struct {
	// MaxTasks bounds plan size. 0 means no bound.
	MaxTasks int
	// MaxTasksSource LABELS where MaxTasks came from, for the rejection message
	// only — "the \"medium\" plan size". Deliberately a phrase and not a second
	// number: a label cannot contradict the bound it describes, whereas a second
	// copy of the count would eventually disagree with the one being enforced
	// (invariant 5). Empty renders a generic message.
	MaxTasksSource string
	// MaxTokens is the ceiling a plan's own budget may not exceed.
	MaxTokens int
	// ParentTools is the grant the parent run holds. A task's Tools must be a
	// subset; anything outside it is rejected.
	//
	// EMPTY MEANS EMPTY, not "unset". The intersection below is unconditional:
	// a caller that does not supply this grants nothing, and every task is
	// rejected. That is deliberate — the previous "skip the check when the
	// list is empty" escape hatch made the rule inert at both production call
	// sites, which supplied no grant at all, and a narrower parent produced a
	// wider child. Fail closed (invariant 3): an unsupplied grant is a wiring
	// bug, and the run must stop rather than assume authority.
	ParentTools []string
	// CurrentDepth is the depth of the run issuing the plan. Its tasks run one
	// level deeper, so the check is against maxSpecialistDepth.
	CurrentDepth int
}

// planIDPattern is an ALLOW-LIST. Enumerating permitted characters rather than
// forbidden ones is this repo's standing rule for classification: every
// deny-list here has leaked (git -C, then git -c, then --exec-path).
var planIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// writeToolMarkers name the mutating capabilities Phase 2 tasks may not have.
// Matched as an allow-list would be inverted, so this list is checked AGAINST a
// read-only allow-list below rather than used as the primary gate.
var planReadOnlyTools = map[string]bool{
	"read_file":          true,
	"read_minified_file": true,
	"list_directory":     true,
	"grep":               true,
	"glob":               true,
	"lsp_navigate":       true,
	// update_plan is DELIBERATELY ABSENT, and it was here.
	//
	// TWO REASONS, and the second was measured rather than reasoned.
	//
	// It is not read-only. It REPLACES the parent's whole plan list on every
	// call, so N tasks each calling it race and the last one wins — and a plan
	// task is an investigator that reports to the parent, which owns the list.
	// Nothing about the job needs it.
	//
	// And it was the single reason a plan child carried the confirmation
	// policy. update_plan declares readOnlySafety but sets no capabilities, so
	// CapabilitiesOf reports EffectUnknown — the fail-closed default — while its
	// safety says read-only. runCanMutate reads the CAPABILITY, so one
	// unclassified tool in an otherwise read-only grant made the whole child
	// look mutating, and ~2,500 tokens of policy it can never need rode along on
	// every task of every plan.
	//
	// That mismatch is a PRE-EXISTING MAIN DEFECT and is filed as one rather
	// than fixed here: the same capability drives concurrency eligibility, and
	// promoting update_plan to EffectReadOnly would make it eligible for
	// parallel execution it is not safe for. Removing it from this grant fixes
	// the plan path without touching a classification that means something else
	// somewhere else.
}

// Name, Description, Tasks and Budget expose a validated plan for execution and
// reporting. Read-only accessors: a Plan cannot be mutated after ParsePlan.
func (p Plan) Name() string        { return p.name }
func (p Plan) Description() string { return p.description }
func (p Plan) Budget() Budget      { return p.budget }

// Tasks returns a copy so a caller cannot mutate a validated plan.
func (p Plan) Tasks() []Task {
	out := make([]Task, len(p.tasks))
	copy(out, p.tasks)
	return out
}

// Order returns the validated topological order.
func (p Plan) Order() []string {
	out := make([]string, len(p.order))
	copy(out, p.order)
	return out
}

// TaskCount is THE counting function. Both admission and the executor call it.
//
// It is a length, not a text scan. The prototype counted source text —
// strings.Count(body, "agent(") in admission and a regex in the compiler — so
// `agent ("x")` with one space counted as zero and executed anyway. Counting a
// parsed structure removes the class rather than fixing the regex.
func (p Plan) TaskCount() int { return len(p.tasks) }

// Args renders a validated plan back into the argument shape ParsePlan accepts.
//
// THE ROUND TRIP IS THE POINT. Saving a plan means saving something that can be
// run again, and the only thing that can be run is what ParsePlan admits — so a
// saved plan is stored as ARGS and re-admitted on load, rather than as a
// serialised Plan that would enter execution having skipped the one constructor
// that validates. The prototype's validator was reachable, correct and never
// called on the production path; a stored object that deserialises straight into
// an executable is the same hole with a filesystem in front of it.
//
// Budget fields that were resolved to defaults are written as the values in
// force, so a plan saved today runs the same way after a default changes.
func (p Plan) Args() map[string]any {
	tasks := make([]any, 0, len(p.tasks))
	for _, task := range p.tasks {
		entry := map[string]any{"id": task.ID, "prompt": task.Prompt}
		if len(task.DependsOn) > 0 {
			entry["depends_on"] = stringsToAny(task.DependsOn)
		}
		if len(task.Tools) > 0 {
			entry["tools"] = stringsToAny(task.Tools)
		}
		if task.Phase != "" {
			entry["phase"] = task.Phase
		}
		tasks = append(tasks, entry)
	}
	budget := map[string]any{
		"max_workers": p.budget.MaxWorkers,
		"max_retries": p.budget.MaxRetries,
	}
	if p.budget.MaxTokens > 0 {
		budget["max_tokens"] = p.budget.MaxTokens
	}
	if p.budget.MaxWall > 0 {
		budget["max_wall_seconds"] = int(p.budget.MaxWall.Seconds())
	}
	if p.budget.MaxStall > 0 {
		budget["max_stall_seconds"] = int(p.budget.MaxStall.Seconds())
	}
	args := map[string]any{"tasks": tasks, "budget": budget}
	if p.name != "" {
		args["name"] = p.name
	}
	if p.description != "" {
		args["description"] = p.description
	}
	return args
}

func stringsToAny(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

// ParsePlan is the ONLY way to obtain a Plan. It parses and validates in one
// step; there is no path from tool arguments to an executable plan that skips
// it. Every check rejects by default.
func ParsePlan(args map[string]any, limits Limits) (Plan, error) {
	plan := Plan{
		name:        planString(args, "name"),
		description: planString(args, "description"),
	}

	rawTasks, err := planTaskList(args)
	if err != nil {
		return Plan{}, err
	}
	if len(rawTasks) == 0 {
		return Plan{}, fmt.Errorf("plan requires at least one task")
	}
	if limits.MaxTasks > 0 && len(rawTasks) > limits.MaxTasks {
		return Plan{}, planTooLargeError(len(rawTasks), limits)
	}

	// DEPTH, at admission. A plan runs inside a child at limits.CurrentDepth, so
	// its tasks are one level deeper. Checking here — with the remaining
	// headroom named — turns an opaque mid-plan failure into a rejection the
	// caller can act on.
	taskDepth := limits.CurrentDepth + 1
	if taskDepth >= maxSpecialistDepth {
		return Plan{}, fmt.Errorf(
			"a plan's tasks would run at depth %d, which reaches the maximum nesting depth of %d; this run is already at depth %d, leaving no headroom for plan tasks",
			taskDepth, maxSpecialistDepth, limits.CurrentDepth)
	}

	seen := map[string]bool{}
	for index, raw := range rawTasks {
		task, err := planTask(raw, index)
		if err != nil {
			return Plan{}, err
		}
		if seen[task.ID] {
			return Plan{}, fmt.Errorf("task id %q appears more than once; ids must be unique", task.ID)
		}
		seen[task.ID] = true
		if err := validateTaskTools(task, limits); err != nil {
			return Plan{}, err
		}
		plan.tasks = append(plan.tasks, task)
	}

	// Every DependsOn must resolve. An unknown edge is REJECTED, never skipped:
	// skipping it would silently execute a task whose stated precondition never
	// ran, which is worse than refusing the plan.
	for _, task := range plan.tasks {
		for _, dep := range task.DependsOn {
			if !seen[dep] {
				return Plan{}, fmt.Errorf("task %q depends on %q, which is not a task in this plan", task.ID, dep)
			}
			if dep == task.ID {
				return Plan{}, fmt.Errorf("task %q depends on itself", task.ID)
			}
		}
	}

	order, cycle := topologicalOrder(plan.tasks)
	if cycle != nil {
		return Plan{}, fmt.Errorf("plan has a dependency cycle involving: %s", strings.Join(cycle, ", "))
	}
	plan.order = order

	budget, err := planBudget(args, limits)
	if err != nil {
		return Plan{}, err
	}
	plan.budget = budget
	return plan, nil
}

// topologicalOrder runs Kahn's algorithm. It returns the emitted order, or the
// ids still carrying unmet dependencies when the queue drains early — which is
// exactly the set involved in a cycle.
//
// There was no cycle detection anywhere in this tree to reuse. Audit U24: a
// cyclic page tree hangs forever precisely because nothing checks.
func topologicalOrder(tasks []Task) (order []string, cycle []string) {
	indegree := map[string]int{}
	dependents := map[string][]string{}
	for _, task := range tasks {
		if _, ok := indegree[task.ID]; !ok {
			indegree[task.ID] = 0
		}
		for _, dep := range task.DependsOn {
			indegree[task.ID]++
			dependents[dep] = append(dependents[dep], task.ID)
		}
	}

	// Seed with every zero-indegree node, in declaration order so the emitted
	// order is deterministic for a given plan.
	ready := []string{}
	for _, task := range tasks {
		if indegree[task.ID] == 0 {
			ready = append(ready, task.ID)
		}
	}
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		order = append(order, id)
		next := append([]string(nil), dependents[id]...)
		sort.Strings(next)
		for _, dependent := range next {
			indegree[dependent]--
			if indegree[dependent] == 0 {
				ready = append(ready, dependent)
			}
		}
	}
	if len(order) == len(tasks) {
		return order, nil
	}
	// The queue drained early: everything still carrying an indegree is part of
	// a cycle or downstream of one. Name them — an unnamed "cycle detected" is
	// not actionable on a twenty-task plan.
	for _, task := range tasks {
		if indegree[task.ID] > 0 {
			cycle = append(cycle, task.ID)
		}
	}
	sort.Strings(cycle)
	return nil, cycle
}

// validateTaskTools enforces both Phase 2 rules: read-only, and never wider
// than the parent's grant. Enforced AGAIN at dispatch — see planToolGrant.
func validateTaskTools(task Task, limits Limits) error {
	parent := map[string]bool{}
	for _, name := range limits.ParentTools {
		parent[name] = true
	}
	for _, name := range task.Tools {
		// A TASK MAY NOW NAME A WRITE TOOL, and only by naming it.
		//
		// The read-only allow-list used to be the whole rule. It is now the
		// DEFAULT — a task that names nothing still inherits read-only tools and
		// nothing else (planToolGrant) — and a task that wants to change
		// something has to say which tool, by name. Writing is opted into per
		// task, never inherited.
		//
		// What still bounds it: the tool must be on the GRANTABLE allow-list —
		// read-only, or one of the few write tools a plan may name — and it must
		// be one the PARENT holds (below). A plan containing any such task
		// cannot run outside an isolated worktree (Plan.RequiresIsolation) or
		// without an approval that shows it (PermissionForArgs). Those two are
		// why this line could be relaxed at all.
		if !planReadOnlyTools[name] && !planWriteTools[name] {
			return fmt.Errorf("task %q requests tool %q, which a plan task may never hold; it may use %s, or name one of %s to write",
				task.ID, name, strings.Join(sortedReadOnlyTools(), ", "), strings.Join(PlanWriteToolNames(), ", "))
		}
		// UNCONDITIONAL. Guarding this on len(limits.ParentTools) > 0 is what
		// made the rule inert: neither production call site supplied a grant,
		// so the check never ran and a task could name any read-only tool the
		// parent did not hold.
		if !parent[name] {
			return fmt.Errorf("task %q requests tool %q, which this run does not hold; a task may narrow the parent's grant, never widen it",
				task.ID, name)
		}
	}
	return nil
}

// planTooLargeError names the ceiling AND how to move it.
//
// The old message was "plan has 24 tasks, which exceeds the limit of 20" — a
// number with no origin and no remedy, so the only way to act on it was to read
// the source. The ceiling is configurable now, and a bound the user cannot
// discover is a bound they will work around by splitting the plan instead.
func planTooLargeError(count int, limits Limits) error {
	source := strings.TrimSpace(limits.MaxTasksSource)
	if source == "" {
		return fmt.Errorf("plan has %d tasks, which exceeds the limit of %d", count, limits.MaxTasks)
	}
	return fmt.Errorf(
		"plan has %d tasks, which exceeds the limit of %d set by %s; raise it with \"profiles\": {\"planSize\": \"%s\"} in .zero/config.json, or split the plan",
		count, limits.MaxTasks, source, config.PlanSizeLarge)
}

func sortedReadOnlyTools() []string {
	names := make([]string, 0, len(planReadOnlyTools))
	for name := range planReadOnlyTools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// PlanWriteToolNames is the sorted set of MUTATING tools a plan task may hold,
// and only ever by naming one explicitly.
//
// An ALLOW-LIST, deliberately, and a short one. The alternative — "anything the
// parent holds that is not read-only" — would hand a plan task every future
// tool the moment it is registered, including ones nobody considered when this
// was written. Every deny-list in this repository has leaked.
//
// Kept narrow on purpose: editing files and running commands is what a
// write-capable plan is for. Network, browser and process-retention tools are
// not on it, and adding one is a decision, not a configuration.
func PlanWriteToolNames() []string {
	names := make([]string, 0, len(planWriteTools))
	for name := range planWriteTools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

var planWriteTools = map[string]bool{
	"write_file":   true,
	"edit_file":    true,
	"apply_patch":  true,
	"bash":         true,
	"exec_command": true,
}

// PlanGrantableToolNames is every tool a plan task may hold by any route: the
// read-only default plus the write tools that must be named. ONE list for the
// caller building a parent grant, so the grant and the validator cannot come to
// disagree about what is grantable.
func PlanGrantableToolNames() []string {
	names := append(PlanReadOnlyToolNames(), PlanWriteToolNames()...)
	sort.Strings(names)
	return names
}

// PlanReadOnlyToolNames is the sorted set of tools a plan task may ever hold.
//
// Exported so a caller building Limits.ParentTools intersects against the SAME
// list this package validates against, rather than maintaining its own copy —
// two duplicated lists drift (invariant 5). Nothing outside this set can be
// granted, so a caller need only consider these names.
func PlanReadOnlyToolNames() []string { return sortedReadOnlyTools() }

func planBudget(args map[string]any, limits Limits) (Budget, error) {
	raw, ok := args["budget"].(map[string]any)
	if !ok {
		return Budget{}, fmt.Errorf("plan requires a budget object with max_workers")
	}
	budget := Budget{
		MaxWorkers: planInt(raw, "max_workers"),
		MaxTokens:  planInt(raw, "max_tokens"),
	}
	if seconds := planInt(raw, "max_wall_seconds"); seconds > 0 {
		budget.MaxWall = time.Duration(seconds) * time.Second
	}
	if seconds := planInt(raw, "max_stall_seconds"); seconds > 0 {
		stall := time.Duration(seconds) * time.Second
		if stall < minStallTimeout {
			return Budget{}, fmt.Errorf(
				"budget.max_stall_seconds must be at least %d: below that the watchdog fires on ordinary think-time and becomes a random task-killer",
				int(minStallTimeout.Seconds()))
		}
		budget.MaxStall = stall
	}
	// max_retries needs PRESENCE, not a value: an explicit 0 means "do not retry
	// this plan" and an absent key means "use the default", and planInt cannot
	// tell them apart — it returns 0 for both. An unset value that reads as an
	// explicit one is invariant 2, the defect that made an empty tool grant
	// expand to the full read-only category.
	budget.MaxRetries = defaultPlanRetries
	if retries, set := planIntSet(raw, "max_retries"); set {
		if retries < 0 || retries > maxPlanRetries {
			return Budget{}, fmt.Errorf(
				"budget.max_retries must be between 0 and %d; a task that stalls repeatedly is a provider or network condition, not something more attempts will fix",
				maxPlanRetries)
		}
		budget.MaxRetries = retries
	}
	// MaxWorkers is a RANGE now, and still rejected rather than coerced outside
	// it. The rule it replaces — "must be exactly 1" — existed because the
	// executor was sequential and a caller that asked for 8 and silently got 1
	// would have been told nothing. That reasoning is unchanged; only the
	// executor changed, so the bound moved rather than dissolved.
	//
	// The ceiling is absolute and small. A plan is one tool call, and every task
	// it runs is a child process inheriting a 320-turn budget under this
	// posture; sixteen of those at once is already a great deal of machine and
	// a great deal of money. A plan asking for more is refused, not trimmed,
	// because a trimmed number is a number nobody can reason about afterwards.
	if budget.MaxWorkers < 1 || budget.MaxWorkers > maxPlanWorkers {
		return Budget{}, fmt.Errorf(
			"budget.max_workers must be between 1 and %d; %d was requested. "+
				"1 runs the plan sequentially, which is the right answer unless its tasks are genuinely independent",
			maxPlanWorkers, budget.MaxWorkers)
	}
	// max_tokens is OPTIONAL and unbounded by default.
	//
	// It was required, and capped at 200k. Both went, because the bound did not
	// work and got in the way of real work. It was checked only BETWEEN tasks:
	// a task was dispatched whenever any budget remained and then spent
	// whatever it spent, so a six-task chain asking for the 200k maximum
	// actually spent 469,555 — 2.3x over — and the last task was cut anyway.
	// A number that neither bounds spend nor lets a heavy plan finish is worse
	// than no number, because it reads like a guarantee.
	//
	// Spend is still METERED and reported (PlanReport.TokensUsed, the
	// plan_completed event, the panel), so what a plan cost is always visible.
	// A caller that wants a bound sets max_tokens and gets the same
	// dispatch-time behaviour as before.
	if budget.MaxTokens < 0 {
		return Budget{}, fmt.Errorf("budget.max_tokens must not be negative")
	}
	if limits.MaxTokens > 0 && budget.MaxTokens > limits.MaxTokens {
		return Budget{}, fmt.Errorf("budget.max_tokens %d exceeds the limit of %d for this run", budget.MaxTokens, limits.MaxTokens)
	}
	return budget, nil
}

func planTask(raw any, index int) (Task, error) {
	fields, ok := raw.(map[string]any)
	if !ok {
		return Task{}, fmt.Errorf("task at position %d is not an object", index)
	}
	task := Task{
		ID:        planString(fields, "id"),
		Prompt:    planString(fields, "prompt"),
		DependsOn: planStrings(fields, "depends_on"),
		Tools:     planStrings(fields, "tools"),
		Phase:     planString(fields, "phase"),
	}
	if task.ID == "" {
		return Task{}, fmt.Errorf("task at position %d has no id", index)
	}
	if !planIDPattern.MatchString(task.ID) {
		return Task{}, fmt.Errorf("task id %q must use only letters, digits, hyphen and underscore", task.ID)
	}
	if task.Prompt == "" {
		return Task{}, fmt.Errorf("task %q has no prompt", task.ID)
	}
	return task, nil
}

func planTaskList(args map[string]any) ([]any, error) {
	raw, ok := args["tasks"]
	if !ok {
		return nil, fmt.Errorf("plan requires a tasks array")
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("plan tasks must be an array")
	}
	return list, nil
}

func planString(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	value, _ := args[key].(string)
	return strings.TrimSpace(value)
}

func planStrings(args map[string]any, key string) []string {
	raw, ok := args[key].([]any)
	if !ok {
		return nil
	}
	out := []string{}
	for _, item := range raw {
		if text, ok := item.(string); ok {
			if trimmed := strings.TrimSpace(text); trimmed != "" {
				out = append(out, trimmed)
			}
		}
	}
	return out
}

// planInt accepts the float64 a JSON number decodes to, as well as an int.
func planInt(args map[string]any, key string) int {
	value, _ := planIntSet(args, key)
	return value
}

// planIntSet also reports whether the key was PRESENT, for the settings where
// an explicit 0 and an absent key mean different things. planInt is this
// function with the answer discarded, so the two can never decode differently.
func planIntSet(args map[string]any, key string) (int, bool) {
	switch value := args[key].(type) {
	case float64:
		return int(value), true
	case int:
		return value, true
	default:
		return 0, false
	}
}

// planBool reads a boolean argument. Absent, or any other type, is false — a
// flag that changes where a plan RUNS must be opted into explicitly, never
// inferred from a value that happened to be there.
func planBool(args map[string]any, key string) bool {
	value, _ := args[key].(bool)
	return value
}
