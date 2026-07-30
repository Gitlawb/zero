package tui

import (
	"os"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
)

// mouseModeEnv forces a mouse mode: "all", "cell", or "off"/"none". It exists
// because the failure it guards against is invisible from here and total for the
// user: a terminal we guessed wrong about delivers no mouse events at all, and
// there is no in-app way to recover. Anything unrecognised is ignored rather
// than treated as "off", so a typo cannot silently kill the mouse.
const mouseModeEnv = "ZERO_MOUSE_MODE"

// mouseModeFor picks the mouse reporting mode, preferring the widest support
// over the nicest behaviour.
//
// The trade is asymmetric. AllMotion (1003) buys exactly one thing, hover
// highlighting, because wheel, click and drag all arrive under CellMotion
// (1002). But when a terminal does not implement 1003, it does not degrade to
// 1002: no mouse event arrives at all. The app holds the alternate screen, so
// the terminal's own scrollback and wheel are gone too, and the user is left
// with no way to scroll by mouse. A long answer then looks like a hang rather
// than like a missing highlight, which is how #870 was reported.
//
// So AllMotion is asked for only where it is known to work.
func mouseModeFor(goos string, env func(string) string, underPRoot bool) tea.MouseMode {
	switch strings.ToLower(strings.TrimSpace(env(mouseModeEnv))) {
	case "all":
		return tea.MouseModeAllMotion
	case "cell":
		return tea.MouseModeCellMotion
	case "off", "none":
		return tea.MouseModeNone
	}
	if underPRoot {
		return tea.MouseModeCellMotion
	}
	if goos == "windows" && !windowsTerminalReportsAllMotion(env) {
		return tea.MouseModeCellMotion
	}
	return tea.MouseModeAllMotion
}

// windowsTerminalReportsAllMotion reports whether the Windows terminal in use is
// one that handles 1003.
//
// Identified terminals are trusted: Windows Terminal sets WT_SESSION, and hosts
// like VS Code and mintty set TERM_PROGRAM. The legacy console host sets
// neither, and it is what a user gets by double-clicking zero.exe or running it
// from an old cmd window, so an unidentified Windows terminal is assumed to be
// that one. Guessing wrong in this direction costs a hover highlight; guessing
// wrong in the other direction costs every mouse event.
// KNOWN LIMIT: these variables are INHERITED, so they answer for the process
// that set them rather than for the console host attached right now. A shell
// launched from Windows Terminal, or from Git Bash, that later runs zero.exe
// against the legacy console still carries them, and this returns true for a
// host that may drop every mouse event.
//
// Left as is on purpose. Deciding it properly means asking the console host
// rather than the environment, and erring the other way costs every Windows
// Terminal user their hover highlighting for a case that needs an unusual
// launch path to reach. ZERO_MOUSE_MODE=cell is the recourse in the meantime,
// which is the main reason that override exists.
// TestInheritedWindowsTerminalEnvStillAsksForAllMotion pins this, so it is a
// documented limit rather than a surprise.
func windowsTerminalReportsAllMotion(env func(string) string) bool {
	return strings.TrimSpace(env("WT_SESSION")) != "" ||
		strings.TrimSpace(env("TERM_PROGRAM")) != ""
}

// parseTracerPid returns true when /proc/self/status contains a non-zero
// TracerPid. This detects any ptrace tracer (PRoot, gdb, strace, dlv, etc.),
// not PRoot specifically. That's intentional: under any ptrace tracer the
// AllMotion (1003) sequence is unreliable, so CellMotion is the safer
// fallback. The only loss is hover-highlighting, which has no functional
// impact.
func parseTracerPid(data []byte) bool {
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "TracerPid:") {
			pid := strings.TrimSpace(line[10:])
			return pid != "0"
		}
	}
	return false
}

var isRunningUnderPRoot = sync.OnceValue(func() bool {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return false
	}
	return parseTracerPid(data)
})

