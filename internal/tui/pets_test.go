package tui

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Gitlawb/zero/internal/terminalpet"
	"github.com/charmbracelet/x/ansi"
)

func TestParsePetsCommandAndAlias(t *testing.T) {
	for _, input := range []string{"/pets", "/pet boba"} {
		parsed := parseCommand(input)
		if parsed.kind != commandPets {
			t.Fatalf("parseCommand(%q).kind = %v, want commandPets", input, parsed.kind)
		}
	}
}

func TestPetPickerOverlayIncludesPreview(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.width = 100
	m.picker = &commandPicker{kind: pickerPet, title: "Choose a companion", items: []pickerItem{{Label: "Boba", Value: "boba"}}, selected: 0}
	m.petEntries["boba"] = terminalpet.Entry{Slug: "boba", DisplayName: "Boba", SubmittedBy: "tester"}
	frame := image.NewNRGBA(image.Rect(0, 0, 12, 12))
	for y := range 12 {
		for x := range 12 {
			frame.SetNRGBA(x, y, color.NRGBA{R: 120, G: 80, B: 200, A: 255})
		}
	}
	animation, err := terminalpet.ThumbnailAnimation(frame)
	if err != nil {
		t.Fatal(err)
	}
	m.petPreview = animation
	plain := plainRender(t, m.petPickerOverlay(m.width))
	if !strings.Contains(plain, "Boba · by tester") || !strings.Contains(plain, "Enter select") {
		t.Fatalf("pet overlay lacks preview detail or controls: %q", plain)
	}
}

func TestPetPickerRowsShowOnlyNamesAndFooterUsesCatalogMetadata(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.width = 100
	m.picker = &commandPicker{kind: pickerPet, title: "Choose a companion", items: []pickerItem{{Label: "Boba", Value: "boba"}}, selected: 0}
	m.petEntries["boba"] = terminalpet.Entry{Slug: "boba", DisplayName: "Boba", Kind: "creature", SubmittedBy: "tester"}

	plain := plainRender(t, m.petPickerOverlay(m.width))
	if !strings.Contains(plain, "Boba · creature · by tester") {
		t.Fatalf("pet footer should show name, kind, and creator:\n%s", plain)
	}
	for _, line := range strings.Split(plain, "\n") {
		if strings.Contains(line, "❯ Boba") && (strings.Contains(line, "creature") || strings.Contains(line, "tester")) {
			t.Fatalf("pet list row should contain only the name: %q", line)
		}
	}
}

func TestPetCatalogUsesClearLocalAndRemoteGroupLabels(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.picker = &commandPicker{kind: pickerPet}
	updated, _ := m.applyPetCatalog(petCatalogLoadedMsg{entries: []terminalpet.Entry{
		{Slug: "local", DisplayName: "Local", Local: true},
		{Slug: "remote", DisplayName: "Remote"},
	}})
	next := updated.(model)
	want := []pickerItem{
		{Label: "No companion", Value: terminalpet.DisabledID, Meta: "off"},
		{Group: "Installed", Label: "Local", Value: "local", Local: true},
		{Group: "Discover", Label: "Remote", Value: "remote", Remote: true},
	}
	if !reflect.DeepEqual(next.picker.items, want) {
		t.Fatalf("pet picker items = %#v, want %#v", next.picker.items, want)
	}
}

func TestPetPickerRowsTruncateLongNames(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.width = 100
	m.picker = &commandPicker{kind: pickerPet, title: "Choose a companion", items: []pickerItem{{
		Label: "A very long companion name that keeps going far beyond the available picker row width and must be truncated safely",
		Value: "long-pet",
	}}, selected: 0}
	m.petEntries["long-pet"] = terminalpet.Entry{Slug: "long-pet", DisplayName: "Long pet", Kind: "character", SubmittedBy: "tester"}

	plain := plainRender(t, m.petPickerOverlay(m.width))
	var row string
	for _, line := range strings.Split(plain, "\n") {
		if strings.Contains(line, "A very long companion") {
			row = line
			break
		}
	}
	if row == "" {
		t.Fatalf("pet row missing:\n%s", plain)
	}
	if !strings.Contains(row, "…") || strings.Contains(row, "truncated safely") {
		t.Fatalf("pet row should truncate its long name: %q", row)
	}
}

