package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gitlawb/zero/internal/config"
)

type fakeTUITranscriber struct{}

func (fakeTUITranscriber) Transcribe(context.Context, []byte) (string, error) { return "", nil }
func (fakeTUITranscriber) StreamTranscribe(context.Context, <-chan []byte, func(string, bool)) (string, error) {
	return "", nil
}

// batchOnlyController builds a controller whose recordings take the batch path
// (streaming disabled), so startDictation returns an unexecuted command instead
// of exec'ing a real capture tool.
func batchOnlyController() dictationController {
	streamOff := false
	return dictationController{
		cfg:      config.STTConfig{Streaming: &streamOff},
		platform: "linux",
		build: func(config.STTConfig, bool) (Transcriber, bool, error) {
			return fakeTUITranscriber{}, false, nil
		},
	}
}

func TestToggleVoiceModeFlips(t *testing.T) {
	m := model{dictation: batchOnlyController()}
	next, _ := m.toggleVoiceMode()
	if !next.dictation.voiceModeEnabled {
		t.Fatal("first /voice should enable voice mode")
	}
	if !transcriptHasText(next, "Voice mode on") {
		t.Error("expected voice-mode-on notice")
	}
	off, _ := next.toggleVoiceMode()
	if off.dictation.voiceModeEnabled {
		t.Error("second /voice should disable voice mode")
	}
}

func TestVoiceModeUnavailableWithoutFactory(t *testing.T) {
	m := model{} // no build factory
	next, _ := m.toggleVoiceMode()
	if next.dictation.voiceModeEnabled {
		t.Error("voice mode must not enable when dictation is unavailable")
	}
	if !transcriptHasText(next, "not configured") {
		t.Error("expected a not-configured hint")
	}
}

func TestKeyboardEnhancementsRecorded(t *testing.T) {
	m := model{dictation: batchOnlyController()}
	// Flags with the event-types bit unset → not supported.
	m = m.handleKeyboardEnhancements(tea.KeyboardEnhancementsMsg{Flags: 0})
	if !m.dictation.eventTypesKnown || m.dictation.eventTypesSupported {
		t.Error("Flags=0 should be known-but-unsupported")
	}
}

func TestVoiceSpaceHoldStartsRecording(t *testing.T) {
	m := model{dictation: batchOnlyController()}
	m.dictation.voiceModeEnabled = true
	m.dictation.eventTypesSupported = true

	press := tea.KeyPressMsg(tea.Key{Code: tea.KeySpace})
	next, cmd := m.handleVoiceSpacePress(press)
	if next.dictation.phase != dictStarting {
		t.Fatalf("Space press should start recording (phase=%d)", next.dictation.phase)
	}
	if !next.dictation.spaceHeld {
		t.Error("spaceHeld should be set in hold mode")
	}
	if cmd == nil {
		t.Error("expected a start command")
	}
}

func TestVoiceSpaceHoldIgnoresRepeat(t *testing.T) {
	m := model{dictation: batchOnlyController()}
	m.dictation.voiceModeEnabled = true
	m.dictation.eventTypesSupported = true
	m.dictation.phase = dictRecording
	m.dictation.spaceHeld = true

	repeat := tea.KeyPressMsg(tea.Key{Code: tea.KeySpace, IsRepeat: true})
	next, _ := m.handleVoiceSpacePress(repeat)
	if next.dictation.phase != dictRecording {
		t.Error("auto-repeat while held must not restart or change phase")
	}
}

func TestVoiceSpaceReleaseStops(t *testing.T) {
	m := model{dictation: batchOnlyController()}
	m.dictation.voiceModeEnabled = true
	m.dictation.eventTypesSupported = true
	m.dictation.phase = dictRecording
	m.dictation.spaceHeld = true

	next, _ := m.handleVoiceSpaceRelease()
	if next.dictation.phase != dictTranscribing {
		t.Errorf("release should stop recording → transcribing (phase=%d)", next.dictation.phase)
	}
	if next.dictation.spaceHeld {
		t.Error("spaceHeld should clear on release")
	}
}

func TestVoiceReleaseDuringStartupDefersStop(t *testing.T) {
	m := model{dictation: batchOnlyController()}
	m.dictation.voiceModeEnabled = true
	m.dictation.eventTypesSupported = true
	m.dictation.phase = dictStarting
	m.dictation.spaceHeld = true

	next, _ := m.handleVoiceSpaceRelease()
	if !next.dictation.voiceStopPending {
		t.Error("a release during startup should defer the stop")
	}
	// When the recording finishes starting, the deferred stop fires.
	after, _ := next.handleDictationStarted(dictationStartedMsg{})
	if after.dictation.phase != dictTranscribing {
		t.Errorf("deferred stop should transition to transcribing (phase=%d)", after.dictation.phase)
	}
}

func TestVoiceSpaceToggleFallback(t *testing.T) {
	m := model{dictation: batchOnlyController()}
	m.dictation.voiceModeEnabled = true
	m.dictation.eventTypesSupported = false // no release events → toggle fallback

	press := tea.KeyPressMsg(tea.Key{Code: tea.KeySpace})
	next, _ := m.handleVoiceSpacePress(press)
	if next.dictation.phase != dictStarting {
		t.Errorf("toggle-mode Space should start recording (phase=%d)", next.dictation.phase)
	}
}
