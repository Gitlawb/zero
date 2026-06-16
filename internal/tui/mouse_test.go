package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gitlawb/zero/internal/config"
)

func TestMouseClickSelectsThenAppliesCommandSuggestionRow(t *testing.T) {
	m := mouseTestModel()
	m = typeRunes(t, m, "/sp")
	if len(m.suggestions) == 0 {
		t.Fatalf("expected command suggestions, got %#v", m.suggestions)
	}

	width := chatWidth(m.width)
	top := m.overlayMouseTop(len(viewLines(m.suggestionOverlay(width))), width)
	click := testMouseClick(tea.MouseLeft, width/2, top+3)
	updated, cmd := m.Update(click)
	next := updated.(model)
	if cmd != nil {
		t.Fatal("first command click should not return a command")
	}
	if got := next.input.Value(); got != "/sp" {
		t.Fatalf("input after first command click = %q, want /sp", got)
	}
	if !next.suggestionsActive() {
		t.Fatal("suggestions should stay open after first command click")
	}

	updated, cmd = next.Update(click)
	next = updated.(model)
	_ = cmd
	if got := next.input.Value(); got != "/spec" {
		t.Fatalf("input after second command click = %q, want /spec", got)
	}
	if next.suggestionsActive() {
		t.Fatalf("suggestions should close after second command click, got %#v", next.suggestions)
	}
}

func TestMouseClickSelectsThenAppliesPickerRow(t *testing.T) {
	m := mouseTestModel()
	m.modelName = "claude-sonnet-4.5"
	m.picker = &commandPicker{
		kind:  pickerEffort,
		title: "select reasoning effort",
		items: []pickerItem{
			{Label: "auto", Value: "auto"},
			{Label: "high", Value: "high"},
		},
		selected: 0,
	}
	m.picker.allItems = append([]pickerItem{}, m.picker.items...)
	m.mouseCapture = true

	width := chatWidth(m.width)
	top := m.overlayMouseTop(len(viewLines(m.pickerOverlay(width))), width)
	click := testMouseClick(tea.MouseLeft, width/2, top+3)
	updated, cmd := m.Update(click)
	next := updated.(model)
	if cmd != nil {
		t.Fatal("first picker click should not return a command")
	}
	if next.picker == nil || next.picker.selected != 1 {
		t.Fatalf("picker after first click = %#v, want selected index 1", next.picker)
	}
	if next.reasoningEffort != "" {
		t.Fatalf("reasoning effort after first picker click = %q, want unchanged", next.reasoningEffort)
	}

	updated, cmd = next.Update(click)
	next = updated.(model)
	_ = cmd
	if next.picker != nil {
		t.Fatalf("picker should close after second click apply, got %#v", next.picker)
	}
	if next.reasoningEffort != "high" {
		t.Fatalf("reasoning effort after second picker click = %q, want high", next.reasoningEffort)
	}
}

func TestMouseClickSelectsProviderWizardRow(t *testing.T) {
	m := mouseTestModel()
	m.providerWizard = m.newProviderWizard()
	m.providerWizard.step = providerWizardStepProvider // skip the new method chooser
	if m.providerWizard == nil || len(m.providerWizard.providers) < 2 {
		t.Fatalf("expected multiple providers, got %#v", m.providerWizard)
	}
	m.mouseCapture = true

	width := chatWidth(m.width)
	top := m.overlayMouseTop(len(viewLines(m.providerWizardOverlay(width))), width)
	click := testMouseClick(tea.MouseLeft, width/2, top+5)
	updated, cmd := m.Update(click)
	next := updated.(model)
	if cmd != nil {
		t.Fatal("mouse selection should not return a command")
	}
	if next.providerWizard == nil || next.providerWizard.selectedProvider != 1 {
		t.Fatalf("provider selection = %#v, want selected index 1", next.providerWizard)
	}
	if next.providerWizard.step != providerWizardStepProvider {
		t.Fatalf("first provider click should not advance, got step %v", next.providerWizard.step)
	}

	updated, cmd = next.Update(click)
	next = updated.(model)
	_ = cmd
	if next.providerWizard == nil || next.providerWizard.step == providerWizardStepProvider {
		t.Fatalf("second provider click should advance, got %#v", next.providerWizard)
	}
}

