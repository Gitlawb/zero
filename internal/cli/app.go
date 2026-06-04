package cli

import (
	"fmt"
	"io"
)

const version = "0.1.0"

// Run executes the minimal Go CLI surface. It returns an exit code so tests can
// exercise command behavior without terminating the test process.
func Run(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		printHelp(stdout)
		return 0
	}

	switch args[0] {
	case "-h", "--help", "help":
		printHelp(stdout)
		return 0
	case "-v", "--version", "version":
		fmt.Fprintf(stdout, "zero %s\n", version)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		fmt.Fprintln(stderr, "Run zero --help for usage.")
		return 2
	}
}

func printHelp(w io.Writer) {
	fmt.Fprint(w, `ZERO terminal coding agent

Usage:
  zero [command]

Commands:
  help       Show this help
  version    Print version

Flags:
  -h, --help       Show this help
  -v, --version    Print version
`)
}
