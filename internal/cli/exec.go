package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/imageinput"
	"github.com/Gitlawb/zero/internal/modelregistry"
	"github.com/Gitlawb/zero/internal/notify"
	"github.com/Gitlawb/zero/internal/providers"
	"github.com/Gitlawb/zero/internal/sandbox"
	"github.com/Gitlawb/zero/internal/sessions"
	"github.com/Gitlawb/zero/internal/specialist"
	"github.com/Gitlawb/zero/internal/specmode"
	"github.com/Gitlawb/zero/internal/streamjson"
	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/worktrees"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

const (
	exitSuccess  = 0
	exitCrash    = 1
	exitUsage    = 2
	exitProvider = 3
	// exitInterrupted is the conventional shell exit code for a process stopped by
	// SIGINT (128 + signal number 2).
	exitInterrupted = 130
)

type execOutputFormat string
type execInputFormat string

const (
	execOutputText       execOutputFormat = "text"
	execOutputJSON       execOutputFormat = "json"
	execOutputStreamJSON execOutputFormat = "stream-json"
	execInputText        execInputFormat  = "text"
	execInputStreamJSON  execInputFormat  = "stream-json"
)

type execOptions struct {
	promptParts []string
	file        string
	imagePaths  []string
	mode        string
	model       string
	// modelProfile captures the legacy --profile flag. It is accepted for
	// backward compatibility (so old invocations do not error) but is
	// intentionally inert: nothing consumes it. Model selection is driven by
	// --model / --mode instead. See writeExecHelp ("Accept legacy model profile
	// selection") and TestRunExecAcceptsLegacyModelProfileFlags.
	modelProfile          string
	reasoningEffort       string
	useSpec               bool
	specModel             string
	specReasoningEffort   string
	maxTurns              int
	cwd                   string
	inputFormat           execInputFormat
	outputFormat          execOutputFormat
	autonomy              string
	enabledTools          []string
	disabledTools         []string
	listTools             bool
	resume                string
	resumeLatest          bool
	fork                  string
	callingSessionID      string
	callingToolUseID      string
	tag                   string
	depth                 int
	sessionTitle          string
	initSessionID         string
	worktree              bool
	worktreeName          string
	worktreeDir           string
	skipPermissionsUnsafe bool
	// allowEscalation opts the run into mid-run model escalation: it registers
	// the escalate_model tool and wires agent.Options.ModelSwitcher. Off by
	// default — a run without the flag is byte-identical to before (no tool, nil
	// switcher).
	allowEscalation bool
	// notifyMode overrides config.Notify.Mode for this run. Mutually exclusive
	// with noNotify.
	notifyMode string
	// noNotify forces ModeOff for this run. Mutually exclusive with notifyMode.
	noNotify bool
	// addDirs holds directories passed via --add-dir that should be allowed as
	// additional write roots for this run. Unioned with
	// config.SandboxConfig.AdditionalWriteRoots at scope construction time.
	addDirs []string
}

type execUsageError struct {
	message string
}

func (err execUsageError) Error() string {
	return err.message
}