type mouseOverlayHit struct {
	x int
	y int
}

type mouseSelectionTarget struct {
	Scope string
	Kind  int
	Value string
	Index int
}

// scrollChatExtendingSelection scrolls the chat and, if a drag-selection is in
// progress, extends its cursor to the mouse's current position against the
// POST-scroll viewport — mirroring what a mouseMotion event does. Without this, a
// selection's cursor is only ever updated by actual mouse movement (see
// handleTranscriptSelectionMouse's mouseMotion case): a wheel-scroll event is a
// distinct message type that never reaches that code, so scrolling while
// selecting left the selection frozen at whatever line the mouse last physically
// moved over — capped at the viewport that was visible before the scroll.
func (m model) scrollChatExtendingSelection(delta int, msg tea.MouseMsg) model {
	m = m.scrollChat(delta)
	if !m.transcriptSelection.active {
		return m
	}
	if line, ok := m.nearestTranscriptLineAtMouse(msg); ok {
		m.transcriptSelection.cursor = transcriptSelectionPointForMouse(line, mouseX(msg))
	}
	return m
}

func (m model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// While a provider-wizard OAuth login is in flight, ignore all mouse input
	// (clicks and wheel) so a stray scroll can't change the selected provider
	// mid-login — mirroring the keyboard handler's pending guard.
	if m.providerWizard != nil && m.providerWizard.oauthPending {
		return m, nil
	}
	// A click is a deliberate action, same as a keypress — it means the user
	// moved on to something else, so it disarms a stale Esc cancel-confirmation
	// rather than leaving it armed for some later, unrelated Esc to act on.
	// Scoped to actual clicks (not hover/motion/wheel), since motion events fire
	// continuously while the mouse merely sits over the terminal and would
	// otherwise defeat the confirmation window on every render tick.
	if mouseLeftPress(msg) || mouseRightPress(msg) {
		m = m.disarmCancelConfirmation()
	}
	// A right-click pastes the clipboard straight into the focused field — no
	// menu. pasteFromClipboardCmd reads the clipboard off the Update goroutine; the
	// clipboardReadMsg result is routed by routePaste to wherever input is focused
	// (composer, wizard, setup) or swallowed on a surface with no text field.
	// Keyboard/selection copy-paste keep working unchanged.
	if mouseRightPress(msg) {
		return m, pasteFromClipboardCmd()
	}
	if next, cmd, handled := m.handlePetMouse(msg); handled {
		return next, cmd
	}
	// Clicking the orchestrate plan's header line opens or closes it. Checked
	// before the surface switch below because the panel lives in the FOOTER,
	// outside every transcript/sidebar region those cases test.
	// Clicking the posture chip opens /effort, where it can be turned off or
	// changed. A chip that highlights under the cursor and then does nothing
	// when pressed is worse than one that never highlighted.
	if mouseLeftPress(msg) && m.zeromaxingChipAtMouse(msg) {
		// Not while a turn is in flight: the effort picker refuses mid-run for
		// the same reason /effort does, and opening one that cannot be acted on
		// would be a dead dialog.
		if !m.pending {
			if picker := m.newEffortPicker(); picker != nil {
				m.picker = picker
			}
		}
		return m, nil
	}
	// The plan lives in the sidebar: clicking a task selects it (the TASK
	// section below shows it), clicking the PLAN header collapses the section.
	// Checked before the surface switch because the sidebar is not one of the
	// regions those cases test.
	if mouseLeftPress(msg) {
		if index, ok := m.orchestrateTaskAtMouse(msg); ok {
			m.orchestrateSelected = index
			return m, nil
		}
		if m.orchestrateHeaderAtMouse(msg) {
			m.orchestrate.sidebarCollapsed = !m.orchestrate.sidebarCollapsed
			return m, nil
		}
	}
	// Clicking the inline panel's header still expands it in place.
	if mouseLeftPress(msg) && m.clickedOrchestrateHeader(msg) {
		m.orchestrate.expanded = !m.orchestrate.expanded
		return m, nil
	}
	if mouseLeftPress(msg) {
		switch {
		case m.providerWizard != nil:
			if target, ok := m.selectProviderWizardAtMouse(msg); ok {
				if m.repeatMouseSelection(target) {
					m.clearMouseSelection()
					return m.advanceProviderWizard()
				}
				m.lastMouseSelection = target
				return m, nil
			}
		case m.mcpAddWizard != nil:
			if target, ok := m.selectMCPAddWizardAtMouse(msg); ok {
				if m.repeatMouseSelection(target) {
					m.clearMouseSelection()
					return m.handleMCPAddWizardMouseActivate()
				}
				m.lastMouseSelection = target
				return m, nil
			}
		case m.mcpManager != nil:
			if target, ok := m.selectMCPManagerAtMouse(msg); ok {
				if m.repeatMouseSelection(target) {
					m.clearMouseSelection()
					return m.chooseMCPManagerItem()
				}
				m.lastMouseSelection = target
				return m, nil
			}
		case m.picker != nil:
			if target, ok := m.selectPickerAtMouse(msg); ok {
				if m.repeatMouseSelection(target) {
					m.clearMouseSelection()
					return m.choosePicker()
				}
				m.lastMouseSelection = target
				if m.picker.kind == pickerPet {
					return m.schedulePetPreview()
				}
				return m, nil
			}
		case m.suggestionsActive():
			if target, ok := m.selectSuggestionAtMouse(msg); ok {
				if m.repeatMouseSelection(target) {
					m.clearMouseSelection()
					return m.chooseSuggestion()
				}
				m.lastMouseSelection = target
				return m, nil
			}
		}
	}
	if next, cmd, ok := m.handleComposerSelectionMouse(msg); ok {
		return next, cmd
	}
	if next, cmd, ok := m.handleTranscriptSelectionMouse(msg); ok {
		return next, cmd
	}
	// A plain hover (cursor moved, no button pressed) never matches the
	// press/drag/release cases above, so it falls through here — resolve what's
	// under the cursor so it can render with the hover highlight.
	if mouseHover(msg) {
		hovered := m.updateHoverTarget(msg)
		// Starting the tick HERE is what lets the hover flow move while the
		// cursor rests. Bounded by zeromaxingChipAnimating: it runs only while
		// the chip is actually hovered and stops the moment the cursor leaves,
		// so an idle session still schedules nothing.
		return hovered, hovered.ensureSpinnerTick()
	}

	switch {
	case mouseWheelUp(msg):
		m.clearMouseSelection()
		m = m.clearHover()
		if m.providerWizard != nil {
			m.providerWizard.move(-1)
			return m, nil
		}
		if m.mcpAddWizard != nil {
			m.mcpAddWizard.move(-1)
			return m, nil
		}
		if m.mcpManager != nil {
			m.moveMCPManager(-1)
			return m, nil
		}
		if m.picker != nil {
			if m.modelPickerIsLoading() {
				return m, nil
			}
			return m.pickerMoved(-1)
		}
		if m.suggestionsActive() {
			m.moveSuggestion(-1)
			return m, nil
		}
		if m.mouseOverComposer(msg) {
			if next, ok := m.moveComposerVisualCursor(-1); ok {
				return next, nil
			}
		}
		return m.scrollChatExtendingSelection(chatWheelScrollLines, msg), nil
	case mouseWheelDown(msg):
		m.clearMouseSelection()
		m = m.clearHover()
		if m.providerWizard != nil {
			m.providerWizard.move(1)
			return m, nil
		}
		if m.mcpAddWizard != nil {
			m.mcpAddWizard.move(1)
			return m, nil
		}
		if m.mcpManager != nil {
			m.moveMCPManager(1)
			return m, nil
		}
		if m.picker != nil {
			if m.modelPickerIsLoading() {
				return m, nil
			}
			return m.pickerMoved(1)
		}
		if m.suggestionsActive() {
			m.moveSuggestion(1)
			return m, nil
		}
		if m.mouseOverComposer(msg) {
			if next, ok := m.moveComposerVisualCursor(1); ok {
				return next, nil
			}
		}
		return m.scrollChatExtendingSelection(-chatWheelScrollLines, msg), nil
	default:
		return m, nil
	}
}

