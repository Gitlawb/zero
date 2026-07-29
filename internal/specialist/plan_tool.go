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
	// Limits overrides the default caps; nil uses them.
	Limits *Limits
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
		"Tasks run sequentially; declare dependencies with depends_on so the plan records which work was independent."
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
			"budget": {
				Type:        "object",
				Description: "Required. max_workers must be 1 (this phase executes sequentially). max_tokens and max_wall_seconds are optional bounds; omit them to run unbounded — spend is reported either way. max_stall_seconds bounds how long ONE task may emit nothing (default 180); it resets on every event, so a slow-but-working task is never stopped.",
			},
		},
		Required:             []string{"tasks", "budget"},
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

// Limits supplies the hard caps a plan must fit inside. nil means the defaults.
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
	if tool.Limits != nil {
		limits = *tool.Limits
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
	// ParsePlan validates as part of parsing; there is no other constructor, so
	// this call cannot be bypassed by any argument shape.
	plan, err := ParsePlan(args, tool.limits(options))
	if err != nil {
		return tools.Result{Status: tools.StatusError, Output: "Error: " + err.Error()}
	}
	if tool.RunTask == nil {
		return tools.Result{Status: tools.StatusError, Output: "Error: orchestrate has no task runner wired."}
	}

	recordPlanAdmitted(tool.Recorder, plan)
	report := ExecutePlan(ctx, plan, tool.ParentTools, tool.runnerForCall(options), tool.Recorder)
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