func runExec(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	options, help, err := parseExecArgs(args)
	if err != nil {
		return writeExecFormatUsageError(stdout, stderr, options.outputFormat, err.Error())
	}
	if help {
		if err := writeExecHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}

	// A mode seeds model/effort/max-turns/tool filters as a preset. Expand it up
	// front — before tool-filter validation and the --list-tools branch — so a
	// mode-injected tool filter is validated and reflected in --list-tools, and a
	// mode-supplied model flows through the same resolution (and deprecation
	// notice) path as an explicit --model. Explicit flags still win: applyExecMode
	// only fills fields the caller left unset.
	if err := applyExecMode(&options); err != nil {
		return writeExecFormatUsageError(stdout, stderr, options.outputFormat, err.Error())
	}

	workspaceRoot, err := resolveWorkspaceRoot(options.cwd, deps)
	if err != nil {
		return writeExecFormatUsageError(stdout, stderr, options.outputFormat, err.Error())
	}
	if options.worktree {
		preparedWorktree, err := deps.prepareWorktree(context.Background(), worktrees.Options{
			Cwd:     workspaceRoot,
			Name:    options.worktreeName,
			BaseDir: options.worktreeDir,
			Now:     deps.now,
		})
		if err != nil {
			return writeExecFormatUsageError(stdout, stderr, options.outputFormat, err.Error())
		}
		workspaceRoot = preparedWorktree.Path
	}

	registry := newCoreRegistry(workspaceRoot)
	// Register the escalate_model tool only when the run opted into mid-run
	// escalation. It is registered before --list-tools formatting and
	// validateExecToolFilters so the tool is both listable and filter-validatable.
	// The no-arg constructor builds its own default registry internally (and
	// degrades to an inert tool if the catalog fails), so no registry is threaded.
	if options.allowEscalation {
		registry.Register(tools.NewEscalateModelTool())
	}
	var specialistRuntime *specialist.Runtime
	if shouldRegisterExecSpecialistTools(options) {
		var err error
		specialistRuntime, err = registerSpecialistTools(registry, workspaceRoot)
		if err != nil {
			return writeExecProviderError(stdout, stderr, options.outputFormat, "specialist_error", err.Error())
		}
		defer closeSpecialistRuntime(stderr, specialistRuntime)
	}
	permissionMode, err := resolveExecPermissionMode(options)
	if err != nil {
		return writeExecFormatUsageError(stdout, stderr, options.outputFormat, err.Error())
	}
	if options.useSpec {
		permissionMode = agent.PermissionModeSpecDraft
	}
	mcpRuntime, err := registerMCPToolsForWorkspace(context.Background(), workspaceRoot, registry, deps, execMCPAutonomy(options))
	if err != nil {
		return writeExecProviderError(stdout, stderr, options.outputFormat, "mcp_error", err.Error())
	}
	defer closeMCPRuntime(stderr, mcpRuntime)
	// Make local plugins live for this run: register their declared tools into the
	// registry and collect their hooks + skill roots for the dispatcher and skill
	// tool below. Done before --list-tools and filter validation so plugin tools
	// are listable and filter-validatable; it fails OPEN (a bad plugin is skipped).
	pluginActivation := activatePlugins(workspaceRoot, registry, deps, stderr)
	if options.useSpec {
		specmode.RegisterDraftTools(registry, workspaceRoot, deps.now)
	}
	if err := validateExecToolFilters(options, registry); err != nil {
		return writeExecFormatUsageError(stdout, stderr, options.outputFormat, err.Error())
	}
	if options.listTools {
		if options.outputFormat == execOutputStreamJSON {
			return writeExecStreamJSONFinal(stdout, workspaceRoot, execRunMetadata{}, permissionMode, formatExecToolList(registry, options, permissionMode), exitSuccess)
		}
		if err := writeExecToolList(stdout, registry, options, permissionMode); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if err := preflightExecSession(options); err != nil {
		return writeExecFormatUsageError(stdout, stderr, options.outputFormat, err.Error())
	}

	prompt, streamImages, err := resolveExecPrompt(options, workspaceRoot, deps.stdin)
	if err != nil {
		return writeExecFormatUsageError(stdout, stderr, options.outputFormat, err.Error())
	}
	sessionTitle := execSessionTitle(options, prompt)

	// Build the model registry once and reuse it across every model-aware
	// lookup in this exec run (model resolution, reasoning-effort advisory, and
	// context-window sizing). DefaultRegistry builds the full catalog and
	// compiles its match patterns, so rebuilding it per lookup is wasteful.
	// modelRegistry is the zero Registry when the catalog fails to build; the
	// helpers below degrade to safe no-op behavior in that case.
	modelRegistry, _ := modelregistry.DefaultRegistry()

	overrides := config.Overrides{}
	modelOverride := options.model
	if options.useSpec && options.specModel != "" {
		modelOverride = options.specModel
	}
	if modelOverride != "" {
		resolvedModel, notice := resolveSelectedModel(modelRegistry, modelOverride)
		overrides.Provider.Model = resolvedModel
		if notice != "" {
			if _, err := fmt.Fprintln(stderr, notice); err != nil {
				return exitCrash
			}
		}
	}
	if options.maxTurns > 0 {
		overrides.MaxTurns = options.maxTurns
	}
	resolved, err := deps.resolveConfig(workspaceRoot, overrides)
	if err != nil {
		return writeExecProviderError(stdout, stderr, options.outputFormat, "provider_error", err.Error())
	}
	if !config.HasProviderProfile(resolved.Provider) {
		return writeExecProviderError(stdout, stderr, options.outputFormat, "provider_error", "No provider configured. Set OPENAI_MODEL/OPENAI_API_KEY or add .zero/config.json.")
	}
	// Activate deferred MCP-tool loading for this run only when the VISIBLE
	// deferred-eligible count meets the resolved threshold; below threshold this
	// is a no-op and tool advertising stays byte-identical. The registry is
	// already complete (core + MCP) at this point, so the count is accurate. The
	// permission mode and operator tool filters MUST match the values passed to
	// agent.Run below so this registration gate counts the same population the
	// loop's partition gate counts.
	// tool_search is the only way to reach a hidden deferred tool, so if the
	// operator explicitly disables it, deferral must not activate at all —
	// otherwise a positive threshold would hide tools behind a loader the run
	// rejects (a dead-end). Force the effective threshold to 0 so this
	// registration gate and agent.Run's partition gate agree the run is inactive.
	effectiveDeferThreshold := resolved.Tools.DeferThreshold
	if toolListContains(options.disabledTools, tools.ToolSearchToolName) {
		effectiveDeferThreshold = 0
	}
	registerToolSearchIfEligible(registry, effectiveDeferThreshold, permissionMode, options.enabledTools, options.disabledTools)
	images, err := resolveExecImages(options.imagePaths, workspaceRoot)
	if err != nil {
		return writeExecFormatUsageError(stdout, stderr, options.outputFormat, err.Error())
	}
	// Merge stream-json message images with --image attachments so BOTH input
	// sources flow through the same vision gate (drop+warn) and the same
	// agent.Options.Images wiring below. Without this merge, images sent over
	// stream-json are parsed and validated but never reach the agent.
	images = append(images, streamImages...)
	// Gate against the EFFECTIVE resolved model (not the --model override). An
	// unknown/custom id can't be confirmed vision-capable, so drop+warn rather
	// than error: image input is best-effort, never fatal to the run.
	if len(images) > 0 && !modelregistry.SupportsVision(modelRegistry, resolved.Provider.Model) {
		if _, err := fmt.Fprintf(stderr, "Model %s does not support image input; ignoring %d image(s).\n", resolved.Provider.Model, len(images)); err != nil {
			return exitCrash
		}
		images = nil
	}
	execScope, err := sandbox.NewScope(workspaceRoot, append(append([]string{}, resolved.Sandbox.AdditionalWriteRoots...), options.addDirs...))
	if err != nil {
		return writeExecProviderError(stdout, stderr, options.outputFormat, "sandbox_error", err.Error())
	}
	// Re-register the core tools with the run scope, OVERWRITING the nil-scope
	// instances registered before config resolve (the registry must exist that
	// early for --list-tools and tool-filter validation, which run without a
	// provider). This is safe only while two invariants hold:
	//   1. Registry.Register replaces by NAME, so every path-confining core
	//      tool is swapped wholesale; and
	//   2. nothing between the initial registration and this point captures a
	//      core-tool INSTANCE (tool_search holds the *Registry* and resolves
	//      names lazily; --list-tools and filter validation use names only).
	// A new wrapper that snapshots a core tool before this line would silently
	// ship nil-scope enforcement — add it below this re-registration instead.
	for _, tool := range tools.CoreToolsScoped(workspaceRoot, execScope) {
		registry.Register(tool)
	}
	sandboxEngine, err := buildExecSandboxEngine(workspaceRoot, resolved, deps, execScope)
	if err != nil {
		return writeExecProviderError(stdout, stderr, options.outputFormat, "sandbox_error", err.Error())
	}
	runReasoningEffort := options.reasoningEffort
	if options.useSpec && options.specReasoningEffort != "" {
		runReasoningEffort = options.specReasoningEffort
	}
	// Evaluate the --reasoning-effort advisory against the EFFECTIVE resolved
	// model (resolved.Provider.Model), not the override. Without an explicit
	// --model the override model is empty, so checking it here would silently
	// skip the advisory even though the run uses a concrete effective model.
	if runReasoningEffort != "" {
		if notice := reasoningEffortNotice(modelRegistry, resolved.Provider.Model, runReasoningEffort); notice != "" {
			if _, err := fmt.Fprintln(stderr, notice); err != nil {
				return exitCrash
			}
		}
	}
	// Effort to forward on the provider request, gated to the resolved model's
	// supported levels (empty for non-reasoning models).
	forwardEffort := forwardedReasoningEffort(modelRegistry, resolved.Provider.Model, runReasoningEffort)

	provider, err := buildProvider(resolved, deps)
	if err != nil {
		return writeExecProviderError(stdout, stderr, options.outputFormat, "provider_error", err.Error())
	}

	// currentModel tracks the model in force for usage attribution. It starts at
	// the resolved model and is reassigned by the model switcher on a mid-run
	// escalation so post-switch turns are attributed to the escalated model.
	currentModel := resolved.Provider.Model
	var modelSwitcher func(context.Context, string) (agent.Provider, error)
	if options.allowEscalation {
		modelSwitcher = func(_ context.Context, modelID string) (agent.Provider, error) {
			switchedProfile := resolved.Provider
			switchedProfile.Model = modelID
			switchedProvider, err := deps.newProvider(switchedProfile)
			if err != nil {
				return nil, err
			}
			// Mirror the agent loop's switch guard (it only reassigns the provider
			// when newProvider != nil). Updating currentModel only on a non-nil
			// provider keeps usage attribution consistent with whether the loop
			// actually switched — a (nil, nil) return leaves both untouched.
			if switchedProvider != nil {
				currentModel = modelID
			}
			return switchedProvider, nil
		}
	}

	runMetadata, err := resolveExecRunMetadata(resolved.Provider)
	if err != nil {
		return writeExecProviderError(stdout, stderr, options.outputFormat, "provider_error", err.Error())
	}
	// Notify on completion via stderr (never stdout, which may carry stream-json).
	// Headless has no terminal-focus signal, so focus gating does not apply here:
	// always emit when a mode is configured (focusMode is a TUI-only concept).
	notifier := notify.New(stderr, notify.Config{
		Mode:      notify.Mode(strings.TrimSpace(execNotifyMode(options, resolved))),
		FocusMode: notify.FocusAlways,
	})
	if options.useSpec {
		return runExecSpecDraft(execSpecDraftRun{
			options:            options,
			stdout:             stdout,
			stderr:             stderr,
			deps:               deps,
			workspaceRoot:      workspaceRoot,
			registry:           registry,
			modelRegistry:      modelRegistry,
			resolved:           resolved,
			runMetadata:        runMetadata,
			provider:           provider,
			sandboxEngine:      sandboxEngine,
			prompt:             prompt,
			sessionTitle:       sessionTitle,
			images:             images,
			reasoningEffort:    forwardEffort,
			specPermissionMode: permissionMode,
			notifier:           notifier,
		})
	}

	preparedSession := sessions.PreparedExec{}
	agentPrompt := prompt
	if shouldUseExecSession(options) {
		preparedSession, err = sessions.PrepareExec(sessions.PrepareExecOptions{
			SessionID:        options.initSessionID,
			Title:            sessionTitle,
			Cwd:              workspaceRoot,
			ModelID:          resolved.Provider.Model,
			Provider:         runMetadata.Provider,
			Tag:              options.tag,
			Depth:            options.depth,
			CallingSessionID: options.callingSessionID,
			CallingToolUseID: options.callingToolUseID,
			AgentName:        specialistAgentName(options.sessionTitle),
			TaskID:           options.initSessionID,
			Resume:           options.resume,
			ResumeLatest:     options.resumeLatest,
			Fork:             options.fork,
		})
		if err != nil {
			return writeExecFormatUsageError(stdout, stderr, options.outputFormat, err.Error())
		}
		agentPrompt = sessions.FormatExecPrompt(prompt, preparedSession)
	}
	runID, err := streamjson.CreateRunID(time.Now())
	if err != nil {
		return writeAppError(stderr, "failed to create run id: "+err.Error(), exitCrash)
	}
	writer := execEventWriter{
		stdout:       stdout,
		stderr:       stderr,
		format:       options.outputFormat,
		runID:        runID,
		sessionID:    preparedSession.Session.SessionID,
		streamedText: &strings.Builder{},
	}
	writer.runStart(workspaceRoot, runMetadata, permissionMode)
	if writer.err != nil {
		return exitCrash
	}
	// Surface the unsafe-permissions warning whenever the run resolves to unsafe
	// mode, covering BOTH --skip-permissions-unsafe and --auto high (which also
	// resolves to PermissionModeUnsafe). Previously only the explicit flag path
	// warned, so --auto high silently ran without notice.
	if permissionMode == agent.PermissionModeUnsafe {
		reason := "--auto high"
		if options.skipPermissionsUnsafe {
			reason = "--skip-permissions-unsafe"
		}
		writer.warning(fmt.Sprintf("Unsafe permissions are active for this run because %s was passed.", reason))
		if writer.err != nil {
			return exitCrash
		}
	}

	sessionRecorder := execSessionRecorder{prepared: preparedSession}
	sessionRecorder.append(sessions.EventMessage, map[string]any{
		"role":    "user",
		"content": prompt,
	})

	// OnAskUser is intentionally left unset: headless runs have no interactive
	// user, so ask_user degrades to a "proceed with your best assumption" result
	// rather than blocking. (Future enhancement: collect answers over stream-json
	// input when a controlling client is attached.)
	// Cancel the agent run on Ctrl+C / SIGTERM so a long headless run shuts down
	// cleanly (the loop honors context cancellation) instead of being killed.
	runCtx, stopSignals := signalContext()
	defer stopSignals()
	result, err := agent.Run(runCtx, agentPrompt, provider, agent.Options{
		MaxTurns:         resolved.MaxTurns,
		ContextWindow:    modelContextWindow(modelRegistry, resolved.Provider.Model),
		DeferThreshold:   effectiveDeferThreshold,
		SessionID:        preparedSession.Session.SessionID,
		CallingSessionID: options.callingSessionID,
		CallingToolUseID: options.callingToolUseID,
		Tag:              options.tag,
		Depth:            options.depth,
		SessionTitle:     sessionTitle,
		ProviderName:     resolved.Provider.Name,
		Model:            resolved.Provider.Model,
		ModelSwitcher:    modelSwitcher,
		ReasoningEffort:  forwardEffort,
		Cwd:              workspaceRoot,
		Images:           images,
		Registry:         registry,
		PermissionMode:   permissionMode,
		Autonomy:         options.autonomy,
		Sandbox:          sandboxEngine,
		FileTracker:      tools.NewFileTracker(),
		Hooks:            newHookDispatcherWithExtra(workspaceRoot, pluginActivation.hooks),
		EnabledTools:     options.enabledTools,
		DisabledTools:    options.disabledTools,
		OnText:           writer.text,
		OnToolCall: func(call agent.ToolCall) {
			writer.toolCall(call, registry)
			sessionRecorder.append(sessions.EventToolCall, map[string]any{
				"id":        call.ID,
				"name":      call.Name,
				"arguments": call.Arguments,
			})
			// Snapshot before-state of files this call will mutate (safe rewind).
			if checkpoint, ok := sessionRecorder.captureCheckpoint(workspaceRoot, call); ok {
				writer.checkpoint(checkpoint)
			}
		},
		OnPermission: func(event agent.PermissionEvent) {
			writer.permission(event)
			sessionRecorder.append(sessionPermissionEventType(event), event)
		},
		OnToolResult: func(result agent.ToolResult) {
			writer.toolResult(result)
			payload := map[string]any{
				"toolCallId": result.ToolCallID,
				"name":       result.Name,
				"status":     string(result.Status),
				"output":     result.Output,
			}
			if len(result.Meta) > 0 {
				payload["meta"] = result.Meta
			}
			if result.Redacted {
				payload["redacted"] = true
			}
			if len(result.ChangedFiles) > 0 {
				payload["changedFiles"] = result.ChangedFiles
			}
			sessionRecorder.append(sessions.EventToolResult, payload)
		},
		OnUsage: func(usage agent.Usage) {
			writer.usage(usage)
			payload := map[string]any{
				"promptTokens":     usage.EffectiveInputTokens(),
				"completionTokens": usage.EffectiveOutputTokens(),
				"totalTokens":      usage.TotalTokens(),
			}
			// Attribute usage to a specific model ONLY when escalation is enabled:
			// the model in force can change mid-run only under --allow-escalation, so
			// the "model" key is meaningful exclusively then. Omitting it otherwise
			// keeps a non-escalation run's persisted usage payload byte-identical to
			// before this feature (the origin/main guarantee).
			if options.allowEscalation {
				payload["model"] = currentModel
			}
			sessionRecorder.append(sessions.EventUsage, payload)
		},
	})
	notifier.Notify(notify.Completion, notify.DefaultMessage(notify.Completion))
	if writer.err != nil {
		return exitCrash
	}
	if err != nil {
		// A Ctrl+C / SIGTERM cancellation is a clean shutdown, not a provider error.
		if errors.Is(err, context.Canceled) || runCtx.Err() != nil {
			sessionRecorder.append(sessions.EventError, map[string]any{"message": "interrupted"})
			if options.outputFormat == execOutputStreamJSON {
				writer.errorEvent("interrupted", "run cancelled by signal", false)
				writer.runEnd("interrupted", exitInterrupted)
				if writer.err != nil {
					return exitCrash
				}
			} else {
				fmt.Fprintln(stderr, "Interrupted.")
			}
			return exitInterrupted
		}
		sessionRecorder.append(sessions.EventError, map[string]any{"message": err.Error()})
		if options.outputFormat == execOutputStreamJSON {
			writer.errorEvent("provider_error", err.Error(), false)
			writer.runEnd("error", exitProvider)
			if writer.err != nil {
				return exitCrash
			}
			return exitProvider
		}
		return writeExecProviderError(stdout, stderr, options.outputFormat, "provider_error", err.Error())
	}
	sessionRecorder.append(sessions.EventMessage, map[string]any{
		"role":    "assistant",
		"content": result.FinalAnswer,
	})

	if notice := result.TruncationNotice(); notice != "" {
		writer.warning(notice)
	}
	writer.final(result.FinalAnswer)
	writer.runEnd("success", exitSuccess)
	if writer.err != nil {
		return exitCrash
	}
	return exitSuccess
}

// deferredEligibleCount returns the number of registered tools that are
// deferred-eligible (MCP tools) AND visible to the model for THIS run — i.e. they
// pass the same agent.ToolVisible gate (permission-mode advertising + operator
// allow/deny filters) that the agent loop's partitionTools applies when it
// decides whether deferral activates. Counting the SAME visible-deferred
// population here keeps registration and activation in agreement: tool_search is
// registered iff the partition will actually go active. Built-ins never implement
// the Deferred interface, so they never count.
func deferredEligibleCount(registry *tools.Registry, permissionMode agent.PermissionMode, enabledTools []string, disabledTools []string) int {
	count := 0
	for _, tool := range registry.All() {
		if !tools.IsDeferred(tool) {
			continue
		}
		if !agent.ToolVisible(tool, permissionMode, enabledTools, disabledTools) {
			continue
		}
		count++
	}
	return count
}

// registerToolSearchIfEligible registers the tool_search tool only when deferral
// is active for this run: the visible-deferred count (the same population the
// agent loop's partition counts) meets the (positive) threshold. Below threshold
// or with a zero/negative threshold, tool_search is never registered, so the
// agent loop's partition stays inactive and tool advertising is byte-identical to
// today. The permissionMode + enabled/disabled filters MUST match the values the
// run passes to agent.Run so the registration gate and the activation gate count
// the same tools.
func registerToolSearchIfEligible(registry *tools.Registry, deferThreshold int, permissionMode agent.PermissionMode, enabledTools []string, disabledTools []string) {
	if deferThreshold <= 0 {
		return
	}
	if deferredEligibleCount(registry, permissionMode, enabledTools, disabledTools) < deferThreshold {
		return
	}
	registry.Register(tools.NewToolSearchTool(registry))
}

func buildExecSandboxEngine(workspaceRoot string, resolved config.ResolvedConfig, deps appDeps, scope *sandbox.Scope) (*sandbox.Engine, error) {
	store, err := deps.newSandboxStore()
	if err != nil {
		return nil, err
	}
	policy := applyConfiguredSandboxPolicy(sandbox.DefaultPolicy(), resolved.Sandbox)
	backend := deps.selectSandboxBackend(sandbox.BackendOptions{})
	return sandbox.NewEngine(sandbox.EngineOptions{
		WorkspaceRoot: workspaceRoot,
		Policy:        policy,
		Store:         store,
		Backend:       backend,
		Scope:         scope,
	}), nil
}

// applyConfiguredAutonomyCeiling overlays a config-sourced autonomy ceiling onto
// a policy. An empty ceiling is a no-op so the default High ceiling is preserved
// (backward compatible).
//
// A non-empty but unrecognised value FAILS CLOSED: it clamps to the most
// restrictive ceiling (AutonomyLow) rather than leaving the policy unchanged.
// config.Resolve already rejects invalid values, so this branch is defense in
// depth — it guarantees that even a direct/programmatic caller passing a typo
// can never WIDEN the ceiling back to the High default.
// applyConfiguredSandboxPolicy overlays every config-sourced sandbox knob onto
// the default policy: the autonomy ceiling and the network mode.
func applyConfiguredSandboxPolicy(policy sandbox.Policy, cfg config.SandboxConfig) sandbox.Policy {
	policy = applyConfiguredAutonomyCeiling(policy, cfg.MaxAutonomy)
	if network := strings.TrimSpace(cfg.Network); network != "" {
		switch sandbox.NetworkMode(network) {
		case sandbox.NetworkAllow, sandbox.NetworkDeny:
			policy.Network = sandbox.NetworkMode(network)
		}
	}
	return policy
}

func applyConfiguredAutonomyCeiling(policy sandbox.Policy, maxAutonomy string) sandbox.Policy {
	trimmed := strings.TrimSpace(maxAutonomy)
	if trimmed == "" {
		return policy
	}
	normalized, err := sandbox.NormalizeAutonomy(sandbox.Autonomy(trimmed))
	if err != nil {
		policy.MaxAutonomy = sandbox.AutonomyLow
		return policy
	}
	policy.MaxAutonomy = normalized
	return policy
}

func resolveWorkspaceRoot(cwd string, deps appDeps) (string, error) {
	current, err := deps.getwd()
	if err != nil {
		return "", fmt.Errorf("failed to resolve workspace: %w", err)
	}

	workspaceRoot := strings.TrimSpace(cwd)
	if workspaceRoot == "" {
		workspaceRoot = current
	} else if !filepath.IsAbs(workspaceRoot) {
		workspaceRoot = filepath.Join(current, workspaceRoot)
	}
	workspaceRoot = filepath.Clean(workspaceRoot)

	info, err := os.Stat(workspaceRoot)
	if err != nil || !info.IsDir() {
		return "", execUsageError{fmt.Sprintf("cwd must be an existing directory: %s", workspaceRoot)}
	}
	return workspaceRoot, nil
}

// resolveExecPrompt resolves the run's prompt text and, for stream-json input,
// the images carried on its message events. The returned image slice is nil for
// text input and for stream-json input that carries no images; it is merged with
// any --image attachments by the caller before the shared vision gate, so both
// sources flow through the same drop+warn and agent.Options.Images wiring.
func resolveExecPrompt(options execOptions, workspaceRoot string, stdin io.Reader) (string, []zeroruntime.ImageBlock, error) {
	if options.inputFormat == execInputStreamJSON {
		input := ""
		if options.file != "" {
			promptPath := options.file
			if !filepath.IsAbs(promptPath) {
				promptPath = filepath.Join(workspaceRoot, promptPath)
			}
			data, err := os.ReadFile(promptPath)
			if err != nil {
				return "", nil, execUsageError{fmt.Sprintf("prompt file not found: %s", promptPath)}
			}
			input = string(data)
		} else {
			data, err := io.ReadAll(stdin)
			if err != nil {
				return "", nil, execUsageError{fmt.Sprintf("failed to read stream-json input: %v", err)}
			}
			input = string(data)
		}
		events, err := streamjson.ParseInput(input)
		if err != nil {
			return "", nil, execUsageError{err.Error()}
		}
		streamImages, err := streamjson.ResolveImages(events)
		if err != nil {
			return "", nil, execUsageError{err.Error()}
		}
		prompt, perr := streamjson.ResolvePrompt(events)
		if perr != nil {
			// An image-only turn (a message event with empty content but at least
			// one image) is valid: ResolvePrompt rejects empty content, but with
			// images present the run proceeds with an empty prompt. Only a turn
			// with neither text nor images is a real error.
			if len(streamImages) == 0 {
				return "", nil, execUsageError{perr.Error()}
			}
			prompt = ""
		}
		return prompt, streamImages, nil
	}

	parts := []string{}
	inlinePrompt := strings.TrimSpace(strings.Join(options.promptParts, " "))
	if inlinePrompt != "" {
		parts = append(parts, inlinePrompt)
	}

	if options.file != "" {
		promptPath := options.file
		if !filepath.IsAbs(promptPath) {
			promptPath = filepath.Join(workspaceRoot, promptPath)
		}
		data, err := os.ReadFile(promptPath)
		if err != nil {
			return "", nil, execUsageError{fmt.Sprintf("prompt file not found: %s", promptPath)}
		}
		filePrompt := strings.TrimSpace(string(data))
		if filePrompt == "" {
			return "", nil, execUsageError{fmt.Sprintf("prompt file is empty: %s", promptPath)}
		}
		parts = append(parts, filePrompt)
	}

	prompt := strings.TrimSpace(strings.Join(parts, "\n\n"))
	if prompt == "" {
		return "", nil, execUsageError{"Prompt required. Use `zero exec \"prompt\"` or `zero exec --file prompt.txt`."}
	}
	return prompt, nil, nil
}

// resolveExecImages loads each --image attachment through the shared
// imageinput.LoadFile loader (read, sniff, normalize, 10-MiB cap), resolving
// relative paths against workspaceRoot. It is a thin per-path loop: the actual
// read/sniff/cap logic lives in internal/imageinput so the CLI and TUI surfaces
// never duplicate it. Any loader error (missing file, unsupported type,
// oversized) is wrapped into an execUsageError so the run reports it as a usage
// problem rather than reaching a provider with an invalid image. Returns nil for
// an empty path list (text-only behavior unchanged).
func resolveExecImages(paths []string, workspaceRoot string) ([]zeroruntime.ImageBlock, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	images := make([]zeroruntime.ImageBlock, 0, len(paths))
	for _, path := range paths {
		image, err := imageinput.LoadFile(path, workspaceRoot)
		if err != nil {
			return nil, execUsageError{err.Error()}
		}
		images = append(images, image)
	}
	return images, nil
}

func writeExecUsageError(stderr io.Writer, message string) int {
	if _, err := fmt.Fprintf(stderr, "[zero] %s\n", message); err != nil {
		return exitCrash
	}
	return exitUsage
}

func writeExecFormatUsageError(stdout io.Writer, stderr io.Writer, format execOutputFormat, message string) int {
	if format == execOutputStreamJSON {
		return writeStreamJSONError(stdout, "usage_error", message, false, exitUsage)
	}
	return writeExecUsageError(stderr, message)
}

func writeExecProviderError(stdout io.Writer, stderr io.Writer, format execOutputFormat, code string, message string) int {
	if format == execOutputStreamJSON {
		return writeStreamJSONError(stdout, code, message, false, exitProvider)
	}
	if format == execOutputJSON {
		if err := writeJSONLine(stdout, map[string]any{
			"type":    "error",
			"code":    code,
			"message": message,
		}); err != nil {
			return exitCrash
		}
		if err := writeJSONLine(stdout, map[string]any{
			"type":      "done",
			"exit_code": exitProvider,
		}); err != nil {
			return exitCrash
		}
		return exitProvider
	}
	if _, err := fmt.Fprintf(stderr, "[zero] %s\n", message); err != nil {
		return exitCrash
	}
	return exitProvider
}

// applyExecMode expands a --mode preset onto the exec options. The preset only
// fills fields the caller left unset, so an explicit --model / --reasoning-effort
// / --max-turns / tool filter always wins over the mode. The mode's model is left
// as the preset's raw id/alias so the shared --model resolution path resolves it
// through the registry (canonical ids/deprecation fallbacks) AND surfaces any
// deprecation notice on stderr, exactly like an explicit --model. An unknown mode
// is a usage error listing the valid presets.
func applyExecMode(options *execOptions) error {
	name := strings.TrimSpace(options.mode)
	if name == "" {
		return nil
	}
	mode, ok := modelregistry.LookupMode(name)
	if !ok {
		return execUsageError{fmt.Sprintf("unknown mode %q. Valid modes: %s.", options.mode, strings.Join(modelregistry.ModeNames(), ", "))}
	}
	if options.model == "" && mode.Model != "" {
		options.model = mode.Model
	}
	if options.reasoningEffort == "" && mode.Effort != "" {
		options.reasoningEffort = string(mode.Effort)
	}
	if options.maxTurns == 0 && mode.MaxTurns > 0 {
		options.maxTurns = mode.MaxTurns
	}
	if len(options.enabledTools) == 0 && len(mode.EnabledTools) > 0 {
		options.enabledTools = append([]string{}, mode.EnabledTools...)
	}
	if len(options.disabledTools) == 0 && len(mode.DisabledTools) > 0 {
		options.disabledTools = append([]string{}, mode.DisabledTools...)
	}
	return nil
}

// resolveSelectedModel routes a user-supplied --model value through the model
// registry so that fuzzy aliases (e.g. "sonnet 4.5") resolve to canonical ids
// and deprecated models auto-redirect to their fallback. It returns the model id
// to use plus a non-empty notice when a deprecation redirect or warning applies.
// Inputs that the registry does not recognize (e.g. custom openai-compatible
// model names) are returned unchanged so provider passthrough still works.
func resolveSelectedModel(registry modelregistry.Registry, input string) (string, string) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return input, ""
	}
	entry, notice, ok := registry.ResolveWithFallback(trimmed)
	if !ok {
		return input, ""
	}
	return entry.ID, notice
}