// sidebarLineAtMouse maps a left-click in the AGENTS sidebar column to the swarm
// member whose row was clicked, when that member's session is known. Geometry:
// twoColumnTranscriptView is the full screen (sidebarActive ⇒ alt-screen), and
// joinColumns lays out [chat(chatColumnWidth)][" │ " 3-cell divider][sidebar],
// zipping row-by-row — so a sidebar line's screen Y equals its sidebar index and
// the sidebar starts at screen X = chatColumnWidth + 3. Recomputed on demand
// (View can't persist a registry on the value-receiver model), like
// transcriptLineAtMouse.
func (m model) sidebarLineAtMouse(msg tea.MouseMsg) (sidebarAgentHit, bool) {
	if !m.sidebarActive() {
		return sidebarAgentHit{}, false
	}
	if m.setup.visible || m.providerWizard != nil || m.mcpAddWizard != nil || m.mcpManager != nil || m.picker != nil || m.suggestionsActive() {
		return sidebarAgentHit{}, false
	}
	sidebarW := sidebarWidth(m.width)
	if sidebarW <= 0 {
		return sidebarAgentHit{}, false
	}
	x0 := m.chatColumnWidth() + 3 // " │ " divider between the columns
	x, y := mouseX(msg), mouseY(msg)
	if x < x0 || x >= x0+sidebarW {
		return sidebarAgentHit{}, false
	}
	for _, hit := range m.sidebarAgentSelectables(sidebarW) {
		if hit.lineOffset == y && (hit.sessionID != "" || hit.toggleDone) {
			return hit, true
		}
	}
	return sidebarAgentHit{}, false
}

