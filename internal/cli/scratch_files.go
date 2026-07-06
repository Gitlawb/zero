package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// scratchFileWarning builds a completion-time warning listing brand-new files
// the model created during this run (via write_file) that are still sitting in
// the workspace, untracked by git, when the run ends. This is the safest fix
// for https://github.com/Gitlawb/zero/issues/551 — Zero never guesses which
// files are "scratch" and deletes them (a wrong guess would destroy real
// work), it just surfaces what git would otherwise silently let `git add -A`
// sweep up, so a leftover `_debug.py` is caught before it is committed instead
// of after.
//
// Returns "" when there is nothing to report: no files were created, git is
// unavailable, workspaceRoot isn't a git repo, or every created file was
// either removed again or already tracked/staged by the model itself.
func scratchFileWarning(workspaceRoot string, createdFiles []string) string {
	if len(createdFiles) == 0 {
		return ""
	}
	if _, err := exec.LookPath("git"); err != nil {
		return ""
	}

	// Filter to files that still exist; the model may have deleted its own
	// scratch work already, which needs no warning.
	existing := make([]string, 0, len(createdFiles))
	for _, path := range createdFiles {
		if _, err := os.Stat(path); err == nil {
			existing = append(existing, path)
		}
	}
	if len(existing) == 0 {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	args := append([]string{"-C", workspaceRoot, "status", "--porcelain", "--untracked-files=all", "--"}, existing...)
	output, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		// Not a git repo (or git failed for some other reason) — nothing
		// reliable to report against, so stay silent rather than guess.
		return ""
	}

	var untracked []string
	for _, line := range strings.Split(string(output), "\n") {
		if !strings.HasPrefix(line, "?? ") {
			continue
		}
		relative := strings.TrimSpace(strings.TrimPrefix(line, "??"))
		relative = strings.Trim(relative, "\"")
		untracked = append(untracked, relative)
	}
	if len(untracked) == 0 {
		return ""
	}

	displayPaths := make([]string, len(untracked))
	for i, path := range untracked {
		displayPaths[i] = filepath.ToSlash(path)
	}
	plural := "s"
	verb := "are"
	if len(displayPaths) == 1 {
		plural = ""
		verb = "is"
	}
	return "This run created " + strconv.Itoa(len(displayPaths)) + " new file" + plural + " that " + verb +
		" still untracked in git and may be scratch/debug output left behind: " +
		strings.Join(displayPaths, ", ") + ". Review before committing/staging."
}
