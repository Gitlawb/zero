package specialist

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/tools"
)

// OrchestrateToolName is the single spelling of the plan-capture tool.
const OrchestrateToolName = "orchestrate"

// OrchestrateTool accepts a structured PLAN as tool arguments, validates it,
// records it as session events, and executes it SEQUENTIALLY through the same
// specialist path a Task call uses.
//
// It is DEFERRED unless the zeromaxing posture is active. That is the whole
// mechanism behind Phase 2's overriding constraint: with the posture off the
// tool is registered but never advertised, so the advertised tool set, the
// tool-definition bytes and the assembled prompt are byte-identical to a build
// without the feature (proved by
// TestPostureOffPrefixUnchangedByRegisteringTheTool in internal/agent).
//
// Phase 2 is read-only and sequential by construction, not by convention: plan
// tasks may not request mutating tools, and Budget.MaxWorkers must be 1. Both
// are rejected at parse time rather than coerced, so the fields stay meaningful
// for a later phase instead of quietly lying.
type OrchestrateTool struct {
	// PostureActive reports whether the zeromaxing posture is on. nil means off
	// — a caller that never wires it gets today's behaviour, which is the
	// fail-safe direction for a tool that spends budget.
	PostureActive func() bool
	// RunTask executes one plan task. nil makes the tool refuse rather than
	// pretend it ran a plan.
	RunTask PlanRunner
	// Recorder receives plan lifecycle events; nil disables recording without
	// affecting execution.
	Recorder PlanRecorder
	// ParentTools is this run's tool grant. A task may narrow it, never widen.
	ParentTools []string
	// Depth is the nesting depth of the run holding this tool, used for the
	// admission-time headroom check.
	Depth int
	// Size is the configured plan-size tier. The zero value is the default tier,
	// so a caller that never wires it gets the same ceiling as before.
	Size config.PlanSize
	// Launch runs a plan in the BACKGROUND, and nil means background plans are
	// unavailable in this run — which is the honest default.
	//
	// THE SEAM IS A LAUNCHER, not a context, and that is deliberate. A tool that
	// held a context would have to hold one that outlives the tool call, and the
	// only thing that legitimately knows how long a background plan may live is
	// the surface that owns the session. So the surface supplies the launcher,
	// keeps the context, and can drain or cancel on exit; the tool only asks to
	// be run. It also makes headless exec's refusal fall out for free: that
	// process exits when the turn ends, so it supplies no launcher and a
	// background plan there is refused rather than silently orphaned.
	//
	// It reports false when it will not run the work, so the tool can say so
	// instead of returning a run id for a plan nobody started.
	Launch func(run func(ctx context.Context)) bool
	// Isolate prepares a worktree for a plan that may write. nil means this run
	// cannot isolate, and a plan requiring it is REFUSED rather than run in the
	// parent's tree — see resolvePlanWorkspace.
	Isolate PlanIsolator
	// Plans locates saved plans. Both directories empty means saved plans are
	// simply unavailable — the tool refuses a `saved` reference with a reason
	// rather than searching nothing and reporting "not found", which would read
	// as "you never saved it".
	Plans PlanPaths
}

const (
	// defaultPlanMaxTokens is 0: NO ceiling on what a plan may request.
	//
	// It was 200_000, and a six-task chain asking for exactly that spent
	// 469,555 — the check ran only between tasks, so the last task dispatched
	// overshot without limit and the plan was cut short anyway. Capping the
	// REQUEST while not bounding the SPEND is the worst of both: heavy work
	// cannot finish and the number means nothing. Spend is metered and
	// reported either way; a caller that wants a bound still sets max_tokens.
	defaultPlanMaxTokens = 0
)

func (tool *OrchestrateTool) Name() string { return OrchestrateToolName }

func (tool *OrchestrateTool) Description() string {
	return "Execute a structured plan of read-only sub-agent tasks in dependency order. " +
		"Tasks run sequentially; declare dependencies with depends_on so the plan records which work was independent. " +
		// The either/or the schema cannot express, and the shape worth
		// encouraging: a plan is only worth more than reading the code yourself
		// when its tasks are genuinely independent.
		"Supply EITHER tasks and budget, OR saved with the name of a stored plan — never neither. " +
		"Use it when a question splits into parts that can be answered independently and then combined; " +
		"a single lookup is faster done directly."
}

