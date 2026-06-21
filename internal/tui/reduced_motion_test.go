package tui

import (
	"testing"

	"github.com/charmbracelet/colorprofile"
)

func TestReducedMotionEnabled(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	cases := []struct {
		name    string
		vars    map[string]string
		profile colorprofile.Profile
		want    bool
	}{
		{"off by default", nil, colorprofile.TrueColor, false},
		{"explicit 1", map[string]string{"ZERO_REDUCED_MOTION": "1"}, colorprofile.TrueColor, true},
		{"explicit true", map[string]string{"ZERO_REDUCED_MOTION": "true"}, colorprofile.TrueColor, true},
		{"explicit 0 is off", map[string]string{"ZERO_REDUCED_MOTION": "0"}, colorprofile.TrueColor, false},
		{"explicit false is off", map[string]string{"ZERO_REDUCED_MOTION": "false"}, colorprofile.TrueColor, false},
		{"no-TTY forces on", nil, colorprofile.NoTTY, true},
		{"SSH alone does not force reduced motion", map[string]string{"SSH_CONNECTION": "x"}, colorprofile.TrueColor, false},
	}
	for _, c := range cases {
		if got := reducedMotionEnabled(env(c.vars), c.profile); got != c.want {
			t.Errorf("%s: reducedMotionEnabled = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSpinnerGlyphStaticUnderReducedMotion(t *testing.T) {
	var m model
	m.reducedMotion = true
	if g := m.spinnerGlyph(); g != "•" {
		t.Fatalf("reduced motion glyph = %q, want a steady dot", g)
	}
}
