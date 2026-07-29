package specialist

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
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
	// MaxWorkers must be 1. Phase 2 is sequential, and a plan asking for more is
	// REJECTED rather than coerced — coercion would let a caller believe it got
	// concurrency, and would make the field meaningless for Phase 3.
	MaxWorkers int
	MaxTokens  int
	MaxWall    time.Duration
}

// Limits are the caller-supplied hard caps a plan must fit inside.
type Limits struct {
	// MaxTasks bounds plan size.
	MaxTasks int
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
	"update_plan":        true,
	"lsp_navigate":       true,
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
		return Plan{}, fmt.Errorf("plan has %d tasks, which exceeds the limit of %d", len(rawTasks), limits.MaxTasks)
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
		// READ-ONLY, by allow-list. Phase 2 tasks cannot write, edit or run
		// shell: granting that is an authority widening that needs its own
		// decision, not a side effect of shipping plan capture.
		if !planReadOnlyTools[name] {
			return fmt.Errorf("task %q requests tool %q; plan tasks are read-only in this phase and may only use: %s",
				task.ID, name, strings.Join(sortedReadOnlyTools(), ", "))
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

func sortedReadOnlyTools() []string {
	names := make([]string, 0, len(planReadOnlyTools))
	for name := range planReadOnlyTools {
		names = append(names, name)
	}
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
	// MaxWorkers must be exactly 1. Rejecting rather than coercing keeps the
	// field meaningful: a caller that asked for 8 and silently got 1 would have
	// been told nothing, and Phase 3 would inherit a field nobody trusts.
	if budget.MaxWorkers != 1 {
		return Budget{}, fmt.Errorf(
			"budget.max_workers must be 1: this phase executes plan tasks sequentially, and a plan asking for %d is rejected rather than quietly run with one worker",
			budget.MaxWorkers)
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
	switch value := args[key].(type) {
	case float64:
		return int(value)
	case int:
		return value
	default:
		return 0
	}
}