// Deferred hides the tool unless the posture is active — as a SECOND layer.
//
// It is not the primary mechanism, and the brief's original design assumed it
// was. Deferral only hides anything when it is ACTIVE, which needs a configured
// threshold, enough eligible tools AND a runnable tool_search loader; when it is
// inactive — an ordinary configuration — every visible tool is exposed eagerly.
// An unsafe-mode session with deferral off would therefore have advertised this
// tool with the posture off, breaking the additivity constraint. Safety()
// returning PermissionDeny is what actually enforces it, because ToolAdvertised
// short-circuits on Deny in every permission mode and runs BEFORE the deferral
// machinery. This stays as defence in depth and is asserted, not assumed.
func (tool *OrchestrateTool) Deferred() bool {
	return !tool.postureActive()
}

// DeferralEligible keeps this tool counting toward the DeferThreshold even when
// it un-defers, so turning the posture on cannot deactivate deferral for other
// tools and force-expose them.
func (tool *OrchestrateTool) DeferralEligible() bool { return true }

func (tool *OrchestrateTool) postureActive() bool {
	return tool != nil && tool.PostureActive != nil && tool.PostureActive()
}

// StreamsChildProgress declares that a plan's tasks are child agent runs whose
// stream-json events should reach the parent's UI, exactly as a Task
// sub-agent's do. Declaring it is what un-gates the progress path — the loop
// used to key on the tool name, so this tool ran invisibly.
func (tool *OrchestrateTool) StreamsChildProgress() bool { return true }

func (tool *OrchestrateTool) Parameters() tools.Schema {
	return tools.Schema{
		Type: "object",
		Properties: map[string]tools.PropertySchema{
			"name":        {Type: "string", Description: "Short label for the plan."},
			"description": {Type: "string", Description: "What the plan is for."},
			"tasks": {
				Type:        "array",
				Description: "The plan's tasks. Each has an id, a prompt, optional depends_on ids, an optional read-only tool subset, and an optional phase label.",
			},
			"saved": {
				Type: "string",
				Description: "Run a plan saved earlier, by name, instead of supplying tasks. " +
					"Mutually exclusive with tasks/budget/name/description: a saved plan runs as it was saved.",
			},
			"background": {
				Type: "boolean",
				Description: "Run the plan in the background and report when it finishes, instead of holding this turn. " +
					"Only available in the interactive TUI; a headless run exits when the turn ends, so it refuses this.",
			},
			"budget": {
				Type:        "object",
				Description: "Required. max_workers must be 1 (this phase executes sequentially). max_tokens and max_wall_seconds are optional bounds; omit them to run unbounded — spend is reported either way. max_stall_seconds bounds how long ONE task may emit nothing (default 180); it resets on every event, so a slow-but-working task is never stopped. max_retries (0-3, default 1) is how many extra attempts a STALLED task gets; a task that failed with a real error is never retried.",
			},
		},
		// EMPTY, and the either/or lives in the DESCRIPTION instead.
		//
		// `saved` supplies tasks and budget, so neither is unconditionally
		// required any more — but dropping them from Required emits no
		// "required" key at all, and the model is then handed a tool whose every
		// argument looks optional. tools.Schema has no oneOf, so the constraint
		// that is actually true — "tasks and budget, or saved, never neither" —
		// cannot be spelled here. It is spelled in Description, which is the
		// thing a model reads for intent, and enforced in ParsePlan, which is
		// the only place that can see the resolved arguments.
		Required:             []string{},
		AdditionalProperties: false,
	}
}

