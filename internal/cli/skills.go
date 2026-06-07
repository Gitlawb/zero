package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/Gitlawb/zero/internal/redaction"
	"github.com/Gitlawb/zero/internal/skills"
)

type skillListOptions struct {
	json bool
}

func runSkills(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	command := "list"
	rest := args
	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help", "help":
			if err := writeSkillsHelp(stdout); err != nil {
				return exitCrash
			}
			return exitSuccess
		case "list":
			command, rest = "list", args[1:]
		default:
			// Treat a leading flag (e.g. --json) as belonging to the implicit
			// `list` command so `zero skills --json` works like `zero plugins`.
			if !strings.HasPrefix(args[0], "-") {
				return writeExecUsageError(stderr, fmt.Sprintf("unknown skills subcommand %q", args[0]))
			}
		}
	}

	switch command {
	case "list":
		options, help, err := parseSkillListArgs(rest)
		if err != nil {
			return writeExecUsageError(stderr, err.Error())
		}
		if help {
			if err := writeSkillsListHelp(stdout); err != nil {
				return exitCrash
			}
			return exitSuccess
		}
		return runSkillsList(deps.skillsDir(), options, stdout, stderr)
	default:
		return writeExecUsageError(stderr, fmt.Sprintf("unknown skills subcommand %q", command))
	}
}

func runSkillsList(dir string, options skillListOptions, stdout io.Writer, stderr io.Writer) int {
	discovered, err := skills.List(dir)
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	if options.json {
		payload := struct {
			Skills []skills.Skill `json:"skills"`
		}{Skills: discovered}
		if err := writePrettyJSON(stdout, redaction.RedactValue(payload, redaction.Options{})); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	output := redaction.RedactString(formatSkillList(discovered, dir), redaction.Options{})
	if _, err := fmt.Fprintln(stdout, output); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func formatSkillList(discovered []skills.Skill, dir string) string {
	if len(discovered) == 0 {
		return fmt.Sprintf("No Zero skills found in %s.", dir)
	}
	lines := []string{"Zero Skills:"}
	for _, skill := range discovered {
		line := "  " + skill.Name
		if skill.Description != "" {
			line += " - " + skill.Description
		}
		lines = append(lines, line)
		lines = append(lines, "    "+skill.Path)
	}
	return strings.Join(lines, "\n")
}

func parseSkillListArgs(args []string) (skillListOptions, bool, error) {
	options := skillListOptions{}
	for _, arg := range args {
		switch arg {
		case "-h", "--help", "help":
			return options, true, nil
		case "--json":
			options.json = true
		default:
			return options, false, execUsageError{fmt.Sprintf("unknown skills list flag %q", arg)}
		}
	}
	return options, false, nil
}

func writeSkillsHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero skills <command>

Commands:
  list    List discovered Zero skills
`)
	return err
}

func writeSkillsListHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero skills list [flags]

Flags:
      --json    Print discovered skills as JSON
  -h, --help    Show this help
`)
	return err
}
