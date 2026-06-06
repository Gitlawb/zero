package tui

import (
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const (
	defaultStartupWidth  = 96
	defaultStartupHeight = 30
	minStartupWidth      = 58
	maxPromptWidth       = 140
)

// zeroLogoLines is the ZERO wordmark in the ANSI Shadow figlet style. The solid
// block strokes render in bright cyan and the drop-shadow box-drawing strokes in
// dim cyan, which gives the wordmark depth without any per-glyph color hacks.
var zeroLogoLines = []string{
	`███████╗███████╗██████╗  ██████╗ `,
	`╚══███╔╝██╔════╝██╔══██╗██╔═══██╗`,
	`  ███╔╝ █████╗  ██████╔╝██║   ██║`,
	` ███╔╝  ██╔══╝  ██╔══██╗██║   ██║`,
	`███████╗███████╗██║  ██║╚██████╔╝`,
	`╚══════╝╚══════╝╚═╝  ╚═╝ ╚═════╝ `,
}

// logoShadowRunes are the box-drawing strokes that form the wordmark's shadow and
// render in the dim cyan tone; everything else (the solid blocks) renders bright.
var logoShadowRunes = map[rune]bool{
	'╗': true, '╔': true, '╝': true, '╚': true, '║': true, '═': true,
}

func (m model) startupView() string {
	width := normalizedStartupWidth(m.width)
	height := normalizedStartupHeight(m.height)

	header := m.startupHeader(width)
	logo := m.startupLogo(width)
	chips := centerLine(m.commandChips(), width)
	subtitle := centerLine(zeroTheme.accent.Render("terminal coding agent"), width)
	prompt := m.startupPrompt(width)
	shortcuts := centerLine(m.modeHint(), width)

	contentLines := countLines(header) + countLines(logo) + 1 + 1 + countLines(prompt) + 1
	centerGap := clamp((height-contentLines)/3, 1, 7)
	promptGap := clamp(height-contentLines-centerGap, 1, 8)

	var builder strings.Builder
	builder.WriteString(header)
	builder.WriteString(strings.Repeat("\n", centerGap))
	builder.WriteString(logo)
	builder.WriteString("\n")
	builder.WriteString(subtitle)
	builder.WriteString("\n\n")
	builder.WriteString(chips)
	builder.WriteString(strings.Repeat("\n", promptGap))
	builder.WriteString(prompt)
	builder.WriteString("\n")
	builder.WriteString(shortcuts)

	return builder.String()
}

func (m model) startupHeader(width int) string {
	project := strings.ToLower(filepath.Base(m.cwd))
	if project == "." || project == "" {
		project = "zero"
	}

	provider := displayValue(m.providerName, "none")
	model := displayValue(m.modelName, "none")
	line := startupHeaderLine(width-4, []headerCandidate{
		{
			left: zeroTheme.accent.Render("ZERO") +
				zeroTheme.muted.Render(" | cwd: ") + zeroTheme.text.Render(displayValue(m.cwd, "unknown")) +
				zeroTheme.muted.Render(" | project: ") + zeroTheme.text.Render(project),
			right: zeroTheme.muted.Render("status: ") + zeroTheme.green.Render("READY") +
				zeroTheme.muted.Render(" | provider: ") + zeroTheme.accent.Render(provider) +
				zeroTheme.text.Render(" / "+model),
		},
		{
			left: zeroTheme.accent.Render("ZERO") +
				zeroTheme.muted.Render(" | cwd: ") + zeroTheme.text.Render(displayValue(filepath.Base(m.cwd), "unknown")) +
				zeroTheme.muted.Render(" | project: ") + zeroTheme.text.Render(project),
			right: zeroTheme.green.Render("READY") +
				zeroTheme.muted.Render(" | provider: ") + zeroTheme.accent.Render(provider) +
				zeroTheme.text.Render(" / "+model),
		},
		{
			left:  zeroTheme.accent.Render("ZERO") + zeroTheme.muted.Render(" | ") + zeroTheme.text.Render(project),
			right: zeroTheme.green.Render("READY") + zeroTheme.muted.Render(" | ") + zeroTheme.accent.Render(provider) + zeroTheme.text.Render("/"+model),
		},
		{
			left:  zeroTheme.accent.Render("ZERO"),
			right: zeroTheme.green.Render("READY"),
		},
	})
	return borderedBlock(width, []string{line})
}

func (m model) startupLogo(width int) string {
	lines := make([]string, 0, len(zeroLogoLines))
	for _, line := range zeroLogoLines {
		lines = append(lines, centerLine(renderTwoToneLogo(line), width))
	}
	return strings.Join(lines, "\n")
}

// renderTwoToneLogo colors a single wordmark line: solid blocks bright, shadow
// strokes dim. lipgloss.Width (used by centerLine) ignores ANSI, so centering
// stays correct regardless of color profile.
func renderTwoToneLogo(line string) string {
	var builder strings.Builder
	for _, glyph := range line {
		switch {
		case glyph == ' ':
			builder.WriteByte(' ')
		case logoShadowRunes[glyph]:
			builder.WriteString(zeroTheme.logoDim.Render(string(glyph)))
		default:
			builder.WriteString(zeroTheme.logoBright.Render(string(glyph)))
		}
	}
	return builder.String()
}

func (m model) commandChips() string {
	chips := []string{"/plan", "/debug", "/tools", "/model", "/provider"}
	parts := make([]string, 0, len(chips))
	for _, chip := range chips {
		parts = append(parts, zeroTheme.border.Render("[ "+chip+" ]"))
	}
	return strings.Join(parts, "  ")
}

func (m model) startupPrompt(width int) string {
	promptWidth := width - 12
	if promptWidth > maxPromptWidth {
		promptWidth = maxPromptWidth
	}
	if promptWidth < minStartupWidth {
		promptWidth = minStartupWidth
	}

	block := borderedBlock(promptWidth, []string{m.input.View()})
	return indentBlock(block, (width-promptWidth)/2)
}

func borderedBlock(width int, lines []string) string {
	if width < 4 {
		width = 4
	}

	rule := strings.Repeat("─", width-2)
	top := zeroTheme.border.Render("╭" + rule + "╮")
	bottom := zeroTheme.border.Render("╰" + rule + "╯")
	body := make([]string, 0, len(lines)+2)
	body = append(body, top)
	for _, line := range lines {
		available := width - 4
		body = append(body, zeroTheme.border.Render("│ ")+fitStyledLine(line, available)+strings.Repeat(" ", maxInt(0, available-lipgloss.Width(line)))+zeroTheme.border.Render(" │"))
	}
	body = append(body, bottom)
	return strings.Join(body, "\n")
}

func joinHeaderLine(left string, right string, width int) string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 2 {
		return left + "  " + right
	}
	return left + strings.Repeat(" ", gap) + right
}

