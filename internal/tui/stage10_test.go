package tui

import (
	"testing"

	"github.com/charmbracelet/colorprofile"
)

func TestAssistantMeasureCap(t *testing.T) {
	if got := assistantMeasure(200); got != assistantMeasureCap {
		t.Errorf("wide: assistantMeasure(200) = %d, want %d", got, assistantMeasureCap)
	}
	if got := assistantMeasure(80); got != 80 {
		t.Errorf("under cap: assistantMeasure(80) = %d, want 80", got)
	}
	if got := assistantMeasure(5); got != 16 {
		t.Errorf("floor: assistantMeasure(5) = %d, want 16", got)
	}
	if assistantMeasureCap < 80 || assistantMeasureCap > 100 {
		t.Errorf("cap %d outside the 80-100 readability range", assistantMeasureCap)
	}
}

func TestStreamingFadeDisabled(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	cases := []struct {
		name    string
		envMap  map[string]string
		profile colorprofile.Profile
		want    bool
	}{
		{"truecolor default", nil, colorprofile.TrueColor, false},
		{"ansi256 ok", nil, colorprofile.ANSI256, false},
		{"ZERO_NO_FADE=1", map[string]string{"ZERO_NO_FADE": "1"}, colorprofile.TrueColor, true},
		{"ZERO_NO_FADE=0 stays on", map[string]string{"ZERO_NO_FADE": "0"}, colorprofile.TrueColor, false},
		{"ZERO_NO_FADE=false stays on", map[string]string{"ZERO_NO_FADE": "false"}, colorprofile.TrueColor, false},
		{"ssh", map[string]string{"SSH_CONNECTION": "10.0.0.1 22 ..."}, colorprofile.TrueColor, true},
		{"ssh tty", map[string]string{"SSH_TTY": "/dev/pts/3"}, colorprofile.TrueColor, true},
		{"tmux TERM", map[string]string{"TERM": "tmux-256color"}, colorprofile.TrueColor, true},
		{"screen TERM", map[string]string{"TERM": "screen.xterm"}, colorprofile.TrueColor, true},
		{"16-color", nil, colorprofile.ANSI, true},
		{"ascii", nil, colorprofile.ASCII, true},
		{"no tty", nil, colorprofile.NoTTY, true},
	}
	for _, c := range cases {
		if got := streamingFadeDisabled(env(c.envMap), c.profile); got != c.want {
			t.Errorf("%s: streamingFadeDisabled = %v, want %v", c.name, got, c.want)
		}
	}
}