func TestMouseWheelMovesProviderWizardRows(t *testing.T) {
	m := mouseTestModel()
	m.providerWizard = m.newProviderWizard()
	m.providerWizard.step = providerWizardStepProvider // skip the new method chooser
	m.mouseCapture = true

	updated, cmd := m.Update(testMouseWheel(tea.MouseWheelDown, 0, 0))
	next := updated.(model)
	if cmd != nil {
		t.Fatal("mouse wheel should not return a command")
	}
	if next.providerWizard == nil || next.providerWizard.selectedProvider != 1 {
		t.Fatalf("provider selection after wheel = %#v, want selected index 1", next.providerWizard)
	}
}

func TestMouseClickSelectsThenContinuesSetupProviderRow(t *testing.T) {
	m := setupMouseTestModel()
	m.mouseCapture = true
	m.setup.stage = setupStageProvider

	width := chatWidth(m.width)
	height := normalizedStartupHeight(m.height)
	rowWidth := setupProviderBlockWidth(width, m.setup.providers)
	top := setupContentTop(height, len(m.setupProviderLines(width, height)), m.setup.err != "")
	click := testMouseClick(tea.MouseLeft, maxInt(0, (width-rowWidth)/2)+2, top+3)
	updated, cmd := m.Update(click)
	next := updated.(model)
	if cmd != nil {
		t.Fatal("first setup provider click should not return a command")
	}
	if next.setup.selected != 1 {
		t.Fatalf("setup provider selection = %d, want 1", next.setup.selected)
	}
	if next.setup.stage != setupStageProvider {
		t.Fatalf("first setup provider click advanced to %v", next.setup.stage)
	}

	updated, cmd = next.Update(click)
	next = updated.(model)
	_ = cmd
	if next.setup.stage != setupStageCredentials {
		t.Fatalf("second setup provider click should advance to credentials, got %v", next.setup.stage)
	}
}

func TestMouseClickSelectsThenContinuesSetupModelRow(t *testing.T) {
	m := setupMouseTestModel()
	m.mouseCapture = true
	m.setup.stage = setupStageModel
	m.setup.models = []providerWizardModel{
		{ID: "alpha"},
		{ID: "beta", Meta: "128K ctx"},
	}
	m.setup.modelForID = m.setupProviderDescriptor().ID

	width := chatWidth(m.width)
	height := normalizedStartupHeight(m.height)
	rowWidth := setupModelBlockWidth(width, m.setup.models)
	top := setupContentTop(height, len(m.setupModelLines(width, height)), m.setup.err != "")
	click := testMouseClick(tea.MouseLeft, maxInt(0, (width-rowWidth)/2)+2, top+5)
	updated, cmd := m.Update(click)
	next := updated.(model)
	if cmd != nil {
		t.Fatal("first setup model click should not return a command")
	}
	if next.setup.modelIndex != 1 {
		t.Fatalf("setup model selection = %d, want 1", next.setup.modelIndex)
	}
	if next.setup.stage != setupStageModel {
		t.Fatalf("first setup model click advanced to %v", next.setup.stage)
	}

	updated, cmd = next.Update(click)
	next = updated.(model)
	_ = cmd
	if next.setup.stage != setupStageSafety {
		t.Fatalf("second setup model click should advance to safety, got %v", next.setup.stage)
	}
}

func TestMouseCaptureOnlyWhileInteractiveSurfaceOpen(t *testing.T) {
	m := mouseTestModel()
	m.transcript = appendRow(m.transcript, rowUser, "hello")
	if !m.wantsMouseCapture() {
		t.Fatal("chat should capture mouse for Zero-owned transcript selection")
	}

	m = typeRunes(t, m, "/")
	if !m.wantsMouseCapture() || !m.mouseCapture {
		t.Fatalf("open command palette should capture mouse, wants=%v active=%v", m.wantsMouseCapture(), m.mouseCapture)
	}

	updated, cmd := m.Update(testKey(tea.KeyEsc))
	m = updated.(model)
	_ = cmd
	if !m.wantsMouseCapture() || !m.mouseCapture {
		t.Fatalf("closed command palette should keep chat mouse capture, wants=%v active=%v", m.wantsMouseCapture(), m.mouseCapture)
	}
}

