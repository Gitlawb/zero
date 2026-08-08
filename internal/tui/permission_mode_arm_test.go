package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea/v2"

	"github.com/Gitlawb/zero/internal/agent"
)

// These drive the REAL key handler rather than the pure cycle functions.
//
// The pure functions are easy to get right. The dangerous part is the offer
// state living on the model across keypresses: if any path fails to clear it, a
// later innocent ctrl+g commits full-auto mode with nobody having decided
// anything, and nothing would say so. The disarm is written as an unconditional
// clear at the top of the key handler with only shift+tab re-arming, and these
// exist to prove that inversion actually holds through the handler.

func armedModel(t *testing.T) model {
	t.Helper()
	m := model{permissionMode: agent.PermissionModeAsk}
	armed := pressKey(t, m, tea.Key{Code: tea.KeyTab, Mod: tea.ModShift})
	if !armed.unsafeArmed {
		t.Fatal("shift+tab from Ask did not raise the full-auto offer")
	}
	if armed.permissionMode != agent.PermissionModeAsk {
		t.Fatalf("mode changed to %s while merely offering full-auto", armed.permissionMode)
	}
	return armed
}

func pressKey(t *testing.T, m model, key tea.Key) model {
	t.Helper()
	next, _ := m.updateModel(tea.KeyPressMsg(key))
	got, ok := next.(model)
	if !ok {
		t.Fatalf("updateModel returned %T, want model", next)
	}
	return got
}

// The confirm key immediately after the offer commits full-auto. This is the
// feature working.
func TestConfirmKeyCommitsUnsafeRightAfterTheOffer(t *testing.T) {
	m := pressKey(t, armedModel(t), tea.Key{Code: 'g', Mod: tea.ModCtrl})
	if m.permissionMode != agent.PermissionModeFullAuto {
		t.Fatalf("mode = %s after confirming, want full-auto", m.permissionMode)
	}
	if m.unsafeArmed {
		t.Error("offer still live after being accepted")
	}
}

// THE ONE THAT MATTERS. Any other key in between must cancel the offer, so the
// confirm key afterwards does nothing. Each of these is a separate path through
// the handler, and every one of them has to clear.
func TestAnyOtherKeyCancelsTheUnsafeOffer(t *testing.T) {
	for name, key := range map[string]tea.Key{
		"printable":     {Code: 'a', Text: "a"},
		"space":         {Code: tea.KeySpace, Text: " "},
		"escape":        {Code: tea.KeyEscape},
		"enter":         {Code: tea.KeyEnter},
		"backspace":     {Code: tea.KeyBackspace},
		"plain tab":     {Code: tea.KeyTab},
		"up arrow":      {Code: tea.KeyUp},
		"unrelated ctl": {Code: 'b', Mod: tea.ModCtrl},
	} {
		t.Run(name, func(t *testing.T) {
			m := pressKey(t, armedModel(t), key)
			if m.unsafeArmed {
				t.Fatalf("%s left the full-auto offer live, so a later ctrl+g would commit it silently", name)
			}
			// And prove the consequence rather than trusting the flag.
			m = pressKey(t, m, tea.Key{Code: 'g', Mod: tea.ModCtrl})
			if m.permissionMode == agent.PermissionModeFullAuto {
				t.Fatalf("ctrl+g after %s entered full-auto mode with no live offer", name)
			}
		})
	}
}

// The confirm key with no offer at all must be inert, so it cannot be used as a
// standalone shortcut into full-auto.
func TestConfirmKeyAloneNeverEntersUnsafe(t *testing.T) {
	for _, start := range []agent.PermissionMode{agent.PermissionModeAuto, agent.PermissionModeAsk} {
		m := model{permissionMode: start}
		for press := 0; press < 3; press++ {
			m = pressKey(t, m, tea.Key{Code: 'g', Mod: tea.ModCtrl})
			if m.permissionMode == agent.PermissionModeFullAuto {
				t.Fatalf("ctrl+g alone from %s reached full-auto on press %d", start, press+1)
			}
		}
	}
}

// Repeated shift+tab must never commit unsafe, only offer and then decline.
// Someone holding the key down must not end up with prompts disabled.
func TestHoldingShiftTabNeverCommitsUnsafe(t *testing.T) {
	m := model{permissionMode: agent.PermissionModeAuto}
	for press := 0; press < 10; press++ {
		m = pressKey(t, m, tea.Key{Code: tea.KeyTab, Mod: tea.ModShift})
		if m.permissionMode == agent.PermissionModeFullAuto {
			t.Fatalf("shift+tab alone reached full-auto on press %d", press+1)
		}
	}
}

// The offer must be visible, name the key, and not claim the mode has changed.
func TestOfferLabelNamesTheConfirmKey(t *testing.T) {
	label, _ := armedModel(t).modeLabel()
	if label == "full-auto" {
		t.Fatal("the offer renders as though full-auto is already active")
	}
	for _, want := range []string{"full-auto", "ctrl+g"} {
		if !strings.Contains(label, want) {
			t.Errorf("offer label %q does not mention %q", label, want)
		}
	}
}
