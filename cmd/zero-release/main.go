package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Gitlawb/zero/internal/release"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "zero-release command required. Use `zero-release package` or `zero-release verify`.")
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		if err := writeHelp(stdout); err != nil {
			return 1
		}
		return 0
	case "package":
		return runPackage(args[1:], stdout, stderr)
	case "verify":
		return runVerify(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown zero-release command %q\n", args[0])
		return 2
	}
}

func runPackage(args []string, stdout io.Writer, stderr io.Writer) int {
	options, help, err := parsePackageArgs(args)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err.Error())
		return 2
	}
	if help {
		if err := writePackageHelp(stdout); err != nil {
			return 1
		}
		return 0
	}
	result, err := release.Package(context.Background(), options)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "[zero] Release packaging failed: "+err.Error())
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "Packaged %s\n", result.ArchiveName)
	_, _ = fmt.Fprintf(stdout, "Wrote %s.sha256\n", result.ArchiveName)
	return 0
}

func runVerify(args []string, stdout io.Writer, stderr io.Writer) int {
	options, help, err := parseVerifyArgs(args)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err.Error())
		return 2
	}
	if help {
		if err := writeVerifyHelp(stdout); err != nil {
			return 1
		}
		return 0
	}
	results, err := release.VerifyReleaseChecksums(options)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "[zero] Release checksum verification failed: "+err.Error())
		return 1
	}
	for _, result := range results {
		_, _ = fmt.Fprintf(stdout, "Verified %s.sha256 (%s)\n", result.ArchiveName, result.ActualChecksum)
	}
	_, _ = fmt.Fprintf(stdout, "Verified %d release checksum(s)\n", len(results))
	return 0
}

func parsePackageArgs(args []string) (release.PackageOptions, bool, error) {
	options := release.PackageOptions{}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		flag, inlineValue := splitFlagValue(arg)
		switch flag {
		case "-h", "--help", "help":
			if inlineValue != "" {
				return options, false, fmt.Errorf("%s does not accept a value", flag)
			}
			return options, true, nil
		case "--root":
			value, next, err := readOptionValue(args, inlineValue, index, flag)
			if err != nil {
				return options, false, err
			}
			options.RootDir = value
			index = next
		case "--release-dir":
			value, next, err := readOptionValue(args, inlineValue, index, flag)
			if err != nil {
				return options, false, err
			}
			options.ReleaseDir = value
			index = next
		case "--staging-dir":
			value, next, err := readOptionValue(args, inlineValue, index, flag)
			if err != nil {
				return options, false, err
			}
			options.StagingRoot = value
			index = next
		case "--version":
			value, next, err := readOptionValue(args, inlineValue, index, flag)
			if err != nil {
				return options, false, err
			}
			options.Version = value
			index = next
		default:
			return options, false, fmt.Errorf("unknown package flag %q", arg)
		}
	}
	return options, false, nil
}

func parseVerifyArgs(args []string) (release.VerifyOptions, bool, error) {
	options := release.VerifyOptions{}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		flag, inlineValue := splitFlagValue(arg)
		switch flag {
		case "-h", "--help", "help":
			if inlineValue != "" {
				return options, false, fmt.Errorf("%s does not accept a value", flag)
			}
			return options, true, nil
		case "--dir":
			value, next, err := readOptionValue(args, inlineValue, index, flag)
			if err != nil {
				return options, false, err
			}
			options.ReleaseDir = value
			index = next
		default:
			if strings.HasPrefix(arg, "-") {
				return options, false, fmt.Errorf("unknown verify flag %q", arg)
			}
			if options.ReleaseDir != "" {
				return options, false, fmt.Errorf("unexpected argument: %s", arg)
			}
			options.ReleaseDir = arg
		}
	}
	return options, false, nil
}

func splitFlagValue(arg string) (string, string) {
	flag, value, ok := strings.Cut(arg, "=")
	if !ok {
		return arg, ""
	}
	return flag, value
}

func readOptionValue(args []string, inlineValue string, index int, flag string) (string, int, error) {
	if inlineValue != "" {
		return inlineValue, index, nil
	}
	if strings.Contains(args[index], "=") {
		return "", index, fmt.Errorf("%s requires a value", flag)
	}
	next := index + 1
	if next >= len(args) || strings.HasPrefix(args[next], "-") {
		return "", index, fmt.Errorf("%s requires a value", flag)
	}
	return args[next], next, nil
}

func writeHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero-release <command>

Commands:
  package    Build and package the current platform release archive
  verify     Verify release archive checksums
`)
	return err
}

func writePackageHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero-release package [flags]

Builds the Go-native zero binary, stages npm wrapper files, writes a release
archive, and writes the matching SHA-256 checksum file.

Flags:
      --root <path>         Repository root (default: current directory)
      --release-dir <path>  Release output directory (default: dist/release)
      --staging-dir <path>  Package staging root (default: dist/package)
      --version <version>   Release version (default: package.json version)
  -h, --help                Show this help
`)
	return err
}

func writeVerifyHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero-release verify [--dir <path>]

Verifies that every release archive has a matching .sha256 file and digest.

Flags:
      --dir <path>  Release directory to verify (default: dist/release)
  -h, --help        Show this help
`)
	return err
}
