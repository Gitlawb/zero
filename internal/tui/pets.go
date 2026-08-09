package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/terminalpet"
)

const (
	petAmbientImageID  uint32 = 0xC0DE
	petPreviewImageID  uint32 = 0xC0DF
	petPreviewDelay           = 140 * time.Millisecond
	petFrameDelay             = 180 * time.Millisecond
	petImageColumns           = 9
	petImageRows              = 5
	petWrapGapColumns         = 2
	petReservedColumns        = petImageColumns + petWrapGapColumns
	petOutcomeHold            = 2200 * time.Millisecond
	petPickerMaxWidth         = 58
	petSidePreviewMin         = 50
	petPreviewPaneGap         = 2
)

type petCatalogLoadedMsg struct {
	entries []terminalpet.Entry
	err     error
}

type petPreviewDebounceMsg struct {
	seq  uint64
	slug string
}

type petPreviewLoadedMsg struct {
	seq       uint64
	slug      string
	animation *terminalpet.Animation
	err       error
}

type petInstalledMsg struct {
	entry     terminalpet.Entry
	animation *terminalpet.Animation
	err       error
}

type petTickMsg struct{}

func petTickCmd() tea.Cmd {
	return tea.Tick(petFrameDelay, func(time.Time) tea.Msg { return petTickMsg{} })
}

func (m model) handlePetsCommand(argument string) (tea.Model, tea.Cmd) {
	if m.petClient == nil {
		return m.appendSystemNotice("Pets are unavailable because the user config directory could not be resolved."), nil
	}
	if m.petRenderer != nil && !m.petRenderer.Support().Supported() {
		return m.appendSystemNotice(m.petRenderer.Support().Reason), nil
	}
	argument = strings.ToLower(strings.TrimSpace(argument))
	switch argument {
	case "off", "disable", "disabled", "hide", "hidden", "none":
		m.cancelPetPreview()
		if _, err := config.SetPet(m.userConfigPath, terminalpet.DisabledID); err != nil {
			return m.appendSystemNotice("Could not disable the terminal companion: " + err.Error()), nil
		}
		m.petID = terminalpet.DisabledID
		m.petName = ""
		m.petAnimation = nil
		m.petPreview = nil
		m.picker = nil
		return m.appendSystemNotice("Terminal companion hidden. Run /pets to choose another."), nil
	}
	m.petRequestedSlug = argument
	m.picker = &commandPicker{kind: pickerPet, title: "Choose a companion", loading: true}
	return m, func() tea.Msg {
		entries, err := m.petClient.Catalog(m.ctx)
		return petCatalogLoadedMsg{entries: entries, err: err}
	}
}

func (m model) applyPetCatalog(msg petCatalogLoadedMsg) (tea.Model, tea.Cmd) {
	if m.picker == nil || m.picker.kind != pickerPet {
		return m, nil
	}
	if msg.err != nil && len(msg.entries) == 0 {
		m.picker = nil
		return m.appendSystemNotice("Could not load the pet catalog: " + msg.err.Error()), nil
	}
	m.petEntries = make(map[string]terminalpet.Entry, len(msg.entries))
	items := make([]pickerItem, 0, len(msg.entries)+1)
	items = append(items, pickerItem{Label: "No companion", Value: terminalpet.DisabledID, Meta: "off"})
	for _, entry := range msg.entries {
		m.petEntries[entry.Slug] = entry
		group := "Discover"
		if entry.Local {
			group = "Installed"
		}
		items = append(items, pickerItem{Group: group, Label: entry.Label(), Value: entry.Slug, Local: entry.Local, Remote: !entry.Local})
	}
	if requested := strings.TrimSpace(m.petRequestedSlug); requested != "" {
		m.petRequestedSlug = ""
		if requested == terminalpet.DisabledID {
			return m.installPet(requested)
		}
		if _, ok := m.petEntries[requested]; !ok {
			m.picker = nil
			return m.appendSystemNotice(fmt.Sprintf("No pet named %q. Run /pets to search the catalog.", requested)), nil
		}
		return m.installPet(requested)
	}
	selected := 0
	for index, item := range items {
		if item.Value == m.petID {
			selected = index
			break
		}
	}
	m.picker = &commandPicker{kind: pickerPet, title: "Choose a companion", items: items, allItems: append([]pickerItem{}, items...), selected: selected}
	return m.schedulePetPreview()
}

