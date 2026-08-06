package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func envFrom(pairs map[string]string) func(string) string {
	return func(key string) string { return pairs[key] }
}

// AllMotion (1003) buys hover highlighting and nothing else: wheel, click and
// drag all arrive under CellMotion (1002). When a terminal does not support
// 1003 the cost is not a missing highlight, it is that NO mouse event arrives
// at all, and because the app holds the alternate screen the terminal's own
// scrollback is gone too. The user cannot scroll by any means, and a screen of
// cut-off output reads as a hang (#870).
//
// So the rule is: ask for AllMotion only where it is known to work.
func TestMouseModeFallsBackWhereAllMotionIsUnreliable(t *testing.T) {
	tests := []struct {
		name       string
		goos       string
		env        map[string]string
		underPRoot bool
		want       tea.MouseMode
	}{
		{
			name: "linux terminal",
			goos: "linux",
			want: tea.MouseModeAllMotion,
		},
		{
			name: "macOS terminal",
			goos: "darwin",
			want: tea.MouseModeAllMotion,
		},
		{
			// Pre-existing carve-out: under PRoot the 1003 sequence is unreliable
			// and breaks touch-gesture scrolling.
			name:       "under PRoot",
			goos:       "linux",
			underPRoot: true,
			want:       tea.MouseModeCellMotion,
		},
		{
			// #870. The legacy Windows console does not handle 1003 reliably, and
			// it is what a user gets by running zero.exe from anywhere that is not
			// Windows Terminal.
			name: "legacy Windows console",
			goos: "windows",
			want: tea.MouseModeCellMotion,
		},
		{
			name: "Windows Terminal",
			goos: "windows",
			env:  map[string]string{"WT_SESSION": "d1c2-session"},
			want: tea.MouseModeAllMotion,
		},
		{
			name: "VS Code terminal on Windows",
			goos: "windows",
			env:  map[string]string{"TERM_PROGRAM": "vscode"},
			want: tea.MouseModeAllMotion,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := mouseModeFor(test.goos, envFrom(test.env), test.underPRoot)
			if got != test.want {
				t.Fatalf("mouseModeFor(%q) = %v, want %v", test.goos, got, test.want)
			}
		})
	}
}

// The override exists so a user who hits an unsupported terminal can fix it
// themselves without waiting for a release, and so a maintainer can ask a bug
// reporter to test one specific mode.
func TestMouseModeOverrideWins(t *testing.T) {
	tests := []struct {
		value string
		want  tea.MouseMode
	}{
		{"cell", tea.MouseModeCellMotion},
		{"CELL", tea.MouseModeCellMotion},
		{" cell ", tea.MouseModeCellMotion},
		{"all", tea.MouseModeAllMotion},
		{"off", tea.MouseModeNone},
		{"none", tea.MouseModeNone},
	}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			// windows + PRoot would otherwise force CellMotion, so "all" proves the
			// override beats the fallbacks rather than agreeing with them.
			got := mouseModeFor("windows", envFrom(map[string]string{mouseModeEnv: test.value}), true)
			if got != test.want {
				t.Fatalf("%s=%q gave %v, want %v", mouseModeEnv, test.value, got, test.want)
			}
		})
	}
}

// An unreadable override must not silently disable the mouse: fall through to
// the normal decision rather than guessing.
func TestMouseModeIgnoresAnUnknownOverride(t *testing.T) {
	got := mouseModeFor("linux", envFrom(map[string]string{mouseModeEnv: "sideways"}), false)
	if got != tea.MouseModeAllMotion {
		t.Fatalf("an unknown override changed the mode to %v, want the normal AllMotion", got)
	}
}
