package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/Gitlawb/zero/internal/config"
	zeroSandbox "github.com/Gitlawb/zero/internal/sandbox"
	"github.com/Gitlawb/zero/internal/zerocommands"
)

type sandboxCheckOptions struct {
	tool       string
	sideEffect string
	path       string
	autonomy   string
	reason     string
	json       bool
}

// sandboxCheckReport is the combined snapshot `zero sandbox check` emits: the
// active plan (policy + backend + restrictions), the decision the engine would
// make for the described tool action, and any persistent grant that matches the
// tool. It is the production consumer of the zerocommands sandbox-snapshot
// contract, giving operators and CI a stable, redacted JSON view of "what would
// the sandbox do for this action?".
type sandboxCheckReport struct {
	Tool     string                                 `json:"tool"`
	Plan     zerocommands.SandboxPlanSnapshot       `json:"plan"`
	Decision zerocommands.SandboxDecisionSnapshot   `json:"decision"`
	Grant    zerocommands.SandboxGrantMatchSnapshot `json:"grant"`
}

func runSandboxCheck(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	options, help, err := parseSandboxCheckArgs(args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if err := writeSandboxCheckHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if strings.TrimSpace(options.tool) == "" {
		return writeExecUsageError(stderr, "tool name is required: zero sandbox check <tool> [flags]")
	}

	workspaceRoot, err := resolveWorkspaceRoot("", deps)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	store, err := deps.newSandboxStore()
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	resolved, err := deps.resolveConfig(workspaceRoot, config.Overrides{})
	if err != nil {
		return writeAppError(stderr, err.Error(), exitProvider)
	}
	policy := applyConfiguredSandboxPolicy(zeroSandbox.DefaultPolicy(), resolved.Sandbox)
	backend := deps.selectSandboxBackend(zeroSandbox.BackendOptions{})
	plan := backend.BuildPlan(workspaceRoot, policy)

	sideEffect := zeroSandbox.SideEffect(strings.ToLower(strings.TrimSpace(options.sideEffect)))
	if sideEffect == "" {
		sideEffect = zeroSandbox.SideEffectRead
	}
	autonomy := zeroSandbox.Autonomy(strings.ToLower(strings.TrimSpace(options.autonomy)))
	if autonomy == "" {
		autonomy = zeroSandbox.AutonomyHigh
	}

	requestArgs := map[string]any{}
	scopeAbs := workspaceRoot
	if path := strings.TrimSpace(options.path); path != "" {
		requestArgs["path"] = path
		if filepath.IsAbs(path) {
			scopeAbs = filepath.Clean(path)
		} else {
			scopeAbs = filepath.Join(workspaceRoot, path)
		}
	}

	engine := zeroSandbox.NewEngine(zeroSandbox.EngineOptions{
		WorkspaceRoot: workspaceRoot,
		Policy:        policy,
		Store:         store,
		Backend:       backend,
	})
	ctx, stop := signalContext()
	defer stop()
	decision := engine.Evaluate(ctx, zeroSandbox.Request{
		WorkspaceRoot:  workspaceRoot,
		ToolName:       options.tool,
		SideEffect:     sideEffect,
		PermissionMode: zeroSandbox.PermissionModeAsk,
		Autonomy:       autonomy,
		Args:           requestArgs,
		Reason:         strings.TrimSpace(options.reason),
	})

	lookup, err := store.Lookup(options.tool, scopeAbs, autonomy)
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}

	report := sandboxCheckReport{
		Tool:     strings.TrimSpace(options.tool),
		Plan:     zerocommands.SandboxPlanSnapshotFromPlan(plan),
		Decision: zerocommands.SandboxDecisionSnapshotFromDecision(decision),
		Grant:    zerocommands.SandboxGrantMatchSnapshotFromLookup(options.tool, lookup),
	}

	if options.json {
		if err := writePrettyJSON(stdout, report); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintln(stdout, formatSandboxCheckReport(report)); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func parseSandboxCheckArgs(args []string) (sandboxCheckOptions, bool, error) {
	options := sandboxCheckOptions{}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "-h" || arg == "--help" || arg == "help":
			return options, true, nil
		case arg == "--json":
			options.json = true
		case arg == "--side-effect" || arg == "--path" || arg == "--autonomy" || arg == "--reason":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			assignSandboxCheckFlag(&options, arg, value)
			index = next
		case strings.HasPrefix(arg, "--side-effect="):
			value, err := requiredInlineFlagValue(arg, "--side-effect")
			if err != nil {
				return options, false, err
			}
			options.sideEffect = value
		case strings.HasPrefix(arg, "--path="):
			value, err := requiredInlineFlagValue(arg, "--path")
			if err != nil {
				return options, false, err
			}
			options.path = value
		case strings.HasPrefix(arg, "--autonomy="):
			value, err := requiredInlineFlagValue(arg, "--autonomy")
			if err != nil {
				return options, false, err
			}
			options.autonomy = value
		case strings.HasPrefix(arg, "--reason="):
			value, err := requiredInlineFlagValue(arg, "--reason")
			if err != nil {
				return options, false, err
			}
			options.reason = value
		case strings.HasPrefix(arg, "-"):
			return options, false, execUsageError{fmt.Sprintf("unknown flag %q", arg)}
		default:
			if options.tool != "" {
				return options, false, execUsageError{fmt.Sprintf("unexpected argument %q", arg)}
			}
			options.tool = arg
		}
	}
	return options, false, nil
}

