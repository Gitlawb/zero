package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Gitlawb/zero/internal/agent"
)

// This file implements the settled-row flush frontier that gives the chat
// surface real terminal scrollback.
//
// Bubble Tea's inline renderer manages at most one screen of output: anything
// View() returns above the terminal height is clipped and never reaches the
// terminal, so a transcript rendered wholly inside View() loses its history —
// scrolling up shows whatever was on screen before the program started. The
// fix is the same architecture Claude Code uses: once a transcript row's
// visual can never change again ("settled"), render it exactly once and emit
// it ABOVE the managed region via tea.Println, where it becomes native
// terminal text — scrollable, selectable, copyable. View() then renders only
// the live tail: running tool cards, undecided prompts, streaming text, the
// composer, and the status line.
//
// m.flushed is the frontier: rows[:flushed] are already in scrollback and are
// never re-rendered. The frontier only moves forward; an unsettled row (a
// still-running tool call, an undecided permission prompt) blocks it so
// scrollback order always matches transcript order, including the collapse
// rules (a call row collapses into its result card; a decided prompt collapses
// into its decision line).

// flushedMsg acknowledges that a queued scrollback print landed. Prints are
// serialized through this ack because Bubble Tea executes batched commands
// concurrently: two outstanding tea.Println commands could deliver out of
// order, swapping rows in the permanent history.
type flushedMsg struct{}

// settledRow reports whether a row's visual can never change again, making it
// safe to print to native scrollback exactly once.
func (m model) settledRow(row transcriptRow, rc rowContext) bool {
	switch row.kind {
	case rowToolCall:
		// Resolved calls collapse into their result card (which is itself
		// settled). An unresolved call still animates its spinner while its run
		// is live; once the run is over it is a static orphan card.
		if row.id != "" && rc.resolved[rcKey(row.runID, row.id)] {
			return true
		}
		return !(m.pending && row.runID != 0 && row.runID == m.activeRunID)
	case rowPermission:
		event := row.permission
		if event == nil || event.ToolCallID == "" || event.Action != agent.PermissionActionPrompt {
			return true
		}
		if rc.decided[rcKey(row.runID, event.ToolCallID)] {
			return true
		}
		// An undecided prompt renders live (with the modal card) until its
		// decision lands or its run ends.
		return !(m.pending && row.runID != 0 && row.runID == m.activeRunID)
	default:
		// User/system/error/assistant/ask-user rows are appended in final form;
		// welcome rows render nothing and settle trivially.
		return true
	}
}

// settleTranscript advances the flush frontier: every settled row at the front
// of the unflushed tail is rendered once and queued for scrollback. It returns
// the (possibly nil) print command for the queued batch.
func (m model) settleTranscript() (model, tea.Cmd) {
	// In alt-screen mode there is no native scrollback surface for tea.Println:
	// Bubble Tea drops that output by design. Keep rows in the managed view so
	// the chat behaves like a fullscreen app and cannot reveal prior shell
	// history by scrolling.
	if m.altScreen {
		return m, nil
	}
	// Never freeze history at the pre-WindowSizeMsg default width: the first
	// real WindowSizeMsg arrives before any user-visible content settles, and
	// flushing earlier would hard-wrap startup rows at 96 cols forever.
	if m.width <= 0 {
		return m, nil
	}
	if m.flushed > len(m.transcript) {
		// The transcript was rebuilt shorter (/clear, /resume, /rewind reset the
		// frontier themselves; this is a safety net).
		m.flushed = len(m.transcript)
	}
	rc := buildRowContext(m.transcript)
	width := chatWidth(m.width)
	batch := []string{}
	previousKind, havePreviousKind := previousVisibleTranscriptKind(m.transcript, m.flushed, rc)
	for m.flushed < len(m.transcript) {
		row := m.transcript[m.flushed]
		if !m.settledRow(row, rc) {
			break
		}
		m.flushed++
		if row.kind == rowWelcome || rc.skip(row) {
			continue
		}
		rendered := m.renderRowMode(row, width, rc, true)
		if rendered == "" {
			continue
		}
		if m.flushedAny && startsTurn(row.kind) {
			rendered = "\n" + rendered
		}
		if (m.flushedAny || havePreviousKind) && previousKind == rowUser && row.kind == rowReasoning {
			rendered = "\n" + rendered
		}
		m.flushedAny = true
		batch = append(batch, rendered)
		previousKind = row.kind
		havePreviousKind = true
	}
	if len(batch) > 0 {
		m.flushQueue = append(m.flushQueue, strings.Join(batch, "\n"))
	}
	return m.drainFlushQueue()
}

// drainFlushQueue emits everything queued for scrollback as one ordered print,
// unless a print is already in flight (its flushedMsg ack re-drains).
func (m model) drainFlushQueue() (model, tea.Cmd) {
	if m.printInFlight || len(m.flushQueue) == 0 {
		return m, nil
	}
	out := strings.Join(m.flushQueue, "\n")
	m.flushQueue = nil
	m.printInFlight = true
	return m, tea.Sequence(tea.Println(out), func() tea.Msg { return flushedMsg{} })
}

// resetFlushFrontier rewinds the frontier after the transcript is rebuilt
// (/clear, /resume, /rewind). Scrollback cannot be un-printed, so the rebuilt
// transcript flushes fresh below a faint divider that marks where the previous
// surface ended.
func (m *model) resetFlushFrontier(divider string) {
	m.flushed = 0
	if divider != "" && m.flushedAny {
		m.flushQueue = append(m.flushQueue, zeroTheme.faint.Render(divider))
	}
}
