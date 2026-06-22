package tui

import tea "charm.land/bubbletea/v2"

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

func (m model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// While a provider-wizard OAuth login is in flight, ignore all mouse input
	// (clicks and wheel) so a stray scroll can't change the selected provider
	// mid-login — mirroring the keyboard handler's pending guard.
	if m.providerWizard != nil && m.providerWizard.oauthPending {
		return m, nil
	}
	// A right-click pastes the clipboard straight into the focused field — no
	// menu. pasteFromClipboardCmd reads the clipboard off the Update goroutine; the
	// clipboardReadMsg result is routed by routePaste to wherever input is focused
	// (composer, wizard, setup) or swallowed on a surface with no text field.
	// Keyboard/selection copy-paste keep working unchanged.
	if mouseRightPress(msg) {
		return m, pasteFromClipboardCmd()
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

	switch {
	case mouseWheelUp(msg):
		m.clearMouseSelection()
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
			m.picker.move(-1)
			return m, nil
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
		return m.scrollChat(chatWheelScrollLines), nil
	case mouseWheelDown(msg):
		m.clearMouseSelection()
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
			m.picker.move(1)
			return m, nil
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
		return m.scrollChat(-chatWheelScrollLines), nil
	default:
		return m, nil
	}
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
	if !m.altScreen || m.height <= 0 {
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
	line := 2
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

func (m model) overlayMouseTop(overlayHeight int, width int) int {
	return m.overlayMouseRect(overlayHeight, width).y
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