func (m model) repeatMouseSelection(target mouseSelectionTarget) bool {
	return target.Scope != "" && m.lastMouseSelection == target
}

func (m *model) clearMouseSelection() {
	m.lastMouseSelection = mouseSelectionTarget{}
}

func (m model) wantsMouseCapture() bool {
	if m.mouseReleased {
		return false // user released the mouse for native text selection/copy
	}
	return m.altScreen && (m.setupWantsMouseCapture() || m.chatWantsMouseCapture() || m.providerWizard != nil || m.mcpAddWizard != nil || m.mcpManager != nil || m.picker != nil || m.suggestionsActive())
}

func (m model) setupWantsMouseCapture() bool {
	if !m.setup.visible {
		return false
	}
	return m.setup.stage == setupStageProvider || m.setup.stage == setupStageModel
}

func (m model) chatWantsMouseCapture() bool {
	return !m.setup.visible
}

func (m model) syncMouseCapture() (model, tea.Cmd) {
	want := m.wantsMouseCapture()
	if m.mouseCapture == want {
		return m, nil
	}
	m.mouseCapture = want
	return m, nil
}

func (m model) mouseOverComposer(msg tea.MouseMsg) bool {
	if !m.altScreen || m.height <= 0 || m.transcriptDetailed {
		return false
	}
	width := m.chatColumnWidth()
	frame := m.scrollableTranscriptFrame(m.pinnedTitleBar(width), m.footerView(width))
	return frame.composerRect.contains(mouseX(msg), mouseY(msg))
}

func lineSequenceIndex(lines []string, sequence []string) int {
	if len(sequence) == 0 || len(sequence) > len(lines) {
		return -1
	}
	for index := 0; index+len(sequence) <= len(lines); index++ {
		matched := true
		for offset := range sequence {
			if lines[index+offset] != sequence[offset] {
				matched = false
				break
			}
		}
		if matched {
			return index
		}
	}
	return -1
}

