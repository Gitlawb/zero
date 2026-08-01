// Package usercommands loads user-defined slash commands from markdown files.
//
// A user drops a file at `.zero/commands/<name>.md` (project, checked into the
// repo and shared with the team) or `<userConfigDir>/zero/commands/<name>.md`
// (personal), with optional YAML-style frontmatter:
//
//	---
//	description: Open a PR for the current branch
//	model: claude-sonnet-4.5
//	---
//	Create a pull request for the current branch. Title: $1. Summarize: $ARGUMENTS
//
// Typing `/<name> some args` expands the body template ($ARGUMENTS, $1..$N) and
// submits it as a normal prompt. This turns Zero's model-pulled skills into
// user-invokable, repo-checked-in team workflows. Project commands override
// user commands of the same name (mirrors the specialist scope precedence).
package usercommands

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Command is a user-defined slash command parsed from a markdown file.
type Command struct {
	Name        string // the `/name` (file basename, lowercased, sans .md)
	Description string // frontmatter `description:`, for help + autocomplete
	Model       string // optional frontmatter `model:` override
	Agent       string // optional frontmatter `agent:`/`mode:` routing
	Template    string // the markdown body, expanded on invocation
	Path        string // source file, for diagnostics
	Project     bool   // true if from the project `.zero/commands` dir
}

// Paths are the directories scanned for command files, project first.
type Paths struct {
	ProjectDir string
	UserDir    string
}

// DefaultPaths returns the project and user command directories. workspaceRoot
// is the repo root; userConfigDir is the OS config dir (os.UserConfigDir).
func DefaultPaths(workspaceRoot, userConfigDir string) Paths {
	p := Paths{}
	if strings.TrimSpace(workspaceRoot) != "" {
		p.ProjectDir = filepath.Join(workspaceRoot, ".zero", "commands")
	}
	if strings.TrimSpace(userConfigDir) != "" {
		p.UserDir = filepath.Join(userConfigDir, "zero", "commands")
	}
	return p
}

// Load reads every `*.md` command file under the given paths and returns them
// keyed by lowercased name, sorted by name. A project command shadows a user
// command of the same name. Unreadable files are skipped (best-effort);
// directories that do not exist are simply empty.
func Load(paths Paths) []Command {
	byName := map[string]Command{}
	// User first, then project — so the project entry overwrites on collision.
	for _, dir := range []string{paths.UserDir, paths.ProjectDir} {
		project := dir == paths.ProjectDir && dir != ""
		for _, cmd := range loadDir(dir, project) {
			byName[cmd.Name] = cmd
		}
	}
	out := make([]Command, 0, len(byName))
	for _, cmd := range byName {
		out = append(out, cmd)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func loadDir(dir string, project bool) []Command {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var cmds []Command
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		name := strings.ToLower(strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())))
		if !validCommandName(name) {
			continue
		}
		cmds = append(cmds, parseCommand(name, path, project, string(raw)))
	}
	return cmds
}

// validCommandName keeps file-sourced names to a safe slash-command shape so a
// stray file can't shadow a builtin via odd characters: lowercase letters,
// digits, and hyphens, non-empty.
func validCommandName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

func parseCommand(name, path string, project bool, raw string) Command {
	cmd := Command{Name: name, Path: path, Project: project}
	body := strings.ReplaceAll(raw, "\r\n", "\n")
	if fm, remainder, ok := splitFrontmatter(body); ok {
		body = remainder
		cmd.Description = frontmatterValue(fm, "description")
		cmd.Model = frontmatterValue(fm, "model")
		cmd.Agent = frontmatterValue(fm, "agent")
		if cmd.Agent == "" {
			cmd.Agent = frontmatterValue(fm, "mode")
		}
	}
	cmd.Template = strings.TrimSpace(body)
	if cmd.Description == "" {
		cmd.Description = "User command: /" + name
	}
	return cmd
}

// Expand substitutes the argument placeholders in a command template:
//
//	$ARGUMENTS      → all args, space-joined (the whole arg string)
//	$1 .. $9        → the Nth whitespace-separated positional arg ("" if absent)
//	$$              → a literal "$"
//
// A template with no placeholders is returned with the raw arg string appended
// on its own line, so a trivial command still receives the user's input.
func Expand(template, args string) string {
	args = strings.TrimSpace(args)
	positional := strings.Fields(args)

	if !strings.Contains(template, "$") {
		if args == "" {
			return template
		}
		return template + "\n\n" + args
	}

	var b strings.Builder
	runes := []rune(template)
	for i := 0; i < len(runes); i++ {
		if runes[i] != '$' || i == len(runes)-1 {
			b.WriteRune(runes[i])
			continue
		}
		next := runes[i+1]
		switch {
		case next == '$':
			b.WriteByte('$')
			i++
		case next >= '1' && next <= '9':
			idx := int(next - '1')
			if idx < len(positional) {
				b.WriteString(positional[idx])
			}
			i++
		case strings.HasPrefix(string(runes[i+1:]), "ARGUMENTS"):
			b.WriteString(args)
			i += len("ARGUMENTS")
		default:
			b.WriteRune('$')
		}
	}
	return b.String()
}

// --- frontmatter helpers (self-contained; mirror internal/skills) ------------

func splitFrontmatter(normalized string) (string, string, bool) {
	if !strings.HasPrefix(normalized, "---\n") {
		return "", "", false
	}
	lines := strings.Split(normalized, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", "", false
	}
	for index := 1; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) == "---" {
			return strings.Join(lines[1:index], "\n"), strings.Join(lines[index+1:], "\n"), true
		}
	}
	return "", "", false
}

func frontmatterValue(frontmatter, key string) string {
	prefix := strings.ToLower(key) + ":"
	for _, line := range strings.Split(frontmatter, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(trimmed), prefix) {
			return strings.Trim(strings.TrimSpace(trimmed[len(prefix):]), `"'`)
		}
	}
	return ""
}
