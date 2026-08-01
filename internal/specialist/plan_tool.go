package specialist

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/streamjson"
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
	// DiscoverModels reports what the active provider can serve, for
	// auto_assign. nil means auto-assignment is unavailable and a plan asking
	// for it is told so rather than silently running without it.
	DiscoverModels ModelDiscoverer
	// ProbeModel asks whether the provider will actually RUN a model, as opposed
	// to merely listing it. nil skips proving entirely, which is the behaviour
	// every plan had before this existed.
	ProbeModel ModelProber
	// probes remembers verdicts for the life of the process, so the cost is one
	// trivial request per model per session rather than per plan.
	probes probeCache
	// ModelPrefs is what the user has said about which models plans may use:
	// per-role pins and an exclusion list. Empty leaves the automatic choice
	// alone, which is the behaviour for anyone who never configures it.
	ModelPrefs ModelPreferences
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
	return "Execute a structured plan of sub-agent tasks in dependency order. " +
		"Tasks inherit the parent's sandbox and tool grant; write tools are grantable to a task by name. " +
		"Independent tasks run in parallel up to budget.max_workers; declare dependencies with depends_on and a task never starts before what it waits on has finished. " +
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
				Type: "array",
				Description: "The plan's tasks. Each has an id, a prompt, optional depends_on ids, an optional read-only tool subset, and an optional phase label. " +
					"A task may also name a model to run on — any model this provider serves, by id or alias; omit it to inherit " +
					"this session's model. Use it to spend where it matters: a cheaper model for mechanical scanning, a stronger " +
					"one for the tasks that judge or decide. A name the provider cannot serve fails when the task runs.\n\n" +
					// WRITING THE TASKS IS THE LEVER, and it sits upstream of everything
					// else this tool does. Routing cannot rescue a badly split plan and
					// verification cannot rescue a task that never asked a real question.
					// A measured run produced ten tasks whose prompts all opened with the
					// same wrapper — ten paraphrases of one job rather than ten jobs.
					"Writing good tasks matters more than how many you write:\n" +
					"- A task should be a whole question someone could answer alone and be judged right or wrong about. " +
					"\"Trace how X is computed and quote file:line for each step\" is a task; \"look at X\" is not.\n" +
					"- Split by SUBJECT, never by phrasing. Two tasks that would read the same files are one task.\n" +
					"- Say what the task must return. A task whose output cannot be checked cannot be verified by anything downstream.\n" +
					"- Use depends_on for real dependencies only. A dependent receives its dependencies' results in its prompt, " +
					"so chain a task that must build on findings — and leave independent work independent so it runs in parallel.\n" +
					"- Do not split one modest job into pieces to look thorough. Fewer, larger, genuinely independent tasks beat many small ones: " +
					"each task pays for its own context.\n" +
					"- Be honest about what a task can do. A task that reads and reports is not the same as one that decides; " +
					"give the deciding work to a task that says it is deciding, and name a stronger model for it.",
			},
			"saved": {
				Type: "string",
				Description: "Run a plan saved earlier, by name, instead of supplying tasks. " +
					"Mutually exclusive with tasks/budget/name/description: a saved plan runs as it was saved.",
			},
			"auto_assign": {
				Type: "boolean",
				Description: "Pick a model per task automatically from what this provider serves: the cheapest tool-calling model " +
					"for tasks that scan or read, a mid-priced one for tasks that change code, and the strongest for tasks that " +
					"judge or verify. Off by default. A task that names its own model is never overridden, and the result reports " +
					"what was chosen.",
			},
			"background": {
				Type: "boolean",
				Description: "Run the plan in the background and report when it finishes, instead of holding this turn. " +
					"Only available in the interactive TUI; a headless run exits when the turn ends, so it refuses this.",
			},
			"budget": {
				Type:        "object",
				Description: "Required. max_workers (1-16) is how many tasks may run at once; 1 is sequential and is the right answer unless the tasks are genuinely independent. The machine's own capacity may be lower and the report says which number applied. max_tokens and max_wall_seconds are optional bounds; omit them to run unbounded — spend is reported either way. max_stall_seconds bounds how long ONE task may emit nothing (default 180); it resets on every event, so a slow-but-working task is never stopped. max_retries (0-3, default 1) is how many extra attempts a STALLED task gets; a task that failed with a real error is never retried. max_tokens_per_task bounds what ONE task may spend; without it a single task can spend many times the whole plan's budget. Sizing, from measured runs: a task that traces or audits a large repo cost 510k-1,017k tokens; one reasoning over what its dependencies already found costs far less. A cap BELOW what a task needs saves nothing — a plan capping tasks at 200k lost all six of them between 213k and 259k and still spent 1,437,049 tokens, finishing none. A capped task is told its budget and asked to write a partial answer before reaching it, so tight is survivable and too-tight is not. Budget from task count x expected depth, or omit max_tokens to run unbounded within this run's own ceiling. A budget too small for its task count is refused at admission rather than half-spent: when the plan runs out, tasks in flight are cancelled and the rest never run, so the answer comes back incomplete.",
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
		// Not "read-only": this Reason is rendered on the approval card, and
		// PermissionForArgs only prompts when argsCanWrite is true — so every
		// card a user actually sees is asking about a plan that CAN write.
		// Describing it as read-only there was precisely backwards.
		Reason: "Runs a plan of specialist sub-agents under the parent's sandbox and tool grant; tasks may hold write tools granted by name.",
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
// RefusesPersistentPermission reports that an approval for this tool must never
// be remembered.
//
// A read-only plan does not prompt at all, so EVERY prompt this tool produces is
// for a plan that can write — and a plan that can write can be given bash, for
// which the permission layer already refuses to persist an approval. Letting
// "always allow orchestrate" be remembered would be a strictly broader standing
// grant than the one that refusal exists to prevent, and it would disable the
// approval gate permanently with a single keystroke.
//
// The plan is still approvable for THIS call and for the session; only the
// permanent form is withheld, which is exactly how bash behaves.
func (tool *OrchestrateTool) RefusesPersistentPermission() bool { return true }

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
// sub-key. THE CONSEQUENCE, now that plans can be concurrent: with more than one
// worker a consumer CANNOT attribute an event to a task, and the TUI stops
// trying rather than attributing every child to whichever task started last.
// Threading the child's identity through the loop's callback is what would fix
// it properly. Previously this read: MaxWorkers is validated to be 1,
// so exactly one task is in flight at any moment and the consumer can attribute
// events to the task it last saw dispatched — which is no longer true.
// moment two tasks can run at once — see the note on the TUI plan recorder.
func (tool *OrchestrateTool) runnerForCall(options tools.RunOptions) PlanRunner {
	run := tool.RunTask
	if run == nil {
		return nil
	}
	recorder := tool.Recorder
	return func(ctx context.Context, req PlanTaskRequest) (TaskResult, error) {
		// PER TASK, not per call. The loop's own callback carries the parent's
		// tool-call id and nothing else, so every task's events look alike to a
		// consumer; routing them through the recorder with the task id attached
		// is what lets a display tell them apart. The recorder falls back to
		// doing nothing when a surface has no live UI, which is the headless
		// case.
		taskID := req.Task.ID
		callerProgress := options.Progress
		req.Progress = func(event streamjson.Event) {
			// BOTH, and they are not alternatives. The recorder gets the event
			// WITH the task id so a display can route it; the caller's own
			// callback still fires because that is the contract a plan task's
			// child streams under — the same one a Task sub-agent's child does,
			// and restoring it was a fix in its own right. Sending only to the
			// recorder would have quietly undone that for every caller without
			// one, which is the headless case.
			planTaskProgress(recorder, taskID, event)
			if callerProgress != nil {
				callerProgress(event)
			}
		}
		// The parent's identity, read from the same RunOptions fields the Task
		// tool reads (task_tool.go). A plan task inherits the model its parent
		// is running on; without this it fell back to the child's own config,
		// so switching model with /model or --model left plan tasks running
		// somewhere else entirely.
		req.ParentSessionID = options.SessionID
		req.ParentModel = options.Model
		req.ParentReasoningEffort = options.ReasoningEffort
		req.ParentToolCallID = options.ToolCallID
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
	// AUTO-ASSIGNMENT RUNS BEFORE THE CONSTRUCTOR, on the arguments.
	//
	// Not inside ParsePlan: that has six call sites, four of them TUI verb and
	// render paths through savedPlanLimits(), and a provider probe on those would
	// block the interface, make parsing non-deterministic, and give a saved plan
	// different models every time it was re-admitted. Here it happens once, on
	// the dispatch path, where a network call is already expected.
	//
	// Working on the args rather than the parsed plan is what makes an assigned
	// model indistinguishable from a hand-written one: same validation, same
	// Args() round trip into a saved plan, same resume.
	// VALIDATE BEFORE SPENDING ANYTHING. Auto-assignment lists the provider's
	// models and, when routing is on, spends a call on a frontier model before a
	// single task runs — and it was doing that for plans that were then rejected
	// outright: a duplicate task id, a cycle, one task over the size tier. A
	// model that proposes an oversized plan twice paid for routing twice and got
	// nothing either time.
	//
	// ParsePlan is pure — no I/O, no network — so running it first costs
	// microseconds against a provider round trip. Assignment can only ADD a model
	// field to tasks that already parsed, so nothing it does can rescue a plan
	// that failed here, and the real parse below still has the final word.
	if _, err := ParsePlan(args, tool.limits(options)); err != nil {
		return tools.Result{Status: tools.StatusError, Output: "Error: " + err.Error()}
	}
	assignNotes, autoErr := tool.autoAssignModels(ctx, args, options)
	if autoErr != nil {
		return tools.Result{Status: tools.StatusError, Output: "Error: " + autoErr.Error()}
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
	// ONE PLAN AT A TIME, on this path too.
	//
	// The surface that displays a plan can hold exactly one, and the card table
	// pairing tasks with rows is keyed by task id — unique within a plan, not
	// between two. The TUI already refused a second plan on the path a USER
	// drives; this is the path the MODEL drives, and it is the reachable one:
	// a background plan returns immediately by design, so the very next tool
	// call lands while it is still running.
	//
	// Checked AFTER parsing, so an invalid plan is still reported as invalid
	// rather than blamed on the plan already running — and BEFORE admission, so
	// a refused plan leaves no record of having started.
	if running, busy := runningPlanOn(tool.Recorder); busy {
		return tools.Result{
			Status: tools.StatusError,
			Output: fmt.Sprintf(
				"Error: plan %q is still running, and a session shows one plan at a time. "+
					"Wait for it to finish — its result arrives on a later turn if it is a background plan — "+
					"or stop it with /plans stop, then run this one.", running),
		}
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
		Output: report.Summary() + planWorkspaceNote(workspace) + autoAssignSummary(assignNotes),
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

// autoAssignModels fills in a model per task when the plan asked for it,
// returning one note per assignment for the result.
//
// OFF UNLESS ASKED. planBool reports false for a missing key, and that is the
// wanted default: turning it on by default would change which model every
// existing plan runs on — and what it costs — without anyone choosing that.
//
// The two unavailable cases are told apart deliberately. A plan that asked for
// auto-assignment on a run that CANNOT do it is refused, because silently
// running without it is the thing the user asked not to happen. A provider that
// answers but offers nothing usable is NOT an error: every task simply inherits,
// which is exactly the behaviour before this existed.
func (tool *OrchestrateTool) autoAssignModels(ctx context.Context, args map[string]any, options tools.RunOptions) ([]string, error) {
	// THE ARGUMENT WINS IN BOTH DIRECTIONS. A plan that supplies auto_assign is
	// believed — true or false — and only a plan that says nothing falls back to
	// what the user configured. Without the presence check a configured default
	// of true could never be turned off for a single plan, because an absent
	// argument and an explicit false look identical.
	requested, supplied := planBoolSet(args, "auto_assign")
	if !supplied {
		requested = tool.ModelPrefs.AutoAssign
	}
	if !requested {
		return nil, nil
	}
	// ASKED FOR vs CONFIGURED, and they must fail differently.
	//
	// A plan that SAYS auto_assign wants it: running silently without it is the
	// thing that request exists to prevent, so an unavailable run is refused.
	// A CONFIGURED default is a standing preference, not a demand — refusing
	// every plan because a models endpoint blinked would make one setting break
	// all planning, offline or behind a proxy. There it degrades: nothing is
	// assigned, the reason is reported, and the plan runs on the session's model
	// exactly as it did before the setting existed.
	if tool.DiscoverModels == nil {
		if !supplied {
			return []string{"none — this run cannot list the provider's models, so every task kept this session's model"}, nil
		}
		return nil, fmt.Errorf(
			"auto_assign is not available in this run — it needs a provider that can list its models. " +
				"Name a model per task instead, or omit auto_assign to inherit this session's model")
	}
	raw, ok := args["tasks"].([]any)
	if !ok {
		// A saved plan or a malformed one: leave it entirely to ParsePlan, which
		// owns every message about task shape.
		return nil, nil
	}
	models, err := tool.DiscoverModels(ctx)
	if err != nil {
		if !supplied {
			return []string{"none — could not list the provider's models (" + err.Error() +
				"), so every task kept this session's model"}, nil
		}
		return nil, fmt.Errorf("auto_assign could not list the provider's models: %w", err)
	}
	// PROVED BEFORE ANYTHING IS BUILT FROM IT. Tiers, the served set and the
	// router's candidate list are all derived from this slice, so a model that
	// cannot run has to be gone before any of them exist — filtering later would
	// leave it in the tiers it was already ranked into.
	models, probeNotes := proveModels(ctx, models, tool.ProbeModel, &tool.probes)

	tiers := buildModelTiers(models, tool.ModelPrefs)
	served := servedModels(models)

	// THE SESSION'S OWN MODEL MUST BE IN THE LIST, or the list is not this
	// provider's and nothing built from it can be trusted.
	//
	// Discovery and child execution resolve the provider by different routes, and
	// when those routes disagree this is the only place that can notice. A real
	// run proved how bad the disagreement is: the session was xai/grok-4.5, the
	// discovered list was nineteen Ollama models, and because grok-4.5 was absent
	// from it even routerModel's "the session's model comes first" rule rejected
	// the session's model — so the ROUTER ITSELF ran on qwen3.5:397b and the
	// provider refused it. Four tasks died at dispatch on models that never
	// existed for them.
	//
	// Every downstream guard was defeated by the same root fact. The served-set
	// check passed, because the wrong list was internally consistent. Refusing
	// here turns a plan-wide failure into the no-op it should always have been:
	// every task keeps the session's model, which is exactly what it did before
	// this feature existed.
	//
	// Guarded on a NON-EMPTY served set: an empty one means discovery told us
	// nothing, which is the existing fail-open and not evidence of disagreement.
	if parent := strings.TrimSpace(options.Model); parent != "" && len(served) > 0 && !servedContains(served, parent) {
		mismatch := fmt.Sprintf(
			"none — this session runs on %q, which is not among the %d model(s) discovered, "+
				"so that list belongs to a different provider and was ignored "+
				"(every task kept this session's model)", parent, len(models))
		if !supplied {
			return []string{mismatch}, nil
		}
		// Asked for explicitly: the same evidence, raised rather than reported,
		// because a plan that demanded auto-assignment must not quietly not get it.
		return nil, fmt.Errorf("auto_assign refused: %s", mismatch)
	}

	// THE ROUTER READS THE TASKS; the classifier only reads their verbs. It runs
	// once per plan, on the strongest model available, and every way it can fail
	// falls back to the classifier — a routing decision is worth having, never
	// worth stopping a plan for.
	// The router needs a grant only so the child is not refused for holding
	// none; it reads nothing. Parent identity is attached by runnerForCall.
	routerGrant, _ := planToolGrant(Task{}, tool.ParentTools)
	router := routerModel(tool.ModelPrefs, tiers, served, options.Model)
	routed, routerTokens, routeErr := routeTaskModels(ctx, tool.runnerForCall(options), PlanTaskRequest{Tools: routerGrant},
		router, routableTasks(raw), eligibleForRouting(models, tool.ModelPrefs), tool.ModelPrefs.RouterGuidance)

	tasks, notes := assignModelsToTaskArgs(raw, tiers, tool.ModelPrefs, served, routed)
	// SAID, NOT SILENT. A model disappearing from routing with no explanation is
	// indistinguishable from a bug, and these are precisely the ids the user has
	// been adding to planModels.exclude by hand after each plan died on one.
	for _, dropped := range probeNotes {
		notes = append(notes, "not offered — "+dropped)
	}
	args["tasks"] = tasks
	if len(routed) > 0 {
		// NAME THE ROUTER. A per-task model that appeared from nowhere is
		// indistinguishable from the classifier's guess, and the whole reason to
		// pay for a routing call is being able to see whose judgement it was.
		// NAME THE PRICE WITH THE NAME. Routing spends a frontier-model call
		// before a single task runs, and it is not part of any task's total —
		// so without this line the plan's reported spend is under its real spend
		// by exactly this much, every run, invisibly.
		headline := "routed by " + router
		if routerTokens > 0 {
			headline += fmt.Sprintf(" (%d tokens)", routerTokens)
		}
		notes = append([]string{headline}, notes...)
	}
	if routeErr != nil {
		// Reported, not raised. The plan ran with classifier routing, and a user
		// who configured a router deserves to know it did not answer.
		notes = append(notes, "router unavailable ("+routeErr.Error()+"); roles chosen from task wording instead")
	}
	if len(notes) == 0 {
		// ASKED FOR AND DID NOTHING IS A RESULT, and it has to say WHICH nothing.
		//
		// One message covered two unrelated causes and blamed the wrong one: a
		// run with nineteen perfectly good models reported "none were usable"
		// when the truth was that no task looked like scan, implement or verify,
		// so nothing was assigned. That sent the reader hunting through provider
		// capabilities for a problem that was in the task wording.
		if tiers == (modelTiers{}) {
			notes = append(notes, fmt.Sprintf(
				"none — %d model(s) discovered, none of them usable for a plan task "+
					"(every task kept this session's model)", len(models)))
		} else {
			notes = append(notes, fmt.Sprintf(
				"none — %d model(s) available, but no task called for a different one "+
					"(every task kept this session's model)", len(models)))
		}
	}
	return notes, nil
}

// planWorkspaceNote says WHERE a write-capable plan did its work.
//
// PlanWorkspace.Describe exists "for the approval card and the run" and nothing
// read it, so a plan that wrote files reported what it did and never where. The
// work lands in a git worktree that is deliberately NOT removed afterwards —
// "a plan that wrote produced work nobody has reviewed; deleting it would delete
// the only copy" — which makes an unnamed location the difference between work
// the user can review and work they cannot find.
//
// Empty for a read-only plan, which runs in the parent's own tree and has
// nothing to disclose.
func planWorkspaceNote(workspace PlanWorkspace) string {
	if !workspace.Isolated {
		return ""
	}
	where := strings.TrimSpace(workspace.Describe)
	if where == "" {
		where = strings.TrimSpace(workspace.Path)
	}
	if where == "" {
		return ""
	}
	return "\n\nThis plan wrote in an isolated workspace: " + where +
		"\nIt is left in place for review; nothing was written to the parent tree."
}

// autoAssignSummary reports what auto-assignment chose.
//
// REPORTED, not merely applied. A feature that silently changes which model
// each task runs on — and therefore what the plan costs — has to say what it
// did, or the user cannot tell a plan that ran as they expected from one that
// did not. Empty when nothing was assigned, so the untouched path is unchanged.
func autoAssignSummary(notes []string) string {
	if len(notes) == 0 {
		return ""
	}
	return "\n\nModels assigned automatically:\n  " + strings.Join(notes, "\n  ")
}
