package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gitlawb/zero/internal/agent"
)

// armFullAutoOffer puts the model in the state where the very next ctrl+g would
// enter full-auto, and fails rather than continuing if that state was not
// reached, so a change in the mode cycle cannot make these tests vacuous.
func armFullAutoOffer(t *testing.T) model {
	t.Helper()
	m := newModel(context.Background(), Options{PermissionMode: agent.PermissionModeAuto})
	m.width = 96
	updated, _ := m.Update(testKeyShift(tea.KeyTab)) // Auto -> Ask
	m = updated.(model)
	updated, _ = m.Update(testKeyShift(tea.KeyTab)) // Ask -> offers full-auto
	m = updated.(model)
	if !m.unsafeArmed {
		t.Fatalf("SETUP INVALID: the offer was not armed, so this test proves nothing")
	}
	return m
}

// DEFERRED INPUT CANCELS THE OFFER, and a right-click paste is deferred.
//
// tea.PasteMsg clears the arm, but a right-click does not arrive that way. It
// starts pasteFromClipboardCmd, and the OS clipboard read takes long enough that
// shift+tab can reach the full-auto offer while it is in flight. The delayed
// clipboardReadMsg then inserts text through routePaste, and the arm used to
// survive it, so an ordinary ctrl+g afterwards silently turned permission
// prompts off with the user several actions past the confirmation.
func TestDeferredClipboardPasteCancelsTheFullAutoOffer(t *testing.T) {
	for _, testCase := range []struct {
		name string
		msg  tea.Msg
	}{
		{name: "text arrives", msg: clipboardReadMsg{content: "some pasted text"}},
		{name: "read failed", msg: clipboardReadMsg{err: errTestClipboard}},
		{name: "empty clipboard probes for an image", msg: clipboardReadMsg{}},
		{name: "image arrives", msg: clipboardImageMsg{data: []byte("not-a-real-png"), mediaType: "image/png"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			m := armFullAutoOffer(t)

			updated, _ := m.Update(testCase.msg)
			m = updated.(model)
			if m.unsafeArmed {
				t.Errorf("the offer survived deferred clipboard delivery, so an unrelated ctrl+g can still enter full-auto")
			}

			updated, _ = m.Update(testKeyCtrl('g'))
			m = updated.(model)
			if m.permissionMode == agent.PermissionModeFullAuto {
				t.Errorf("full-auto entered after deferred clipboard input, with no fresh offer")
			}
		})
	}
}

// Transcribed speech is committed into the composer, so it is deferred input on
// the same rule.
func TestDeferredDictationCancelsTheFullAutoOffer(t *testing.T) {
	m := armFullAutoOffer(t)

	updated, _ := m.Update(dictationTranscribedMsg{})
	m = updated.(model)
	if m.unsafeArmed {
		t.Errorf("the offer survived a transcription delivery")
	}

	updated, _ = m.Update(testKeyCtrl('g'))
	m = updated.(model)
	if m.permissionMode == agent.PermissionModeFullAuto {
		t.Errorf("full-auto entered after dictation input, with no fresh offer")
	}
}

var errTestClipboard = errTestClipboardRead{}

type errTestClipboardRead struct{}

func (errTestClipboardRead) Error() string { return "no clipboard utility" }
