package tui

import (
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
)

const (
	defaultStartupWidth  = 96
	defaultStartupHeight = 30
	minStartupWidth      = 58
)

// zeroWordmarkPrefixLines is the white `ZER` part of the empty-state ANSI
// Shadow-style wordmark. zeroWordmarkOLines keeps the old lime `O` glyph.
var zeroWordmarkPrefixLines = []string{
	`███████╗███████╗██████╗ `,
	`╚══███╔╝██╔════╝██╔══██╗`,
	`  ███╔╝ █████╗  ██████╔╝`,
	` ███╔╝  ██╔══╝  ██╔══██╗`,
	`███████╗███████╗██║  ██║`,
	`╚══════╝╚══════╝╚═╝  ╚═╝`,
}

var zeroWordmarkOLines = []string{
	` ██████╗ `,
	`██╔═══██╗`,
	`██║   ██║`,
	`██║   ██║`,
	`╚██████╔╝`,
	` ╚═════╝ `,
}

const emptyStateTagline = "Any model. Every tool. Zero limits."

// emptyState renders the centered stream-area block shown while the
// transcript has no real content: the brand glyph and tagline.
func (m model) emptyState(width int) string {
	lines := m.emptyStateLines(width)

	// Vertically center within the stream area: the frame around it (title bar,
	// rules, composer, status line) occupies ~6 terminal rows.
	height := normalizedStartupHeight(m.height)
	gap := clamp((height-6-len(lines))/2, 0, 12)
	return strings.Repeat("\n", gap) + strings.Join(lines, "\n") + strings.Repeat("\n", gap)
}

func (m model) emptyStateWithOverlay(width int, overlay string) string {
	lines := viewLines(overlay)
	for index := range lines {
		lines[index] = fitStyledLine(lines[index], width)
	}

	// Center the palette in the visible chat area. While the command palette is
	// open it replaces the empty-state wordmark instead of sitting below it.
	available := normalizedStartupHeight(m.height) - 5
	if m.titleBarInTranscriptBody() {
		available -= 2
	}
	gap := maxInt(0, (available-len(lines))/2)
	return strings.Repeat("\n", gap) + strings.Join(lines, "\n") + strings.Repeat("\n", gap)
}

func (m model) emptyStateLines(width int) []string {
	lines := []string{}
	for _, glyph := range zeroWordmarkLines() {
		lines = append(lines, centerLine(glyph, width))
	}
	lines = append(lines, "")
	lines = append(lines, centerLine(zeroTheme.muted.Render(emptyStateTagline), width))
	// Orientation: where ZERO is pointed (cwd · branch · model) so a returning user
	// sees the context before typing instead of a blank brand screen.
	if orient := m.emptyStateOrientation(); orient != "" {
		lines = append(lines, "")
		lines = append(lines, centerLine(orient, width))
	}
	// A couple of example prompts to seed the first message.
	lines = append(lines, "")
	lines = append(lines, centerLine(zeroTheme.faint.Render(emptyStateExamples), width))
	lines = append(lines, "")
	lines = append(lines, centerLine(zeroTheme.faint.Render("Press ? for keyboard shortcuts · / for commands"), width))
	// centerLine pads but never truncates; below ~62 cols the lines would exceed
	// the frame without this fit.
	for index := range lines {
		lines[index] = fitStyledLine(lines[index], width)
	}
	return lines
}

// emptyStateExamples seeds the first prompt with a few representative asks.
const emptyStateExamples = `Try  "explain this codebase"  ·  "fix the failing test"  ·  "add a --json flag"`

