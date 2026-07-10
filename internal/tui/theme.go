package tui

import (
	"github.com/charmbracelet/lipgloss"
)

// Theme defines the visual styling and layout properties for the TUI.
type Theme interface {
	Name() string

	// ChatStyle returns the styling for the chat container.
	ChatStyle() lipgloss.Style

	// MessageStyle returns the styling for individual message cards based on source.
	MessageStyle(sender string) lipgloss.Style

	// SidebarStyle returns the styling for the sidebar.
	SidebarStyle() lipgloss.Style

	// Border returns the character definitions for panel borders.
	Border() lipgloss.Border

	// BorderStyle returns the border styling.
	BorderStyle() lipgloss.Style
}

// ClaudeTheme represents the warm, card-based, minimalist Claude layout.
type ClaudeTheme struct{}

var _ Theme = ClaudeTheme{}

func (t ClaudeTheme) Name() string { return "claude" }

func (t ClaudeTheme) ChatStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Padding(1, 4).
		Align(lipgloss.Center)
}

func (t ClaudeTheme) MessageStyle(sender string) lipgloss.Style {
	if sender == "model" {
		return lipgloss.NewStyle().
			Background(lipgloss.Color("#fbf0e3")). // warm card background
			Foreground(lipgloss.Color("#1f1f1f")).
			Padding(1, 2).
			MarginBottom(1)
	}
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("#1f1f1f")).
		Padding(1, 1).
		MarginBottom(1)
}

func (t ClaudeTheme) SidebarStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.LeftBorder()).
		BorderForeground(lipgloss.Color("#d0c4b2")).
		Padding(1)
}

func (t ClaudeTheme) Border() lipgloss.Border { return lipgloss.RoundedBorder() }

func (t ClaudeTheme) BorderStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("#d0c4b2"))
}

// CodexTheme represents the high-density cyberpunk / IDE technical layout.
type CodexTheme struct{}

var _ Theme = CodexTheme{}

func (t CodexTheme) Name() string { return "codex" }

func (t CodexTheme) ChatStyle() lipgloss.Style {
	return lipgloss.NewStyle().Padding(0, 1)
}

func (t CodexTheme) MessageStyle(sender string) lipgloss.Style {
	if sender == "model" {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color("#00ff00")). // Matrix green
			Padding(0)
	}
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("#00ffff")). // Cyberpunk cyan
		Padding(0)
}

func (t CodexTheme) SidebarStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("#00ff00")).
		Padding(0)
}

func (t CodexTheme) Border() lipgloss.Border { return lipgloss.ThickBorder() }

func (t CodexTheme) BorderStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("#00ff00"))
}
