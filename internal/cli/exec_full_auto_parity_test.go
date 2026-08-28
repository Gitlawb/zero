package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/config"
)

func execListToolsFor(t *testing.T, args ...string) string {
	t.Helper()
	cwd := t.TempDir()
	var stdout, stderr bytes.Buffer
	exitCode := runWithDeps(append([]string{"exec"}, append(args, "--list-tools")...), &stdout, &stderr, appDeps{
		getwd:         func() (string, error) { return cwd, nil },
		resolveConfig: func(string, config.Overrides) (config.ResolvedConfig, error) { return execResolvedConfig(), nil },
	})
	if exitCode != exitSuccess {
		t.Fatalf("exit %d for %v: %s", exitCode, args, stderr.String())
	}
	return stdout.String()
}

// EVERY SPELLING OF FULL-AUTO GETS THE SAME TOOLS.
//
// Registration read the raw CLI fields while execution read the resolved mode,
// so `--permission-mode full-auto` composed a registry without Task and swarm
// tooling while `--auto high` and `--full-auto`, which resolve to exactly the
// same effective mode, composed one with it. The explicitly selected full-auto
// agent could not delegate, and --list-tools reported a different capability set
// for the same run.
func TestFullAutoEntryPointsListTheSameSpecialistTools(t *testing.T) {
	baseline := execListToolsFor(t, "--auto", "high")
	if !strings.Contains(baseline, "swarm_spawn") {
		t.Fatalf("SETUP INVALID: --auto high did not list swarm_spawn, so this test cannot detect the gap:\n%s", baseline)
	}

	for _, args := range [][]string{
		{"--full-auto"},
		{"--permission-mode", "full-auto"},
		{"--permission-mode", "full_auto"},
		{"--permission-mode", "unsafe"},
		{"--permission-mode", "high"},
		{"--skip-permissions-unsafe"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			listing := execListToolsFor(t, args...)
			if !strings.Contains(listing, "swarm_spawn") {
				t.Errorf("%v resolves to full-auto but did not list swarm_spawn, so this entry point cannot delegate:\n%s", args, listing)
			}
		})
	}
}
