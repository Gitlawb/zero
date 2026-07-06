package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/Gitlawb/zero/internal/dictation"
)

// sttPartialMsg carries an incremental streaming transcript into the TUI. It is
// injected from the streaming goroutine via runtimeMessageSink — the same
// mechanism agent text deltas use (§6). text is the cumulative best transcript
// so far (not a delta); final marks a settled segment.
type sttPartialMsg struct {
	text  string
	final bool
}

// startStreamingDictation begins continuous capture and launches the streaming
// transcription command. StartStreaming spawns the capture process
// synchronously, so a failure here is immediate; on success we are already
// recording (no separate "started" round-trip).
func (m model) startStreamingDictation() (model, tea.Cmd) {
	chunks, stop, err := m.dictation.recorder.StartStreaming()
	if err != nil {
		m.dictation.reset()
		return m.appendSystemNotice(dictationErrorText(err)), nil
	}
	m.dictation.streamStop = stop
	m.dictation.phase = dictRecording

	sink := m.runtimeMessageSink
	ctx := m.dictation.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	transcriber := m.dictation.transcriber
	submit := m.dictation.cfg.AutoSubmitEnabled()

	// Tap the audio: compute a mic level per chunk for the live waveform, then
	// forward the chunk to the transcriber. A small buffer keeps the tap from
	// stalling capture if the transcriber briefly lags.
	tapped := make(chan []byte, 16)
	go func() {
		defer close(tapped)
		for chunk := range chunks {
			if sink != nil {
				sink(sttLevelMsg{level: dictation.ChunkLevel(chunk)})
			}
			tapped <- chunk
		}
	}()

	streamCmd := func() tea.Msg {
		onPartial := func(text string, final bool) {
			if sink != nil {
				sink(sttPartialMsg{text: text, final: final})
			}
		}
		text, err := transcriber.StreamTranscribe(ctx, tapped, onPartial)
		return dictationTranscribedMsg{text: text, err: err, submit: submit, streaming: true}
	}
	// Streaming drives the waveform from real levels (no synthetic tick needed).
	return m, streamCmd
}

// handleDictationPartial renders a cumulative partial transcript into the
// composer, replacing the previously-rendered live region wholesale so the text
// builds up in place as the user keeps talking.
func (m model) handleDictationPartial(msg sttPartialMsg) model {
	// Ignore stragglers that arrive after the session ended (cancel/final).
	if m.dictation.phase != dictRecording && m.dictation.phase != dictTranscribing {
		return m
	}
	m.applyStreamingText(msg.text)
	return m
}

// applyStreamingText replaces the current live region with text, tracking the
// region's rune bounds so the next partial can overwrite it. The first partial
// records the region start at the cursor and (if needed) a separating space.
func (m *model) applyStreamingText(text string) {
	state := m.currentComposerState()
	if !m.dictation.regionActive {
		m.dictation.regionActive = true
		m.dictation.regionStart = state.cursor
		m.dictation.regionEnd = state.cursor
		m.dictation.regionPrefix = ""
		if needsLeadingSpace(state) {
			// Fold the separator into the region so a cancel removes it too.
			m.dictation.regionPrefix = " "
		}
	}
	// Replace [regionStart, regionEnd) with prefix + the new cumulative text.
	rendered := m.dictation.regionPrefix + text
	cleared := deleteComposerRange(state, m.dictation.regionStart, m.dictation.regionEnd)
	cleared.cursor = m.dictation.regionStart
	updated := insertComposerText(cleared, rendered)
	m.dictation.regionEnd = m.dictation.regionStart + len([]rune(rendered))
	m.setComposerState(updated)
}

// commitDictationRegion keeps the streamed text in the composer and stops
// tracking it as a live region (used on successful completion — the final
// transcript equals the last partial already rendered).
func (m model) commitDictationRegion() model {
	m.dictation.regionActive = false
	return m
}

// discardDictationRegion removes the live streamed text from the composer (used
// on cancel — the user aborted, so the half-formed transcript is dropped).
func (m model) discardDictationRegion() model {
	if m.dictation.regionActive {
		state := m.currentComposerState()
		m.setComposerState(deleteComposerRange(state, m.dictation.regionStart, m.dictation.regionEnd))
		m.dictation.regionActive = false
	}
	return m
}
