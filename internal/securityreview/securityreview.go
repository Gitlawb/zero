// Package securityreview builds the prompt for the builtin /security-review
// slash command. It pre-collects the pending branch changes with a fixed set of
// read-only git commands and injects them into the review prompt template, so
// the model starts its first turn with the full diff already in context instead
// of spending turns discovering it.
//
// The git invocations are a hard-coded allowlist — no user input is ever
// interpolated into a command line, and no shell is involved.
package securityreview

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// MaxOutputBytes caps each collected git output so a huge diff cannot blow up
// the model's context. Truncated sections say so in place.
const MaxOutputBytes = 256 * 1024

// commandTimeout bounds each individual git invocation.
const commandTimeout = 30 * time.Second

// ErrNotGitRepo is returned by CollectGitContext when dir is not inside a git
// working tree.
var ErrNotGitRepo = errors.New("not inside a git repository")

//go:embed prompt.md
var promptTemplate string

// GitContext holds the collected, read-only view of the pending branch changes.
type GitContext struct {
	Status  string // git status
	Files   string // git diff --name-only origin/HEAD...
	Commits string // git log --no-decorate origin/HEAD...
	Diff    string // git diff origin/HEAD...
}

// gitCommands is the allowlisted set of read-only git invocations used to fill
// a GitContext, in template order. Each entry is a fixed argv (no shell).
var gitCommands = []struct {
	field string
	args  []string
}{
	{"status", []string{"status"}},
	{"files", []string{"diff", "--name-only", "origin/HEAD..."}},
	{"commits", []string{"log", "--no-decorate", "origin/HEAD..."}},
	{"diff", []string{"diff", "origin/HEAD..."}},
}

// IsGitRepo reports whether dir is inside a git working tree.
func IsGitRepo(ctx context.Context, dir string) bool {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = dir
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// CollectGitContext runs the allowlisted read-only git commands in dir and
// returns their (possibly truncated) outputs. It returns ErrNotGitRepo before
// running anything else when dir is not inside a git working tree.
func CollectGitContext(ctx context.Context, dir string) (GitContext, error) {
	if !IsGitRepo(ctx, dir) {
		return GitContext{}, ErrNotGitRepo
	}
	var gc GitContext
	for _, spec := range gitCommands {
		out, err := runGit(ctx, dir, spec.args)
		if err != nil {
			// A missing origin/HEAD (detached, no upstream, brand-new clone) is
			// common; surface it inside the section instead of failing the
			// whole review so the model can work with what exists.
			out = fmt.Sprintf("[command `git %s` failed: %v]", strings.Join(spec.args, " "), err)
		}
		switch spec.field {
		case "status":
			gc.Status = out
		case "files":
			gc.Files = out
		case "commits":
			gc.Commits = out
		case "diff":
			gc.Diff = out
		}
	}
	return gc, nil
}

// BuildPrompt renders the review prompt template with the collected git
// context injected.
func BuildPrompt(gc GitContext) string {
	replacer := strings.NewReplacer(
		"{{GIT_STATUS}}", gc.Status,
		"{{GIT_FILES}}", gc.Files,
		"{{GIT_COMMITS}}", gc.Commits,
		"{{GIT_DIFF}}", gc.Diff,
	)
	return replacer.Replace(promptTemplate)
}

// BuildPromptForDir is the one-call convenience path used by the TUI command:
// collect, then render.
func BuildPromptForDir(ctx context.Context, dir string) (string, error) {
	gc, err := CollectGitContext(ctx, dir)
	if err != nil {
		return "", err
	}
	return BuildPrompt(gc), nil
}

// runGit executes one allowlisted git command and returns its combined output,
// truncated to MaxOutputBytes with a marker.
func runGit(ctx context.Context, dir string, args []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return "", errors.New(detail)
	}
	out := stdout.String()
	if len(out) > MaxOutputBytes {
		out = out[:MaxOutputBytes] + fmt.Sprintf("\n\n[... truncated at %d bytes ...]", MaxOutputBytes)
	}
	return strings.TrimSpace(out), nil
}
