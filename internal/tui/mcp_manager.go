package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	mcpManagerOverlayMaxWidth = 168
	mcpManagerOverlayMinWidth = 58
	mcpManagerMaxVisible      = 7
)

type mcpManagerState struct {
	selected int
}

type mcpManagerItemKind int

const (
	mcpManagerItemServer mcpManagerItemKind = iota
	mcpManagerItemAddRemote
	mcpManagerItemAddStdio
	mcpManagerItemList
)

type mcpManagerItem struct {
	Kind  mcpManagerItemKind
	Name  string
	Label string
	Meta  string
}

func (m model) openMCPManager() model {
	m.mcpManager = &mcpManagerState{}
	m.clearSuggestions()
	return m
}

func (m model) handleMCPManagerKey(msg tea.KeyMsg) (model, tea.Cmd) {
	if m.mcpManager == nil {
		return m, nil
	}
	switch msg.Type {
	case tea.KeyEsc:
		m.mcpManager = nil
	case tea.KeyUp:
		m.moveMCPManager(-1)
	case tea.KeyDown, tea.KeyTab:
		m.moveMCPManager(1)
	case tea.KeyEnter:
		return m.chooseMCPManagerItem()
	case tea.KeyRunes:
		switch strings.ToLower(string(msg.Runes)) {
		case "a":
			return m.prefillMCPManagerCommand("/mcp add <name> --url <url>"), nil
		case "s":
			return m.prefillMCPManagerCommand("/mcp add <name> -- <command> [args...]"), nil
		case "l":
			return m.runMCPManagerCommand([]string{"list"})
		case "c":
			if item, ok := m.currentMCPManagerItem(); ok && item.Kind == mcpManagerItemServer {
				return m.runMCPManagerCommand([]string{"check", item.Name})
			}
		case "d":
			if item, ok := m.currentMCPManagerItem(); ok && item.Kind == mcpManagerItemServer {
				return m.runMCPManagerCommand([]string{"disable", item.Name})
			}
		case "e":
			if item, ok := m.currentMCPManagerItem(); ok && item.Kind == mcpManagerItemServer {
				return m.runMCPManagerCommand([]string{"enable", item.Name})
			}
		case "r":
			if item, ok := m.currentMCPManagerItem(); ok && item.Kind == mcpManagerItemServer {
				return m.runMCPManagerCommand([]string{"remove", item.Name})
			}
		}
	}
	return m, nil
}

func (m *model) moveMCPManager(delta int) {
	if m.mcpManager == nil {
		return
	}
	count := len(m.mcpManagerItems())
	if count == 0 {
		m.mcpManager.selected = 0
		return
	}
	m.mcpManager.selected = ((m.mcpManager.selected+delta)%count + count) % count
}

func (m model) chooseMCPManagerItem() (model, tea.Cmd) {
	item, ok := m.currentMCPManagerItem()
	if !ok {
		return m, nil
	}
	switch item.Kind {
	case mcpManagerItemServer:
		return m.runMCPManagerCommand([]string{"check", item.Name})
	case mcpManagerItemAddRemote:
		return m.prefillMCPManagerCommand("/mcp add <name> --url <url>"), nil
	case mcpManagerItemAddStdio:
		return m.prefillMCPManagerCommand("/mcp add <name> -- <command> [args...]"), nil
	case mcpManagerItemList:
		return m.runMCPManagerCommand([]string{"list"})
	default:
		return m, nil
	}
}

func (m model) prefillMCPManagerCommand(command string) model {
	m.mcpManager = nil
	m.input.SetValue(command)
	m.input.SetCursor(len([]rune(command)))
	m.resetComposerFromInput()
	m.clearSuggestions()
	return m
}

func (m model) runMCPManagerCommand(args []string) (model, tea.Cmd) {
	selected := 0
	if m.mcpManager != nil {
		selected = m.mcpManager.selected
	}
	text := ""
	m, text = m.handleMCPCommand(strings.Join(args, " "))
	m.mcpManager = &mcpManagerState{selected: selected}
	if items := m.mcpManagerItems(); len(items) > 0 {
		m.mcpManager.selected = clampInt(m.mcpManager.selected, 0, len(items)-1)
	}
	if text != "" {
		m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowSystem, tool: "mcp", text: text})
	}
	return m, nil
}

func (m model) currentMCPManagerItem() (mcpManagerItem, bool) {
	if m.mcpManager == nil {
		return mcpManagerItem{}, false
	}
	items := m.mcpManagerItems()
	if len(items) == 0 {
		return mcpManagerItem{}, false
	}
	m.mcpManager.selected = clampInt(m.mcpManager.selected, 0, len(items)-1)
	return items[m.mcpManager.selected], true
}