// emptyStateOrientation renders a faint "version · cwd · branch · model" line
// for the home screen, omitting any piece that's unknown. Empty when nothing
// is known.
func (m model) emptyStateOrientation() string {
	parts := make([]string, 0, 4)
	if version := displayVersion(m.appVersion); version != "" {
		parts = append(parts, version)
	}
	if cwd := strings.TrimSpace(m.cwd); cwd != "" {
		parts = append(parts, shortenPath(cwd))
	}
	if branch := strings.TrimSpace(m.gitBranch); branch != "" {
		parts = append(parts, branch)
	}
	if model := strings.TrimSpace(m.modelName); model != "" {
		parts = append(parts, model)
	}
	if len(parts) == 0 {
		return ""
	}
	return zeroTheme.faint.Render(strings.Join(parts, "  ·  "))
}

// displayVersion formats the CLI build version for display: numeric releases
// gain a "v" prefix (0.2.0 → v0.2.0), anything else (dev, git SHAs) passes
// through unchanged. Empty stays empty so unset builds show nothing.
func displayVersion(version string) string {
	version = strings.TrimSpace(version)
	if version == "" {
		return ""
	}
	if version[0] >= '0' && version[0] <= '9' {
		return "v" + version
	}
	return version
}

func zeroWordmarkLines() []string {
	lines := make([]string, 0, minInt(len(zeroWordmarkPrefixLines), len(zeroWordmarkOLines)))
	for index := 0; index < len(zeroWordmarkPrefixLines) && index < len(zeroWordmarkOLines); index++ {
		lines = append(lines, zeroTheme.ink.Render(zeroWordmarkPrefixLines[index])+zeroTheme.accent.Render(zeroWordmarkOLines[index]))
	}
	return lines
}

// Wordmark renders the brand ASCII art shown on the TUI's empty state, for
// reuse outside the TUI (e.g. `zero --version`). It is deliberately
// uncolored: only newModel resolves --theme/ZERO_THEME, so painting the
// global palette here would ignore a selected light theme and be unreadable
// on light terminals. The terminal's default foreground works everywhere.
func Wordmark() string {
	lines := make([]string, 0, minInt(len(zeroWordmarkPrefixLines), len(zeroWordmarkOLines)))
	for index := 0; index < len(zeroWordmarkPrefixLines) && index < len(zeroWordmarkOLines); index++ {
		lines = append(lines, zeroWordmarkPrefixLines[index]+zeroWordmarkOLines[index])
	}
	return strings.Join(lines, "\n")
}

// styledBlock draws a rounded box around lines with the given border style,
// padding every row to the full width.
func styledBlock(width int, lines []string, borderStyle lipgloss.Style) string {
	return styledBlockFill(width, lines, borderStyle, lipgloss.NewStyle())
}

// styledBlockFill is styledBlock with a fill style painting the row padding,
// so tinted cards (permission, panel surfaces) read as solid bands. On tiny
// terminals every card loses its side borders (top/bottom rules stay) so the
// 4 border cells go back to content.
func styledBlockFill(width int, lines []string, borderStyle lipgloss.Style, fill lipgloss.Style) string {
	if width < 4 {
		width = 4
	}

	if widthTier(width) == tierTiny {
		rule := borderStyle.Render(strings.Repeat("─", width))
		body := make([]string, 0, len(lines)+2)
		body = append(body, rule)
		for _, line := range lines {
			body = append(body, fitStyledLine(line, width))
		}
		body = append(body, rule)
		return strings.Join(body, "\n")
	}

	rule := strings.Repeat("─", width-2)
	top := borderStyle.Render("╭" + rule + "╮")
	bottom := borderStyle.Render("╰" + rule + "╯")
	body := make([]string, 0, len(lines)+2)
	body = append(body, top)
	for _, line := range lines {
		available := width - 4
		fitted := fitStyledLine(line, available)
		pad := fill.Render(strings.Repeat(" ", maxInt(0, available-lipgloss.Width(fitted))))
		body = append(body, borderStyle.Render("│ ")+fitted+pad+borderStyle.Render(" │"))
	}
	body = append(body, bottom)
	return strings.Join(body, "\n")
}