func TestMouseCaptureOnEmptyChatSplash(t *testing.T) {
	m := mouseTestModel()
	if !m.wantsMouseCapture() {
		t.Fatal("empty chat splash should capture mouse so only real transcript content becomes selectable")
	}

	m.transcript = appendRow(m.transcript, rowUser, "hello")
	if !m.wantsMouseCapture() {
		t.Fatal("chat with transcript rows should keep mouse capture for Zero-owned selection")
	}
}

func TestTranscriptSelectionOnlyStartsOnTranscriptText(t *testing.T) {
	m := mouseTestModel()
	m.mouseCapture = true
	m.transcript = appendRow(m.transcript, rowUser, "hello world")

	updated, cmd := m.Update(testMouseClick(tea.MouseLeft, 40, 20))
	next := updated.(model)
	if cmd != nil {
		t.Fatal("empty-area click should not return a command")
	}
	if next.transcriptSelection.active {
		t.Fatal("empty-area click should not start transcript selection")
	}

	updated, cmd = next.Update(testMouseClick(tea.MouseLeft, 3, 1))
	next = updated.(model)
	if cmd != nil {
		t.Fatal("transcript press should not copy yet")
	}
	if !next.transcriptSelection.active {
		t.Fatal("transcript text click should start transcript selection")
	}
}

func TestTranscriptSelectionExtractsVisibleTextRange(t *testing.T) {
	m := mouseTestModel()
	m.mouseCapture = true
	m.transcript = appendRow(m.transcript, rowUser, "hello world")

	updated, _ := m.Update(testMouseClick(tea.MouseLeft, 3, 1))
	m = updated.(model)
	updated, _ = m.Update(testMouseMotion(tea.MouseLeft, 8, 1))
	m = updated.(model)

	if got := m.selectedTranscriptText(); got != "hello" {
		t.Fatalf("selectedTranscriptText() = %q, want hello", got)
	}
}

func TestTranscriptSelectionUpdatesOnGenericMotion(t *testing.T) {
	m := mouseTestModel()
	m.mouseCapture = true
	m.transcript = appendRow(m.transcript, rowUser, "hello world")

	updated, _ := m.Update(testMouseClick(tea.MouseLeft, 3, 1))
	m = updated.(model)
	updated, _ = m.Update(testMouseMotion(tea.MouseNone, 8, 1))
	m = updated.(model)

	if got := m.selectedTranscriptText(); got != "hello" {
		t.Fatalf("selectedTranscriptText() after generic motion = %q, want hello", got)
	}
}

func TestTranscriptSelectionLeftDragDoesNotResetAnchor(t *testing.T) {
	m := mouseTestModel()
	m.mouseCapture = true
	m.transcript = appendRow(m.transcript, rowUser, "hello world")

	updated, _ := m.Update(testMouseClick(tea.MouseLeft, 3, 1))
	m = updated.(model)
	// A left-button drag is Action==Motion with Button==Left; this must update the
	// cursor without resetting the selection anchor.
	updated, _ = m.Update(testMouseMotion(tea.MouseLeft, 8, 1))
	m = updated.(model)

	if got := m.selectedTranscriptText(); got != "hello" {
		t.Fatalf("selectedTranscriptText() after left-button drag = %q, want hello", got)
	}
}

func TestTranscriptSelectionReleaseExtendsRangeWithoutMotion(t *testing.T) {
	m := mouseTestModel()
	m.mouseCapture = true
	m.transcript = appendRow(m.transcript, rowUser, "hello world")

	updated, _ := m.Update(testMouseClick(tea.MouseLeft, 3, 1))
	m = updated.(model)
	updated, cmd := m.Update(testMouseRelease(tea.MouseNone, 8, 1))
	m = updated.(model)
	if cmd == nil {
		t.Fatal("release after range selection should return copy command")
	}
	if got := m.selectedTranscriptText(); got != "hello" {
		t.Fatalf("selectedTranscriptText() after release = %q, want hello", got)
	}
}

func TestTranscriptSelectionClearsAfterCopy(t *testing.T) {
	m := mouseTestModel()
	m.transcriptSelection = transcriptSelectionState{active: true}

	updated, cmd := m.Update(transcriptCopiedMsg{chars: 5})
	next := updated.(model)
	if cmd == nil {
		t.Fatal("copy feedback should schedule a status clear")
	}
	if next.transcriptSelection.active {
		t.Fatal("selection highlight should clear after copy feedback")
	}
	if next.copyStatus != "Copied!" {
		t.Fatalf("copyStatus = %q, want Copied!", next.copyStatus)
	}
}

