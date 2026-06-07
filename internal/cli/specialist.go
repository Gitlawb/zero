package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/Gitlawb/zero/internal/specialist"
)

type specialistOptions struct {
	json bool
}

func runSpecialists(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	command, remaining, options, help, err := parseSpecialistArgs(args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if err := writeSpecialistHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}

	if err := validateSpecialistCommand(command, remaining); err != nil {
		return writeExecUsageError(stderr, err.Error())
	}

	workspaceRoot, err := resolveWorkspaceRoot("", deps)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	paths, err := specialist.DefaultPaths(workspaceRoot)
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}

	switch command {
	case "list":
		return runSpecialistList(paths, options, stdout, stderr)
	case "show":
		return runSpecialistShow(paths, remaining[0], options, stdout, stderr)
	case "path":
		return runSpecialistPath(paths, options, stdout)
	default:
		return writeExecUsageError(stderr, fmt.Sprintf("unknown specialist command %q", command))
	}
}

func parseSpecialistArgs(args []string) (string, []string, specialistOptions, bool, error) {
	command := "list"
	commandExplicit := false
	remaining := []string{}
	options := specialistOptions{}
	for _, arg := range args {
		switch arg {
		case "-h", "--help", "help":
			return command, remaining, options, true, nil
		case "--json":
			options.json = true
		default:
			if strings.HasPrefix(arg, "-") {
				return command, remaining, options, false, fmt.Errorf("unknown specialist flag %q", arg)
			}
			if !commandExplicit {
				command = arg
				commandExplicit = true
			} else {
				remaining = append(remaining, arg)
			}
		}
	}
	return command, remaining, options, false, nil
}

func validateSpecialistCommand(command string, remaining []string) error {
	switch command {
	case "list":
		if len(remaining) != 0 {
			return fmt.Errorf("specialist list does not accept positional arguments")
		}
	case "show":
		if len(remaining) != 1 {
			return fmt.Errorf("specialist show requires a specialist name")
		}
	case "path":
		if len(remaining) != 0 {
			return fmt.Errorf("specialist path does not accept positional arguments")
		}
	default:
		return fmt.Errorf("unknown specialist command %q", command)
	}
	return nil
}

func runSpecialistList(paths specialist.Paths, options specialistOptions, stdout io.Writer, stderr io.Writer) int {
	result, err := specialist.Load(specialist.LoadOptions{Paths: paths})
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	if options.json {
		if err := writePrettyJSON(stdout, struct {
			Paths       specialist.Paths     `json:"paths"`
			Specialists []specialist.Summary `json:"specialists"`
			Warnings    []string             `json:"warnings,omitempty"`
		}{
			Paths:       result.Paths,
			Specialists: specialist.Summaries(result.Specialists),
			Warnings:    result.Warnings,
		}); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintln(stdout, specialist.FormatList(result)); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func runSpecialistShow(paths specialist.Paths, name string, options specialistOptions, stdout io.Writer, stderr io.Writer) int {
	result, err := specialist.Load(specialist.LoadOptions{Paths: paths})
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	manifest, ok := specialist.Find(result, name)
	if !ok {
		return writeExecUsageError(stderr, "Zero specialist not found: "+name)
	}
	if options.json {
		if err := writePrettyJSON(stdout, manifest); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintln(stdout, specialist.FormatShow(manifest)); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func runSpecialistPath(paths specialist.Paths, options specialistOptions, stdout io.Writer) int {
	if options.json {
		if err := writePrettyJSON(stdout, paths); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintln(stdout, specialist.FormatPaths(paths)); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func writeSpecialistHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero specialist [command] [flags]

Commands:
  list       List built-in, user, and project specialists
  show NAME  Show one specialist profile
  path       Print specialist directories
  help       Show this help

Flags:
  --json     Print JSON output
`)
	return err
}
