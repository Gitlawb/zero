package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/Gitlawb/zero/internal/update"
)

type updateOptions struct {
	check bool
	json  bool
}

func runUpdate(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	options, help, err := parseUpdateArgs(args)
	if err != nil {
		return writeUsageError(stderr, err.Error())
	}
	if help {
		if err := writeUpdateHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if !options.check {
		return writeUsageError(stderr, "Only `zero update --check` is available right now.")
	}
	result, err := deps.checkUpdate(context.Background(), update.Options{CurrentVersion: version})
	if err != nil {
		return writeAppError(stderr, "Could not check for updates: "+err.Error(), exitCrash)
	}
	if options.json {
		if err := writePrettyJSON(stdout, result); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintln(stdout, update.Format(result)); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func parseUpdateArgs(args []string) (updateOptions, bool, error) {
	options := updateOptions{}
	for _, arg := range args {
		switch arg {
		case "-h", "--help", "help":
			return options, true, nil
		case "--check":
			options.check = true
		case "--json":
			options.json = true
		default:
			return options, false, execUsageError{fmt.Sprintf("unknown update flag %q", arg)}
		}
	}
	return options, false, nil
}

func writeUpdateHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero update --check [flags]

Flags:
      --check    Check the latest GitHub release without installing
      --json     Print the update check result as JSON
  -h, --help     Show this help
`)
	return err
}