func TestMouseClickTogglesReasoningRow(t *testing.T) {
	m := mouseTestModel()
	m.mouseCapture = true
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowReasoning, text: "private thought"})

	width := chatWidth(m.width)
	body, selectable := m.transcriptBody(width, "")
	start, _ := m.transcriptViewportStart(body, width)
	var target transcriptSelectableLine
	for _, line := range selectable {
		if line.toggle {
			target = line
			break
		}
	}
	if !target.toggle {
		t.Fatalf("expected reasoning header to be clickable, selectable=%#v", selectable)
	}

	updated, cmd := m.Update(testMouseClick(tea.MouseLeft, target.textStart, target.bodyY-start))
	next := updated.(model)
	if cmd != nil {
		t.Fatal("reasoning toggle click should not return a command")
	}
	if !next.transcript[len(next.transcript)-1].expanded {
		t.Fatalf("reasoning row should expand after click: %#v", next.transcript[len(next.transcript)-1])
	}
	if next.transcriptSelection.active {
		t.Fatal("reasoning toggle should not start transcript selection")
	}
}

func TestMouseClickTogglesStreamingReasoning(t *testing.T) {
	m := mouseTestModel()
	m.mouseCapture = true
	m.pending = true
	m.activeRunID = 1
	m.streamingReasoning = "private **thought**"

	width := chatWidth(m.width)
	body, selectable := m.transcriptBody(width, "")
	start, _ := m.transcriptViewportStart(body, width)
	var target transcriptSelectableLine
	for _, line := range selectable {
		if line.toggle && line.live {
			target = line
			break
		}
	}
	if !target.toggle || !target.live {
		t.Fatalf("expected live reasoning header to be clickable, selectable=%#v", selectable)
	}

	updated, cmd := m.Update(testMouseClick(tea.MouseLeft, target.textStart, target.bodyY-start))
	next := updated.(model)
	if cmd != nil {
		t.Fatal("streaming reasoning toggle click should not return a command")
	}
	if !next.streamingReasoningExpanded {
		t.Fatal("streaming reasoning should expand after click")
	}
	view := plainRender(t, next.interimBlock(width))
	if !strings.Contains(view, "private thought") || strings.Contains(view, "**") {
		t.Fatalf("expanded streaming reasoning should render markdown-clean text, got:\n%s", view)
	}
}

func TestTranscriptCopyStatusClearsOnlyForLatestCopy(t *testing.T) {
	m := mouseTestModel()

	updated, _ := m.Update(transcriptCopiedMsg{chars: 5})
	m = updated.(model)
	firstSeq := m.copyStatusSeq

	updated, _ = m.Update(transcriptCopiedMsg{chars: 8})
	m = updated.(model)
	secondSeq := m.copyStatusSeq

	updated, _ = m.Update(transcriptCopyStatusExpiredMsg{seq: firstSeq})
	m = updated.(model)
	if m.copyStatus != "Copied!" {
		t.Fatalf("stale expiry cleared status: %q", m.copyStatus)
	}

	updated, _ = m.Update(transcriptCopyStatusExpiredMsg{seq: secondSeq})
	m = updated.(model)
	if m.copyStatus != "" {
		t.Fatalf("latest expiry left status = %q, want empty", m.copyStatus)
	}
}

func TestMCPManagerMouseSelectsFirstItemRow(t *testing.T) {
	m := newModel(context.Background(), Options{
		MCPConfig: config.MCPConfig{Servers: map[string]config.MCPServerConfig{
			"docs": {Type: "stdio", Command: "docs-mcp"},
		}},
	})
	m.width = 120
	m.height = 36
	m = m.openMCPManager()
	width := chatWidth(m.width)
	overlay := m.mcpManagerOverlay(width)
	lines := viewLines(overlay)
	left, _, _ := normalizeOverlayBlock(lines, width)
	y := m.overlayMouseTop(len(lines), width) + mcpManagerFirstItemRow(m.mcpViewState())

	target, ok := m.selectMCPManagerAtMouse(testMouseClick(tea.MouseLeft, left+2, y))
	if !ok {
		t.Fatal("expected click on first manager item row to select")
	}
	if target.Index != 0 || m.mcpManager.selected != 0 || target.Value != "docs" {
		t.Fatalf("selected target = %#v manager=%#v, want first docs item", target, m.mcpManager)
	}
}

