package tui

import (
	"strings"
	"testing"
	"time"
)

func TestParseLoopCommandModes(t *testing.T) {
	cases := []struct {
		in       string
		action   loopAction
		mode     loopMode
		prompt   string
		interval time.Duration
	}{
		{"", loopActionUsage, 0, "", 0},
		{"list", loopActionList, 0, "", 0},
		{"status", loopActionList, 0, "", 0},
		{"stop all", loopActionStopAll, 0, "", 0},
		{"stop ab12", loopActionStop, 0, "", 0},
		{"5m /babysit-prs", loopActionStart, loopModeFixed, "/babysit-prs", 5 * time.Minute},
		{"90s check the build", loopActionStart, loopModeFixed, "check the build", 90 * time.Second},
		{"watch CI every 2m", loopActionStart, loopModeFixed, "watch CI", 2 * time.Minute},
		{"keep improving the docs", loopActionStart, loopModeSelfPaced, "keep improving the docs", 0},
		// "every PR" is not a cadence — the token after "every" isn't a duration.
		{"review every PR", loopActionStart, loopModeSelfPaced, "review every PR", 0},
	}
	for _, c := range cases {
		got := parseLoopCommand(c.in)
		if got.action != c.action {
			t.Errorf("%q action = %v, want %v", c.in, got.action, c.action)
		}
		if c.action == loopActionStart {
			if got.mode != c.mode {
				t.Errorf("%q mode = %v, want %v", c.in, got.mode, c.mode)
			}
			if got.prompt != c.prompt {
				t.Errorf("%q prompt = %q, want %q", c.in, got.prompt, c.prompt)
			}
			if got.interval != c.interval {
				t.Errorf("%q interval = %v, want %v", c.in, got.interval, c.interval)
			}
		}
	}
}

func TestParseLoopCommandStopTarget(t *testing.T) {
	if got := parseLoopCommand("stop"); got.action != loopActionStop || got.targetID != "" {
		t.Fatalf("bare stop = %+v, want stop with empty target", got)
	}
	if got := parseLoopCommand("stop XyZ9"); got.targetID != "XyZ9" {
		t.Fatalf("stop target = %q, want XyZ9", got.targetID)
	}
}

func TestClampLoopInterval(t *testing.T) {
	if d, note := clampLoopInterval(5 * time.Second); d != loopMinInterval || note == "" {
		t.Fatalf("below-min = (%v,%q), want raised to min with note", d, note)
	}
	if d, note := clampLoopInterval(48 * time.Hour); d != loopMaxInterval || note == "" {
		t.Fatalf("above-max = (%v,%q), want lowered to max with note", d, note)
	}
	if d, note := clampLoopInterval(5 * time.Minute); d != 5*time.Minute || note != "" {
		t.Fatalf("in-range = (%v,%q), want unchanged, no note", d, note)
	}
}

func TestClampSelfPaceDelay(t *testing.T) {
	if got := clampSelfPaceDelay(0); got != loopSelfPaceDefault {
		t.Errorf("zero delay = %v, want default %v", got, loopSelfPaceDefault)
	}
	if got := clampSelfPaceDelay(10 * time.Second); got != loopSelfPaceMin {
		t.Errorf("below-min = %v, want %v", got, loopSelfPaceMin)
	}
	if got := clampSelfPaceDelay(3 * time.Hour); got != loopSelfPaceMax {
		t.Errorf("above-max = %v, want %v", got, loopSelfPaceMax)
	}
	if got := clampSelfPaceDelay(10 * time.Minute); got != 10*time.Minute {
		t.Errorf("in-band = %v, want unchanged", got)
	}
}

func TestFormatLoopDuration(t *testing.T) {
	cases := map[time.Duration]string{
		30 * time.Second: "30s",
		90 * time.Second: "90s",
		5 * time.Minute:  "5m",
		2 * time.Hour:    "2h",
		24 * time.Hour:   "1d",
	}
	for d, want := range cases {
		if got := formatLoopDuration(d); got != want {
			t.Errorf("formatLoopDuration(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestParseLoopControl(t *testing.T) {
	cases := []struct {
		in   string
		done bool
		wake time.Duration
	}{
		{"did the work\nLOOP: DONE", true, 0},
		{"progress\nLOOP: CONTINUE", false, 0},
		{"progress\nLOOP: CONTINUE 10m", false, 10 * time.Minute},
		{"progress\n- loop: continue 1h", false, time.Hour},                     // markdown bullet, lowercase
		{"LOOP: CONTINUE 5m\n...more...\nLOOP: DONE", true, 0},                  // last control line wins
		{"no control line here", false, 0},                                      // absent -> adaptive
		{"LOOP: DONE — all tests pass", true, 0},                                // a trailing note is fine
		{"progress\nLOOP: CONTINUE 5m — still tidying", false, 5 * time.Minute}, // note after the interval
		{"work done.\r\nLOOP: DONE\r\n", true, 0},                               // CRLF endings still parse
		{"work\r\nLOOP: CONTINUE 10m\r\n", false, 10 * time.Minute},             // CRLF + interval
		{"I will emit LOOP: DONE when finished", false, 0},                      // mid-sentence mention doesn't match
		{"LOOP: CONTINUE 99z", false, 0},                                        // bad unit -> continue, no wake hint
	}
	for _, c := range cases {
		done, wake := parseLoopControl(c.in)
		if done != c.done || wake != c.wake {
			t.Errorf("parseLoopControl(%q) = (%v,%v), want (%v,%v)", c.in, done, wake, c.done, c.wake)
		}
	}
}

func TestFirePromptWrapsSelfPacedOnly(t *testing.T) {
	self := &loopState{mode: loopModeSelfPaced, prompt: "tidy the docs"}
	got := self.firePrompt()
	if !strings.Contains(got, "LOOP: DONE") || !strings.Contains(got, "tidy the docs") {
		t.Errorf("self-paced firePrompt should embed the playbook and goal, got %q", got)
	}
	fixed := &loopState{mode: loopModeFixed, prompt: "check the build"}
	if fixed.firePrompt() != "check the build" {
		t.Errorf("fixed-interval firePrompt should be verbatim, got %q", fixed.firePrompt())
	}
	// A self-paced /command is a real command, not a goal — send it verbatim, no playbook.
	cmd := &loopState{mode: loopModeSelfPaced, prompt: "/babysit"}
	if cmd.firePrompt() != "/babysit" {
		t.Errorf("self-paced /command firePrompt should be verbatim, got %q", cmd.firePrompt())
	}
}

func TestLoopStateDue(t *testing.T) {
	base := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	l := &loopState{nextRunAt: base}
	if l.due(base.Add(-time.Second)) {
		t.Error("loop should not be due before nextRunAt")
	}
	if !l.due(base) {
		t.Error("loop should be due at nextRunAt")
	}
	l.paused = true
	if l.due(base.Add(time.Hour)) {
		t.Error("paused loop should never be due")
	}
}
