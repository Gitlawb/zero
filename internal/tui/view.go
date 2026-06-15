package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/Gitlawb/zero/internal/agent"
)

const (
	suggestionPaletteMaxVisible = 7
	suggestionPaletteMaxWidth   = 76
	suggestionPaletteMinWidth   = 44
	pickerOverlayMaxVisible     = 10
	pickerOverlayMaxWidth       = 92
	pickerOverlayMinWidth       = 56
	modelPickerOverlayMaxWidth  = 76
	modelPickerOverlayMinWidth  = 58
)

// layoutTier buckets the terminal width into the spec's adaptive tiers. It
// is derived from the live width at every render, so a WindowSizeMsg
// re-evaluates it implicitly.
type layoutTier int

const (
	tierTiny   layoutTier = iota // < 58: single-segment header, rail-less cards
	tierNarrow                   // 58–79: no gutters, bare badge, lean status
	tierMedium                   // 80–99: no tool-arg column, no ctx
	tierFull                     // ≥ 100: everything
)

func widthTier(width int) layoutTier {
	switch {
	case width >= 100:
		return tierFull
	case width >= 80:
		return tierMedium
	case width >= minStartupWidth:
		return tierNarrow
	default:
		return tierTiny
	}
}

// titleBar renders the top zone of the chat surface: the brand badge, cwd and
// branch on the left, provider/model and context window on the right, then a
// rule. Segments drop with the width tier (full → no ctx → no cwd → bare
// badge + model only), reusing the startupHeaderLine candidate fallback.
func (m model) titleBar(width int) string {
	tier := widthTier(width)

	badge := zeroTheme.badge.Render(" 0 ") + " " + zeroTheme.ink.Bold(true).Render("zero")
	if tier <= tierNarrow {
		badge = zeroTheme.accent.Render("0") + " " + zeroTheme.ink.Bold(true).Render("zero")
	}
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

	var candidates []headerCandidate
	switch tier {
	case tierFull:
		candidates = []headerCandidate{
			{left: badge + cwd + branch, right: model + ctx},
			{left: badge + cwd + branch, right: model},
			{left: badge, right: model},
		}
	case tierMedium:
		candidates = []headerCandidate{
			{left: badge + cwd + branch, right: model},
			{left: badge, right: model},
		}
	case tierNarrow:
		candidates = []headerCandidate{
			{left: badge, right: model},
		}
	default:
		// Tiny: one segment, no right column.
		candidates = []headerCandidate{
			{left: badge, right: ""},
		}
	}

	line := startupHeaderLine(width, candidates)
	rule := zeroTheme.line.Render(strings.Repeat("─", width))
	return line + "\n" + rule
}

