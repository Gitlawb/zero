package cli

import (
	"fmt"
	"io"
	"strings"

	zeroSandbox "github.com/Gitlawb/zero/internal/sandbox"
)

type sandboxCommandOptions struct {
	json     bool
	confirm  bool
	autonomy string
	reason   string
}

func runSandbox(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	if len(args) == 0 {
		return writeExecUsageError(stderr, "sandbox subcommand required. Use `zero sandbox policy` or `zero sandbox grants list`.")
	}
	switch args[0] {
	case "-h", "--help", "help":
		if err := writeSandboxHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	case "policy":
		return runSandboxPolicy(args[1:], stdout, stderr, deps)
	case "grants":
		return runSandboxGrants(args[1:], stdout, stderr, deps)
	default:
		return writeExecUsageError(stderr, fmt.Sprintf("unknown sandbox subcommand %q", args[0]))
	}
}

func runSandboxPolicy(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	options, help, err := parseSandboxCommandOptions(args, false)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if err := writeSandboxPolicyHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	workspaceRoot, err := resolveWorkspaceRoot("", deps)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	store, err := deps.newSandboxStore()
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	policy := zeroSandbox.DefaultPolicy()
	backend := deps.selectSandboxBackend(zeroSandbox.BackendOptions{})
	plan := backend.BuildPlan(workspaceRoot, policy)
	if options.json {
		payload := struct {
			Policy     zeroSandbox.Policy      `json:"policy"`
			Backend    zeroSandbox.Backend     `json:"backend"`
			Plan       zeroSandbox.BackendPlan `json:"plan"`
			GrantsPath string                  `json:"grantsPath"`
		}{
			Policy:     policy,
			Backend:    backend,
			Plan:       plan,
			GrantsPath: store.FilePath(),
		}
		if err := writePrettyJSON(stdout, payload); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintln(stdout, formatSandboxPolicy(workspaceRoot, policy, backend, plan, store.FilePath())); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func runSandboxGrants(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	if len(args) == 0 {
		return writeExecUsageError(stderr, "sandbox grants subcommand required. Use `zero sandbox grants list`.")
	}
	switch args[0] {
	case "-h", "--help", "help":
		if err := writeSandboxGrantsHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	case "list":
		options, help, err := parseSandboxCommandOptions(args[1:], false)
		if err != nil {
			return writeExecUsageError(stderr, err.Error())
		}
		if help {
			if err := writeSandboxGrantsHelp(stdout); err != nil {
				return exitCrash
			}
			return exitSuccess
		}
		store, err := deps.newSandboxStore()
		if err != nil {
			return writeAppError(stderr, err.Error(), exitCrash)
		}
		grants, err := store.List()
		if err != nil {
			return writeAppError(stderr, err.Error(), exitCrash)
		}
		if options.json {
			if err := writePrettyJSON(stdout, struct {
				Grants []zeroSandbox.Grant `json:"grants"`
			}{Grants: grants}); err != nil {
				return exitCrash
			}
			return exitSuccess
		}
		if _, err := fmt.Fprintln(stdout, zeroSandbox.FormatGrantList(grants)); err != nil {
			return exitCrash
		}
		return exitSuccess
	case "allow", "deny":
		return runSandboxGrantSet(args[0], args[1:], stdout, stderr, deps)
	case "revoke":
		return runSandboxGrantRevoke(args[1:], stdout, stderr, deps)
	case "clear":
		return runSandboxGrantClear(args[1:], stdout, stderr, deps)
	default:
		return writeExecUsageError(stderr, fmt.Sprintf("unknown sandbox grants subcommand %q", args[0]))
	}
}

func runSandboxGrantSet(command string, args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	options, positional, help, err := parseSandboxPositionalOptions(args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if err := writeSandboxGrantSetHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if len(positional) != 1 {
		return writeExecUsageError(stderr, "usage: zero sandbox grants "+command+" <tool> [--auto low|medium|high] [--reason text] [--json]")
	}
	decision := zeroSandbox.GrantAllow
	if command == "deny" {
		decision = zeroSandbox.GrantDeny
	}
	store, err := deps.newSandboxStore()
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	grant, err := store.Grant(zeroSandbox.GrantInput{
		ToolName:    positional[0],
		Decision:    decision,
		MaxAutonomy: zeroSandbox.Autonomy(options.autonomy),
		Reason:      options.reason,
	})
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if options.json {
		if err := writePrettyJSON(stdout, struct {
			Grant zeroSandbox.Grant `json:"grant"`
		}{Grant: grant}); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintf(stdout, "Sandbox grant saved: %s [%s/%s]\n", grant.ToolName, grant.Decision, grant.MaxAutonomy); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func runSandboxGrantRevoke(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	options, positional, help, err := parseSandboxPositionalOptions(args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if err := writeSandboxGrantRevokeHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if len(positional) != 1 {
		return writeExecUsageError(stderr, "usage: zero sandbox grants revoke <tool> [--json]")
	}
	store, err := deps.newSandboxStore()
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	revoked, err := store.Revoke(positional[0])
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if options.json {
		if err := writePrettyJSON(stdout, struct {
			Revoked int `json:"revoked"`
		}{Revoked: revoked}); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintf(stdout, "Sandbox grants revoked: %d\n", revoked); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func runSandboxGrantClear(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	options, help, err := parseSandboxCommandOptions(args, true)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if err := writeSandboxGrantClearHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if !options.confirm {
		return writeExecUsageError(stderr, "zero sandbox grants clear requires --confirm")
	}
	store, err := deps.newSandboxStore()
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	cleared, err := store.Clear()
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	if options.json {
		if err := writePrettyJSON(stdout, struct {
			Cleared int `json:"cleared"`
		}{Cleared: cleared}); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintf(stdout, "Sandbox grants cleared: %d\n", cleared); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func parseSandboxCommandOptions(args []string, allowConfirm bool) (sandboxCommandOptions, bool, error) {
	options := sandboxCommandOptions{autonomy: string(zeroSandbox.AutonomyLow)}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "-h" || arg == "--help" || arg == "help":
			return options, true, nil
		case arg == "--json":
			options.json = true
		case allowConfirm && arg == "--confirm":
			options.confirm = true
		case strings.HasPrefix(arg, "-"):
			return options, false, execUsageError{fmt.Sprintf("unknown sandbox flag %q", arg)}
		default:
			return options, false, execUsageError{fmt.Sprintf("unexpected sandbox argument %q", arg)}
		}
	}
	return options, false, nil
}

func parseSandboxPositionalOptions(args []string) (sandboxCommandOptions, []string, bool, error) {
	options := sandboxCommandOptions{autonomy: string(zeroSandbox.AutonomyLow)}
	positional := []string{}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "-h" || arg == "--help" || arg == "help":
			return options, positional, true, nil
		case arg == "--json":
			options.json = true
		case arg == "--auto":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, positional, false, err
			}
			options.autonomy = value
			index = next
		case strings.HasPrefix(arg, "--auto="):
			options.autonomy = strings.TrimSpace(strings.TrimPrefix(arg, "--auto="))
		case arg == "--reason":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, positional, false, err
			}
			options.reason = value
			index = next
		case strings.HasPrefix(arg, "--reason="):
			options.reason = strings.TrimSpace(strings.TrimPrefix(arg, "--reason="))
		case strings.HasPrefix(arg, "-"):
			return options, positional, false, execUsageError{fmt.Sprintf("unknown sandbox grants flag %q", arg)}
		default:
			positional = append(positional, strings.TrimSpace(arg))
		}
	}
	if _, err := zeroSandbox.NormalizeAutonomy(zeroSandbox.Autonomy(options.autonomy)); err != nil {
		return options, positional, false, execUsageError{err.Error()}
	}
	return options, positional, false, nil
}

func formatSandboxPolicy(workspaceRoot string, policy zeroSandbox.Policy, backend zeroSandbox.Backend, plan zeroSandbox.BackendPlan, grantsPath string) string {
	lines := []string{
		"Zero sandbox policy",
		"root: " + workspaceRoot,
		"mode: " + string(policy.Mode),
		"network: " + string(policy.Network),
		"backend: " + string(backend.Name),
		"support_level: " + string(plan.SupportLevel),
	}
	if backend.Platform != "" {
		lines = append(lines, "backend_platform: "+backend.Platform)
	}
	lines = append(lines,
		"backend_available: "+fmt.Sprintf("%t", backend.Available),
		"backend_fallback: "+fmt.Sprintf("%t", backend.Fallback),
		"backend_command_wrapping: "+fmt.Sprintf("%t", backend.CommandWrapping),
		"backend_native_isolation: "+fmt.Sprintf("%t", backend.NativeIsolation),
		"grants: "+grantsPath,
	)
	if backend.Message != "" {
		lines = append(lines, "backend_message: "+backend.Message)
	}
	for _, warning := range plan.Warnings {
		lines = append(lines, "warning: "+warning)
	}
	return strings.Join(lines, "\n")
}

func writeSandboxHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero sandbox <command>

Commands:
  policy      Inspect active sandbox policy and platform backend
  grants      Manage persistent sandbox grants

`)
	return err
}

func writeSandboxPolicyHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero sandbox policy [flags]

Flags:
      --json              Print JSON output
  -h, --help              Show this help
`)
	return err
}

func writeSandboxGrantsHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero sandbox grants <command>

Commands:
  list        List persistent sandbox grants
  allow       Persistently allow a tool up to an autonomy level
  deny        Persistently deny a tool up to an autonomy level
  revoke      Revoke a tool grant
  clear       Clear all sandbox grants
`)
	return err
}

func writeSandboxGrantSetHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero sandbox grants allow <tool> [flags]
  zero sandbox grants deny <tool> [flags]

Flags:
      --auto <level>      Maximum autonomy covered by the grant
      --reason <text>     Human-readable reason for the grant
      --json              Print JSON output
  -h, --help              Show this help
`)
	return err
}

func writeSandboxGrantRevokeHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero sandbox grants revoke <tool> [flags]

Flags:
      --json              Print JSON output
  -h, --help              Show this help
`)
	return err
}

func writeSandboxGrantClearHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero sandbox grants clear --confirm [flags]

Flags:
      --confirm           Confirm removal of all sandbox grants
      --json              Print JSON output
  -h, --help              Show this help
`)
	return err
}
