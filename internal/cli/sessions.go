package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/Gitlawb/zero/internal/redaction"
	"github.com/Gitlawb/zero/internal/sessions"
)

type sessionCommandOptions struct {
	json bool
}

func runSessions(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	command, remaining, options, help, err := parseSessionsArgs(args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if err := writeSessionsHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}

	store := deps.newSessionStore()
	switch command {
	case "list":
		return runSessionsList(store, options, stdout, stderr)
	case "children":
		if len(remaining) != 1 {
			return writeExecUsageError(stderr, "sessions children requires a session id")
		}
		return runSessionsChildren(store, remaining[0], options, stdout, stderr)
	case "lineage":
		if len(remaining) != 1 {
			return writeExecUsageError(stderr, "sessions lineage requires a session id")
		}
		return runSessionsLineage(store, remaining[0], options, stdout, stderr)
	case "tree":
		if len(remaining) != 1 {
			return writeExecUsageError(stderr, "sessions tree requires a session id")
		}
		return runSessionsTree(store, remaining[0], options, stdout, stderr)
	default:
		return writeExecUsageError(stderr, fmt.Sprintf("unknown sessions command %q", command))
	}
}

func parseSessionsArgs(args []string) (string, []string, sessionCommandOptions, bool, error) {
	options := sessionCommandOptions{}
	command := "list"
	remaining := []string{}
	for _, arg := range args {
		switch arg {
		case "-h", "--help", "help":
			return command, remaining, options, true, nil
		case "--json":
			options.json = true
		default:
			if strings.HasPrefix(arg, "-") {
				return command, remaining, options, false, execUsageError{fmt.Sprintf("unknown sessions flag %q", arg)}
			}
			if command == "list" && len(remaining) == 0 && isSessionsCommand(arg) {
				command = arg
				continue
			}
			remaining = append(remaining, arg)
		}
	}
	return command, remaining, options, false, nil
}

func isSessionsCommand(command string) bool {
	switch command {
	case "list", "children", "lineage", "tree":
		return true
	default:
		return false
	}
}

func runSessionsList(store *sessions.Store, options sessionCommandOptions, stdout io.Writer, stderr io.Writer) int {
	items, err := store.List()
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	if options.json {
		if err := writePrettyJSON(stdout, redaction.RedactValue(items, redaction.Options{})); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintln(stdout, formatSessionsList(items)); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func runSessionsChildren(store *sessions.Store, sessionID string, options sessionCommandOptions, stdout io.Writer, stderr io.Writer) int {
	items, err := store.ListChildren(sessionID)
	if err != nil {
		return writeSessionCommandError(stderr, err)
	}
	if options.json {
		if err := writePrettyJSON(stdout, redaction.RedactValue(items, redaction.Options{})); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintln(stdout, formatSessionsList(items)); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func runSessionsLineage(store *sessions.Store, sessionID string, options sessionCommandOptions, stdout io.Writer, stderr io.Writer) int {
	lineage, err := store.Lineage(sessionID)
	if err != nil {
		return writeSessionCommandError(stderr, err)
	}
	if options.json {
		if err := writePrettyJSON(stdout, redaction.RedactValue(lineage, redaction.Options{})); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	ids := make([]string, 0, len(lineage))
	for _, session := range lineage {
		ids = append(ids, redact(session.SessionID))
	}
	if _, err := fmt.Fprintln(stdout, strings.Join(ids, " -> ")); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func runSessionsTree(store *sessions.Store, sessionID string, options sessionCommandOptions, stdout io.Writer, stderr io.Writer) int {
	tree, err := store.Tree(sessionID)
	if err != nil {
		return writeSessionCommandError(stderr, err)
	}
	if options.json {
		if err := writePrettyJSON(stdout, redaction.RedactValue(tree, redaction.Options{})); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprint(stdout, formatSessionTree(tree)); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func writeSessionCommandError(stderr io.Writer, err error) int {
	message := strings.TrimPrefix(err.Error(), "zero session")
	if message != err.Error() {
		message = "Zero session" + message
	}
	if strings.Contains(message, "not found") || strings.Contains(message, "invalid zero session id") {
		return writeExecUsageError(stderr, message)
	}
	return writeAppError(stderr, message, exitCrash)
}

func formatSessionsList(items []sessions.Metadata) string {
	if len(items) == 0 {
		return "No Zero sessions found."
	}
	lines := []string{fmt.Sprintf("Zero sessions (%d):", len(items))}
	for _, session := range items {
		lines = append(lines, "  "+formatSessionLine(session))
	}
	return strings.Join(lines, "\n")
}

func formatSessionTree(node sessions.TreeNode) string {
	lines := []string{"Zero session tree:"}
	appendSessionTree(&lines, node, "")
	return strings.Join(lines, "\n") + "\n"
}

func appendSessionTree(lines *[]string, node sessions.TreeNode, prefix string) {
	*lines = append(*lines, prefix+formatSessionLine(node.Session))
	for _, child := range node.Children {
		appendSessionTree(lines, child, prefix+"  ")
	}
}

func formatSessionLine(session sessions.Metadata) string {
	parts := []string{"- " + redact(session.SessionID)}
	if session.SessionKind != "" {
		parts = append(parts, "["+redact(string(session.SessionKind))+"]")
	}
	if session.Title != "" {
		parts = append(parts, redact(session.Title))
	}
	details := []string{}
	if session.AgentName != "" {
		details = append(details, "agent="+redact(session.AgentName))
	}
	if session.TaskID != "" {
		details = append(details, "task="+redact(session.TaskID))
	}
	if session.ParentSessionID != "" {
		details = append(details, "parent="+redact(session.ParentSessionID))
	}
	if session.ModelID != "" {
		details = append(details, "model="+redact(session.ModelID))
	}
	if len(details) > 0 {
		parts = append(parts, "("+strings.Join(details, ", ")+")")
	}
	return strings.Join(parts, " ")
}

func redact(value string) string {
	return redaction.RedactString(value, redaction.Options{})
}

func writeSessionsHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero sessions <command> [flags]

Commands:
  list                  List local Zero sessions
  children <id>         List direct child sessions for a parent session
  lineage <id>          Print the root-to-session lineage path
  tree <id>             Print a child-session tree

Flags:
      --json            Print JSON output
  -h, --help            Show this help
`)
	return err
}