func (tool *OrchestrateTool) Safety() tools.Safety {
	// PermissionDeny when the posture is off. This — not Deferred() — is what
	// actually enforces the additivity constraint: ToolVisible consults
	// ToolAdvertised, which short-circuits on PermissionDeny for EVERY
	// permission mode, and it runs BEFORE the deferral machinery. Deferred()
	// alone is insufficient because deferral only activates when a usable
	// tool_search loader is registered and the eligible count clears the
	// threshold; when it is inactive every visible tool is exposed eagerly, so
	// an unsafe-mode session with deferral off would have advertised this tool
	// with the posture off. Deferred() below is now belt-and-braces.
	permission := tools.PermissionDeny
	if tool.postureActive() {
		// PermissionAllow, not PermissionPrompt, and this is deliberate.
		//
		// WHY NOT PROMPT: the approval surface renders "permission: orchestrate
		// prompt" plus this static Reason and nothing else. PermissionRequest
		// carries an Args map, but no renderer reads it — verified across
		// permission_prompt.go, transcript.go and rendering.go. So the user
		// would be asked to approve a plan without being shown its task count,
		// its prompts, its budget or its graph. A dialog that cannot show what
		// it is approving is not a gate; it trains click-through, which is
		// worse than no dialog because it also erodes the prompts that DO carry
		// information.
		//
		// WHAT BOUNDS IT INSTEAD, all enforced rather than advisory:
		//   - tasks are READ-ONLY, rejected at validation (validateTaskTools)
		//     and intersected again at dispatch (planToolGrant)
		//   - a budget is REQUIRED and enforced at dispatch, not just validated
		//   - the posture itself was explicit user consent: this tool does not
		//     exist until someone types /effort zeromaxing
		//
		// PHASE 3 MUST REVISIT THIS. The moment plan tasks can WRITE, an
		// approval gate becomes necessary — and it needs a real renderer that
		// shows the plan first. Inheriting Allow without that renderer would be
		// inheriting this reasoning without its precondition.
		permission = tools.PermissionAllow
	}
	return tools.Safety{
		SideEffect: tools.SideEffectShell,
		Permission: permission,
		Reason:     "Runs a plan of read-only specialist sub-agents under the parent's sandbox and tool grant.",
		// Irrelevant while Permission is Allow (auto advertises Allow tools
		// anyway) and equally irrelevant while it is Deny (ToolAdvertised
		// short-circuits on Deny before reading this). Left false so the field
		// never becomes the thing holding the gate open.
		AdvertiseInAuto: false,
	}
}

// PermanentlyDenied reports that no arguments can make this tool callable while
// the posture is off. The posture is a session state, not a parameter, and
// ArgsPermissioner cannot express that — see tools.PermanentDenier.
func (tool *OrchestrateTool) PermanentlyDenied() bool { return !tool.postureActive() }

// PermissionForArgs is what makes a WRITE-CAPABLE plan ask, and a read-only one
// not.
//
// Safety() cannot decide this: it sees no arguments, so it can only describe the
// tool, and "orchestrate may write" is a property of the PLAN. A read-only plan
// keeps PermissionAllow — prompting for it would add friction with no safety to
// show for it, which is the click-through argument Safety() records. A plan that
// can write is a different decision and gets a different answer.
//
// The card that answers it can now show the plan (the permission detail
// renderer) and the work lands in a worktree of the plan's own (the isolator).
// Prompting before either existed would have been asking a user to approve
// something the screen could not describe.
func (tool *OrchestrateTool) PermissionForArgs(args map[string]any) tools.Permission {
	if !tool.postureActive() {
		return tools.PermissionDeny
	}
	if tool.argsCanWrite(args) {
		return tools.PermissionPrompt
	}
	return tools.PermissionAllow
}

// argsCanWrite reports whether the arguments describe a plan that could change
// something.
//
// It reads the ARGUMENTS rather than a parsed Plan because the permission
// decision happens before parsing, and it errs toward PROMPTING: a saved plan
// whose tasks live on disk, or arguments this cannot read, are treated as
// possibly-writing. A wrong guess in that direction costs one prompt; the other
// direction runs write tasks without asking.
func (tool *OrchestrateTool) argsCanWrite(args map[string]any) bool {
	if planString(args, "saved") != "" {
		// The tasks are in a file this has not opened. Ask.
		return true
	}
	rawTasks, ok := args["tasks"].([]any)
	if !ok {
		return false
	}
	for _, raw := range rawTasks {
		task, ok := raw.(map[string]any)
		if !ok {
			// Unreadable entry: ask rather than assume it is harmless.
			return true
		}
		for _, name := range planStrings(task, "tools") {
			if !planReadOnlyTools[name] {
				return true
			}
		}
	}
	return false
}

func (tool *OrchestrateTool) Run(ctx context.Context, args map[string]any) tools.Result {
	return tool.RunWithOptions(ctx, args, tools.RunOptions{})
}