func (m *model) selectSuggestionAtMouse(msg tea.MouseMsg) (mouseSelectionTarget, bool) {
	if !m.suggestionsActive() || len(m.suggestions) == 0 {
		return mouseSelectionTarget{}, false
	}
	width := chatWidth(m.width)
	hit, ok := m.overlayMouseHit(msg, m.suggestionOverlay(width), width)
	if !ok {
		return mouseSelectionTarget{}, false
	}
	maxVisible := minInt(suggestionPaletteMaxVisible, len(m.suggestions))
	start := selectableListStart(len(m.suggestions), maxVisible, clampInt(m.suggestionIdx, 0, len(m.suggestions)-1))
	row := hit.y - 3
	if row < 0 || row >= maxVisible {
		return mouseSelectionTarget{}, false
	}
	index := start + row
	m.suggestionIdx = index
	scope := "command"
	if m.suggestionsAreFiles {
		scope = "file"
	}
	return mouseSelectionTarget{Scope: scope, Value: m.suggestions[index].Name, Index: index}, true
}

func (m *model) selectPickerAtMouse(msg tea.MouseMsg) (mouseSelectionTarget, bool) {
	if m.picker == nil || len(m.picker.items) == 0 || m.modelPickerIsLoading() {
		return mouseSelectionTarget{}, false
	}
	if m.picker.kind == pickerModel {
		return m.selectModelPickerAtMouse(msg)
	}
	return m.selectGenericPickerAtMouse(msg)
}

func (m *model) selectMCPManagerAtMouse(msg tea.MouseMsg) (mouseSelectionTarget, bool) {
	if m.mcpManager == nil {
		return mouseSelectionTarget{}, false
	}
	items := m.mcpManagerItems()
	if len(items) == 0 {
		return mouseSelectionTarget{}, false
	}
	width := chatWidth(m.width)
	hit, ok := m.overlayMouseHit(msg, m.mcpManagerOverlay(width), width)
	if !ok {
		return mouseSelectionTarget{}, false
	}
	_, itemRows := m.renderMCPManagerItemLines(maxInt(1, chatWidth(m.width)-4), items)
	baseRow := mcpManagerFirstItemRow(m.mcpViewState())
	row := hit.y - baseRow
	if row < 0 || row >= len(itemRows) || itemRows[row] < 0 {
		return mouseSelectionTarget{}, false
	}
	index := itemRows[row]
	m.mcpManager.selected = index
	return mouseSelectionTarget{Scope: "mcp-manager", Kind: int(items[index].Kind), Value: items[index].Name, Index: index}, true
}

func (m *model) selectMCPAddWizardAtMouse(msg tea.MouseMsg) (mouseSelectionTarget, bool) {
	if m.mcpAddWizard == nil {
		return mouseSelectionTarget{}, false
	}
	width := chatWidth(m.width)
	hit, ok := m.overlayMouseHit(msg, m.mcpAddWizardOverlay(width), width)
	if !ok {
		return mouseSelectionTarget{}, false
	}
	baseRow := 4
	if m.mcpAddWizard.err != "" {
		baseRow += 2
	}
	switch m.mcpAddWizard.step {
	case mcpAddWizardStepType:
		row := hit.y - baseRow
		if row < 0 || row >= len(mcpAddWizardTypes) {
			return mouseSelectionTarget{}, false
		}
		m.mcpAddWizard.selectedType = row
		m.mcpAddWizard.serverType = mcpAddWizardTypes[row].ID
		return mouseSelectionTarget{Scope: "mcp-add-wizard", Kind: int(m.mcpAddWizard.step), Value: m.mcpAddWizard.serverType, Index: row}, true
	case mcpAddWizardStepResult:
		actions := m.mcpAddWizard.resultActions()
		if len(actions) == 0 {
			return mouseSelectionTarget{}, false
		}
		rowStart := m.mcpAddWizard.mcpAddWizardResultActionStartRow()
		row := hit.y - rowStart
		if row < 0 || row >= len(actions) {
			return mouseSelectionTarget{}, false
		}
		m.mcpAddWizard.resultSelected = row
		return mouseSelectionTarget{Scope: "mcp-add-wizard", Kind: int(m.mcpAddWizard.step), Value: actions[row], Index: row}, true
	default:
		return mouseSelectionTarget{}, false
	}
}

