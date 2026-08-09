package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

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

// newModel rather than a model literal: the key handler calls m.now() before any
// early return, so a literal leaves that hook nil and every keypress panics.
//
// The filename matters as much as the constructor. This file was originally
// permission_mode_arm_test.go, and Go read the trailing "_arm" as a GOARCH
// constraint and excluded it from every amd64 build. None of these tests ran,
// locally or in CI, so the panic never surfaced and the offer gate they exist to
// prove had no coverage at all. See TestPermissionModeOfferTestsActuallyRun.
func modelInMode(t *testing.T, mode agent.PermissionMode) model {
	t.Helper()
	m := newModel(context.Background(), Options{})
	m.permissionMode = mode
	return m
}

func armedModel(t *testing.T) model {
	t.Helper()
	armed := pressKey(t, modelInMode(t, agent.PermissionModeAsk), tea.Key{Code: tea.KeyTab, Mod: tea.ModShift})
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

// Input is not only keys. The offer state lives on the model across EVERY
// message, so a path that does not clear it is a path to full-auto: pasting and
// then pressing ctrl+g for its ordinary meaning used to enter the mode with
// nobody having accepted anything.
//
// These are separate dispatch cases from the key handler, which is exactly why
// a key-only table missed them.
func TestPasteAndMouseCancelTheUnsafeOffer(t *testing.T) {
	for name, msg := range map[string]tea.Msg{
		"paste":        tea.PasteMsg{Content: "hello"},
		"mouse click":  tea.MouseClickMsg{},
		"mouse wheel":  tea.MouseWheelMsg{},
		"mouse releas": tea.MouseReleaseMsg{},
	} {
		t.Run(name, func(t *testing.T) {
			next, _ := armedModel(t).updateModel(msg)
			m := next.(model)
			if m.unsafeArmed {
				t.Fatalf("%s left the full-auto offer live, so a later ctrl+g would commit it silently", name)
			}
			m = pressKey(t, m, tea.Key{Code: 'g', Mod: tea.ModCtrl})
			if m.permissionMode == agent.PermissionModeFullAuto {
				t.Fatalf("ctrl+g after %s entered full-auto mode with no live offer", name)
			}
		})
	}
}

// Motion is the deliberate exception. Terminals stream it while tracking is on,
// so cancelling on a twitch would retract the offer before it could be accepted
// and make the gate unusable rather than safe.
func TestMouseMotionAloneDoesNotCancelTheOffer(t *testing.T) {
	next, _ := armedModel(t).updateModel(tea.MouseMotionMsg{})
	if !next.(model).unsafeArmed {
		t.Fatal("passive mouse motion withdrew the full-auto offer, so moving the mouse makes the confirm key unreachable")
	}
}

// The confirm key with no offer at all must be inert, so it cannot be used as a
// standalone shortcut into full-auto.
func TestConfirmKeyAloneNeverEntersUnsafe(t *testing.T) {
	for _, start := range []agent.PermissionMode{agent.PermissionModeAuto, agent.PermissionModeAsk} {
		m := modelInMode(t, start)
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
	m := modelInMode(t, agent.PermissionModeAuto)
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
