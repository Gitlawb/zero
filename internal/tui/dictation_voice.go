package tui

import (
	tea "charm.land/bubbletea/v2"
)

// Voice mode's Space-hold gesture — the (only) dictation trigger. Two terminal
// tiers:
//
//  1. The terminal confirms key-release reporting (Ghostty/Kitty/WezTerm, …):
//     Space press starts recording, Space release stops it — true hold-to-record,
//     no ambiguity.
//  2. The terminal does not confirm release events: Space falls back to
//     press-to-toggle (press to start, press again to stop) — a deliberately
//     simpler, robust fallback than inferring release from key-repeat timing
//     (racy in a terminal). This works on ANY terminal, so voice mode is always
//     usable once enabled.
//
// Only active while voice mode is on; the rest of dispatch is built on
// KeyPressMsg and is untouched.

// handleKeyboardEnhancements records the terminal's confirmed keyboard
// capabilities, so voice mode knows whether hold-to-record is available.
func (m model) handleKeyboardEnhancements(msg tea.KeyboardEnhancementsMsg) model {
	m.dictation.eventTypesKnown = true
	m.dictation.eventTypesSupported = msg.SupportsEventTypes()
	return m
}

// handleVoiceSpacePress handles a Space press while voice mode is on.
func (m model) handleVoiceSpacePress(msg tea.KeyMsg) (model, tea.Cmd) {
	if !m.dictation.eventTypesSupported {
		// Tier 2: no release events — press-to-toggle.
		return m.toggleDictation()
	}
	// Tier 1: hold-to-record. Ignore auto-repeat presses while the key is held.
	if msg.Key().IsRepeat {
		return m, nil
	}
	if m.dictation.phase == dictIdle {
		m.dictation.spaceHeld = true
		m.dictation.voiceStopPending = false
		return m.startDictation()
	}
	return m, nil // already recording; the release will stop it
}

// handleVoiceSpaceRelease stops a hold-to-record session when Space is released.
func (m model) handleVoiceSpaceRelease() (model, tea.Cmd) {
	if !m.dictation.spaceHeld {
		return m, nil
	}
	m.dictation.spaceHeld = false
	switch m.dictation.phase {
	case dictRecording:
		return m.stopDictation()
	case dictStarting:
		// Released before the recording finished starting; stop as soon as it does.
		m.dictation.voiceStopPending = true
		return m, nil
	}
	return m, nil
}