func TestMCPAddWizardMouseSelectsAndActivatesType(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.width = 120
	m.height = 36
	m.mcpAddWizard = newMCPAddWizard("http")
	m.mcpAddWizard.step = mcpAddWizardStepType
	width := chatWidth(m.width)
	overlay := m.mcpAddWizardOverlay(width)
	lines := viewLines(overlay)
	left, _, _ := normalizeOverlayBlock(lines, width)
	y := m.overlayMouseTop(len(lines), width) + 5 // second type row: top border + step + rule + title + first row
	msg := testMouseClick(tea.MouseLeft, left+2, y)

	updated, cmd := m.Update(msg)
	next := updated.(model)
	if cmd != nil {
		t.Fatal("single click should only select the type")
	}
	if next.mcpAddWizard.serverType != "sse" {
		t.Fatalf("serverType after click = %q, want sse", next.mcpAddWizard.serverType)
	}

	updated, cmd = next.Update(msg)
	next = updated.(model)
	if cmd != nil {
		t.Fatal("double-click type activation should advance synchronously")
	}
	if next.mcpAddWizard.step != mcpAddWizardStepEndpoint {
		t.Fatalf("wizard step after double-click = %v, want endpoint", next.mcpAddWizard.step)
	}
}

func TestTranscriptCopyStatusUsesComposerSpacerWithoutFooterGrowth(t *testing.T) {
	m := mouseTestModel()
	m.providerName = "ollama-cloud"
	m.modelName = "qwen3-coder:480b"

	normalFooterLines := len(viewLines(plainRender(t, m.footerView(80))))
	normalViewLines := len(viewLines(plainRender(t, m.View())))
	m.copyStatus = "Copied!"

	footer := plainRender(t, m.footerView(80))
	if got := len(viewLines(footer)); got != normalFooterLines {
		t.Fatalf("copy status changed footer height from %d to %d:\n%s", normalFooterLines, got, footer)
	}
	view := plainRender(t, m.View())
	if got := len(viewLines(view)); got != normalViewLines {
		t.Fatalf("copy status changed view height from %d to %d:\n%s", normalViewLines, got, view)
	}
	if !strings.Contains(view, "Copied!") {
		t.Fatalf("view should show copy status, got:\n%s", view)
	}
	footerLines := viewLines(footer)
	if len(footerLines) < 2 || !strings.Contains(footerLines[0], "Copied!") || !strings.HasPrefix(footerLines[1], "╭") {
		t.Fatalf("copy status should replace the spacer directly above composer, got:\n%s", footer)
	}
	if strings.Contains(plainRender(t, m.statusLine(80)), "Copied!") {
		t.Fatalf("status line should not contain copy feedback: %q", plainRender(t, m.statusLine(80)))
	}
}

func TestMouseCaptureOnlyDuringInteractiveSetupStages(t *testing.T) {
	m := setupMouseTestModel()
	stages := []struct {
		stage setupStage
		want  bool
	}{
		{setupStageWelcome, false},
		{setupStageProvider, true},
		{setupStageCredentials, false},
		{setupStageModel, true},
		{setupStageSafety, false},
		{setupStageReady, false},
	}

	for _, tt := range stages {
		m.setup.stage = tt.stage
		if got := m.wantsMouseCapture(); got != tt.want {
			t.Fatalf("wantsMouseCapture at setup stage %v = %v, want %v", tt.stage, got, tt.want)
		}
	}
}

func mouseTestModel() model {
	m := newModel(context.Background(), Options{})
	m.width = 100
	m.height = 30
	m.altScreen = true
	m.headerPrinted = true
	return m
}

func setupMouseTestModel() model {
	m := newModel(context.Background(), Options{
		Setup: SetupOptions{
			Visible: true,
			Providers: []SetupProviderOption{
				{ID: "openai", Name: "OpenAI", DefaultModel: "gpt-4.1", EnvVar: "OPENAI_API_KEY", RequiresAuth: true},
				{ID: "anthropic", Name: "Anthropic", DefaultModel: "claude-sonnet-4.5", EnvVar: "ANTHROPIC_API_KEY", RequiresAuth: true},
				{ID: "ollama", Name: "Ollama Local", DefaultModel: "llama3.1", Local: true},
			},
		},
	})
	m.width = 100
	m.height = 30
	m.altScreen = true
	return m
}
