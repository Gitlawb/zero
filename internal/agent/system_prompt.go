package agent

import (
	_ "embed"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/Gitlawb/zero/internal/repomap"
	"github.com/Gitlawb/zero/internal/workspaceseed"
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
// agent honors project-specific conventions (mirrors AGENTS.md / CLAUDE.md).
// The first match at each directory level wins; the loader walks the chain
// from the git root down to the cwd and injects the matches in
// general-to-specific order.
var projectContextFiles = []string{"AGENTS.md", "ZERO.md", ".zero/AGENTS.md"}

// maxProjectContextBytes caps how much of a single project doc is injected so
// a large guidelines file can't blow the context budget.
const maxProjectContextBytes = 8 << 10 // 8 KiB per file

// maxProjectContextTotalBytes caps the total bytes across all matched
// project guideline files. With path-walking, multiple files can match (root
// + sub-tree), so a per-file cap alone is not enough.
const maxProjectContextTotalBytes = 32 << 10 // 32 KiB total

// maxRepoMapContextBytes keeps the repository map useful but compact enough to
// remain stable across normal agent turns.
const maxRepoMapContextBytes = 4 << 10 // 4 KiB

const (
	workspaceSeedMaxLines = 12
	workspaceSeedWidth    = 100
)

// buildSystemPrompt assembles the full system prompt for a run: the core
// coding-craft instructions, dynamic workspace context (cwd, git branch, project
// guidelines), and the safety confirmation policy. It is built once per run so
// every turn shares one (cacheable) system turn.
func buildSystemPrompt(options Options) string {
	core := strings.TrimSpace(options.SystemPrompt)
	if core == "" {
		core = strings.TrimSpace(coreSystemPrompt)
	}
	if core == "" {
		core = fallbackSystemPrompt
	}
	sections := []string{core}
	if addendum := modelPromptAddendum(options.Model); addendum != "" {
		sections = append(sections, addendum)
	}
	if session := sessionRuntimeContext(options); session != "" {
		sections = append(sections, session)
	}
	if prefixes := approvedCommandPrefixContext(options); prefixes != "" {
		sections = append(sections, prefixes)
	}
	if seed := workspaceSeedContext(options.Cwd); seed != "" {
		sections = append(sections, seed)
	}
	if ws := workspaceContext(options.Cwd); ws != "" {
		sections = append(sections, ws)
	}
	if delegation := specialistDelegationContext(options); delegation != "" {
		sections = append(sections, delegation)
	}
	if style := responseStyleContext(options); style != "" {
		sections = append(sections, style)
	}
	if policy := strings.TrimSpace(confirmationPolicy); policy != "" {
		sections = append(sections, policy)
	}
	return strings.Join(sections, "\n\n")
}

// responseStyleContext renders the operator-selected reply style (TUI /style) as
// a system-prompt directive so the choice actually shapes responses. "balanced"
// (the default) and unknown/empty values add nothing, keeping the prompt
// byte-identical to the pre-style behavior.
func responseStyleContext(options Options) string {
	switch strings.ToLower(strings.TrimSpace(options.ResponseStyle)) {
	case "concise":
		return "Response style: concise. Lead with the result and keep answers short and direct; omit preamble, restating the question, and any explanation not needed to be correct."
	case "explanatory":
		return "Response style: explanatory. Explain the reasoning behind your answers and changes, surface relevant trade-offs, and add brief context a learner would want — while staying on task and not padding."
	case "review":
		return "Response style: review. Work like a critical reviewer: call out risks, edge cases, and unstated assumptions, flag anything questionable, and prefer precise, evidence-backed statements over reassurance."
	default:
		return ""
	}
}

func approvedCommandPrefixContext(options Options) string {
	if options.Sandbox == nil {
		return ""
	}
	prefixes := options.Sandbox.ApprovedCommandPrefixes()
	if len(prefixes) == 0 {
		return ""
	}
	lines := make([]string, 0, len(prefixes))
	for _, grant := range prefixes {
		if len(grant.Prefix) == 0 {
			continue
		}
		lines = append(lines, "- "+grant.ToolName+": "+strings.Join(grant.Prefix, " "))
	}
	if len(lines) == 0 {
		return ""
	}
	return "## Approved Command Prefixes\n\nThe following command prefixes have already been approved and do not need another permission prompt:\n" + strings.Join(lines, "\n")
}

// specialistDelegationContext nudges the orchestrator to offload read-heavy or
// parallelizable work to a specialist sub-agent via the Task tool, keeping large
// tool outputs out of the main context. It renders only when specialists are
// known (which is only where the Task tool is actually registered), so a run with
// no delegatable specialists produces the previous prompt unchanged.
func specialistDelegationContext(options Options) string {
	if len(options.Specialists) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<specialists>\n")
	b.WriteString("Delegate focused or read-heavy work to a specialist sub-agent with the Task tool instead of doing it inline. ")
	b.WriteString("When a request matches a specialist's purpose, delegate to it proactively — you do not need the user to ask first. ")
	b.WriteString("This keeps large tool outputs — searches, file dumps, multi-step exploration — out of your own context, so you stay fast and token-efficient. ")
	b.WriteString("Prefer delegating codebase search and exploration; for independent subtasks, launch several specialists in parallel. Handle small, direct edits yourself.\n")
	b.WriteString("Available specialists (call Task with the matching name when the task fits its purpose):\n")
	for _, info := range options.Specialists {
		name := strings.TrimSpace(info.Name)
		if name == "" {
			continue
		}
		if desc := strings.TrimSpace(info.WhenToUse); desc != "" {
			b.WriteString("- " + name + ": " + desc + "\n")
		} else {
			b.WriteString("- " + name + "\n")
		}
	}
	b.WriteString("</specialists>")
	return b.String()
}

func sessionRuntimeContext(options Options) string {
	provider := strings.TrimSpace(options.ProviderName)
	model := strings.TrimSpace(options.Model)
	if provider == "" && model == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("<session>\n")
	if provider != "" {
		b.WriteString("Active provider: " + provider + "\n")
	}
	if model != "" {
		b.WriteString("Active model: " + model + "\n")
	}
	b.WriteString("Use the active provider/model above when answering questions about what is currently running. Persisted config commands may show saved defaults that differ from this live run/session.\n")
	b.WriteString("</session>")
	return b.String()
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
	b.WriteString("Operating system: " + runtime.GOOS + "\n")
	if runtime.GOOS == "windows" {
		b.WriteString("Shell syntax: Windows cmd.exe syntax for bash tool; prefer the cwd argument instead of cd when changing directories.\n")
	} else {
		b.WriteString("Shell syntax: /bin/sh syntax for bash tool; prefer the cwd argument instead of cd when changing directories.\n")
	}
	if branch := gitBranchForPrompt(cwd); branch != "" {
		b.WriteString("Git branch: " + branch + "\n")
	}
	b.WriteString("</environment>")

	b.WriteString(projectGuidelines(cwd, findProjectGitRoot(cwd)))
	if repoMap := repoMapContext(cwd); repoMap != "" {
		b.WriteString("\n\n## Repo map\n\n" + repoMap)
	}
	return b.String()
}

// projectGuidelines walks the directory chain from the git root to cwd
// (inclusive), finds the first matching project context file at each level
// (case-insensitive basename match), reads it, and returns the joined
// content in general-to-specific order — the most general file (at the git
// root) appears first, the most specific (at cwd) last. Each file is capped
// at maxProjectContextBytes; the total across all files is capped at
// maxProjectContextTotalBytes. Returns "" when no file matches.
func projectGuidelines(cwd, gitRoot string) string {
	dirs := projectGuidelineDirs(cwd, gitRoot)
	if len(dirs) == 0 {
		return ""
	}
	var b strings.Builder
	totalUsed := 0
	for _, dir := range dirs {
		if totalUsed >= maxProjectContextTotalBytes {
			break
		}
		match := findProjectContextFile(dir)
		if match == "" {
			continue
		}
		data, err := os.ReadFile(match)
		if err != nil {
			continue
		}
		content := strings.TrimSpace(string(data))
		if content == "" {
			continue
		}
		// Per-file cap is the lesser of the configured per-file limit and
		// whatever remains of the total budget, so a single file can never
		// blow past either bound.
		limit := maxProjectContextBytes
		if remaining := maxProjectContextTotalBytes - totalUsed; remaining < limit {
			limit = remaining
		}
		if len(content) > limit {
			cut := limit
			for cut > 0 && !utf8.RuneStart(content[cut]) {
				cut--
			}
			content = content[:cut] + "\n… (truncated)"
		}
		label := projectGuidelineLabel(match, gitRoot)
		b.WriteString("\n\n## Project guidelines (" + label + ")\n\n" + content)
		totalUsed += len(content)
	}
	return b.String()
}

// projectGuidelineDirs returns the directory chain from gitRoot to cwd
// (inclusive), in root-to-leaf order. If gitRoot is empty or unreachable
// from cwd, the chain collapses to [cwd].
func projectGuidelineDirs(cwd, gitRoot string) []string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return nil
	}
	gitRoot = strings.TrimSpace(gitRoot)
	if gitRoot == "" {
		return []string{cwd}
	}
	rel, err := filepath.Rel(gitRoot, cwd)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return []string{cwd}
	}
	if rel == "." {
		return []string{gitRoot}
	}
	dirs := []string{gitRoot}
	cur := gitRoot
	for _, seg := range strings.Split(rel, string(filepath.Separator)) {
		if seg == "" || seg == "." {
			continue
		}
		cur = filepath.Join(cur, seg)
		dirs = append(dirs, cur)
	}
	return dirs
}