func (m model) schedulePetPreview() (model, tea.Cmd) {
	m.cancelPetPreview()
	m.petPreviewSeq++
	m.petPreview = nil
	m.petPreviewError = ""
	item, ok := m.picker.current()
	if !ok || item.Value == terminalpet.DisabledID {
		m.petPreviewLoading = false
		m.petPreviewSlug = ""
		return m, nil
	}
	m.petPreviewLoading = true
	m.petPreviewSlug = item.Value
	seq := m.petPreviewSeq
	slug := item.Value
	return m, tea.Tick(petPreviewDelay, func(time.Time) tea.Msg {
		return petPreviewDebounceMsg{seq: seq, slug: slug}
	})
}

func (m model) startPetPreview(msg petPreviewDebounceMsg) (tea.Model, tea.Cmd) {
	if msg.seq != m.petPreviewSeq || m.picker == nil || m.picker.kind != pickerPet {
		return m, nil
	}
	item, ok := m.picker.current()
	if !ok || item.Value != msg.slug {
		return m, nil
	}
	entry, ok := m.petEntries[msg.slug]
	if !ok {
		return m, nil
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.petPreviewCancel = cancel
	return m, func() tea.Msg {
		animation, err := m.petClient.Preview(ctx, entry)
		return petPreviewLoadedMsg{seq: msg.seq, slug: msg.slug, animation: animation, err: err}
	}
}

func (m model) applyPetPreview(msg petPreviewLoadedMsg) model {
	if msg.seq != m.petPreviewSeq || msg.slug != m.petPreviewSlug {
		return m
	}
	m.petPreviewCancel = nil
	m.petPreviewLoading = false
	if msg.err != nil {
		if !errorsIsContext(msg.err) {
			m.petPreviewError = "Preview unavailable"
		}
		return m
	}
	m.petPreview = msg.animation
	m.petPreviewError = ""
	return m
}

func errorsIsContext(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func (m *model) cancelPetPreview() {
	if m.petPreviewCancel != nil {
		m.petPreviewCancel()
		m.petPreviewCancel = nil
	}
}

func (m model) installPet(slug string) (tea.Model, tea.Cmd) {
	m.cancelPetPreview()
	m.picker = nil
	if slug == terminalpet.DisabledID {
		if _, err := config.SetPet(m.userConfigPath, terminalpet.DisabledID); err != nil {
			return m.appendSystemNotice("Could not save the pet preference: " + err.Error()), nil
		}
		m.petID = terminalpet.DisabledID
		m.petName = ""
		m.petAnimation = nil
		return m.appendSystemNotice("Terminal companion hidden. Run /pets to choose another."), nil
	}
	entry, ok := m.petEntries[slug]
	if !ok {
		return m.appendSystemNotice(fmt.Sprintf("Pet %q is no longer in the catalog.", slug)), nil
	}
	m = m.appendSystemNotice("Installing " + entry.Label() + "…")
	return m, func() tea.Msg {
		animation, err := m.petClient.Install(m.ctx, entry)
		return petInstalledMsg{entry: entry, animation: animation, err: err}
	}
}

func (m model) applyPetInstall(msg petInstalledMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m.appendSystemNotice("Could not install " + msg.entry.Label() + ": " + msg.err.Error()), nil
	}
	if _, err := config.SetPet(m.userConfigPath, msg.entry.Slug); err != nil {
		return m.appendSystemNotice("The pet was downloaded but could not be selected: " + err.Error()), nil
	}
	m.petID = msg.entry.Slug
	m.petName = msg.entry.Label()
	m.petAnimation = msg.animation
	m.petPhase = 0
	m.petOutcome = terminalpet.Idle
	m = m.appendSystemNotice(msg.entry.Label() + " is now your terminal companion. Use /pets off to hide it.")
	if m.reducedMotion {
		return m, nil
	}
	return m, petTickCmd()
}

func (m model) petPickerOverlay(width int) string {
	if m.picker == nil {
		return ""
	}
	overlayWidth := minInt(width, petPickerMaxWidth)
	if overlayWidth < pickerOverlayMinWidth {
		overlayWidth = width
	}
	innerWidth := maxInt(1, overlayWidth-4)
	item, hasItem := m.picker.current()
	hasPreviewTarget := hasItem && item.Value != terminalpet.DisabledID
	sidePreview := hasPreviewTarget && innerWidth >= petSidePreviewMin
	listWidth := innerWidth
	if sidePreview {
		listWidth -= petImageColumns + petPreviewPaneGap
	}
	previewDivider := zeroTheme.line.Render("│") + " "
	listLine := func(line string) string {
		line = fitStyledLine(line, listWidth)
		if !sidePreview {
			return line
		}
		return padStyledLine(line, listWidth) + previewDivider + strings.Repeat(" ", petImageColumns)
	}
	listHeight := minInt(8, len(m.picker.items))
	start := 0
	if len(m.picker.items) > 0 {
		m.picker.selected = clampInt(m.picker.selected, 0, len(m.picker.items)-1)
		start = selectableListStart(len(m.picker.items), listHeight, m.picker.selected)
	}
	lines := []string{renderPickerSearchLine(m.picker.query, "search companions…", innerWidth), zeroTheme.line.Render(strings.Repeat("─", innerWidth))}
	listStartRow := len(lines)
	if m.picker.loading {
		lines = append(lines, listLine(zeroTheme.faint.Render("Fetching the pet catalog…")))
	} else if len(m.picker.items) == 0 {
		lines = append(lines, listLine(zeroTheme.faint.Render("  no matching companions")))
	} else {
		lastGroup := ""
		for index, item := range m.picker.items[start : start+listHeight] {
			if item.Group != "" && item.Group != lastGroup {
				lines = append(lines, listLine(zeroTheme.accent.Render(item.Group)))
				lastGroup = item.Group
			}
			selected := start+index == m.picker.selected
			surface := transparentSurface
			marker := "  "
			if selected {
				surface = zeroTheme.onSel
				marker = "❯ "
			}
			name := truncatePetPickerColumn(item.Label, maxInt(1, listWidth-lipgloss.Width(marker)))
			lines = append(lines, listLine(surface(zeroTheme.accent).Render(marker)+surface(zeroTheme.ink).Render(name)))
		}
	}
	if sidePreview && m.petPreview != nil {
		for len(lines)-listStartRow < petImageRows {
			lines = append(lines, listLine(""))
		}
	}
	if sidePreview && len(lines) > listStartRow {
		previewTitle := centerRenderedBlock(zeroTheme.accent.Render("Preview"), petImageColumns)
		lines[listStartRow] = padStyledLine(ansi.Cut(lines[listStartRow], 0, listWidth), listWidth) +
			previewDivider + previewTitle
	}
	lines = append(lines, zeroTheme.line.Render(strings.Repeat("─", innerWidth)))
	switch {
	case m.petPreviewLoading:
		lines = append(lines, zeroTheme.faint.Render("Loading preview…"))
	case m.petPreviewError != "":
		lines = append(lines, zeroTheme.faint.Render(m.petPreviewError))
	case m.petPreview != nil && !sidePreview:
		for range petImageRows {
			lines = append(lines, "")
		}
	}
	if hasItem && item.Value != terminalpet.DisabledID {
		if entry, ok := m.petEntries[item.Value]; ok {
			details := []string{entry.Label()}
			if kind := strings.TrimSpace(entry.Kind); kind != "" {
				details = append(details, kind)
			}
			if entry.SubmittedBy != "" {
				details = append(details, "by "+entry.SubmittedBy)
			}
			lines = append(lines, centerRenderedBlock(zeroTheme.faint.Render(strings.Join(details, " · ")), innerWidth))
		}
	}
	lines = append(lines, zeroTheme.line.Render(strings.Repeat("─", innerWidth)))
	lines = append(lines, zeroTheme.faint.Render("↑/↓ preview   Enter select   Esc close"))
	return centerRenderedBlock(styledBlockFillTitle(overlayWidth, "Choose a companion", lines, zeroTheme.lineStrong, lipgloss.NewStyle()), width)
}

func truncatePetPickerColumn(value string, width int) string {
	value = strings.TrimSpace(value)
	if width <= 0 || value == "" {
		return ""
	}
	if lipgloss.Width(value) <= width {
		return value
	}
	if width == 1 {
		return "…"
	}
	return strings.TrimSpace(ansi.Cut(value, 0, width-1)) + "…"
}

func (m model) petLayoutActive() bool {
	if m.petLayoutRendering {
		return false
	}
	if m.petAnimation == nil || m.petID == "" || m.petID == terminalpet.DisabledID {
		return false
	}
	if m.petRenderer != nil && !m.petRenderer.Support().Supported() {
		return false
	}
	layoutWidth := m.width
	if m.sidebarActive() {
		layoutWidth = m.chatColumnWidth()
	}
	if !m.altScreen || layoutWidth < petImageColumns+4 || m.height < petImageRows+8 || m.subchat.active || m.transcriptDetailed {
		return false
	}
	if !m.noBlockingModal() || m.suggestionsActive() || m.setup.visible || m.helpOverlay || m.leaderHelpOverlay {
		return false
	}
	return true
}

func (m model) petComposerReservedColumns(width int) int {
	if !m.petLayoutRendering && !m.petLayoutActive() {
		return 0
	}
	return minInt(petReservedColumns, maxInt(0, width-8))
}

func (m model) footerStatusLine(width int) string {
	reserved := m.petComposerReservedColumns(width)
	return m.statusLine(width-reserved) + strings.Repeat(" ", reserved)
}

func (m model) floatingPetTranscriptView() string {
	chatModel := m
	chatModel.petLayoutRendering = true
	chat := m.reservePetImageSlot(viewLines(chatModel.transcriptView()), m.width)
	return strings.Join(chat, "\n")
}

func (m model) reservePetImageSlot(lines []string, width int) []string {
	chat := append([]string(nil), lines...)
	start := maxInt(0, len(chat)-petImageRows-1)
	rightStart := maxInt(0, width-petReservedColumns)
	for row := start; row < minInt(len(chat)-1, start+petImageRows); row++ {
		line := fitStyledLine(chat[row], width)
		chat[row] = padStyledLine(ansi.Cut(line, 0, rightStart), rightStart) + strings.Repeat(" ", width-rightStart)
	}
	return chat
}

func (m model) petImageDraw(content string) *terminalpet.ImageDraw {
	if m.petRenderer == nil || !m.petRenderer.Support().Supported() {
		return nil
	}
	if m.picker != nil && m.picker.kind == pickerPet && m.petPreview != nil {
		x, y, ok := petPickerImagePosition(content, petImageColumns, petImageRows)
		if !ok {
			return nil
		}
		return &terminalpet.ImageDraw{
			ID: petPreviewImageID, Animation: m.petPreview, State: terminalpet.Idle, Phase: m.petPhase,
			X: x, Y: y, Columns: petImageColumns, Rows: petImageRows, HeightPixels: 75,
		}
	}
	if !m.petLayoutActive() {
		return nil
	}
	y := maxInt(0, len(viewLines(content))-petImageRows-1)
	x := maxInt(0, m.width-petImageColumns-2)
	return &terminalpet.ImageDraw{
		ID: petAmbientImageID, Animation: m.petAnimation, State: m.petState(), Phase: m.petPhase,
		X: x, Y: y, Columns: petImageColumns, Rows: petImageRows, HeightPixels: 75,
	}
}

func petPickerImagePosition(content string, columns, rows int) (int, int, bool) {
	lines := viewLines(content)
	top, search, footer := -1, -1, -1
	for index, line := range lines {
		plain := ansi.Strip(line)
		if top < 0 && strings.Contains(plain, "Choose a companion") {
			top = index
		}
		if search < 0 && strings.Contains(plain, "search >") {
			search = index
			if top < 0 {
				top = maxInt(0, index-1)
			}
		}
		if top >= 0 && strings.Contains(plain, "↑/↓ preview") {
			footer = index
			break
		}
	}
	if top < 0 || footer < top || footer-rows-2 < 0 {
		return 0, 0, false
	}
	topLine := ansi.Strip(lines[top])
	left := len(topLine) - len(strings.TrimLeft(topLine, " "))
	overlayWidth := len([]rune(strings.TrimRight(topLine, " "))) - left
	if overlayWidth < columns {
		return 0, 0, false
	}
	if overlayWidth-4 >= petSidePreviewMin && search >= 0 {
		firstRule, secondRule := -1, -1
		for index := search + 1; index < footer; index++ {
			if strings.Count(ansi.Strip(lines[index]), "─") < columns {
				continue
			}
			if firstRule < 0 {
				firstRule = index
			} else {
				secondRule = index
				break
			}
		}
		listTop := firstRule + 1
		if firstRule >= 0 && secondRule-listTop >= rows {
			x := left + overlayWidth - columns - 2
			y := listTop + (secondRule-listTop-rows)/2
			return x, y, true
		}
	}
	return left + (overlayWidth-columns)/2, footer - rows - 2, true
}

func (m model) petState() terminalpet.State {
	if m.pendingPermission != nil || m.pendingAskUser != nil || m.pendingSpecReview != nil {
		return terminalpet.Waiting
	}
	if m.pending {
		return terminalpet.Running
	}
	if m.petOutcome != "" && m.now().Sub(m.petOutcomeAt) < petOutcomeHold {
		return m.petOutcome
	}
	return terminalpet.Idle
}