func (m model) titleModelSegment() string {
	provider := strings.TrimSpace(m.providerDisplayName())
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

func (m model) composerDividerLine(width int) string {
	model := displayValue(strings.TrimSpace(m.modelName), "no model")
	label, style := m.modeLabel()
	meta := zeroTheme.muted.Render(model) + zeroTheme.muted.Render(" · ") + style.Render(label)
	metaWidth := lipgloss.Width(meta)
	if width < 8 {
		return zeroTheme.lineStrong.Render(strings.Repeat("─", width))
	}
	if width < metaWidth+4 {
		return zeroTheme.lineStrong.Render("╰" + strings.Repeat("─", width-2) + "╯")
	}
	rule := strings.Repeat("─", width-metaWidth-4)
	return zeroTheme.lineStrong.Render("╰"+rule+" ") + meta + zeroTheme.lineStrong.Render(" ╯")
}

// statusLine renders the bottom readout as ` │ `-separated groups: provider
// on the left, then a flexible gap, then token/cost usage on the right. Groups
// drop with the width tier: narrow keeps provider+usage, tiny shows provider.
func (m model) statusLine(width int) string {
	tier := widthTier(width)
	separator := zeroTheme.line.Render(" │ ")
	prefix := "  "
	providerName := displayValue(strings.TrimSpace(m.providerDisplayName()), "no provider")

	if tier == tierTiny {
		provider := prefix + zeroTheme.accent.Render("●") + " " + zeroTheme.ink.Render(providerName)
		return fitStyledLine(provider, width)
	}

	left := prefix + zeroTheme.accent.Render("●") + " " + zeroTheme.ink.Render(providerName)

	rightGroups := []string{}
	if usage := m.usageStatusSegment(); usage != "" {
		rightGroups = append(rightGroups, zeroTheme.muted.Render(usage))
	}
	right := strings.Join(rightGroups, separator)

	return fitStyledLine(joinHeaderLine(left, right, width), width)
}

func (m model) providerDisplayName() string {
	provider := strings.TrimSpace(m.providerName)
	if provider == "" {
		provider = strings.TrimSpace(m.providerProfile.Name)
	}
	if !providerDisplayNameIsGenericCustom(provider) {
		return provider
	}
	baseURL := strings.TrimSpace(m.providerProfile.BaseURL)
	if baseURL == "" || strings.Contains(strings.ToLower(baseURL), "example.invalid") {
		return provider
	}
	derived := providerWizardNameFromBaseURL(baseURL)
	if derived == "" || derived == "custom" {
		return provider
	}
	return derived
}

func providerDisplayNameIsGenericCustom(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "custom-openai-compatible", "custom-anthropic-compatible":
		return true
	default:
		return false
	}
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
		// Match on a path boundary: a bare prefix check would mangle siblings
		// like /Users/alice2 when home is /Users/alice.
		if path == home {
			return "~"
		}
		if strings.HasPrefix(path, home+string(os.PathSeparator)) {
			return "~" + path[len(home):]
		}
	}
	return path
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

// suggestionOverlay renders the slash-command/file autocomplete palette below
// the composer: compact, bordered, and capped so short prefixes cannot flood
// the chat surface. Returns "" when no overlay should show.
func (m model) suggestionOverlay(width int) string {
	if !m.suggestionsActive() {
		return ""
	}
	title := "Commands"
	query := commandSuggestionQuery(m.input.Value())
	footer := "↑/↓ move   Enter run   Esc close"
	if m.selectedCommandSuggestionRequiresInput() {
		footer = "↑/↓ move   Enter insert   Esc close"
	}
	if m.suggestionsAreFiles {
		title = "Files"
		query = fileSuggestionQuery(m.input.Value())
		footer = "↑/↓ move   Enter insert   Esc close"
	}
	return centerRenderedBlock(renderSuggestionPalette(selectableItems(m.suggestions, m.suggestionsAreFiles), m.suggestionIdx, width, title, query, footer), width)
}

func commandSuggestionQuery(value string) string {
	trimmed := strings.TrimLeft(value, " ")
	if trimmed == "" {
		return ""
	}
	if fields := strings.Fields(trimmed); len(fields) > 0 {
		return strings.TrimPrefix(fields[0], "/")
	}
	return strings.TrimPrefix(strings.TrimSpace(trimmed), "/")
}

func fileSuggestionQuery(value string) string {
	if token, ok := trailingAtToken(value); ok {
		return token
	}
	return ""
}

