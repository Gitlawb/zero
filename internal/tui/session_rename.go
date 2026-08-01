package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type sessionRenamePrompt struct{}

func (m model) openSessionRenamePrompt() model {
	if m.sessionStore == nil {
		return m.appendSessionRenameError("session storage is unavailable")
	}
	m.clearComposer()
	title := m.activeSession.Title
	if m.activeSession.SessionID == "" {
		title = m.pendingSessionTitle
	}
	m.input.SetValue(title)
	m.input.CursorEnd()
	m.renamePrompt = &sessionRenamePrompt{}
	return m
}

func (m model) handleSessionRenameKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case keyIs(msg, tea.KeyEsc), keyCtrl(msg, 'c'):
		m.renamePrompt = nil
		m.clearComposer()
		return m, nil
	case keyIs(msg, tea.KeyEnter):
		title := m.input.Value()
		m.renamePrompt = nil
		m.clearComposer()
		return m.renameActiveSession(title), nil
	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
}

func (m model) renameActiveSession(title string) model {
	title = cutRunes(strings.TrimSpace(title), tuiSessionTitleLimit)
	if title == "" {
		return m.appendSessionRenameError("session name cannot be empty")
	}
	if m.sessionStore == nil {
		return m.appendSessionRenameError("session storage is unavailable")
	}
	if m.activeSession.SessionID == "" {
		m.pendingSessionTitle = title
		m.transcript = reduceTranscript(m.transcript, transcriptAction{
			kind: actionAppendSystem,
			text: "Session renamed to " + title + ".",
		})
		return m
	}
	updated, err := m.sessionStore.UpdateTitle(m.activeSession.SessionID, title)
	if err != nil {
		return m.appendSessionRenameError(err.Error())
	}
	m.activeSession.Title = updated.Title
	if m.titledSessions == nil {
		m.titledSessions = map[string]bool{}
	}
	m.titledSessions[updated.SessionID] = true
	m.transcript = reduceTranscript(m.transcript, transcriptAction{
		kind: actionAppendSystem,
		text: "Session renamed to " + updated.Title + ".",
	})
	return m
}

func (m model) appendSessionRenameError(detail string) model {
	m.transcript = reduceTranscript(m.transcript, transcriptAction{
		kind: actionAppendError,
		text: fmt.Sprintf("Could not rename session: %s.", strings.TrimSuffix(strings.TrimSpace(detail), ".")),
	})
	return m
}

func (m model) sessionRenamePromptView(width int) string {
	if width <= 0 {
		width = defaultStartupWidth
	}
	input := m.input
	input.Prompt = "> "
	input.Placeholder = "Type a name"
	innerWidth := maxInt(1, width-4)
	line := renderComposerInput(
		input,
		composerState{text: input.Value(), cursor: input.Position()},
		innerWidth,
		m.composerCursorVisible,
		composerSelectionState{},
	)
	lines := append(strings.Split(line, "\n"),
		zeroTheme.line.Render(strings.Repeat("─", innerWidth)),
		zeroTheme.faint.Render("Enter save   Esc cancel"),
	)
	return styledBlockFillTitle(width, "Rename session", lines, zeroTheme.lineStrong, lipgloss.NewStyle())
}
