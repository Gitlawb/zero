// files_git_sweep.go fills the FILES sidebar's blind spot: mutations that
// bypass the file tools entirely — bash/exec_command scaffolding (npm create,
// go generate, heredoc writes) and subagents editing the shared workspace. None
// of those produce a changedFiles-carrying tool result, so the transcript-derived
// roster (files_panel.go) never sees them. The sweep asks git instead: a
// baseline `git status --porcelain` snapshot is taken at startup (Init), and a
// re-run after each command tool result / turn end reports any NEWLY dirty
// paths, which merge into the roster with a diffstat from `git diff --numstat`.
// Pre-existing dirty state stays in the baseline and never shows; a non-git
// workspace fails the first command and the sweep silently stays off.
package tui

import (
	"context"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// gitSweepTimeout bounds each background git invocation so a hung index lock
// can never stall message delivery (the command runs off the update goroutine).
const gitSweepTimeout = 3 * time.Second

// gitSweepFile is one workspace file git reports as dirty: workspace-relative
// path (git's own output convention, matching changedFiles), whether it is new
// (untracked/added), and the tracked diffstat when known (0/0 for untracked).
type gitSweepFile struct {
	path    string
	created bool
	adds    int
	dels    int
}

// gitSweepMsg carries one sweep's result. baseline marks the startup snapshot
// (recorded as "pre-existing, never show") vs a live re-check (merged into the
// roster). ok is false when git failed (not a repo, no git binary) — the
// handler then marks the sweep unavailable rather than retrying every turn.
type gitSweepMsg struct {
	baseline bool
	ok       bool
	files    []gitSweepFile
}

// gitSweepCmd runs the status+numstat pair off the update goroutine. cwd is the
// workspace root the TUI resolves paths against.
func gitSweepCmd(parent context.Context, cwd string, baseline bool) tea.Cmd {
	return func() tea.Msg {
		if parent == nil {
			parent = context.Background()
		}
		ctx, cancel := context.WithTimeout(parent, gitSweepTimeout)
		defer cancel()
		// --untracked-files=all enumerates files inside new directories; plain
		// porcelain collapses them to "dir/", which is useless as a FILES row.
		status, err := defaultPRCommandRunner(ctx, cwd, "git", "status", "--porcelain", "--untracked-files=all")
		if err != nil {
			return gitSweepMsg{baseline: baseline, ok: false}
		}
		files := parseGitPorcelain(status)
		// Diffstat for tracked modifications; untracked files have no diff to
		// stat. Best-effort: a failure (e.g. unborn HEAD in a fresh repo) keeps
		// the file list with zero counts rather than dropping the sweep.
		if numstat, err := defaultPRCommandRunner(ctx, cwd, "git", "diff", "HEAD", "--numstat"); err == nil {
			stats := parseGitNumstat(numstat)
			for i := range files {
				if counts, ok := stats[files[i].path]; ok {
					files[i].adds, files[i].dels = counts[0], counts[1]
				}
			}
		}
		return gitSweepMsg{baseline: baseline, ok: true, files: files}
	}
}

// parseGitPorcelain parses `git status --porcelain` v1 output into sweep files.
// Format: two status columns, a space, then the path ("XY path"); renames show
// "old -> new" (keep the new side); untracked entries are "?? path".
func parseGitPorcelain(out string) []gitSweepFile {
	var files []gitSweepFile
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		code, path := line[:2], strings.TrimSpace(line[3:])
		if to, _, found := cutRename(path); found {
			path = to
		}
		path = unquoteGitPath(path)
		if path == "" {
			continue
		}
		files = append(files, gitSweepFile{
			path:    path,
			created: code == "??" || strings.Contains(code, "A"),
		})
	}
	return files
}

// cutRename splits a porcelain rename value "old -> new", returning the new
// path. found is false for ordinary (non-rename) paths.
func cutRename(path string) (string, string, bool) {
	if idx := strings.Index(path, " -> "); idx >= 0 {
		return path[idx+4:], path[:idx], true
	}
	return "", "", false
}

// unquoteGitPath strips the quotes git wraps around paths with special
// characters ("web/my file.js"). Escapes inside are left as-is — such a path
// still identifies the file well enough for a sidebar row.
func unquoteGitPath(path string) string {
	if len(path) >= 2 && strings.HasPrefix(path, `"`) && strings.HasSuffix(path, `"`) {
		return path[1 : len(path)-1]
	}
	return path
}

// parseGitNumstat parses `git diff --numstat` output ("added\tdeleted\tpath")
// into path → [added, deleted]. Binary files report "-" and are skipped.
func parseGitNumstat(out string) map[string][2]int {
	stats := map[string][2]int{}
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		adds, errA := strconv.Atoi(parts[0])
		dels, errD := strconv.Atoi(parts[1])
		if errA != nil || errD != nil {
			continue
		}
		path := parts[2]
		if to, _, found := cutRename(path); found {
			path = to
		}
		stats[unquoteGitPath(path)] = [2]int{adds, dels}
	}
	return stats
}

// handleGitSweepMsg folds a sweep result into the model: the baseline snapshot
// records what was already dirty before this TUI session (those paths never
// show), a live sweep upserts newly dirty paths into gitTouched in first-seen
// order. A failed sweep marks git unavailable so no further sweeps are issued.
func (m model) handleGitSweepMsg(msg gitSweepMsg) model {
	m.gitSweepInFlight = false
	if !msg.ok {
		m.gitSweepUnavailable = true
		if m.gitFileBaseline == nil {
			m.gitFileBaseline = map[string]bool{}
		}
		return m
	}
	if msg.baseline {
		baseline := make(map[string]bool, len(msg.files))
		for _, f := range msg.files {
			baseline[f.path] = true
		}
		m.gitFileBaseline = baseline
		return m
	}
	for _, f := range msg.files {
		if m.gitFileBaseline[f.path] {
			continue
		}
		found := false
		for i := range m.gitTouched {
			if m.gitTouched[i].path == f.path {
				m.gitTouched[i] = f
				found = true
				break
			}
		}
		if !found {
			// Copy-on-append: model copies share the backing array, so an in-place
			// append from two update branches could alias. Rebuilding is cheap at
			// sidebar scale.
			m.gitTouched = append(append([]gitSweepFile(nil), m.gitTouched...), f)
		}
	}
	return m
}

// maybeGitSweep issues a live sweep when one is useful and none is running:
// the baseline exists (Init's snapshot answered), git works here, and the
// workspace is known. Returns the (possibly nil) command to batch.
func (m model) maybeGitSweep() (model, tea.Cmd) {
	if m.gitSweepInFlight || m.gitSweepUnavailable || m.gitFileBaseline == nil || strings.TrimSpace(m.cwd) == "" {
		return m, nil
	}
	m.gitSweepInFlight = true
	return m, gitSweepCmd(m.ctx, m.cwd, false)
}

// gitTouchedFiles adapts the sweep results to the roster's touchedFile shape,
// for merging under the transcript-derived entries (files_panel.go). No
// transcript row backs them (lastRowIndex -1), so selecting one skips the
// scroll/tint and the drill-in opens on the full file.
func (m model) gitTouchedFiles() []touchedFile {
	if len(m.gitTouched) == 0 {
		return nil
	}
	files := make([]touchedFile, 0, len(m.gitTouched))
	for _, f := range m.gitTouched {
		files = append(files, touchedFile{
			path:         f.path,
			created:      f.created,
			adds:         f.adds,
			dels:         f.dels,
			lastRowIndex: -1,
		})
	}
	return files
}