// modelContextWindow returns the resolved model's context window (max input
// tokens) from the model registry, used to enable agent-loop compaction. An
// unknown model (e.g. a custom openai-compatible name) returns 0, which leaves
// compaction DISABLED — a safe default that never compacts unexpectedly.
func modelContextWindow(registry modelregistry.Registry, modelID string) int {
	trimmed := strings.TrimSpace(modelID)
	if trimmed == "" {
		return 0
	}
	entry, ok := registry.Resolve(trimmed)
	if !ok {
		return 0
	}
	return entry.ContextLimits.ContextWindow
}

// reasoningEffortNotice resolves the requested --reasoning-effort against the
// selected model's supported efforts via EffectiveReasoningEffort and returns a
// short advisory when the requested value is unsupported (and was coerced to the
// model default).
//
// NOTE: the effective effort is not yet forwarded to the provider request — the
// zeroruntime.CompletionRequest / provider wire schemas carry no effort field.
// Full provider-request propagation is deferred (see slice-3 report).
// forwardedReasoningEffort returns the effort to send on the provider request.
// It mirrors reasoningEffortNotice: a known model that does not support reasoning
// yields "" (matching the "ignoring" advisory, so the request never carries an
// unsupported parameter); a known reasoning model yields its effective level; an
// unknown model (e.g. a custom OpenAI-compatible endpoint) forwards the requested
// value as-is, since no support claim can be made for it.
func forwardedReasoningEffort(registry modelregistry.Registry, modelID string, requested string) string {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return ""
	}
	entry, ok := registry.Get(strings.TrimSpace(modelID))
	if !ok {
		return requested
	}
	effective := modelregistry.EffectiveReasoningEffort(entry, modelregistry.ReasoningEffort(strings.ToLower(requested)))
	if effective == modelregistry.ReasoningEffortNone {
		return ""
	}
	return string(effective)
}

