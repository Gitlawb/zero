package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// A committed theme is written to user config and reloaded at startup (via
// Options.SavedTheme -> resolveThemeMode), so a /theme choice survives restart.
func TestThemeChoicePersistsAcrossRestart(t *testing.T) {
	defer applyTheme(themeDark, true)
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	// First session: pick a color theme via the /theme command (same commit path the
	// picker uses).
	m := newModel(context.Background(), Options{UserConfigPath: cfgPath})
	m, _ = m.handleThemeCommand("dracula")
	if m.themeMode != themeMode("dracula") {
		t.Fatalf("themeMode = %q, want dracula", m.themeMode)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("theme commit should have written config: %v", err)
	}
	var cfg struct {
		Preferences struct {
			Theme string `json:"theme"`
		} `json:"preferences"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("config is not valid JSON: %v", err)
	}
	if cfg.Preferences.Theme != "dracula" {
		t.Fatalf("preferences.theme = %q, want dracula", cfg.Preferences.Theme)
	}

	// Second session: the persisted theme seeds startup (no flag / env).
	restarted := newModel(context.Background(), Options{UserConfigPath: cfgPath, SavedTheme: "dracula"})
	if restarted.themeMode != themeMode("dracula") {
		t.Fatalf("restarted themeMode = %q, want dracula (from saved config)", restarted.themeMode)
	}
}

// resolveThemeMode precedence: flag > ZERO_THEME > persisted config > auto.
func TestResolveThemeModeConfigFallback(t *testing.T) {
	if got := resolveThemeMode("", "", "nord"); got != themeMode("nord") {
		t.Errorf("saved-only = %q, want nord", got)
	}
	if got := resolveThemeMode("dracula", "", "nord"); got != themeMode("dracula") {
		t.Errorf("flag should beat saved: got %q, want dracula", got)
	}
	if got := resolveThemeMode("", "gruvbox", "nord"); got != themeMode("gruvbox") {
		t.Errorf("env should beat saved: got %q, want gruvbox", got)
	}
	if got := resolveThemeMode("", "", "bogus-theme"); got != themeAuto {
		t.Errorf("unknown saved theme should fall back to auto, got %q", got)
	}
}

// themePickerRowIndex finds the picker row whose Value is name (index-independent,
// so tests survive registry reordering / additions).
func themePickerRowIndex(t *testing.T, p *commandPicker, name string) int {
	t.Helper()
	for i, item := range p.items {
		if item.Value == name {
			return i
		}
	}
	t.Fatalf("theme picker has no row for %q", name)
	return -1
}

// assertPreviewMatchesSelection checks the live palette equals the palette of the
// currently highlighted theme row (the auto row has no palette of its own).
func assertPreviewMatchesSelection(t *testing.T, m model) {
	t.Helper()
	sel := m.picker.items[m.picker.selected]
	entry, ok := lookupTheme(sel.Value)
	if !ok {
		return
	}
	if r, _, _, _ := zeroTheme.inkColor.RGBA(); r != mustR(t, entry.Palette.ink) {
		t.Errorf("preview of %q did not apply its palette (ink mismatch)", sel.Value)
	}
}

// Bare `/theme` opens the popup picker (like /model and /effort) with one row per
// theme mode, preselecting the active preference. An explicit `/theme <mode>` must
// keep taking the text path so scripts and the existing state view still work.
func TestThemePickerOpensOnBareTheme(t *testing.T) {
	defer applyTheme(themeDark, true)
	m := newModel(context.Background(), Options{})
	m.themeMode = themeLight
	m.input.SetValue("/theme")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	m = updated.(model)
	if cmd != nil {
		t.Fatalf("opening the theme picker should not emit a cmd, got %T", cmd)
	}
	if m.picker == nil || m.picker.kind != pickerTheme {
		t.Fatalf("expected the theme picker to open, got %#v", m.picker)
	}
	if len(m.picker.items) != len(themeModes) {
		t.Fatalf("picker has %d items, want %d", len(m.picker.items), len(themeModes))
	}
	for i, want := range themeModes {
		if m.picker.items[i].Value != want {
			t.Errorf("item %d value = %q, want %q", i, m.picker.items[i].Value, want)
		}
	}
	if got := m.picker.items[m.picker.selected].Value; got != string(themeLight) {
		t.Errorf("preselected value = %q, want the active mode %q", got, themeLight)
	}
}

// An explicit mode argument runs the text handler directly and never opens the
// popup — guards the dispatch branch and keeps /theme <mode> scriptable.
func TestThemeArgSkipsPicker(t *testing.T) {
	defer applyTheme(themeDark, true)
	m := newModel(context.Background(), Options{})
	m.input.SetValue("/theme dark")

	updated, _ := m.Update(testKey(tea.KeyEnter))
	m = updated.(model)
	if m.picker != nil {
		t.Fatalf("explicit /theme dark must not open a picker, got %#v", m.picker)
	}
	if m.themeMode != themeDark {
		t.Fatalf("after /theme dark, mode = %q", m.themeMode)
	}
}

// Moving the cursor live-previews each palette: the global zeroTheme swaps as the
// selection changes, so the whole UI (and the overlay) repaints in the hovered
// theme without committing the preference.
func TestThemePickerPreviewsOnMove(t *testing.T) {
	defer applyTheme(themeDark, true)
	m := newModel(context.Background(), Options{})
	m.themeMode = themeDark
	m.hasDarkBg = true
	applyTheme(themeDark, true)
	m.input.SetValue("/theme")
	updated, _ := m.Update(testKey(tea.KeyEnter))
	m = updated.(model)

	// Each Down moves to the next theme and live-applies its palette; Up follows back.
	for range 3 {
		updated, _ = m.Update(testKey(tea.KeyDown))
		m = updated.(model)
		assertPreviewMatchesSelection(t, m)
	}
	updated, _ = m.Update(testKey(tea.KeyUp))
	m = updated.(model)
	assertPreviewMatchesSelection(t, m)

	// Preview must not have written the committed preference.
	if m.themeMode != themeDark {
		t.Errorf("preview mutated the committed mode to %q", m.themeMode)
	}
}

// Enter on a fixed mode commits it: the palette stays applied, the preference is
// recorded, the popup closes, and the switch is noted in the transcript.
func TestThemePickerCommitAppliesAndRecords(t *testing.T) {
	defer applyTheme(themeDark, true)
	m := newModel(context.Background(), Options{})
	m.themeMode = themeDark
	m.hasDarkBg = true
	applyTheme(themeDark, true)
	m.input.SetValue("/theme")
	updated, _ := m.Update(testKey(tea.KeyEnter))
	m = updated.(model)

	// Choose a color theme by locating its row (index-independent), then confirm.
	m.picker.selected = themePickerRowIndex(t, m.picker, "dracula")
	updated, cmd := m.Update(testKey(tea.KeyEnter))
	m = updated.(model)
	if cmd != nil {
		t.Fatalf("committing a fixed theme should not emit a cmd, got %T", cmd)
	}
	if m.picker != nil {
		t.Fatal("committing should close the picker")
	}
	if m.themeMode != themeMode("dracula") {
		t.Fatalf("committed mode = %q, want dracula", m.themeMode)
	}
	if r, _, _, _ := zeroTheme.inkColor.RGBA(); r != mustR(t, draculaPalette.ink) {
		t.Error("commit did not leave the dracula palette applied")
	}
	if !transcriptContains(m.transcript, "active theme") {
		t.Errorf("commit should record the switch in the transcript:\n%s", transcriptText(m.transcript))
	}
}

// Committing `auto` re-probes the terminal background (like the text dispatch) so
// the palette re-detects light/dark instead of reusing the preview's reading.
func TestThemePickerAutoCommitReprobesBackground(t *testing.T) {
	defer applyTheme(themeDark, true)
	m := newModel(context.Background(), Options{})
	m.themeMode = themeDark
	m.input.SetValue("/theme")
	updated, _ := m.Update(testKey(tea.KeyEnter))
	m = updated.(model)

	m.picker.selected = 0 // "auto"
	updated, cmd := m.Update(testKey(tea.KeyEnter))
	m = updated.(model)
	if m.themeMode != themeAuto {
		t.Fatalf("committed mode = %q, want auto", m.themeMode)
	}
	if cmd == nil {
		t.Fatal("committing auto must return a background re-probe cmd")
	}
	if reflect.ValueOf(cmd).Pointer() != reflect.ValueOf(tea.RequestBackgroundColor).Pointer() {
		t.Error("committing auto must return tea.RequestBackgroundColor")
	}
}

// Typing to filter the list re-previews the newly-highlighted mode, so the applied
// palette never diverges from the row the popup points at. Backspacing back to an
// empty query re-previews the (restored) selection too.
func TestThemePickerFilterRePreviews(t *testing.T) {
	defer applyTheme(themeDark, true)
	m := newModel(context.Background(), Options{})
	m.themeMode = themeDark
	m.hasDarkBg = true
	applyTheme(themeDark, true)
	m.input.SetValue("/theme")
	updated, _ := m.Update(testKey(tea.KeyEnter))
	m = updated.(model)

	// Preview light by moving, then filter to "light" — the applied palette must
	// track the filtered highlight (still light here), not lag behind.
	updated, _ = m.Update(testKey(tea.KeyDown)) // dark -> light preview
	m = updated.(model)
	updated, _ = m.Update(testKeyText("l"))
	m = updated.(model)
	updated, _ = m.Update(testKeyText("i"))
	m = updated.(model)
	if got := m.picker.items[m.picker.selected].Value; got != string(themeLight) {
		t.Fatalf("filter 'li' should highlight light, got %q", got)
	}
	if r, _, _, _ := zeroTheme.inkColor.RGBA(); r != mustR(t, lightPalette.ink) {
		t.Error("filtering to light did not keep the light palette previewed")
	}

	// Re-filter to "dark": highlight and preview must both switch to dark.
	updated, _ = m.Update(testKey(tea.KeyBackspace))
	m = updated.(model)
	updated, _ = m.Update(testKey(tea.KeyBackspace))
	m = updated.(model)
	updated, _ = m.Update(testKeyText("d"))
	m = updated.(model)
	if got := m.picker.items[m.picker.selected].Value; got != string(themeDark) {
		t.Fatalf("filter 'd' should highlight dark, got %q", got)
	}
	if r, _, _, _ := zeroTheme.inkColor.RGBA(); r != mustR(t, darkPalette.ink) {
		t.Error("filtering to dark did not re-preview the dark palette")
	}
}

// A filter that matches nothing must not strand the last preview: the palette
// falls back to the committed theme, and committing (Enter on the empty list) is a
// clean no-op that leaves the committed mode and palette in agreement.
func TestThemePickerEmptyFilterRestoresCommitted(t *testing.T) {
	defer applyTheme(themeDark, true)
	m := newModel(context.Background(), Options{})
	m.themeMode = themeDark
	m.hasDarkBg = true
	applyTheme(themeDark, true)
	m.input.SetValue("/theme")
	updated, _ := m.Update(testKey(tea.KeyEnter))
	m = updated.(model)

	// Preview another theme by moving, then type a char that matches no theme name
	// ('q' appears in none) -> the list empties.
	updated, _ = m.Update(testKey(tea.KeyDown))
	m = updated.(model)
	updated, _ = m.Update(testKeyText("q"))
	m = updated.(model)
	if len(m.picker.items) != 0 {
		t.Fatalf("filter 'q' should match no theme, got %d items", len(m.picker.items))
	}
	if r, _, _, _ := zeroTheme.inkColor.RGBA(); r != mustR(t, darkPalette.ink) {
		t.Error("an empty filter should restore the committed dark palette, not keep the preview")
	}

	// Enter on the empty list closes the picker without changing the committed
	// preference, and leaves the palette matching it.
	updated, _ = m.Update(testKey(tea.KeyEnter))
	m = updated.(model)
	if m.picker != nil {
		t.Fatal("Enter on an empty filter should close the picker")
	}
	if m.themeMode != themeDark {
		t.Fatalf("empty-list commit must not change the mode, got %q", m.themeMode)
	}
	if r, _, _, _ := zeroTheme.inkColor.RGBA(); r != mustR(t, darkPalette.ink) {
		t.Error("after an empty-list commit the palette must match the committed dark mode")
	}
}

// Esc dismisses the picker without choosing and restores the committed palette,
// undoing whatever the live preview applied.
func TestThemePickerEscRestoresCommittedTheme(t *testing.T) {
	defer applyTheme(themeDark, true)
	m := newModel(context.Background(), Options{})
	m.themeMode = themeDark
	m.hasDarkBg = true
	applyTheme(themeDark, true)
	m.input.SetValue("/theme")
	updated, _ := m.Update(testKey(tea.KeyEnter))
	m = updated.(model)

	// Preview another theme by moving, then bail out.
	updated, _ = m.Update(testKey(tea.KeyDown))
	m = updated.(model)
	assertPreviewMatchesSelection(t, m)
	if r, _, _, _ := zeroTheme.inkColor.RGBA(); r == mustR(t, darkPalette.ink) {
		t.Fatal("expected a non-dark preview to be applied before Esc")
	}
	updated, _ = m.Update(testKey(tea.KeyEsc))
	m = updated.(model)
	if m.picker != nil {
		t.Fatal("Esc should close the picker")
	}
	if m.themeMode != themeDark {
		t.Fatalf("Esc must not change the committed mode, got %q", m.themeMode)
	}
	if r, _, _, _ := zeroTheme.inkColor.RGBA(); r != mustR(t, darkPalette.ink) {
		t.Error("Esc did not restore the committed dark palette")
	}
}
