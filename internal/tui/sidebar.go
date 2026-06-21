package tui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/Gitlawb/zero/internal/sessions"
)

// The collapsible sidebar is a left-docked rail composited over the transcript
// body (the title bar and composer stay full width). It reuses the proven
// overlayViewportLine compositor, so it never disturbs the transcript's width,
// scroll, flush frontier, or selectable-line geometry — when hidden (the default)
// the chat is byte-identical to before. All its data comes from existing sources:
// the resumable session list from the session store and the files this session
// has mutated, read out of the transcript's edit/write tool calls.
const (
	sidebarWidth       = 30
	sidebarMaxSessions = 6
	sidebarMaxFiles    = 8
)

// wordmark renders the inline ZERO brand for the title bar: white "ZER" + lime "O".
func wordmark() string {
	return zeroTheme.white.Render("ZER") + zeroTheme.accent.Render("O")
}

// sidebarFits reports whether the terminal is wide enough to dock the rail without
// crushing the conversation.
func sidebarFits(width int) bool { return width >= sidebarWidth+28 }

func (m model) toggleSidebar() model {
	if !m.altScreen {
		return m // the rail is an alt-screen affordance; inline mode stays linear
	}
	m.sidebarVisible = !m.sidebarVisible
	if m.sidebarVisible {
		m.sidebarSessions = m.loadSidebarSessions()
	}
	return m
}

func (m model) loadSidebarSessions() []sessions.Metadata {
	if m.sessionStore == nil {
		return nil
	}
	list, err := m.sessionStore.ListResumable()
	if err != nil || len(list) == 0 {
		return nil
	}
	if len(list) > sidebarMaxSessions {
		list = list[:sidebarMaxSessions]
	}
	return list
}

// sidebarChangedFiles collects the workspace files this session has mutated, from
// the edit/write/apply tool calls already in the transcript — no new data source.
func (m model) sidebarChangedFiles() []string {
	seen := map[string]bool{}
	var files []string
	for _, row := range m.transcript {
		if row.kind != rowToolCall {
			continue
		}
		switch row.tool {
		case "edit_file", "write_file", "apply_patch", "create_file":
		default:
			continue
		}
		path := strings.TrimSpace(row.detail)
		if path == "" || !looksLikePath(path) || seen[path] {
			continue
		}
		seen[path] = true
		files = append(files, path)
		if len(files) >= sidebarMaxFiles {
			break
		}
	}
	return files
}

// sidebarPanelLines renders the rail to exactly `height` rows: the real resumable
// session list (active row highlighted) and this session's changed files, each row
// closed with a right border.
func (m model) sidebarPanelLines(height int) []string {
	innerW := sidebarWidth - 1
	border := zeroTheme.line.Render("│")
	row := func(s string) string { return padStyledLine(s, innerW) + border }
	title := func(s string) string { return row(zeroTheme.dimmer.Render(s)) }
	activeRow := func(label string) string {
		content := " ● " + label
		if pad := innerW - lipgloss.Width(content); pad > 0 {
			content += strings.Repeat(" ", pad)
		}
		return zeroTheme.onSel(zeroTheme.ink).Render(content) + border
	}

	lines := make([]string, 0, height)
	lines = append(lines, title(" SESSIONS"))
	active := strings.TrimSpace(m.activeSession.SessionID)
	if len(m.sidebarSessions) == 0 {
		lines = append(lines, row(zeroTheme.faint.Render("  (no sessions yet)")))
	}
	for _, s := range m.sidebarSessions {
		label := strings.TrimSpace(s.Title)
		if label == "" {
			label = s.SessionID
		}
		label = middleTruncate(label, maxInt(4, innerW-4))
		if active != "" && s.SessionID == active {
			lines = append(lines, activeRow(label))
			continue
		}
		lines = append(lines, row(zeroTheme.muted.Render(" ○ "+label)))
	}

	lines = append(lines, row(""))
	lines = append(lines, title(" CHANGED FILES"))
	changed := m.sidebarChangedFiles()
	if len(changed) == 0 {
		lines = append(lines, row(zeroTheme.faint.Render("  (no changes yet)")))
	}
	for _, f := range changed {
		shown := middleTruncate(displayPath(m.cwd, f), maxInt(4, innerW-2))
		lines = append(lines, row(zeroTheme.accent.Render("  "+shown)))
	}

	for len(lines) < height {
		lines = append(lines, row(""))
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return lines
}

// applySidebar docks the rail over the left columns of the transcript body rows.
func (m model) applySidebar(bodyWindow []string, width int) []string {
	if !m.sidebarVisible || !sidebarFits(width) || len(bodyWindow) == 0 {
		return bodyWindow
	}
	panel := m.sidebarPanelLines(len(bodyWindow))
	for i := range bodyWindow {
		var line string
		if i < len(panel) {
			line = panel[i]
		}
		bodyWindow[i] = overlayViewportLine(bodyWindow[i], line, 0, sidebarWidth, width)
	}
	return bodyWindow
}