func reasoningEffortNotice(registry modelregistry.Registry, modelID string, requested string) string {
	trimmed := strings.TrimSpace(modelID)
	if trimmed == "" {
		return ""
	}
	entry, ok := registry.Get(trimmed)
	if !ok {
		return ""
	}
	want := modelregistry.ReasoningEffort(strings.TrimSpace(strings.ToLower(requested)))
	effective := modelregistry.EffectiveReasoningEffort(entry, want)
	if effective == modelregistry.ReasoningEffortNone {
		return fmt.Sprintf("%s does not support reasoning effort; ignoring --reasoning-effort %s", entry.ID, requested)
	}
	if want != "" && effective != want {
		return fmt.Sprintf("reasoning effort %q is not supported by %s; using %s instead", requested, entry.ID, effective)
	}
	return ""
}

func resolveExecRunMetadata(profile config.ProviderProfile) (execRunMetadata, error) {
	metadata, err := providers.ResolveRuntimeMetadata(profile, providers.Options{})
	if err != nil {
		return execRunMetadata{}, err
	}
	provider := strings.TrimSpace(string(metadata.ProviderKind))
	if provider == "" {
		provider = strings.TrimSpace(profile.Name)
	}
	apiModel := strings.TrimSpace(metadata.APIModel)
	if apiModel == "" {
		apiModel = strings.TrimSpace(profile.Model)
	}
	return execRunMetadata{
		Provider: provider,
		Model:    strings.TrimSpace(profile.Model),
		APIModel: apiModel,
	}, nil
}

