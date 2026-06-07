package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/Gitlawb/zero/internal/agent"
)

// headerBar renders the top zone of the working view: a single status line plus a
// thin rule. Layout: zero · cwd · branch · provider/model  ............  ● state
func (m model) headerBar(width int) string {
	left := strings.Join(nonEmpty([]string{
		zeroTheme.accent.Render("zero"),
		zeroTheme.text.Render(shortenPath(m.cwd)),
		branchSegment(m.gitBranch),
		m.providerSegment(),
	}), zeroTheme.muted.Render(" · "))

	right := m.stateSegment()
	line := joinHeaderLine(left, right, width)
	rule := zeroTheme.border.Render(strings.Repeat("─", width))
	return line + "\n" + rule
}

func (m model) providerSegment() string {
	provider := strings.TrimSpace(m.providerName)
	model := strings.TrimSpace(m.modelName)
	if provider == "" && model == "" {
		return zeroTheme.muted.Render("no provider")
	}
	if model == "" {
		return zeroTheme.accent.Render(provider)
	}
	if provider == "" {
		return zeroTheme.text.Render(model)
	}
	return zeroTheme.accent.Render(provider) + zeroTheme.muted.Render("/") + zeroTheme.text.Render(model)
}

func (m model) stateSegment() string {
	if m.pending {
		return zeroTheme.amber.Render("● working")
	}
	return zeroTheme.green.Render("● ready")
}

func branchSegment(branch string) string {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return ""
	}
	return zeroTheme.muted.Render(branch)
}

// statusLine renders the bottom zone: the permission-mode indicator on the left and
// the live model/usage readout on the right.
func (m model) statusLine(width int) string {
	left := m.modeSegment()
	right := m.usageSegment()
	return joinHeaderLine(left, right, width)
}

func (m model) modeSegment() string {
	label, style := m.modeLabel()
	return style.Render("⏵⏵ "+label) + zeroTheme.muted.Render(" · shift+tab to cycle")
}

// nextPermissionMode toggles between the two prompt-respecting modes:
// Auto ⇄ Ask. Unsafe (which disables permission prompts entirely) is
// deliberately NOT reachable by a casual keypress — a single shift+tab landing
// on it would let prompt-required tools run with no decision. Unsafe stays an
// explicit opt-in (the launch/--skip-permissions-unsafe path), not a UI toggle.
// Unsafe is folded back to Ask so the toggle always lands somewhere safe.
func nextPermissionMode(mode agent.PermissionMode) agent.PermissionMode {
	switch mode {
	case agent.PermissionModeAuto:
		return agent.PermissionModeAsk
	default:
		// Ask (and anything else, incl. an externally-set Unsafe) → Auto.
		return agent.PermissionModeAuto
	}
}

func (m model) modeLabel() (string, lipgloss.Style) {
	switch m.permissionMode {
	case agent.PermissionModeAuto:
		return "auto-approve edits", zeroTheme.modeAuto
	case agent.PermissionModeAsk:
		return "approve each action", zeroTheme.modeAsk
	case agent.PermissionModeUnsafe:
		return "bypass permissions", zeroTheme.modeUnsafe
	default:
		mode := strings.TrimSpace(string(m.permissionMode))
		if mode == "" {
			return "auto-approve edits", zeroTheme.modeAuto
		}
		return mode, zeroTheme.muted
	}
}

func (m model) usageSegment() string {
	model := displayValue(m.modelName, "no model")
	usage := m.usageStatusSegment()
	if usage == "" {
		return zeroTheme.text.Render(model)
	}
	return zeroTheme.text.Render(model) + zeroTheme.muted.Render(" · "+usage)
}

func (m model) usageStatusSegment() string {
	if m.usageTracker == nil {
		return ""
	}
	summary := m.usageTracker.Summary()
	if summary.RecordCount == 0 {
		if m.unpricedRequests > 0 {
			return fmt.Sprintf("%s tok", humanCount(m.unpricedTokens))
		}
		return ""
	}
	return fmt.Sprintf("%s↑ %s↓ · %s",
		humanCount(summary.InputTokens),
		humanCount(summary.OutputTokens),
		summary.FormattedTotalCost,
	)
}

// modeHint is the splash-screen variant of the mode indicator, mirroring the
// professional CLI status line instead of raw keycap hints.
func (m model) modeHint() string {
	label, style := m.modeLabel()
	return style.Render("⏵⏵ "+label) +
		zeroTheme.muted.Render(" · shift+tab to cycle · ") +
		zeroTheme.muted.Render("← agents")
}

func humanCount(n int) string {
	if n < 0 {
		n = 0
	}
	if n < 1000 {
		return strconv.Itoa(n)
	}
	value := float64(n) / 1000
	text := fmt.Sprintf("%.1fk", value)
	return strings.Replace(text, ".0k", "k", 1)
}

func shortenPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "unknown"
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if strings.HasPrefix(path, home) {
			return "~" + path[len(home):]
		}
	}
	return path
}

func nonEmpty(values []string) []string {
	out := values[:0]
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}