// projectGuidelineLabel renders the path of match as a short label relative
// to gitRoot. When gitRoot is empty, the basename is returned.
func projectGuidelineLabel(match, gitRoot string) string {
	if gitRoot == "" {
		return filepath.Base(match)
	}
	rel, err := filepath.Rel(gitRoot, match)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.Base(match)
	}
	return rel
}

// findProjectContextFile returns the first matching project guideline file
// in dir. Lookup is case-insensitive on the basename and on the actual
// filename returned, so a git-tracked file like AGENTS.MD (uppercase MD)
// still resolves to its true filename on case-sensitive filesystems — the
// returned path is what should appear in the project guidelines label.
// Returns "" when nothing matches.
func findProjectContextFile(dir string) string {
	for _, name := range projectContextFiles {
		baseLower := strings.ToLower(filepath.Base(name))
		// Walk to the file's parent through dir with case-insensitive segment
		// matching, then find a regular file with the same lowercased
		// basename. This works on both case-sensitive and case-insensitive
		// filesystems and always returns the on-disk filename.
		parent, ok := resolveDirCaseInsensitive(filepath.Dir(filepath.Join(dir, name)), dir)
		if !ok {
			continue
		}
		entries, err := os.ReadDir(parent)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() && strings.ToLower(e.Name()) == baseLower {
				return filepath.Join(parent, e.Name())
			}
		}
	}
	return ""
}