func TestPetPickerViewSchedulesTerminalImageInsteadOfANSIArt(t *testing.T) {
	m := newModel(context.Background(), Options{AltScreen: true})
	m.width, m.height = 110, 34
	m.picker = &commandPicker{kind: pickerPet, title: "Choose a companion", items: []pickerItem{{Label: "Boba", Value: "boba"}}, selected: 0}
	m.petEntries["boba"] = terminalpet.Entry{Slug: "boba", DisplayName: "Boba", SubmittedBy: "tester"}
	frame := image.NewNRGBA(image.Rect(0, 0, 12, 12))
	for y := range 12 {
		for x := range 12 {
			frame.SetNRGBA(x, y, color.NRGBA{R: 120, G: 80, B: 200, A: 255})
		}
	}
	m.petPreview, _ = terminalpet.ThumbnailAnimation(frame)
	m.petRenderer = terminalpet.NewImageRenderer(terminalpet.ImageSupport{Protocol: terminalpet.ImageProtocolKitty})

	view := m.View()
	if strings.Contains(plainRender(t, view.Content), "▀") {
		t.Fatalf("pet picker still contains ANSI image cells: %q", plainRender(t, view.Content))
	}
	var output bytes.Buffer
	if err := m.petRenderer.Render(&output); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); !strings.Contains(got, "_Ga=T,t=d,f=100,c=9,r=5") {
		t.Fatalf("pet picker did not schedule a Kitty image: %q", got)
	}
}

func TestPetPickerPreviewUsesRightPaneWhenWide(t *testing.T) {
	m := newModel(context.Background(), Options{AltScreen: true})
	m.width, m.height = 110, 34
	m.picker = &commandPicker{kind: pickerPet, title: "Choose a companion", items: []pickerItem{
		{Group: "Discover", Label: "Aion", Value: "aion"},
		{Group: "Discover", Label: "AirRing", Value: "airring"},
		{Group: "Discover", Label: "Akane", Value: "akane"},
	}, selected: 1}
	m.petEntries["airring"] = terminalpet.Entry{Slug: "airring", DisplayName: "AirRing", SubmittedBy: "tester"}
	frame := image.NewNRGBA(image.Rect(0, 0, 12, 12))
	m.petPreview, _ = terminalpet.ThumbnailAnimation(frame)

	content := m.petPickerOverlay(m.width)
	x, y, ok := petPickerImagePosition(content, petImageColumns, petImageRows)
	if !ok {
		t.Fatalf("wide picker should provide preview image geometry:\n%s", plainRender(t, content))
	}
	if x < m.width*2/3 {
		t.Fatalf("wide picker preview x=%d, want a right-side pane in width %d", x, m.width)
	}
	detailRow := -1
	for index, line := range viewLines(content) {
		if strings.Contains(ansi.Strip(line), "AirRing · by tester") {
			detailRow = index
			break
		}
	}
	if detailRow < 0 || y+petImageRows > detailRow {
		t.Fatalf("preview rows [%d,%d) should sit beside the list above detail row %d:\n%s", y, y+petImageRows, detailRow, plainRender(t, content))
	}
	previewRow := ""
	for _, line := range viewLines(content) {
		plain := ansi.Strip(line)
		if strings.Contains(plain, "Preview") {
			previewRow = plain
			break
		}
	}
	if previewRow == "" {
		t.Fatalf("wide picker should label its preview pane:\n%s", plainRender(t, content))
	}
	if !strings.Contains(previewRow, "│") {
		t.Fatalf("wide picker should separate the list and preview panes: %q", previewRow)
	}
	if strings.Contains(previewRow, "…") {
		t.Fatalf("preview heading row should not truncate the list pane: %q", previewRow)
	}
}