func (m model) handleMCPAddWizardMouseActivate() (tea.Model, tea.Cmd) {
	if m.mcpAddWizard == nil {
		return m, nil
	}
	if m.mcpAddWizard.step == mcpAddWizardStepResult {
		return m.handleMCPAddWizardResultEnter()
	}
	return m.advanceMCPAddWizard()
}

func (wizard *mcpAddWizardState) mcpAddWizardResultActionStartRow() int {
	if wizard == nil {
		return 0
	}
	row := 7 // top border + step + separator + title + server + transport + saved/tools line
	if wizard.err != "" {
		row += 2
	}
	if wizard.result.Message != "" {
		row++
	}
	row++ // blank line before actions
	return row
}

func (m *model) selectModelPickerAtMouse(msg tea.MouseMsg) (mouseSelectionTarget, bool) {
	width := chatWidth(m.width)
	hit, ok := m.overlayMouseHit(msg, m.modelPickerOverlay(width), width)
	if !ok {
		return mouseSelectionTarget{}, false
	}
	maxVisible := minInt(pickerOverlayMaxVisible, len(m.picker.items))
	start := selectableListStart(len(m.picker.items), maxVisible, clampInt(m.picker.selected, 0, len(m.picker.items)-1))
	rowStart := 3
	if m.modelPickerLoadError != "" {
		rowStart++
	}
	row := hit.y - rowStart
	if row < 0 || row >= maxVisible {
		return mouseSelectionTarget{}, false
	}
	index := start + row
	m.picker.selected = index
	return mouseSelectionTarget{Scope: "picker", Kind: int(m.picker.kind), Value: m.picker.items[index].Value, Index: index}, true
}

func (m *model) selectGenericPickerAtMouse(msg tea.MouseMsg) (mouseSelectionTarget, bool) {
	width := chatWidth(m.width)
	hit, ok := m.overlayMouseHit(msg, m.pickerOverlay(width), width)
	if !ok {
		return mouseSelectionTarget{}, false
	}
	maxVisible := minInt(pickerOverlayMaxVisible, len(m.picker.items))
	selected := clampInt(m.picker.selected, 0, len(m.picker.items)-1)
	start := selectableListStart(len(m.picker.items), maxVisible, selected)
	visible := m.picker.items[start : start+maxVisible]
	// y=0 titled top border, y=1 search line, y=2 separator; rows begin at y=3.
	line := 3
	lastGroup := ""
	for offset, item := range visible {
		if item.Group != "" && item.Group != lastGroup {
			if hit.y == line {
				return mouseSelectionTarget{}, false
			}
			line++
			lastGroup = item.Group
		}
		if hit.y == line {
			index := start + offset
			m.picker.selected = index
			// Clicking (or hovering onto) a row live-previews it, like arrow keys,
			// so the theme picker repaints before the confirming second click.
			m.previewSelectedTheme()
			return mouseSelectionTarget{Scope: "picker", Kind: int(m.picker.kind), Value: item.Value, Index: index}, true
		}
		line++
	}
	return mouseSelectionTarget{}, false
}