func (m model) mcpManagerItems() []mcpManagerItem {
	state := m.mcpViewState()
	items := make([]mcpManagerItem, 0, len(state.Servers)+3)
	for _, server := range state.Servers {
		name := displayValue(strings.TrimSpace(server.Name), "unnamed")
		items = append(items, mcpManagerItem{
			Kind:  mcpManagerItemServer,
			Name:  name,
			Label: name,
			Meta:  mcpManagerServerMeta(server),
		})
	}
	items = append(items,
		mcpManagerItem{Kind: mcpManagerItemAddRemote, Label: "Add MCP server", Meta: "zero mcp add <name> --url <url>"},
		mcpManagerItem{Kind: mcpManagerItemAddStdio, Label: "Add local stdio MCP", Meta: "zero mcp add <name> -- <command> [args...]"},
		mcpManagerItem{Kind: mcpManagerItemList, Label: "List configured", Meta: "zero mcp list"},
	)
	return items
}

func mcpManagerServerMeta(server MCPServerView) string {
	parts := []string{
		displayValue(strings.TrimSpace(server.State), "configured"),
	}
	if auth := strings.TrimSpace(server.Auth); auth != "" {
		parts = append(parts, auth)
	}
	if server.ToolCount > 0 {
		parts = append(parts, pluralCount(server.ToolCount, "tool"))
	}
	parts = append(parts, displayValue(strings.TrimSpace(server.Transport), "unknown"))
	return strings.Join(parts, " Â· ")
}

func (m model) mcpManagerOverlay(width int) string {
	if m.mcpManager == nil {
		return ""
	}
	if width <= 0 {
		width = defaultStartupWidth
	}
	overlayWidth := minInt(width, mcpManagerOverlayMaxWidth)
	if overlayWidth < mcpManagerOverlayMinWidth {
		overlayWidth = width
	}
	innerWidth := maxInt(1, overlayWidth-4)
	items := m.mcpManagerItems()
	if len(items) > 0 {
		m.mcpManager.selected = clampInt(m.mcpManager.selected, 0, len(items)-1)
	}

	lines := []string{
		fillPaletteLine(zeroTheme.faint.Render("â†‘/â†“ navigate   Enter action   a add remote   s add stdio   Esc close"), innerWidth, transparentSurface),
	}
	lines = append(lines, m.renderMCPManagerItemLines(innerWidth, items)...)
	lines = append(lines, zeroTheme.line.Render(strings.Repeat("â”€", innerWidth)))
	for _, line := range strings.Split(renderMCPView(m.mcpViewState(), innerWidth), "\n") {
		lines = append(lines, fitStyledLine(m.styleMCPManagerDetailLine(line), innerWidth))
	}
	return centerRenderedBlock(styledBlockFillTitle(overlayWidth, "Manage MCP servers", lines, zeroTheme.lineStrong, lipgloss.NewStyle()), width)
}

func (m model) renderMCPManagerItemLines(width int, items []mcpManagerItem) []string {
	if len(items) == 0 {
		return []string{fillPaletteLine(zeroTheme.faint.Render("  no MCP actions"), width, transparentSurface)}
	}
	maxVisible := minInt(mcpManagerMaxVisible, len(items))
	start := selectableListStart(len(items), maxVisible, m.mcpManager.selected)
	visible := items[start : start+maxVisible]
	lines := make([]string, 0, len(visible))
	for offset, item := range visible {
		index := start + offset
		surface := transparentSurface
		marker := surface(zeroTheme.faintest).Render("  ")
		if index == m.mcpManager.selected {
			surface = zeroTheme.onSel
			marker = surface(zeroTheme.accent).Render("â¯ ")
		}
		left := marker + surface(zeroTheme.ink).Render(item.Label)
		right := ""
		if item.Meta != "" {
			right = surface(zeroTheme.faint).Render(item.Meta)
		}
		gap := width - lipgloss.Width(left) - lipgloss.Width(right)
		line := left + surface(zeroTheme.ink).Render(strings.Repeat(" ", maxInt(1, gap))) + right
		lines = append(lines, fillPaletteLine(line, width, surface))
	}
	return lines
}

func (m model) styleMCPManagerDetailLine(line string) string {
	trimmed := strings.TrimSpace(line)
	switch {
	case trimmed == "":
		return ""
	case trimmed == "Manage MCP servers", trimmed == "User MCPs", trimmed == "Tools", trimmed == "Permissions", trimmed == "OAuth", trimmed == "Actions":
		return zeroTheme.accent.Bold(true).Render(line)
	case strings.Contains(trimmed, "zero mcp "):
		return zeroTheme.ink.Render(line)
	case strings.HasPrefix(trimmed, "â€º") || strings.HasPrefix(trimmed, "- "):
		return zeroTheme.ink.Render(line)
	default:
		return zeroTheme.muted.Render(line)
	}
}