// middleTruncate shortens a path-like value from the middle so both the
// leading segment and the file name survive: internal/…/root.go.
func middleTruncate(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit <= 3 {
		return truncateRunes(value, limit)
	}
	keep := limit - 1
	front := keep / 2
	back := keep - front
	return string(runes[:front]) + "…" + string(runes[len(runes)-back:])
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
	// Nothing fits whole: truncate the most minimal candidate rather than
	// inventing different content.
	if len(candidates) == 0 {
		return ""
	}
	last := candidates[len(candidates)-1]
	return fitStyledLine(joinHeaderLine(last.left, last.right, width), width)
}

func centerLine(line string, width int) string {
	padding := (width - lipgloss.Width(line)) / 2
	if padding < 0 {
		padding = 0
	}
	return strings.Repeat(" ", padding) + line
}

func rightAlignedLine(line string, width int) string {
	padding := width - lipgloss.Width(line)
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
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(line) <= width {
		return line
	}
	return truncateStyledLine(line, width)
}

func truncateStyledLine(line string, width int) string {
	const resetANSI = "\x1b[0m"

	ellipsis := "…"
	ellipsisWidth := lipgloss.Width(ellipsis)
	if width <= ellipsisWidth {
		return ellipsis
	}

	targetWidth := width - ellipsisWidth
	usedWidth := 0
	sawANSI := false
	openLink := false

	var builder strings.Builder
	for index := 0; index < len(line); {
		if line[index] == '\x1b' {
			end := ansiSequenceEnd(line, index)
			if end > index {
				sequence := line[index:end]
				builder.WriteString(sequence)
				sawANSI = true
				// Track OSC 8 hyperlink state: truncating between an open and
				// its terminator would leak the link onto everything after.
				if strings.HasPrefix(sequence, "\x1b]8;") {
					openLink = sequence != "\x1b]8;;\x1b\\" && sequence != "\x1b]8;;\a"
				}
				index = end
				continue
			}
		}

		glyph, size := utf8.DecodeRuneInString(line[index:])
		if glyph == utf8.RuneError && size == 0 {
			break
		}

		glyphWidth := lipgloss.Width(string(glyph))
		if usedWidth+glyphWidth > targetWidth {
			break
		}
		builder.WriteString(line[index : index+size])
		usedWidth += glyphWidth
		index += size
	}

	if openLink {
		builder.WriteString("\x1b]8;;\x1b\\")
	}
	builder.WriteString(ellipsis)
	if sawANSI {
		builder.WriteString(resetANSI)
	}
	return builder.String()
}

func ansiSequenceEnd(value string, start int) int {
	if start >= len(value) || value[start] != '\x1b' {
		return start
	}
	index := start + 1
	if index >= len(value) {
		return index
	}

	switch value[index] {
	case '[':
		// CSI: terminated by a final byte in 0x40–0x7e.
		for index++; index < len(value); index++ {
			if value[index] >= 0x40 && value[index] <= 0x7e {
				return index + 1
			}
		}
		return len(value)
	case ']':
		// OSC (e.g. the OSC 8 hyperlinks on tool-card paths): terminated by
		// BEL or ST (ESC \). Without this branch the truncator treated the
		// payload as printable text, wrecking the width math.
		for index++; index < len(value); index++ {
			if value[index] == '\a' {
				return index + 1
			}
			if value[index] == '\x1b' && index+1 < len(value) && value[index+1] == '\\' {
				return index + 2
			}
		}
		return len(value)
	default:
		return minInt(start+2, len(value))
	}
}

// chatWidth resolves the render width for the chat surface. Unlike the old
// splash floor it respects genuinely tiny terminals (so the <58 tier can
// engage). The 24-cell floor is deliberate: below it the cards' own minimum
// width makes the layout meaningless, so we accept terminal-side wrapping
// there rather than degrade every wider tier.
func chatWidth(width int) int {
	if width <= 0 {
		return defaultStartupWidth
	}
	if width < 24 {
		return 24
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

func minInt(left int, right int) int {
	if left < right {
		return left
	}
	return right
}
