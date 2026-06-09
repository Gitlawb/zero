package cron

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const maxLoopPromptBytes = 25000

const builtinLoopPrompt = "Continue the ongoing task in this repository.\n" +
	"Inspect recent changes, make incremental progress, and stop when you reach a natural checkpoint.\n" +
	"Do not take destructive actions without strong justification.\n" +
	"Report what you did succinctly."

// ResolveLoopPrompt returns the loop prompt for a scheduled job: the first
// readable loop.md found in <cwd>/.zero, <cwd>/.agents, <home>/.zero,
// <home>/.agents, else the built-in fallback. Files over maxLoopPromptBytes
// return an error; symlinks are skipped (never read) to prevent exfiltration.
func ResolveLoopPrompt(cwd, home string) (string, error) {
	dirs := []string{
		filepath.Join(cwd, ".zero"),
		filepath.Join(cwd, ".agents"),
		filepath.Join(home, ".zero"),
		filepath.Join(home, ".agents"),
	}
	for _, dir := range dirs {
		path := filepath.Join(dir, "loop.md")
		info, err := os.Lstat(path)
		if err != nil {
			continue // not present
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue // never follow a symlinked loop.md
		}
		if info.Size() > maxLoopPromptBytes {
			return "", fmt.Errorf("loop prompt %s exceeds %d bytes", path, maxLoopPromptBytes)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if text := strings.TrimSpace(string(data)); text != "" {
			return text, nil
		}
	}
	return builtinLoopPrompt, nil
}