// resolveDirCaseInsensitive walks from anchor to target, matching each
// segment case-insensitively. Returns the resolved absolute path and true on
// success, or "" and false if any segment cannot be found.
func resolveDirCaseInsensitive(target, anchor string) (string, bool) {
	if target == "" || target == "." {
		return anchor, true
	}
	rel, err := filepath.Rel(anchor, target)
	if err != nil {
		return "", false
	}
	if rel == "." {
		return anchor, true
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	cur := anchor
	for _, p := range parts {
		if p == "" {
			continue
		}
		entries, err := os.ReadDir(cur)
		if err != nil {
			return "", false
		}
		found := false
		for _, e := range entries {
			if e.IsDir() && strings.EqualFold(e.Name(), p) {
				cur = filepath.Join(cur, e.Name())
				found = true
				break
			}
		}
		if !found {
			return "", false
		}
	}
	return cur, true
}

// findProjectGitRoot returns the nearest ancestor of cwd that contains a
// .git entry (file or directory). Returns "" when no git root is found, so
// the caller can fall back to cwd-only lookup.
func findProjectGitRoot(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ""
	}
	cur := cwd
	for {
		if hasGitMetadata(cur) {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

func hasGitMetadata(dir string) bool {
	gitPath := filepath.Join(dir, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return false
	}
	if info.IsDir() {
		_, err := os.Stat(filepath.Join(gitPath, "HEAD"))
		return err == nil
	}
	data, err := os.ReadFile(gitPath)
	if err != nil {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(string(data)), "gitdir: ")
}

func workspaceSeedContext(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ""
	}
	seed, err := workspaceseed.BuildFromWorkspace(cwd, workspaceseed.GitInfo{
		Branch: gitBranchForPrompt(cwd),
	})
	if err != nil {
		return ""
	}
	rendered := strings.TrimSpace(workspaceseed.Render(seed, workspaceseed.RenderOptions{
		MaxLines: workspaceSeedMaxLines,
		Width:    workspaceSeedWidth,
	}))
	if rendered == "" {
		return ""
	}
	return "<workspace_seed>\n" + rendered + "\n</workspace_seed>"
}

func repoMapContext(cwd string) string {
	// repomap.Scan is best-effort supplemental context for the prompt. If it
	// fails, omit the repo map instead of failing the agent run; successful scans
	// are still capped by repomap.RenderPrompt and maxRepoMapContextBytes.
	snapshot, err := repomap.Scan(cwd, repomap.Options{
		MaxFiles: 300,
		MaxDepth: 5,
	})
	if err != nil {
		return ""
	}
	return repomap.RenderPrompt(snapshot, maxRepoMapContextBytes)
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