type headerCandidate struct {
	left  string
	right string
}

func startupHeaderLine(width int, candidates []headerCandidate) string {
	for _, candidate := range candidates {
		line := joinHeaderLine(candidate.left, candidate.right, width)
		if lipgloss.Width(line) <= width {
			return line
		}
	}
	return zeroTheme.accent.Render("ZERO") + strings.Repeat(" ", maxInt(1, width-10)) + zeroTheme.green.Render("READY")
}

func centerLine(line string, width int) string {
	padding := (width - lipgloss.Width(line)) / 2
	if padding < 0 {
		padding = 0
	}
	return strings.Repeat(" ", padding) + line
}

func indentBlock(block string, spaces int) string {
	if spaces <= 0 {
		return block
	}

	prefix := strings.Repeat(" ", spaces)
	lines := strings.Split(block, "\n")
	for index, line := range lines {
		lines[index] = prefix + line
	}
	return strings.Join(lines, "\n")
}

func fitStyledLine(line string, width int) string {
	if lipgloss.Width(line) <= width {
		return line
	}
	return line
}

func normalizedStartupWidth(width int) int {
	if width <= 0 {
		return defaultStartupWidth
	}
	if width < minStartupWidth {
		return minStartupWidth
	}
	return width
}

func normalizedStartupHeight(height int) int {
	if height <= 0 {
		return defaultStartupHeight
	}
	if height < 18 {
		return 18
	}
	return height
}

func countLines(value string) int {
	if value == "" {
		return 0
	}
	return strings.Count(value, "\n") + 1
}

func clamp(value int, minimum int, maximum int) int {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func maxInt(left int, right int) int {
	if left > right {
		return left
	}
	return right
}