func (m *model) selectProviderWizardAtMouse(msg tea.MouseMsg) (mouseSelectionTarget, bool) {
	if m.providerWizard == nil || m.providerWizard.oauthPending {
		return mouseSelectionTarget{}, false
	}
	width := chatWidth(m.width)
	hit, ok := m.overlayMouseHit(msg, m.providerWizardOverlay(width), width)
	if !ok {
		return mouseSelectionTarget{}, false
	}
	baseRow := 3
	if m.providerWizard.err != "" {
		baseRow += 2
	}
	switch m.providerWizard.step {
	case providerWizardStepProvider:
		if len(m.providerWizard.providers) == 0 {
			return mouseSelectionTarget{}, false
		}
		maxVisible := minInt(maxProviderWizardProvidersVisible, len(m.providerWizard.providers))
		selected := clampInt(m.providerWizard.selectedProvider, 0, len(m.providerWizard.providers)-1)
		start := selectableListStart(len(m.providerWizard.providers), maxVisible, selected)
		row := hit.y - (baseRow + 1)
		if row < 0 || row >= maxVisible {
			return mouseSelectionTarget{}, false
		}
		index := start + row
		m.providerWizard.selectedProvider = index
		m.providerWizard.apiKey = ""
		m.providerWizard.refreshModels()
		return mouseSelectionTarget{Scope: "provider-wizard", Kind: int(m.providerWizard.step), Value: m.providerWizard.providers[index].ID, Index: index}, true
	case providerWizardStepModel:
		if m.providerWizard.modelLoading {
			return mouseSelectionTarget{}, false
		}
		m.providerWizard.refreshModels()
		models := m.providerWizard.filteredModels()
		if len(models) == 0 {
			return mouseSelectionTarget{}, false
		}
		maxVisible := minInt(maxProviderWizardModelsVisible, len(models))
		selected := clampInt(m.providerWizard.selectedModel, 0, len(models)-1)
		start := selectableListStart(len(models), maxVisible, selected)
		rowStart := baseRow + 2
		if m.providerWizard.modelStatusText() != "" {
			rowStart++
		}
		row := hit.y - rowStart
		if row < 0 || row >= maxVisible {
			return mouseSelectionTarget{}, false
		}
		index := start + row
		m.providerWizard.selectedModel = index
		return mouseSelectionTarget{Scope: "provider-wizard", Kind: int(m.providerWizard.step), Value: models[index].ID, Index: index}, true
	default:
		return mouseSelectionTarget{}, false
	}
}

func (m model) overlayMouseHit(msg tea.MouseMsg, overlay string, width int) (mouseOverlayHit, bool) {
	lines := viewLines(overlay)
	if len(lines) == 0 {
		return mouseOverlayHit{}, false
	}
	left, lines, overlayWidth := normalizeOverlayBlock(lines, width)
	if overlayWidth <= 0 || len(lines) == 0 {
		return mouseOverlayHit{}, false
	}
	rect := m.overlayMouseRect(len(lines), width)
	if rect.height <= 0 || mouseY(msg) < rect.y || mouseY(msg) >= rect.y+rect.height {
		return mouseOverlayHit{}, false
	}
	if mouseX(msg) < left || mouseX(msg) >= left+overlayWidth {
		return mouseOverlayHit{}, false
	}
	return mouseOverlayHit{x: mouseX(msg) - left, y: mouseY(msg) - rect.y}, true
}

func (m model) overlayMouseRect(overlayHeight int, width int) tuiRect {
	if overlayHeight <= 0 {
		return tuiRect{}
	}
	if m.altScreen && m.height > 0 {
		frame := m.scrollableTranscriptFrame(m.pinnedTitleBar(width), m.footerView(width))
		visibleHeight := minInt(overlayHeight, frame.bodyRect.height)
		if visibleHeight <= 0 {
			return tuiRect{}
		}
		return tuiRect{
			y:      frame.bodyRect.y + maxInt(0, (frame.bodyRect.height-visibleHeight)/2),
			width:  width,
			height: visibleHeight,
		}
	}
	return tuiRect{
		y:      maxInt(0, (normalizedStartupHeight(m.height)-overlayHeight)/2),
		width:  width,
		height: overlayHeight,
	}
}