// runnerForCall attaches everything that belongs to THIS tool call — the
// progress callback and the parent's identity — to every task request.
//
// PER CALL, not captured at construction: RunTask is built once at
// registration and holds only run-invariant state, while the progress callback
// belongs to one tool call. Capturing it in NewPlanRunner is precisely the
// mistake the runner's own doc comment warns about.
//
// The callback is shared by every task rather than keyed per task, because the
// loop's callback carries only the parent's tool-call id and has no room for a
// sub-key. That is sound HERE and only here: MaxWorkers is validated to be 1,
// so exactly one task is in flight at any moment and the consumer can attribute
// events to the task it last saw dispatched. Stage 2d must revisit this the
// moment two tasks can run at once — see the note on the TUI plan recorder.
func (tool *OrchestrateTool) runnerForCall(options tools.RunOptions) PlanRunner {
	run := tool.RunTask
	if run == nil {
		return nil
	}
	return func(ctx context.Context, req PlanTaskRequest) (TaskResult, error) {
		req.Progress = options.Progress
		// The parent's identity, read from the same RunOptions fields the Task
		// tool reads (task_tool.go). A plan task inherits the model its parent
		// is running on; without this it fell back to the child's own config,
		// so switching model with /model or --model left plan tasks running
		// somewhere else entirely.
		req.ParentSessionID = options.SessionID
		req.ParentModel = options.Model
		req.ParentReasoningEffort = options.ReasoningEffort
		return run(ctx, req)
	}
}

// resolveSavedPlan swaps a `saved` reference for the stored plan's arguments.
//
// Anything the caller supplied ALONGSIDE `saved` is refused rather than merged.
// A half-overridden plan is not the plan that was saved, and silently letting
// one field through would mean "run the sweep plan" ran something else — the
// name would still be right in the transcript.
func (tool *OrchestrateTool) resolveSavedPlan(args map[string]any) (map[string]any, error) {
	name := planString(args, "saved")
	if name == "" {
		return args, nil
	}
	for _, field := range []string{"tasks", "budget", "name", "description"} {
		if _, present := args[field]; present {
			return nil, fmt.Errorf(
				"a saved plan is run as it was saved: remove %q, or supply the plan inline instead of naming one", field)
		}
	}
	if tool.Plans.ProjectDir == "" && tool.Plans.UserDir == "" {
		return nil, fmt.Errorf("saved plans are not available in this run")
	}
	stored, err := FindSavedPlan(tool.Plans, name)
	if err != nil {
		return nil, err
	}
	return stored.Args, nil
}

// limits supplies the hard caps a plan must fit inside, DERIVED — there is no
// override field.
//
// There was one: a `Limits *Limits` that nothing ever set, in production or in
// a test, so `if tool.Limits != nil` could not be true. It was found sweeping
// for siblings of the OrchestrateAvailable defect and it is the same family —
// a knob a reader would trust, sitting next to Size, which is the knob that
// actually works. Deleted rather than wired: nothing needs it, because every
// value it could have overridden is already derived from something real.
//
// The task ceiling comes from the CONFIGURED TIER rather than a constant here.
// It was a hard-coded 20 with no way to move it: too many for a metered
// provider, too few for a real sweep, and discoverable only by reading this
// file. PlanSize.MaxTasks resolves an unset or unrecognised tier to the default,
// so the zero value is exactly the old ceiling.
func (tool *OrchestrateTool) limits(options tools.RunOptions) Limits {
	size := tool.Size
	if !size.Valid() {
		size = config.DefaultPlanSize
	}
	limits := Limits{
		MaxTasks:       size.MaxTasks(),
		MaxTasksSource: fmt.Sprintf("the %q plan size", size),
		MaxTokens:      defaultPlanMaxTokens,
		CurrentDepth:   tool.Depth,
		ParentTools:    tool.ParentTools,
	}
	return limits
}

