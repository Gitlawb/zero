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

// titleBar renders the top zone of the chat surface: the brand badge, cwd and
// branch on the left, provider/model and context window on the right, then a
// rule. Segments drop in fixed order as width shrinks (full → no ctx → no cwd
// → badge+model only), reusing the startupHeaderLine candidate fallback.
func (m model) titleBar(width int) string {
	badge := zeroTheme.badge.Render(" 0 ") + " " + zeroTheme.ink.Bold(true).Render("zero")
	cwd := zeroTheme.faintest.Render(" / ") + zeroTheme.muted.Render(shortenPath(m.cwd))
	branch := ""
	if b := strings.TrimSpace(m.gitBranch); b != "" {
		branch = " " + zeroTheme.faint.Render(b)
	}
	model := m.titleModelSegment()
	ctx := ""
	if window := modelContextWindow(m.modelName); window > 0 {
		ctx = zeroTheme.faint.Render(" · " + formatContextWindow(window))
	}

	line := startupHeaderLine(width, []headerCandidate{
		{left: badge + cwd + branch, right: model + ctx},
		{left: badge + cwd + branch, right: model},
		{left: badge, right: model},
	})
	rule := zeroTheme.line.Render(strings.Repeat("─", width))
	return line + "\n" + rule
}

func (m model) titleModelSegment() string {
	provider := strings.TrimSpace(m.providerName)
	model := strings.TrimSpace(m.modelName)
	switch {
	case provider == "" && model == "":
		return zeroTheme.muted.Render("no provider")
	case model == "":
		return zeroTheme.ink.Render(provider)
	case provider == "":
		return zeroTheme.ink.Render(model)
	default:
		return zeroTheme.ink.Render(provider + "/" + model)
	}
}

// statusLine renders the bottom readout as ` │ `-separated groups: provider
// and model on the left, then a flexible gap, then tokens/cost, the surface
// name, and the permission mode.
func (m model) statusLine(width int) string {
	separator := zeroTheme.line.Render(" │ ")

	left := zeroTheme.accent.Render("●") + " " + zeroTheme.ink.Render(displayValue(strings.TrimSpace(m.providerName), "no provider"))
	if model := strings.TrimSpace(m.modelName); model != "" {
		left += separator + zeroTheme.muted.Render(model)
	}

	rightGroups := []string{}
	if usage := m.usageStatusSegment(); usage != "" {
		rightGroups = append(rightGroups, zeroTheme.muted.Render(usage))
	}
	rightGroups = append(rightGroups, zeroTheme.faint.Render("interactive"))
	label, style := m.modeLabel()
	rightGroups = append(rightGroups, style.Render("⏵⏵ "+label))
	right := strings.Join(rightGroups, separator)

	return joinHeaderLine(left, right, width)
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
	case agent.PermissionModeAsk:
		return agent.PermissionModeAuto
	default:
		// Anything else (incl. an externally-set Unsafe) folds to Ask — the stricter
		// landing, so toggling never makes an Unsafe session less strict.
		return agent.PermissionModeAsk
	}
}

func (m model) modeLabel() (string, lipgloss.Style) {
	switch m.permissionMode {
	case agent.PermissionModeAuto:
		return "auto-approve", zeroTheme.modeAuto
	case agent.PermissionModeAsk:
		return "ask", zeroTheme.modeAsk
	case agent.PermissionModeUnsafe:
		return "unsafe", zeroTheme.modeUnsafe
	default:
		mode := strings.TrimSpace(string(m.permissionMode))
		if mode == "" {
			return "auto-approve", zeroTheme.modeAuto
		}
		return mode, zeroTheme.muted
	}
}

// usageStatusSegment summarizes this session's consumption for the status
// line: cumulative tokens, plus cost once anything is priced.
func (m model) usageStatusSegment() string {
	if m.usageTracker == nil {
		return ""
	}
	summary := m.usageTracker.Summary()
	if summary.RecordCount == 0 {
		if m.unpricedRequests > 0 {
			return humanCount(m.unpricedTokens) + " tok"
		}
		return ""
	}
	return fmt.Sprintf("%s tok · %s",
		humanCount(summary.InputTokens+summary.OutputTokens),
		summary.FormattedTotalCost,
	)
}

// humanCount renders a token count the way the status line wants it: 999,
// 12.4K, 200K.
func humanCount(n int) string {
	if n < 0 {
		n = 0
	}
	if n < 1000 {
		return strconv.Itoa(n)
	}
	value := float64(n) / 1000
	text := fmt.Sprintf("%.1fK", value)
	return strings.Replace(text, ".0K", "K", 1)
}

// formatContextWindow renders a model's context window for the title bar
// (200000 → 200K, 1048576 → 1M).
func formatContextWindow(window int) string {
	if window <= 0 {
		return ""
	}
	if window >= 1_000_000 && window%1_000_000 < 100_000 {
		return strconv.Itoa(window/1_000_000) + "M"
	}
	return strconv.Itoa(window/1000) + "K"
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
		name := zeroTheme.ink.Render(s.Name)
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
		label := zeroTheme.ink.Render(item.Label)
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
	return firstArgValue(raw, []string{"path", "file", "file_path", "filepath", "pattern", "query", "command", "cmd", "url"})
}

// argHintSecondary extracts the card head's faintest arg column: the
// non-target argument (pattern/query/command) when argHint already resolved to
// a path. With no path argument the value is argHint itself, so it stays in
// the target slot and this returns "".
func argHintSecondary(raw string) string {
	secondary := firstArgValue(raw, []string{"pattern", "query", "command", "cmd", "url"})
	if secondary == "" || secondary == argHint(raw) {
		return ""
	}
	return secondary
}

func firstArgValue(raw string, keys []string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" {
		return ""
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return ""
	}
	for _, key := range keys {
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