func TestPetPickerHidesPreviewPaneForNoCompanion(t *testing.T) {
	m := newModel(context.Background(), Options{AltScreen: true})
	m.width, m.height = 110, 34
	m.picker = &commandPicker{kind: pickerPet, title: "Choose a companion", items: []pickerItem{
		{Label: "No companion", Value: terminalpet.DisabledID},
		{Group: "Discover", Label: "Boba", Value: "boba"},
	}, selected: 0}

	plain := plainRender(t, m.petPickerOverlay(m.width))
	if strings.Contains(plain, "Preview") {
		t.Fatalf("no-companion selection should not show an empty preview pane:\n%s", plain)
	}
}

func TestPetPickerMouseWheelSchedulesSelectedPreview(t *testing.T) {
	m := mouseTestModel()
	m.picker = &commandPicker{kind: pickerPet, title: "Choose a companion", items: []pickerItem{
		{Label: "Alpha", Value: "alpha"},
		{Label: "Beta", Value: "beta"},
	}, selected: 0}
	m.petEntries["alpha"] = terminalpet.Entry{Slug: "alpha"}
	m.petEntries["beta"] = terminalpet.Entry{Slug: "beta"}
	m.petPreviewSlug = "alpha"
	m.petPreviewLoading = false

	updated, cmd := m.Update(testMouseWheel(tea.MouseWheelDown, 1, 1))
	next := updated.(model)
	if next.picker.selected != 1 {
		t.Fatalf("wheel selected index = %d, want 1", next.picker.selected)
	}
	if next.petPreviewSlug != "beta" || !next.petPreviewLoading {
		t.Fatalf("wheel preview = %q loading=%v, want beta loading", next.petPreviewSlug, next.petPreviewLoading)
	}
	if cmd == nil {
		t.Fatal("wheel selection should schedule the selected pet preview")
	}
}

func TestPetPickerPreviewStacksBelowListWhenNarrow(t *testing.T) {
	m := newModel(context.Background(), Options{AltScreen: true})
	m.width, m.height = 50, 28
	m.picker = &commandPicker{kind: pickerPet, title: "Choose a companion", items: []pickerItem{{Label: "Boba", Value: "boba"}}, selected: 0}
	m.petEntries["boba"] = terminalpet.Entry{Slug: "boba", DisplayName: "Boba"}
	frame := image.NewNRGBA(image.Rect(0, 0, 12, 12))
	m.petPreview, _ = terminalpet.ThumbnailAnimation(frame)

	content := m.petPickerOverlay(m.width)
	x, y, ok := petPickerImagePosition(content, petImageColumns, petImageRows)
	if !ok {
		t.Fatalf("narrow picker should provide stacked preview geometry:\n%s", plainRender(t, content))
	}
	if x < m.width/3 || x > m.width*2/3 {
		t.Fatalf("narrow picker preview x=%d, want centered in width %d", x, m.width)
	}
	listRow := -1
	for index, line := range viewLines(content) {
		if strings.Contains(ansi.Strip(line), "Boba") {
			listRow = index
			break
		}
	}
	if listRow < 0 || y <= listRow {
		t.Fatalf("stacked preview row %d should follow list row %d:\n%s", y, listRow, plainRender(t, content))
	}
}

func TestPetLayoutRequiresRoomAndNoModal(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.altScreen, m.width, m.height = true, 120, 30
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowUser, text: "hello"})
	frame := image.NewNRGBA(image.Rect(0, 0, 12, 12))
	animation, _ := terminalpet.ThumbnailAnimation(frame)
	m.petID, m.petAnimation = "boba", animation
	if !m.petLayoutActive() {
		t.Fatal("pet layout should be active in a wide, non-modal transcript")
	}
	m.picker = &commandPicker{kind: pickerPet}
	if m.petLayoutActive() {
		t.Fatal("pet layout should hide behind a modal")
	}
}