func renderSuggestionPalette(items []selectableListItem, selected, width int, title, query, footer string) string {
	if width <= 0 {
		width = defaultStartupWidth
	}
	paletteWidth := minInt(width, suggestionPaletteMaxWidth)
	if paletteWidth < suggestionPaletteMinWidth {
		paletteWidth = width
	}
	innerWidth := maxInt(1, paletteWidth-4)
	maxVisible := minInt(suggestionPaletteMaxVisible, len(items))
	visible := []selectableListItem{}
	start := 0
	if len(items) > 0 {
		selected = clampInt(selected, 0, len(items)-1)
		start = selectableListStart(len(items), maxVisible, selected)
		visible = items[start : start+maxVisible]
	}

	labelWidth := 0
	for _, item := range visible {
		if w := lipgloss.Width(item.Label); w > labelWidth {
			labelWidth = w
		}
	}
	labelWidth = minInt(labelWidth, maxInt(8, innerWidth/2))

	lines := make([]string, 0, len(visible)+5)
	searchInset := lipgloss.Width("❯ ")
	searchPrefix := transparentSurface(zeroTheme.ink).Render(strings.Repeat(" ", searchInset))
	lines = append(lines, fillPaletteLine(searchPrefix+renderSuggestionSearchLine(query, maxInt(1, innerWidth-searchInset)), innerWidth, transparentSurface))
	lines = append(lines, zeroTheme.line.Render(strings.Repeat("─", innerWidth)))

	for index, item := range visible {
		absoluteIndex := start + index
		surface := transparentSurface
		marker := surface(zeroTheme.faintest).Render("  ")
		if absoluteIndex == selected {
			surface = zeroTheme.onSel
			marker = surface(zeroTheme.accent).Render("❯ ")
		}

		labelText := truncateRunes(item.Label, labelWidth)
		label := surface(zeroTheme.ink).Render(labelText)
		pad := surface(zeroTheme.ink).Render(strings.Repeat(" ", maxInt(0, labelWidth-lipgloss.Width(labelText))))
		line := marker + label + pad
		if desc := strings.TrimSpace(item.Description); desc != "" {
			descWidth := innerWidth - lipgloss.Width(marker) - labelWidth - 2
			if truncated := truncateRunes(desc, maxInt(0, descWidth)); truncated != "" {
				line += surface(zeroTheme.faint).Render("  " + truncated)
			}
		}
		lines = append(lines, fillPaletteLine(line, innerWidth, surface))
	}
	if len(visible) == 0 {
		message := "no matching commands"
		if strings.EqualFold(strings.TrimSpace(title), "Files") {
			message = "no matching files"
		}
		lines = append(lines, fillPaletteLine(searchPrefix+zeroTheme.faint.Render(message), innerWidth, transparentSurface))
	}

	if footer = strings.TrimSpace(footer); footer != "" {
		lines = append(lines, zeroTheme.line.Render(strings.Repeat("─", innerWidth)))
		line := zeroTheme.faint.Render(footer)
		lines = append(lines, fillPaletteLine(line, innerWidth, transparentSurface))
	}
	return styledBlockFillTitle(paletteWidth, strings.TrimSpace(title), lines, zeroTheme.lineStrong, lipgloss.NewStyle())
}