func assignSandboxCheckFlag(options *sandboxCheckOptions, flag string, value string) {
	switch flag {
	case "--side-effect":
		options.sideEffect = value
	case "--path":
		options.path = value
	case "--autonomy":
		options.autonomy = value
	case "--reason":
		options.reason = value
	}
}

func formatSandboxCheckReport(report sandboxCheckReport) string {
	lines := []string{
		"Sandbox check: " + report.Tool,
		"decision: " + report.Decision.Action,
	}
	if reason := strings.TrimSpace(report.Decision.Reason); reason != "" {
		lines = append(lines, "reason: "+reason)
	}
	risk := report.Decision.Risk
	riskLine := "risk: " + risk.Level
	if len(risk.Categories) > 0 {
		riskLine += " [" + strings.Join(risk.Categories, ", ") + "]"
	}
	lines = append(lines, riskLine)
	if violation := report.Decision.Violation; violation != nil {
		line := "violation: [" + violation.Code + "] " + violation.Reason
		if violation.Path != "" {
			line += " (path: " + violation.Path + ")"
		}
		lines = append(lines, line)
	}
	if report.Grant.Matched && report.Grant.Grant != nil {
		lines = append(lines, "grant: matched "+report.Grant.Grant.Decision+" (maxAutonomy "+report.Grant.Grant.MaxAutonomy+")")
	} else {
		lines = append(lines, "grant: none recorded for this tool")
	}
	lines = append(lines,
		"policy: mode="+report.Plan.Policy.EffectiveMode+" network="+report.Plan.Policy.Network+fmt.Sprintf(" enforceWorkspace=%t", report.Plan.Policy.EnforceWorkspace),
		"backend: "+report.Plan.Backend.Name+fmt.Sprintf(" (available=%t)", report.Plan.Backend.Available),
	)
	if len(report.Plan.Restrictions) > 0 {
		lines = append(lines, "restrictions:")
		for _, restriction := range report.Plan.Restrictions {
			lines = append(lines, "  - "+restriction)
		}
	}
	return strings.Join(lines, "\n")
}

func writeSandboxCheckHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero sandbox check <tool> [flags]

Evaluates the sandbox decision for a hypothetical tool action against the
resolved policy, and prints the plan, the decision (allow/prompt/deny with risk
and any violation), and any persistent grant that matches the tool. Secrets in
grant reasons are redacted.

Flags:
      --side-effect <kind>   read | write | shell | network | none (default read)
      --path <path>          Target path for the action (read/write checks)
      --autonomy <level>     low | medium | high (default high)
      --reason <text>        Reason recorded with the request
      --json                 Print the machine-readable snapshot
  -h, --help                 Show this help
`)
	return err
}