func (tool *OrchestrateTool) RunWithOptions(ctx context.Context, args map[string]any, options tools.RunOptions) tools.Result {
	// The posture gate, again at the point of USE. Safety() already hides the
	// tool, but a model can call a tool it was never advertised — the registry
	// dispatches by name — so refusing here is what makes "only under the
	// posture" a rule rather than a display convention.
	if !tool.postureActive() {
		return tools.Result{
			Status: tools.StatusError,
			Output: "Error: orchestrate is only available under the zeromaxing posture. Turn it on with /effort zeromaxing.",
		}
	}
	// A SAVED plan is loaded into the same argument shape and then validated by
	// the same constructor. It is not a second way to run a plan: by the time
	// ParsePlan sees it, a stored plan and a model-supplied one are
	// indistinguishable, so the tier, the depth check, the read-only rule and
	// the parent-grant intersection all apply to it unchanged.
	args, err := tool.resolveSavedPlan(args)
	if err != nil {
		return tools.Result{Status: tools.StatusError, Output: "Error: " + err.Error()}
	}
	// ParsePlan validates as part of parsing; there is no other constructor, so
	// this call cannot be bypassed by any argument shape.
	plan, err := ParsePlan(args, tool.limits(options))
	if err != nil {
		return tools.Result{Status: tools.StatusError, Output: "Error: " + err.Error()}
	}
	if tool.RunTask == nil {
		return tools.Result{Status: tools.StatusError, Output: "Error: orchestrate has no task runner wired."}
	}

	// BACKGROUND, when asked for and when this surface can carry one.
	//
	// The runner is built HERE, on the tool-call goroutine, because it captures
	// the call's RunOptions — the parent's model, effort and session id. Only
	// the CONTEXT comes from the launcher; capturing a per-call context in a
	// goroutine is the prototype's defect, and capturing per-call VALUES is
	// exactly what must happen.
	if planBool(args, "background") {
		if tool.Launch == nil {
			return tools.Result{
				Status: tools.StatusError,
				Output: "Error: background plans are not available in this run — a headless run exits when the turn ends, " +
					"so a plan launched into the background could never report. Run it in the foreground, or use the interactive TUI.",
			}
		}
		// THE SAME WORKSPACE RULE. Resolved here rather than inside the
		// goroutine so a plan that cannot be isolated is refused NOW, with a
		// reason the model reads, instead of failing invisibly on a later turn.
		// Applying the rule to only one of the two dispatch paths is the defect
		// this feature has produced three times.
		workspace, err := resolvePlanWorkspace(ctx, plan, tool.Isolate)
		if err != nil {
			return tools.Result{Status: tools.StatusError, Output: "Error: " + err.Error()}
		}
		run := tool.runnerForCall(options)
		parentTools := tool.ParentTools
		recorder := tool.Recorder
		launched := tool.Launch(func(backgroundCtx context.Context) {
			defer workspace.Release()
			recordPlanAdmitted(recorder, plan)
			report := ExecutePlanIn(backgroundCtx, plan, workspace, parentTools, run, recorder)
			recordPlanCompleted(recorder, plan, report)
		})
		if !launched {
			workspace.Release()
		}
		if !launched {
			return tools.Result{
				Status: tools.StatusError,
				Output: "Error: the plan was not started — this session is shutting down, or a background plan is already running.",
			}
		}
		// NOT StatusOK-with-a-summary: there is no result yet, and reporting one
		// would be reporting work that has not happened.
		return tools.Result{
			Status: tools.StatusOK,
			Output: fmt.Sprintf(
				"Plan %q started in the background with %d tasks. It is NOT finished — its result will arrive on a later turn. "+
					"Carry on with other work; do not wait for it and do not report it as done.",
				plan.Name(), plan.TaskCount()),
			Meta: map[string]string{"plan_status": "background"},
		}
	}

	// WHERE IT RUNS. A read-only plan runs where the parent runs; one that can
	// write gets a tree of its own or does not run at all. Resolved BEFORE the
	// admission is recorded, so a refused plan leaves no record of having
	// started.
	workspace, err := resolvePlanWorkspace(ctx, plan, tool.Isolate)
	if err != nil {
		return tools.Result{Status: tools.StatusError, Output: "Error: " + err.Error()}
	}
	defer workspace.Release()

	recordPlanAdmitted(tool.Recorder, plan)
	report := ExecutePlanIn(ctx, plan, workspace, tool.ParentTools, tool.runnerForCall(options), tool.Recorder)
	recordPlanCompleted(tool.Recorder, plan, report)

	result := tools.Result{
		Status: tools.StatusOK,
		Output: report.Summary(),
		Meta: map[string]string{
			"plan_status": string(report.Status),
			"max_speedup": strconv.FormatFloat(report.MaxSpeedup, 'f', 2, 64),
		},
	}
	// A plan that did not fully succeed must NOT report OK. This repo has
	// repeatedly reported failure as success; in a plan, nineteen of twenty
	// tasks failing surfacing as a clean result is the same defect at scale.
	if report.Status != PlanCompleted {
		result.Status = tools.StatusError
	}
	return result
}