func styledBlockFillTitle(width int, title string, lines []string, borderStyle lipgloss.Style, fill lipgloss.Style) string {
	if width < 4 {
		width = 4
	}
	if title = strings.TrimSpace(title); title == "" || widthTier(width) == tierTiny {
		return styledBlockFill(width, lines, borderStyle, fill)
	}
	ruleWidth := width - 2
	titleText := " " + title + " "
	titleWidth := lipgloss.Width(titleText)
	if titleWidth >= ruleWidth {
		return styledBlockFill(width, lines, borderStyle, fill)
	}

	leftRule := "──"
	rightRule := strings.Repeat("─", maxInt(0, ruleWidth-lipgloss.Width(leftRule)-titleWidth))
	top := borderStyle.Render("╭"+leftRule) + zeroTheme.ink.Bold(true).Render(titleText) + borderStyle.Render(rightRule+"╮")
	bottom := borderStyle.Render("╰" + strings.Repeat("─", width-2) + "╯")

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

func renderSuggestionSearchLine(query string, width int) string {
	query = strings.TrimSpace(query)
	label := zeroTheme.userPrompt.Render("search > ")
	valueWidth := maxInt(1, width-lipgloss.Width(label))
	value := zeroTheme.ink.Render(truncateRunes(query, valueWidth))
	return fitStyledLine(label+value, width)
}

func transparentSurface(style lipgloss.Style) lipgloss.Style {
	return style
}

func fillPaletteLine(line string, width int, surface func(lipgloss.Style) lipgloss.Style) string {
	line = fitStyledLine(line, width)
	pad := maxInt(0, width-lipgloss.Width(line))
	if pad > 0 {
		line += surface(zeroTheme.ink).Render(strings.Repeat(" ", pad))
	}
	return line
}

func centerRenderedBlock(block string, width int) string {
	if block == "" || width <= 0 {
		return block
	}
	lines := strings.Split(block, "\n")
	blockWidth := 0
	for _, line := range lines {
		if w := lipgloss.Width(line); w > blockWidth {
			blockWidth = w
		}
	}
	pad := maxInt(0, (width-blockWidth)/2)
	if pad == 0 {
		return block
	}
	return indentBlock(block, pad)
}

func selectableItems(suggestions []commandSuggestion, files bool) []selectableListItem {
	items := make([]selectableListItem, 0, len(suggestions))
	for _, suggestion := range suggestions {
		item := selectableListItem{Label: suggestion.Name, Description: suggestion.Desc}
		if files {
			item = fileSelectableItem(suggestion.Name)
		} else {
			item.Label = strings.TrimPrefix(item.Label, "/")
		}
		items = append(items, item)
	}
	return items
}

func fileSelectableItem(token string) selectableListItem {
	rel := strings.TrimPrefix(token, "@")
	rel = filepath.ToSlash(rel)
	isDir := strings.HasSuffix(rel, "/")
	cleanRel := strings.TrimSuffix(rel, "/")
	base := path.Base(cleanRel)
	if base == "." || base == "/" || base == "" {
		return selectableListItem{Label: strings.TrimPrefix(token, "@"), Description: "file"}
	}
	if isDir {
		base += "/"
	}
	dir := path.Dir(cleanRel)
	if dir == "." || dir == "" {
		return selectableListItem{Label: base}
	}
	return selectableListItem{Label: base, Description: dir}
}

// pickerOverlay renders an open interactive selector as a centered modal: a
// bordered panel with a title-and-hints row, rows carrying a provider dot and
// right metadata when the catalog exposes them, and the selected row on the
// selection tint.
func (m model) pickerOverlay(width int) string {
	if m.picker == nil {
		return ""
	}
	if m.picker.kind == pickerModel {
		return m.modelPickerOverlay(width)
	}
	overlayWidth := minInt(width, pickerOverlayMaxWidth)
	if overlayWidth < pickerOverlayMinWidth {
		overlayWidth = width
	}
	innerWidth := maxInt(1, overlayWidth-4)
	maxVisible := minInt(pickerOverlayMaxVisible, len(m.picker.items))
	start := 0
	visible := []pickerItem{}
	if len(m.picker.items) > 0 {
		m.picker.selected = clampInt(m.picker.selected, 0, len(m.picker.items)-1)
		start = selectableListStart(len(m.picker.items), maxVisible, m.picker.selected)
		visible = m.picker.items[start : start+maxVisible]
	}

	lines := make([]string, 0, len(visible)+5)
	title := strings.TrimSpace(m.picker.title)
	hint := "↑/↓ · ⏎ · esc"
	lines = append(lines, zeroTheme.faint.Render(hint))
	lastGroup := ""
	for index, item := range visible {
		absoluteIndex := start + index
		if item.Group != "" && item.Group != lastGroup {
			lines = append(lines, zeroTheme.accent.Render(item.Group))
			lastGroup = item.Group
		}
		surface := transparentSurface
		marker := surface(zeroTheme.faintest).Render("  ")
		if absoluteIndex == m.picker.selected {
			surface = zeroTheme.onSel
			marker = surface(zeroTheme.accent).Render("❯ ")
		}
		left := marker
		switch {
		case item.Local:
			left += surface(zeroTheme.blue).Render("● ")
		case item.Remote:
			left += surface(zeroTheme.accent).Render("● ")
		}
		if item.Favorite {
			left += surface(zeroTheme.accent).Render("* ")
		}
		left += surface(zeroTheme.ink).Render(item.Label)
		right := ""
		if item.Meta != "" {
			right = surface(zeroTheme.faintest).Render(item.Meta)
		}
		// Paint the gap on the row surface so selected rows read as one solid
		// band; joinHeaderLine would pad with bare (untinted) spaces.
		gap := innerWidth - lipgloss.Width(left) - lipgloss.Width(right)
		line := left + surface(zeroTheme.ink).Render(strings.Repeat(" ", maxInt(1, gap))) + right
		lines = append(lines, fitStyledLine(line, innerWidth))
	}
	if len(visible) == 0 {
		lines = append(lines, zeroTheme.faint.Render("  no matching items"))
	}
	return centerRenderedBlock(styledBlockFillTitle(overlayWidth, title, lines, zeroTheme.lineStrong, lipgloss.NewStyle()), width)
}

func (m model) modelPickerOverlay(width int) string {
	if m.picker == nil {
		return ""
	}
	if m.modelPickerLoading {
		return m.modelPickerLoadingOverlay(width)
	}
	overlayWidth := modelPickerOverlayWidth(width, m.picker)
	innerWidth := maxInt(1, overlayWidth-4)
	maxVisible := minInt(pickerOverlayMaxVisible, len(m.picker.items))
	start := 0
	visible := []pickerItem{}
	if len(m.picker.items) > 0 {
		m.picker.selected = clampInt(m.picker.selected, 0, len(m.picker.items)-1)
		start = selectableListStart(len(m.picker.items), maxVisible, m.picker.selected)
		visible = m.picker.items[start : start+maxVisible]
	}

	lines := make([]string, 0, len(visible)+6)
	searchInset := lipgloss.Width("❯ ")
	searchPrefix := transparentSurface(zeroTheme.ink).Render(strings.Repeat(" ", searchInset))
	lines = append(lines, fillPaletteLine(searchPrefix+renderModelPickerSearchLine(m.picker.query, maxInt(1, innerWidth-searchInset)), innerWidth, transparentSurface))
	if status := strings.TrimSpace(m.modelPickerLoadError); status != "" {
		lines = append(lines, fillPaletteLine(searchPrefix+zeroTheme.faint.Render(status), innerWidth, transparentSurface))
	}
	lines = append(lines, zeroTheme.line.Render(strings.Repeat("─", innerWidth)))
	for index, item := range visible {
		lines = append(lines, renderModelPickerRow(innerWidth, start+index == m.picker.selected, item))
	}
	if len(visible) == 0 {
		lines = append(lines, fillPaletteLine(searchPrefix+zeroTheme.faint.Render("no matching models"), innerWidth, transparentSurface))
	}
	if item, ok := m.picker.current(); ok {
		if detail := modelPickerItemDetail(item); detail != "" {
			lines = append(lines, zeroTheme.line.Render(strings.Repeat("─", innerWidth)))
			lines = append(lines, fillPaletteLine(searchPrefix+zeroTheme.faint.Render(detail), innerWidth, transparentSurface))
		}
	}
	lines = append(lines, zeroTheme.line.Render(strings.Repeat("─", innerWidth)))
	footer := "↑/↓ move   Enter select   Ctrl+F favorite   Esc close"
	lines = append(lines, fillPaletteLine(zeroTheme.faint.Render(footer), innerWidth, transparentSurface))
	title := strings.TrimSpace(m.picker.title)
	if title == "" {
		title = "Choose a model"
	}
	return centerRenderedBlock(styledBlockFillTitle(overlayWidth, title, lines, zeroTheme.lineStrong, lipgloss.NewStyle()), width)
}

func (m model) modelPickerLoadingOverlay(width int) string {
	overlayWidth := modelPickerLoadingOverlayWidth(width)
	innerWidth := maxInt(1, overlayWidth-4)
	lines := []string{
		fillPaletteLine(zeroTheme.faint.Render("Checking available models..."), innerWidth, transparentSurface),
		fillPaletteLine(zeroTheme.faint.Render("Built-in models will be used if discovery fails."), innerWidth, transparentSurface),
		zeroTheme.line.Render(strings.Repeat("─", innerWidth)),
		fillPaletteLine(zeroTheme.faint.Render("Esc close"), innerWidth, transparentSurface),
	}
	title := strings.TrimSpace(m.picker.title)
	if title == "" {
		title = "Choose a model"
	}
	return centerRenderedBlock(styledBlockFillTitle(overlayWidth, title, lines, zeroTheme.lineStrong, lipgloss.NewStyle()), width)
}

func modelPickerLoadingOverlayWidth(terminalWidth int) int {
	if terminalWidth <= 0 {
		terminalWidth = defaultStartupWidth
	}
	available := minInt(terminalWidth, modelPickerOverlayMaxWidth)
	if terminalWidth < modelPickerOverlayMinWidth {
		available = terminalWidth
	}
	target := lipgloss.Width("Built-in models will be used if discovery fails.")
	target = maxInt(target, lipgloss.Width("Choose a model"))
	target = maxInt(target, lipgloss.Width("Esc close"))
	overlayWidth := maxInt(modelPickerOverlayMinWidth, target+4)
	return minInt(overlayWidth, maxInt(4, available))
}

func modelPickerOverlayWidth(terminalWidth int, picker *commandPicker) int {
	if terminalWidth <= 0 {
		terminalWidth = defaultStartupWidth
	}
	available := minInt(terminalWidth, modelPickerOverlayMaxWidth)
	if terminalWidth < modelPickerOverlayMinWidth {
		available = terminalWidth
	}
	target := lipgloss.Width("Choose a model")
	target = maxInt(target, lipgloss.Width("  search > model name..."))
	target = maxInt(target, lipgloss.Width("↑/↓ move   Enter select   Ctrl+F favorite   Esc close"))
	target = maxInt(target, lipgloss.Width("  Using built-in model list"))
	if picker != nil {
		for _, item := range picker.items {
			labelWidth := lipgloss.Width(item.Label)
			if item.Favorite {
				labelWidth += lipgloss.Width("* ")
			}
			target = maxInt(target, lipgloss.Width("❯ ")+labelWidth)
			if detail := modelPickerItemDetail(item); detail != "" {
				target = maxInt(target, lipgloss.Width("  "+detail))
			}
		}
	}
	overlayWidth := maxInt(modelPickerOverlayMinWidth, target+4)
	return minInt(overlayWidth, maxInt(4, available))
}

func renderModelPickerSearchLine(query string, width int) string {
	query = strings.TrimSpace(query)
	prompt := zeroTheme.userPrompt.Render("search > ")
	cursor := zeroTheme.accent.Render("▌")
	if query == "" {
		return fitStyledLine(prompt+cursor+zeroTheme.faint.Render("model name..."), width)
	}
	return fitStyledLine(prompt+zeroTheme.ink.Render(query)+cursor, width)
}

func renderModelPickerRow(width int, selected bool, item pickerItem) string {
	surface := transparentSurface
	marker := surface(zeroTheme.faintest).Render("  ")
	if selected {
		surface = zeroTheme.onSel
		marker = surface(zeroTheme.accent).Render("❯ ")
	}
	label := strings.TrimSpace(item.Label)
	if label == "" {
		label = strings.TrimSpace(item.Value)
	}
	prefix := ""
	if item.Favorite {
		prefix = "* "
	}
	line := marker + surface(zeroTheme.ink).Render(prefix+label)
	return fillPaletteLine(line, width, surface)
}

func modelPickerItemDetail(item pickerItem) string {
	parts := []string{}
	value := strings.TrimSpace(item.Value)
	label := strings.TrimSpace(item.Label)
	if value != "" && value != label {
		parts = append(parts, value)
	}
	if meta := strings.TrimSpace(item.Meta); meta != "" {
		parts = append(parts, meta)
	}
	return strings.Join(parts, " · ")
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

// looksLikeDiff reports whether output should be rendered as a diff card: a
// real hunk header, or both old/new file headers. A single line starting with
// "---" (a Markdown rule, YAML document marker, log separator…) must NOT
// hijack ordinary bash/tool output into the diff renderer.
func looksLikeDiff(text string) bool {
	if !strings.Contains(text, "\n") {
		return false
	}
	hasOld, hasNew := false, false
	for _, line := range strings.Split(text, "\n") {
		switch {
		case hunkHeaderPattern.MatchString(line):
			return true
		case strings.HasPrefix(line, "+++ "):
			hasNew = true
		case strings.HasPrefix(line, "--- "):
			hasOld = true
		}
		if hasOld && hasNew {
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
