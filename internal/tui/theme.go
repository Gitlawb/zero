package tui

import "github.com/charmbracelet/lipgloss"

// tuiTheme is the single source of truth for Zero's terminal palette. Colors are
// truecolor hex so the brand cyan renders consistently across terminals; lipgloss
// downsamples automatically on limited displays and renders plain text when there
// is no TTY (e.g. during tests).
type tuiTheme struct {
	// Brand + structure.
	accent lipgloss.Style // bright brand cyan, bold
	border lipgloss.Style // dim cyan rules / frames
	text   lipgloss.Style // primary foreground
	muted  lipgloss.Style // secondary / hints
	green  lipgloss.Style // success / ready
	red    lipgloss.Style // errors
	amber  lipgloss.Style // warnings / context pressure

	// Two-tone logo.
	logoBright lipgloss.Style // solid block strokes
	logoDim    lipgloss.Style // drop-shadow strokes

	// Conversation roles.
	you  lipgloss.Style // user gutter
	zero lipgloss.Style // assistant gutter
	tool lipgloss.Style // tool glyph / name

	// Diff cards.
	diffAdd  lipgloss.Style
	diffDel  lipgloss.Style
	diffMeta lipgloss.Style

	// Permission modes.
	modeAuto   lipgloss.Style
	modeAsk    lipgloss.Style
	modeUnsafe lipgloss.Style
}

const (
	colorCyanBright = "#34E2EA"
	colorCyanSoft   = "#5EC8D8"
	colorCyanDim    = "#1F6E78"
	colorBorder     = "#2C6E78"
	colorText       = "#DCE2EA"
	colorMuted      = "#6C7682"
	colorGreen      = "#43D17A"
	colorRed        = "#F2616B"
	colorAmber      = "#E8B84B"
	colorToolName   = "#9BA6B2"
)

var zeroTheme = tuiTheme{
	accent: lipgloss.NewStyle().Foreground(lipgloss.Color(colorCyanBright)).Bold(true),
	border: lipgloss.NewStyle().Foreground(lipgloss.Color(colorBorder)),
	text:   lipgloss.NewStyle().Foreground(lipgloss.Color(colorText)),
	muted:  lipgloss.NewStyle().Foreground(lipgloss.Color(colorMuted)),
	green:  lipgloss.NewStyle().Foreground(lipgloss.Color(colorGreen)).Bold(true),
	red:    lipgloss.NewStyle().Foreground(lipgloss.Color(colorRed)).Bold(true),
	amber:  lipgloss.NewStyle().Foreground(lipgloss.Color(colorAmber)),

	logoBright: lipgloss.NewStyle().Foreground(lipgloss.Color(colorCyanBright)).Bold(true),
	logoDim:    lipgloss.NewStyle().Foreground(lipgloss.Color(colorCyanDim)),

	you:  lipgloss.NewStyle().Foreground(lipgloss.Color(colorCyanSoft)).Bold(true),
	zero: lipgloss.NewStyle().Foreground(lipgloss.Color(colorCyanBright)).Bold(true),
	tool: lipgloss.NewStyle().Foreground(lipgloss.Color(colorToolName)),

	diffAdd:  lipgloss.NewStyle().Foreground(lipgloss.Color(colorGreen)),
	diffDel:  lipgloss.NewStyle().Foreground(lipgloss.Color(colorRed)),
	diffMeta: lipgloss.NewStyle().Foreground(lipgloss.Color(colorMuted)),

	modeAuto:   lipgloss.NewStyle().Foreground(lipgloss.Color(colorGreen)).Bold(true),
	modeAsk:    lipgloss.NewStyle().Foreground(lipgloss.Color(colorAmber)).Bold(true),
	modeUnsafe: lipgloss.NewStyle().Foreground(lipgloss.Color(colorRed)).Bold(true),
}
