package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/atotto/clipboard"
)

// clipboardReadMsg carries the result of an async OS-clipboard read back to
// Update. It drives the right-click "paste" path.
type clipboardReadMsg struct {
	content string
	err     error
}

// pasteFromClipboardCmd reads the OS clipboard off the Update goroutine (it
// shells out to pbpaste/xclip/etc.) and delivers the text as a clipboardReadMsg.
// A right-click pastes straight from here — no menu.
func pasteFromClipboardCmd() tea.Cmd {
	return func() tea.Msg {
		content, err := clipboard.ReadAll()
		return clipboardReadMsg{content: content, err: err}
	}
}

// routePaste inserts pasted text into whichever input surface is focused. It is
// shared by the terminal bracketed-paste handler (tea.PasteMsg) and the
// right-click paste (clipboardReadMsg) so a bracketed paste and a right-click
// paste behave identically. Surfaces with no editable text field (a permission/
// spec prompt, the MCP manager, an open picker, the detailed transcript) swallow
// the paste; empty content is a no-op.
func (m model) routePaste(content string) (tea.Model, tea.Cmd) {
	if content == "" {
		return m, nil
	}
	// Setup and the ask_user questionnaire share the main text input.
	if m.setup.visible {
		return m.handleSetupPaste(tea.PasteMsg{Content: content})
	}
	if m.pendingAskUser != nil {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(tea.PasteMsg{Content: content})
		return m, cmd
	}
	if m.providerWizard != nil {
		return m.handleProviderWizardPaste(content)
	}
	if m.transcriptDetailed || m.pendingSpecReview != nil || m.pendingPermission != nil || m.mcpAddWizard != nil || m.mcpManager != nil || m.picker != nil {
		return m, nil
	}
	state := m.currentComposerState()
	m = m.applyComposerText(state, content, true)
	m.recomputeSuggestions()
	return m, nil
}