func TestAmbientPetFloatsWithoutSidebarDivider(t *testing.T) {
	m := newModel(context.Background(), Options{AltScreen: true})
	m.width, m.height = 110, 34
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowUser, text: "hello"})
	frame := image.NewNRGBA(image.Rect(0, 0, 12, 12))
	m.petAnimation, _ = terminalpet.ThumbnailAnimation(frame)
	m.petID = "boba"
	m.petRenderer = terminalpet.NewImageRenderer(terminalpet.ImageSupport{Protocol: terminalpet.ImageProtocolKitty})

	view := m.View()
	plain := plainRender(t, view.Content)
	dividerColumn := m.width - petReservedColumns
	for _, line := range strings.Split(plain, "\n") {
		runes := []rune(line)
		if len(runes) > dividerColumn && runes[dividerColumn] == '│' {
			t.Fatalf("ambient pet rendered a sidebar divider at column %d: %q", dividerColumn, line)
		}
	}
	var output bytes.Buffer
	if err := m.petRenderer.Render(&output); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); !strings.Contains(got, "_Ga=T,t=d,f=100,c=9,r=5") {
		t.Fatalf("ambient pet did not use floating image geometry: %q", got)
	}
}

func TestAmbientPetRemainsVisibleInNarrowTerminal(t *testing.T) {
	m := newModel(context.Background(), Options{AltScreen: true})
	m.width, m.height = 60, 24
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowUser, text: "hello"})
	frame := image.NewNRGBA(image.Rect(0, 0, 12, 12))
	m.petAnimation, _ = terminalpet.ThumbnailAnimation(frame)
	m.petID = "boba"
	m.petRenderer = terminalpet.NewImageRenderer(terminalpet.ImageSupport{Protocol: terminalpet.ImageProtocolKitty})

	_ = m.View()
	var output bytes.Buffer
	if err := m.petRenderer.Render(&output); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); !strings.Contains(got, "_Ga=T,t=d,f=100,c=9,r=5") {
		t.Fatalf("narrow terminal hid the ambient pet: %q", got)
	}
}