func writeExecStreamJSONFinal(stdout io.Writer, cwd string, metadata execRunMetadata, permissionMode agent.PermissionMode, text string, exitCode int) int {
	runID, err := streamjson.CreateRunID(time.Now())
	if err != nil {
		return exitCrash
	}
	writer := execEventWriter{
		stdout:       stdout,
		format:       execOutputStreamJSON,
		runID:        runID,
		streamedText: &strings.Builder{},
	}
	writer.runStart(cwd, metadata, permissionMode)
	writer.final(text)
	writer.runEnd("success", exitCode)
	if writer.err != nil {
		return exitCrash
	}
	return exitCode
}

func sessionPermissionEventType(event agent.PermissionEvent) sessions.EventType {
	if event.Action == agent.PermissionActionPrompt {
		return sessions.EventPermissionRequest
	}
	if event.Action == agent.PermissionActionAllow || event.Action == agent.PermissionActionDeny {
		return sessions.EventPermissionDecision
	}
	return sessions.EventPermission
}

// execNotifyMode resolves the effective notification mode for a run:
// --no-notify forces "off", --notify <mode> overrides config, otherwise the
// config value is used.
func execNotifyMode(options execOptions, resolved config.ResolvedConfig) string {
	if options.noNotify {
		return string(notify.ModeOff)
	}
	if options.notifyMode != "" {
		return options.notifyMode
	}
	return resolved.Notify.Mode
}
