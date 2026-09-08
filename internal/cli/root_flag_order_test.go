package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/agent"
)

// ROOT FLAGS COMPOSE IN ANY ORDER. Each leading-flag splitter stops at the first
// token it does not own, so running them once in a fixed sequence made the
// order the operator wrote them in load-bearing: `zero --allow-escalation
// --theme auto` stranded `--theme auto` as an unknown command and exited with
// an argument error instead of launching. Every ordering of the three root
// flags, on the ask path and the unsafe path, has to reach the TUI with all
// three applied.
func TestRootFlagsComposeInAnyOrder(t *testing.T) {
	extra := t.TempDir()
	resolvedExtra, err := filepath.EvalSymlinks(extra)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"--allow-escalation", "--theme", "auto", "--add-dir", extra},
		{"--allow-escalation", "--add-dir", extra, "--theme", "auto"},
		{"--theme", "auto", "--allow-escalation", "--add-dir", extra},
		{"--add-dir", extra, "--allow-escalation", "--theme", "auto"},
		{"--add-dir", extra, "--theme", "auto", "--allow-escalation"},
		{"--allow-escalation", "--add-dir", extra, "--theme", "auto", "--allow-escalation"},
		{"--skip-permissions-unsafe", "--allow-escalation", "--theme", "auto", "--add-dir", extra},
		{"--allow-escalation", "--skip-permissions-unsafe", "--add-dir", extra, "--theme", "auto"},
		{"--theme", "auto", "--skip-permissions-unsafe", "--allow-escalation", "--add-dir", extra},
		{"--add-dir", extra, "--theme", "auto", "--allow-escalation", "--skip-permissions-unsafe"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			options := captureTUIOptions(t, args...)
			wantMode := agent.PermissionModeAsk
			for _, arg := range args {
				if arg == "--skip-permissions-unsafe" {
					wantMode = agent.PermissionModeUnsafe
				}
			}
			if options.PermissionMode != wantMode {
				t.Errorf("PermissionMode = %q, want %q", options.PermissionMode, wantMode)
			}
			if !options.AllowEscalation {
				t.Error("escalation not enabled")
			}
			if options.Theme != "auto" {
				t.Errorf("Theme = %q, want auto", options.Theme)
			}
			if options.AgentOptions.Sandbox == nil {
				t.Fatal("no sandbox engine on the launched options")
			}
			roots := options.AgentOptions.Sandbox.Scope().Roots()
			found := false
			for _, root := range roots {
				if root == resolvedExtra {
					found = true
				}
			}
			if !found {
				t.Errorf("scope roots = %v, want the --add-dir root %q", roots, resolvedExtra)
			}
		})
	}
}

// The last --theme wins even with another root flag between two of them, and a
// --theme written before --skip-permissions-unsafe reaches the TUI: the unsafe
// path used to launch with only the theme written after the flag, dropping one
// written before it.
func TestRootThemeLastOccurrenceWinsAcrossOtherFlags(t *testing.T) {
	for _, testCase := range []struct {
		args []string
		want string
	}{
		{[]string{"--theme", "light", "--allow-escalation", "--theme", "auto"}, "auto"},
		{[]string{"--theme", "auto", "--skip-permissions-unsafe"}, "auto"},
		{[]string{"--theme", "light", "--skip-permissions-unsafe", "--theme", "auto"}, "auto"},
	} {
		t.Run(strings.Join(testCase.args, " "), func(t *testing.T) {
			options := captureTUIOptions(t, testCase.args...)
			if options.Theme != testCase.want {
				t.Fatalf("Theme = %q, want %q", options.Theme, testCase.want)
			}
		})
	}
}