func TestAmbientPetAnchorsInsideComposerPetSlot(t *testing.T) {
	m := newModel(context.Background(), Options{AltScreen: true})
	m.width, m.height = 110, 34
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowUser, text: "hello"})
	frame := image.NewNRGBA(image.Rect(0, 0, 12, 12))
	m.petAnimation, _ = terminalpet.ThumbnailAnimation(frame)
	m.petID = "boba"
	m.petRenderer = terminalpet.NewImageRenderer(terminalpet.ImageSupport{Protocol: terminalpet.ImageProtocolKitty})
	m.unpricedTokens = 11700

	view := m.View()
	plain := plainRender(t, view.Content)
	composerWidth := 0
	for _, line := range strings.Split(plain, "\n") {
		if strings.Contains(line, "╭") && strings.Contains(line, "─") {
			composerWidth = len([]rune(strings.TrimRight(line, " ")))
		}
	}
	if composerWidth != m.width-petReservedColumns {
		t.Fatalf("visible composer width = %d, want pet slot to start at %d", composerWidth, m.width-petReservedColumns)
	}
	slotEdge := m.width - petReservedColumns - 1
	foundTop, foundInput := false, false
	for _, line := range strings.Split(plain, "\n") {
		runes := []rune(line)
		if len(runes) <= slotEdge {
			continue
		}
		if strings.HasPrefix(line, "╭") {
			foundTop = true
			if runes[slotEdge] != '╮' {
				t.Fatalf("composer top is not closed before pet slot: %q", line)
			}
		}
		if strings.HasPrefix(line, "│") && strings.Contains(line, "describe a task for zero") {
			foundInput = true
			if runes[slotEdge] != '│' {
				t.Fatalf("composer input is not closed before pet slot: %q", line)
			}
		}
	}
	if !foundTop || !foundInput {
		t.Fatalf("composer rows missing: top=%v input=%v", foundTop, foundInput)
	}
	status := strings.Split(plain, "\n")[len(strings.Split(plain, "\n"))-1]
	tokensAt := strings.Index(status, "11.7K tok")
	if tokensAt < 0 {
		t.Fatalf("token status missing: %q", status)
	}
	if lipgloss.Width(status[:tokensAt])+len("11.7K tok") > m.width-petReservedColumns {
		t.Fatalf("token status overlaps pet slot: %q", status)
	}
	expectedY := len(viewLines(view.Content)) - petImageRows - 1
	expectedX := m.width - petImageColumns - 2
	var output bytes.Buffer
	if err := m.petRenderer.Render(&output); err != nil {
		t.Fatal(err)
	}
	wantCursor := fmt.Sprintf("\x1b[%d;%dH", expectedY+1, expectedX+1)
	if got := output.String(); !strings.Contains(got, wantCursor) {
		t.Fatalf("pet cursor missing %q: %q", wantCursor, got)
	}
	for _, line := range strings.Split(plain, "\n") {
		modelColumn := strings.Index(line, m.modelName)
		if modelColumn < 0 {
			continue
		}
		if modelColumn+len(m.modelName) > m.width-petReservedColumns {
			t.Fatalf("composer model label overlaps pet slot: %q", line)
		}
		return
	}
	t.Fatal("composer model label not found")
}

func TestAmbientPetStaysInChatComposerWhenSidebarIsVisible(t *testing.T) {
	m := sidebarTestModel()
	m.width, m.height = 120, 34
	frame := image.NewNRGBA(image.Rect(0, 0, 12, 12))
	m.petAnimation, _ = terminalpet.ThumbnailAnimation(frame)
	m.petID = "boba"
	m.petRenderer = terminalpet.NewImageRenderer(terminalpet.ImageSupport{Protocol: terminalpet.ImageProtocolKitty})

	view := m.View()
	var output bytes.Buffer
	if err := m.petRenderer.Render(&output); err != nil {
		t.Fatal(err)
	}
	wantCursor := fmt.Sprintf("\x1b[%d;%dH", len(viewLines(view.Content))-petImageRows, m.width-petImageColumns-1)
	if got := output.String(); !strings.Contains(got, wantCursor) {
		t.Fatalf("sidebar hid or misplaced pet; cursor missing %q: %q", wantCursor, got)
	}

	plain := plainRender(t, view.Content)
	slotEdge := m.width - petReservedColumns - 1
	for _, line := range strings.Split(plain, "\n") {
		if !strings.HasPrefix(line, "╭") {
			continue
		}
		runes := []rune(line)
		if len(runes) <= slotEdge || runes[slotEdge] != '╮' {
			t.Fatalf("sidebar composer is not closed before pet slot: %q", line)
		}
		return
	}
	t.Fatal("sidebar composer top not found")
}

func TestPetsCommandExplainsUnsupportedTerminal(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.petClient = terminalpet.NewClient(t.TempDir())
	m.petRenderer = terminalpet.NewImageRenderer(terminalpet.ImageSupport{Reason: "Terminal companions need Kitty graphics or Sixel image support."})
	next, cmd := m.handlePetsCommand("")
	if cmd != nil {
		t.Fatal("unsupported terminal should not start a catalog request")
	}
	nextModel := next.(model)
	if nextModel.picker != nil {
		t.Fatal("unsupported terminal should not open the pet picker")
	}
	if got := plainRender(t, nextModel.View().Content); !strings.Contains(got, "Kitty graphics or Sixel") {
		t.Fatalf("unsupported-terminal guidance is missing: %q", got)
	}
}
