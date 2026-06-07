package agent

import (
	_ "embed"
	"os"
	"path/filepath"
	"strings"
)

// coreSystemPrompt is the de-branded coding-craft instruction set: identity,
// autonomy, workflow, editing discipline, the testing gate, tool use, and
// communication style.
//
//go:embed system_prompt.md
var coreSystemPrompt string

// confirmationPolicy is the de-branded safety policy appended to the system
// prompt so the model self-polices before risky actions. The sandbox enforces a
// subset of these rules, but the model applies judgement first.
//
//go:embed confirmation_policy.md
var confirmationPolicy string

// fallbackSystemPrompt is used only if the embedded core prompt is somehow empty
// (it never should be) so a run always has a non-empty system turn.
const fallbackSystemPrompt = "You are Zero, a terminal coding agent. Help with the current workspace and use tools when needed."

// projectContextFiles are workspace docs injected into the system prompt so the
// agent honors project-specific conventions (mirrors AGENTS.md / CLAUDE.md). The
// first one found wins.
var projectContextFiles = []string{"AGENTS.md", "ZERO.md", ".zero/AGENTS.md"}

// maxProjectContextBytes caps how much of a project doc is injected so a large
// guidelines file can't blow the context budget.
const maxProjectContextBytes = 8 << 10 // 8 KiB

// buildSystemPrompt assembles the full system prompt for a run: the core
// coding-craft instructions, dynamic workspace context (cwd, git branch, project
// guidelines), and the safety confirmation policy. It is built once per run so
// every turn shares one (cacheable) system turn.
func buildSystemPrompt(options Options) string {
	core := strings.TrimSpace(coreSystemPrompt)
	if core == "" {
		core = fallbackSystemPrompt
	}
	sections := []string{core}
	if addendum := modelPromptAddendum(options.Model); addendum != "" {
		sections = append(sections, addendum)
	}
	if ws := workspaceContext(options.Cwd); ws != "" {
		sections = append(sections, ws)
	}
	if policy := strings.TrimSpace(confirmationPolicy); policy != "" {
		sections = append(sections, policy)
	}
	return strings.Join(sections, "\n\n")
}

// workspaceContext returns an <environment> block describing the working
// directory, git branch, and any project guideline doc, so the model grounds its
// work in the actual repo. Returns "" when cwd is unset (keeps headless/test
// runs deterministic).
func workspaceContext(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("<environment>\n")
	b.WriteString("Working directory: " + cwd + "\n")
	if branch := gitBranchForPrompt(cwd); branch != "" {
		b.WriteString("Git branch: " + branch + "\n")
	}
	b.WriteString("</environment>")

	for _, name := range projectContextFiles {
		data, err := os.ReadFile(filepath.Join(cwd, name))
		if err != nil {
			continue
		}
		content := strings.TrimSpace(string(data))
		if content == "" {
			continue
		}
		if len(content) > maxProjectContextBytes {
			content = content[:maxProjectContextBytes] + "\n… (truncated)"
		}
		b.WriteString("\n\n## Project guidelines (" + name + ")\n\n" + content)
		break // first match wins
	}
	return b.String()
}

// gitBranchForPrompt reads the current branch (or short SHA when detached) for
// cwd, handling both a regular checkout (.git dir) and a worktree (.git file).
// Returns "" on any problem — the prompt simply omits the branch segment.
func gitBranchForPrompt(cwd string) string {
	gitPath := filepath.Join(cwd, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return ""
	}
	headPath := filepath.Join(gitPath, "HEAD")
	if !info.IsDir() {
		data, err := os.ReadFile(gitPath)
		if err != nil {
			return ""
		}
		dir := strings.TrimPrefix(strings.TrimSpace(string(data)), "gitdir: ")
		if dir == "" {
			return ""
		}
		// In worktree mode the gitdir is often RELATIVE (e.g.
		// "gitdir: ../.git/worktrees/<name>") — resolve it against cwd, not the
		// process working directory, or HEAD lookup fails and we drop the branch.
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(cwd, dir)
		}
		headPath = filepath.Join(dir, "HEAD")
	}
	data, err := os.ReadFile(headPath)
	if err != nil {
		return ""
	}
	ref := strings.TrimSpace(string(data))
	if strings.HasPrefix(ref, "ref: ") {
		return strings.TrimPrefix(strings.TrimPrefix(ref, "ref: "), "refs/heads/")
	}
	if len(ref) >= 7 {
		return ref[:7]
	}
	return ref
}