// gitBranch reads the current branch (or short SHA when detached) for cwd, handling
// both regular checkouts (.git dir) and worktrees (.git file). Returns "" on any
// problem — the header simply omits the segment.
func gitBranch(cwd string) string {
	if strings.TrimSpace(cwd) == "" {
		return ""
	}
	gitPath := filepath.Join(cwd, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return ""
	}

	headPath := filepath.Join(gitPath, "HEAD")
	if !info.IsDir() {
		data, err := os.ReadFile(gitPath)
		if err != nil {
			return ""
		}
		dir := strings.TrimPrefix(strings.TrimSpace(string(data)), "gitdir: ")
		if dir == "" {
			return ""
		}
		headPath = filepath.Join(dir, "HEAD")
	}

	data, err := os.ReadFile(headPath)
	if err != nil {
		return ""
	}
	ref := strings.TrimSpace(string(data))
	if strings.HasPrefix(ref, "ref: ") {
		ref = strings.TrimPrefix(ref, "ref: ")
		return strings.TrimPrefix(ref, "refs/heads/")
	}
	if len(ref) >= 7 {
		return ref[:7]
	}
	return ref
}

// suggestionOverlay renders the slash-command autocomplete list below the input
// in the default skin: one row per match (name + dim description), the selected
// row highlighted with a caret and the accent color. Returns "" when no overlay
// should show.
func (m model) suggestionOverlay(width int) string {
	if !m.suggestionsActive() {
		return ""
	}
	nameWidth := 0
	for _, s := range m.suggestions {
		if w := lipgloss.Width(s.Name); w > nameWidth {
			nameWidth = w
		}
	}
	lines := make([]string, 0, len(m.suggestions))
	for index, s := range m.suggestions {
		pad := strings.Repeat(" ", maxInt(0, nameWidth-lipgloss.Width(s.Name)))
		marker := "  "
		name := zeroTheme.text.Render(s.Name)
		if index == m.suggestionIdx {
			marker = zeroTheme.accent.Render("› ")
			name = zeroTheme.accent.Render(s.Name)
		}
		line := marker + name + pad + "  " + zeroTheme.muted.Render(s.Desc)
		lines = append(lines, fitStyledLine(line, width-2))
	}
	return strings.Join(lines, "\n")
}

// pickerOverlay renders an open interactive selector below the input in the
// default skin: a titled bordered list with the selected row highlighted.
func (m model) pickerOverlay(width int) string {
	if m.picker == nil {
		return ""
	}
	lines := make([]string, 0, len(m.picker.items)+1)
	lines = append(lines, zeroTheme.accent.Render(m.picker.title)+zeroTheme.muted.Render("  ↑/↓ move · ⏎ select · esc cancel"))
	for index, item := range m.picker.items {
		marker := "  "
		label := zeroTheme.text.Render(item.Label)
		if index == m.picker.selected {
			marker = zeroTheme.accent.Render("› ")
			label = zeroTheme.accent.Render(item.Label)
		}
		lines = append(lines, fitStyledLine(marker+label, width-4))
	}
	return borderedBlock(width, lines)
}

// argHint extracts the most representative argument from a tool call's raw JSON
// arguments for the single-line tool row (the path, pattern, or command acted on).
func argHint(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" {
		return ""
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return ""
	}
	for _, key := range []string{"path", "file", "file_path", "filepath", "pattern", "query", "command", "cmd", "url"} {
		if value, ok := args[key]; ok {
			if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
		}
	}
	return ""
}

func indentText(text string, spaces int) string {
	prefix := strings.Repeat(" ", spaces)
	lines := strings.Split(text, "\n")
	for index, line := range lines {
		lines[index] = prefix + line
	}
	return strings.Join(lines, "\n")
}

// looksLikeDiff reports whether output should be rendered as a diff card.
func looksLikeDiff(text string) bool {
	if !strings.Contains(text, "\n") {
		return false
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "@@") || strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") {
			return true
		}
	}
	return false
}

func colorizeDiffLine(line string) string {
	switch {
	case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"), strings.HasPrefix(line, "@@"):
		return zeroTheme.diffMeta.Render(line)
	case strings.HasPrefix(line, "+"):
		return zeroTheme.diffAdd.Render(line)
	case strings.HasPrefix(line, "-"):
		return zeroTheme.diffDel.Render(line)
	default:
		return zeroTheme.muted.Render(line)
	}
}

const diffCardMaxLines = 16

func diffCard(title string, detail string, width int) string {
	budget := width - 4
	if budget < 16 {
		budget = 16
	}
	rawLines := strings.Split(strings.ReplaceAll(detail, "\r\n", "\n"), "\n")
	body := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		line = truncateRunes(line, budget)
		body = append(body, colorizeDiffLine(line))
	}
	if len(body) > diffCardMaxLines {
		hidden := len(body) - diffCardMaxLines
		body = body[:diffCardMaxLines]
		body = append(body, zeroTheme.muted.Render(fmt.Sprintf("… %d more lines", hidden)))
	}
	return titledCard("edit · "+title, body, width)
}

// titledCard draws a rounded box with a title embedded in the top border.
func titledCard(title string, body []string, width int) string {
	if width < 24 {
		width = 24
	}
	remaining := width - 5 - lipgloss.Width(title)
	if remaining < 0 {
		remaining = 0
	}
	top := zeroTheme.border.Render("╭─ ") +
		zeroTheme.text.Render(title) +
		zeroTheme.border.Render(" "+strings.Repeat("─", remaining)+"╮")

	lines := make([]string, 0, len(body)+2)
	lines = append(lines, top)
	budget := width - 4
	for _, line := range body {
		pad := budget - lipgloss.Width(line)
		if pad < 0 {
			pad = 0
		}
		lines = append(lines, zeroTheme.border.Render("│ ")+line+strings.Repeat(" ", pad)+zeroTheme.border.Render(" │"))
	}
	lines = append(lines, zeroTheme.border.Render("╰"+strings.Repeat("─", width-2)+"╯"))
	return strings.Join(lines, "\n")
}

func truncateRunes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	if limit == 1 {
		return "…"
	}
	return string(runes[:limit-1]) + "…"
}
