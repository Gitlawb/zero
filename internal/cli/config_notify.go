package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/Gitlawb/zero/internal/config"
)

// runConfigNotify implements `zero config notify`: with no flags it prints the
// current mode/focusMode; --mode/--focus update them via the same
// config.SetNotify writer the TUI /notify command uses, so all surfaces stay
// in lockstep; --reset blanks both fields so the resolver defaults apply.
func runConfigNotify(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	options, help, err := parseConfigNotifyArgs(args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if err := writeConfigNotifyHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}

	resolved, exitCode := resolveCommandCenterConfig(stderr, deps)
	if exitCode != exitSuccess {
		return exitCode
	}

	if options.mode != "" || options.focus != "" || options.reset {
		configPath, err := deps.userConfigPath()
		if err != nil {
			return writeAppError(stderr, err.Error(), exitCrash)
		}
		// Omitted flags preserve the current value — a full replace would let
		// `--mode bell` silently wipe a configured focusMode. --reset is the
		// only path that clears both fields.
		notify := config.NotifyConfig{
			Mode:      resolved.Notify.Mode,
			FocusMode: resolved.Notify.FocusMode,
		}
		if options.reset {
			notify = config.NotifyConfig{}
		} else {
			if options.mode != "" {
				notify.Mode = options.mode
			}
			if options.focus != "" {
				notify.FocusMode = options.focus
			}
		}
		if _, err := config.SetNotify(configPath, notify); err != nil {
			return writeAppError(stderr, err.Error(), exitUsage)
		}
		// Re-resolve so the printed value reflects what the next launch will
		// actually use (e.g. a reset shows the built-in defaults).
		resolved, exitCode = resolveCommandCenterConfig(stderr, deps)
		if exitCode != exitSuccess {
			return exitCode
		}
	}

	if options.json {
		if err := writePrettyJSON(stdout, map[string]any{
			"mode":      resolved.Notify.Mode,
			"focusMode": resolved.Notify.FocusMode,
		}); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	lines := []string{
		"Notify",
		"mode:      " + displayCLIValue(resolved.Notify.Mode, "(default)"),
		"focusMode: " + displayCLIValue(resolved.Notify.FocusMode, "(default)"),
	}
	if _, err := fmt.Fprintln(stdout, strings.Join(lines, "\n")); err != nil {
		return exitCrash
	}
	return exitSuccess
}

type configNotifyOptions struct {
	mode  string
	focus string
	reset bool
	json  bool
}

func parseConfigNotifyArgs(args []string) (configNotifyOptions, bool, error) {
	options := configNotifyOptions{}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "-h" || arg == "--help" || arg == "help":
			return options, true, nil
		case arg == "--json":
			options.json = true
		case arg == "--reset":
			options.reset = true
		case arg == "--mode":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.mode = value
			index = next
		case strings.HasPrefix(arg, "--mode="):
			value, err := requiredInlineFlagValue(arg, "--mode")
			if err != nil {
				return options, false, err
			}
			options.mode = value
		case arg == "--focus":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.focus = value
			index = next
		case strings.HasPrefix(arg, "--focus="):
			value, err := requiredInlineFlagValue(arg, "--focus")
			if err != nil {
				return options, false, err
			}
			options.focus = value
		case strings.HasPrefix(arg, "-"):
			return options, false, execUsageError{fmt.Sprintf("unknown flag %q", arg)}
		default:
			return options, false, execUsageError{fmt.Sprintf("unexpected argument %q", arg)}
		}
	}
	return options, false, nil
}

func writeConfigNotifyHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, "Usage:\n"+
		"  zero config notify [flags]\n"+
		"\n"+
		"Print or update the permission-prompt notify preference.\n"+
		"\n"+
		"When run with no flag, prints the current mode and focusMode (the resolver\n"+
		"defaults to \"both\" and \"unfocused\" when the config block is empty).\n"+
		"\n"+
		"Examples:\n"+
		"  zero config notify\n"+
		"  zero config notify --json\n"+
		"  zero config notify --mode both --focus unfocused\n"+
		"  zero config notify --mode off\n"+
		"  zero config notify --reset         # clear config so the resolver defaults apply\n"+
		"\n"+
		"Flags:\n"+
		"      --mode <off|bell|notify|both>       Notification mechanism\n"+
		"      --focus <unfocused|always|focused>  When the alert fires\n"+
		"      --reset                             Clear both fields so the resolver defaults apply\n"+
		"      --json                              Machine-readable output\n"+
		"  -h, --help                              Show this help\n")
	return err
}
